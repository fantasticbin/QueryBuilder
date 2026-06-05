package builder

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"
)

// CursorPageResult 游标分页查询结果结构体
// 用于 QueryPage 方法的返回值，提供单批次分页查询的结构化结果
// 泛型参数:
//
//	R: 查询结果的实体类型
type CursorPageResult[R any] struct {
	Items            []*R  // 当前页的数据列表
	Total            int64 // 总数（仅在 needTotal=true 时有效）
	HasMore          bool  // 是否还有下一页数据
	NextCursorValues []any // 下一页的游标值（用于传入下次查询的 SetCursorValue），HasMore=false 时为 nil
}

// cursorFetchBatch 游标分批获取函数类型
// 参数:
//
//	ctx: 上下文
//	cursorValues: 当前游标值（首次为 nil，后续为上一批最后一条记录的游标字段值）
//	isFirstBatch: 是否为首批次查询（用于在首批次时并行执行 Count 等操作）
//
// 返回:
//
//	[]*R: 当前批次的记录列表
//	[]any: 下一批次的游标值（由各构建器自行从数据库层面提取）
//	int64: 总数（仅首批次且 needTotal 时有效，其他情况返回 0）
//	bool: 是否还有更多数据（通过 limit+1 探测精确判断）
//	error: 错误信息
type cursorFetchBatch[R any] func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*R, []any, int64, bool, error)

// cursorSortField 表示单个游标排序字段及其方向。
// Asc=true 表示升序，Asc=false 表示降序。
type cursorSortField struct {
	Field string
	Asc   bool
}

// parseCursorSortFields 解析游标排序字段，支持前缀方向标记：
// - "-field" 表示降序
// - "+field" 或 "field" 表示升序
func parseCursorSortFields(rawFields []string) []cursorSortField {
	parsed := make([]cursorSortField, 0, len(rawFields))
	for _, raw := range rawFields {
		field := raw
		asc := true
		if strings.HasPrefix(raw, "-") {
			field = strings.TrimPrefix(raw, "-")
			asc = false
		} else if strings.HasPrefix(raw, "+") {
			field = strings.TrimPrefix(raw, "+")
		}
		parsed = append(parsed, cursorSortField{Field: field, Asc: asc})
	}
	return parsed
}

// isUniformCursorDirection 判断游标排序方向是否一致（全升序或全降序）。
// 返回:
//
//	asc: 统一方向时的方向值（true=ASC, false=DESC）
//	ok:  是否方向一致；当字段为空时返回 false
func isUniformCursorDirection(fields []cursorSortField) (asc bool, ok bool) {
	if len(fields) == 0 {
		return true, false
	}
	first := fields[0].Asc
	for i := 1; i < len(fields); i++ {
		if fields[i].Asc != first {
			return true, false
		}
	}
	return first, true
}

// buildCursorIterator 构建游标迭代器
// 封装"分批获取 → 逐条 yield → 更新游标值 → 继续获取"的迭代循环
// 参数:
//
//	ctx: 上下文
//	cursorFields: 游标排序字段列表
//	batchSize: 每批次获取的数据条数
//	initialCursorValues: 初始游标值（为 nil 时从数据集起始位置开始）
//	singleBatch: 是否只获取单批次
//	fetchBatch: 分批获取函数，由各构建器提供
//	totalPtr: 总数指针，用于接收首批次查询返回的总数（可为 nil）
//
// 返回:
//
//	iter.Seq2[*R, error]: 迭代器类型
func buildCursorIterator[R any](
	ctx context.Context,
	batchSize int,
	initialCursorValues []any,
	singleBatch bool,
	fetchBatch cursorFetchBatch[R],
	totalPtr *int64,
) iter.Seq2[*R, error] {
	return func(yield func(*R, error) bool) {
		// 使用初始游标值（可能为 nil，表示从头开始）
		cursorValues := initialCursorValues
		isFirstBatch := true

		for {
			// 检查 context 是否已取消
			if err := ctx.Err(); err != nil {
				yield(nil, err)
				return
			}

			// 获取一批数据
			batch, nextCursorValues, total, _, err := fetchBatch(ctx, cursorValues, isFirstBatch)
			if err != nil {
				yield(nil, err)
				return
			}

			// 首批次时收集总数
			if isFirstBatch && totalPtr != nil {
				*totalPtr = total
			}
			isFirstBatch = false

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

			// 单批次模式，获取完即终止
			if singleBatch {
				return
			}

			// 当前批次返回的记录数小于 batchSize，已到达数据末尾
			if len(batch) < batchSize {
				return
			}

			// 更新游标值（由各构建器在 fetchBatch 中自行提取）
			if nextCursorValues == nil {
				yield(nil, fmt.Errorf("fetchBatch must return nextCursorValues when batch is not empty"))
				return
			}
			cursorValues = nextCursorValues
		}
	}
}

