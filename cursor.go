package builder

import (
	"context"
	"errors"
	"fmt"
	"iter"
)

// ErrCursorFieldNotSet 游标字段未设置错误
var ErrCursorFieldNotSet = errors.New("cursor fields not set: must call SetCursorField before QueryCursor")

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
