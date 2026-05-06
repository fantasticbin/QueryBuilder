package builder

import (
	"context"
	"errors"
	"fmt"
	"iter"
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
//	[]any: 下一批次的游标值（由各构建器自行从数据库层面提取）
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

			// 更新游标值（由各构建器在 fetchBatch 中自行提取）
			if nextCursorValues == nil {
				yield(nil, fmt.Errorf("fetchBatch must return nextCursorValues when batch is not empty"))
				return
			}
			cursorValues = nextCursorValues
		}
	}
}

