package builder

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strings"
)

// ErrCursorFieldNotSet 游标字段未设置错误
var ErrCursorFieldNotSet = errors.New("cursor fields not set: must call SetCursorField before QueryCursor")

// cursorFetchBatch 游标分批获取函数类型
// 参数:
//
//	ctx: 上下文
//	cursorValues: 当前游标值（首次为 nil，后续为上一批最后一条记录的游标字段值）
//
// 返回:
//
//	[]*R: 当前批次的记录列表
//	[]any: 下一批次的游标值（从本批最后一条记录中提取），如果为 nil 则由 buildCursorIterator 通过反射提取
//	error: 错误信息
type cursorFetchBatch[R any] func(ctx context.Context, cursorValues []any) ([]*R, []any, error)

// buildCursorIterator 构建游标迭代器
// 封装"分批获取 → 逐条 yield → 更新游标值 → 继续获取"的迭代循环
// 参数:
//
//	ctx: 上下文
//	cursorFields: 游标排序字段列表
//	batchSize: 每批次获取的数据条数
//	initialCursorValues: 初始游标值（为 nil 时从数据集起始位置开始）
//	fetchBatch: 分批获取函数，由各构建器提供
//
// 返回:
//
//	iter.Seq2[*R, error]: 迭代器类型
func buildCursorIterator[R any](
	ctx context.Context,
	cursorFields []string,
	batchSize int,
	initialCursorValues []any,
	fetchBatch cursorFetchBatch[R],
) iter.Seq2[*R, error] {
	return func(yield func(*R, error) bool) {
		// 校验游标字段
		if len(cursorFields) == 0 {
			yield(nil, ErrCursorFieldNotSet)
			return
		}

		// 使用初始游标值（可能为 nil，表示从头开始）
		cursorValues := initialCursorValues

		for {
			// 检查 context 是否已取消
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}

			// 获取一批数据
			batch, nextCursorValues, err := fetchBatch(ctx, cursorValues)
			if err != nil {
				yield(nil, err)
				return
			}

			// 当前批次返回 0 条记录，终止迭代
			if len(batch) == 0 {
				return
			}

			// 逐条 yield
			for _, item := range batch {
				if !yield(item, nil) {
					// 调用方通过 break 提前终止
					return
				}
			}

			// 当前批次返回的记录数小于 batchSize，已到达数据末尾
			if len(batch) < batchSize {
				return
			}

			// 更新游标值
			if nextCursorValues != nil {
				// fetchBatch 已提供下一批的游标值（如 ES 的 sort values）
				cursorValues = nextCursorValues
			} else {
				// 从最后一条记录中通过反射提取游标值
				lastItem := batch[len(batch)-1]
				cursorValues, err = extractCursorValues(lastItem, cursorFields)
				if err != nil {
					yield(nil, fmt.Errorf("extract cursor values failed: %w", err))
					return
				}
			}
		}
	}
}

// extractCursorValues 从记录中通过反射提取游标字段对应的值
// 支持结构体字段名匹配（大小写不敏感）和 JSON/BSON tag 匹配
func extractCursorValues[R any](item *R, cursorFields []string) ([]any, error) {
	if item == nil {
		return nil, errors.New("cannot extract cursor values from nil item")
	}

	v := reflect.ValueOf(item).Elem()
	t := v.Type()

	values := make([]any, 0, len(cursorFields))
	for _, field := range cursorFields {
		fieldVal, err := getFieldValue(v, t, field)
		if err != nil {
			return nil, err
		}
		values = append(values, fieldVal)
	}

	return values, nil
}

// getFieldValue 根据字段名从结构体中获取字段值
// 匹配优先级：1. 精确字段名匹配 2. 大小写不敏感字段名匹配 3. JSON tag 匹配 4. BSON tag 匹配 5. gorm column tag 匹配
func getFieldValue(v reflect.Value, t reflect.Type, fieldName string) (any, error) {
	// 1. 精确字段名匹配
	if f := v.FieldByName(fieldName); f.IsValid() {
		return f.Interface(), nil
	}

	// 2. 遍历所有字段，尝试大小写不敏感匹配和 tag 匹配
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		// 大小写不敏感字段名匹配
		if strings.EqualFold(sf.Name, fieldName) {
			return v.Field(i).Interface(), nil
		}

		// JSON tag 匹配
		if tag := sf.Tag.Get("json"); tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if tagName == fieldName {
				return v.Field(i).Interface(), nil
			}
		}

		// BSON tag 匹配
		if tag := sf.Tag.Get("bson"); tag != "" {
			tagName := strings.Split(tag, ",")[0]
			if tagName == fieldName {
				return v.Field(i).Interface(), nil
			}
		}

		// gorm column tag 匹配
		if tag := sf.Tag.Get("gorm"); tag != "" {
			for _, part := range strings.Split(tag, ";") {
				kv := strings.SplitN(part, ":", 2)
				if len(kv) == 2 && strings.TrimSpace(kv[0]) == "column" && strings.TrimSpace(kv[1]) == fieldName {
					return v.Field(i).Interface(), nil
				}
			}
		}
	}

	return nil, fmt.Errorf("cursor field %q not found in struct %s", fieldName, t.Name())
}