// executeBuilderCursorQuery 封装各专属构建器 QueryCursor 的公共入口生命周期。
func executeBuilderCursorQuery[B queryBuilder[B, R], R any](
	ctx context.Context,
	b *builder[B, R],
	fetchBatch cursorFetchBatch[R],
) iter.Seq2[*R, error] {
	return executeBuilderCursorQueryWithCleanup(ctx, b, fetchBatch, nil)
}

// executeBuilderCursorQueryWithCleanup 在公共 QueryCursor 生命周期结束时执行额外清理。
func executeBuilderCursorQueryWithCleanup[B queryBuilder[B, R], R any](
	ctx context.Context,
	b *builder[B, R],
	fetchBatch cursorFetchBatch[R],
	cleanup func(),
) iter.Seq2[*R, error] {
	b.beginQueryMode(true)
	if err := b.prepareAndValidate(); err != nil {
		defer b.finishCursorQuery()
		return func(yield func(*R, error) bool) {
			yield(nil, err)
		}
	}

	innerIter := executeCursorWithMiddlewares(
		ctx,
		newMiddlewareContext[R](b),
		fetchBatch,
	)
	return func(yield func(*R, error) bool) {
		defer func() {
			if cleanup != nil {
				cleanup()
			}
			b.finishCursorQuery()
		}()
		for item, err := range innerIter {
			if !yield(item, err) {
				return
			}
		}
	}
}

// resolveInitialCursorValues 解析初始游标值
// 优先级：cursorValues（方案B：显式设置）> start（方案A：复用 start 作为单字段数值游标）
// 返回 nil 表示从数据集起始位置开始查询
// 参数:
//
//	cursorValues: 外部显式设置的游标初始值
//	start: 分页起始位置（当 cursorValues 为空且 start > 0 时，作为单字段数值游标的初始值）
func resolveInitialCursorValues(cursorValues []any, start uint32) []any {
	// 方案B：如果显式设置了 cursorValues，直接使用
	if len(cursorValues) > 0 {
		return cursorValues
	}

	// 方案A：如果 start > 0，将其作为单字段数值游标的初始值
	if start > 0 {
		return []any{start}
	}

	return nil
}

// prepareCursorPipeline 抽离游标查询的公共准备逻辑
// 包含：确定批次大小、解析初始游标值、设置查询开始时间、执行前置钩子、构建中间件链执行器
// 参数:
//
//	ctx: 请求上下文
//	mc: 中间件执行上下文
//
// 返回:
//
//	ctx: 经过前置钩子处理后的上下文
//	batchSize: 每批次获取的数据条数
//	initialCursorValues: 初始游标值
//	runChain: 中间件链执行器，将查询函数包装进中间件链并执行
func prepareCursorPipeline[R any](
	ctx context.Context,
	mc *middlewareContext[R],
) (context.Context, int, []any, middlewareRunner[R]) {
	// 确定批次大小
	batchSize := int(mc.limit)
	if batchSize == 0 {
		batchSize = defaultLimit
	}

	// 解析初始游标值：优先使用 cursorValues（方案B），其次使用 start（方案A）
	initialCursorValues := resolveInitialCursorValues(mc.cursorValues, mc.start)
	// 设置查询开始时间
	mc.onStartTime(time.Now())
	// 执行前置钩子
	if mc.beforeHook != nil {
		ctx = mc.beforeHook(ctx)
	}

	// 构建中间件链执行器
	runChain := buildRunner[R](mc)
	return ctx, batchSize, initialCursorValues, runChain
}
