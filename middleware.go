package builder

import (
	"context"
	"iter"
	"time"

	"github.com/fantasticbin/QueryBuilder/core"
)

// Middleware 查询中间件类型定义
// 参数:
//
//	ctx: 上下文
//	builder: 查询构建器实例（Querier[R] 接口类型，提供基本的类型安全）
//	next: 下一个中间件或最终查询处理器
//
// 返回:
//
//	Result[R]: 查询结果
//	error: 错误信息
type Middleware[R any] func(
	ctx context.Context,
	builder Querier[R],
	next func(context.Context) (core.Result[R], error),
) (core.Result[R], error)

// BeforeQueryHook 查询前置钩子函数类型
// 参数:
//
//	ctx: 上下文
//
// 返回:
//
//	context.Context: 可修改后的上下文
type BeforeQueryHook func(ctx context.Context) context.Context

// AfterQueryHook 查询后置钩子函数类型
// 泛型参数:
//
//	R: 查询结果的实体类型
//
// 参数:
//
//	ctx: 上下文
//	result: 查询结果
//	err: 错误信息
type AfterQueryHook[R any] func(ctx context.Context, result core.Result[R], err error)

// middlewareRunner 中间件链执行器类型
// 接收 ctx 和查询函数，返回经过中间件链处理后的结果
type middlewareRunner[R any] func(ctx context.Context, queryFn func(context.Context) (core.Result[R], error)) (core.Result[R], error)

// middlewareProvider 中间件执行层数据提供者接口
// 由 builder 实现，供 newMiddlewareContext 通过接口约束获取数据，彻底解耦对 builder 实例的直接依赖
// 泛型参数:
//
//	R: 查询结果的实体类型
type middlewareProvider[R any] interface {
	GetQueryMeta() QueryMeta
	getMiddlewares() []Middleware[R]
	getQuerierRef() Querier[R]
	getBeforeHook() BeforeQueryHook
	getAfterHook() AfterQueryHook[R]
	setStartTime(t time.Time)
}

// middlewareContext 中间件执行上下文，封装中间件执行层所需的全部状态
// 由 newMiddlewareContext 通过 middlewareProvider 接口提取，彻底解耦对 builder 实例的直接依赖
// 泛型参数:
//
//	R: 查询结果的实体类型
type middlewareContext[R any] struct {
	middlewares    []Middleware[R]   // 中间件链
	querierRef     Querier[R]        // Querier 接口引用，传递给中间件
	beforeHook     BeforeQueryHook   // 查询前置钩子
	afterHook      AfterQueryHook[R] // 查询后置钩子
	needTotal      bool              // 是否需要查询总数
	needPagination bool              // 是否需要分页（游标查询时控制单批次/多批次）
	limit          uint32            // 每页数据条数
	cursorValues   []any             // 游标初始值
	start          uint32            // 分页起始位置
	onStartTime    func(time.Time)   // 回写查询开始时间
}

// newMiddlewareContext 通过 middlewareProvider 接口提取中间件执行所需的状态快照
func newMiddlewareContext[R any](p middlewareProvider[R]) *middlewareContext[R] {
	meta := p.GetQueryMeta()
	return &middlewareContext[R]{
		middlewares:    p.getMiddlewares(),
		querierRef:     p.getQuerierRef(),
		beforeHook:     p.getBeforeHook(),
		afterHook:      p.getAfterHook(),
		needTotal:      meta.NeedTotal,
		needPagination: meta.NeedPagination,
		limit:          meta.Limit,
		cursorValues:   meta.CursorValues,
		start:          meta.Start,
		onStartTime:    p.setStartTime,
	}
}

// buildRunner 构建中间件链执行器
// 将中间件按逆序包装，返回 middlewareRunner，调用时传入 queryFn 即可执行完整中间件链
func buildRunner[R any](mc *middlewareContext[R]) middlewareRunner[R] {
	return func(ctx context.Context, queryFn func(context.Context) (core.Result[R], error)) (core.Result[R], error) {
		next := queryFn
		for i := len(mc.middlewares) - 1; i >= 0; i-- {
			next = func(mw Middleware[R], fn func(context.Context) (core.Result[R], error)) func(context.Context) (core.Result[R], error) {
				return func(ctx context.Context) (core.Result[R], error) {
					return mw(ctx, mc.querierRef, fn)
				}
			}(mc.middlewares[i], next)
		}
		return next(ctx)
	}
}

// executeWithMiddlewares 执行中间件链并调用最终查询逻辑
// 由各专属构建器在 QueryList 中调用，传入最终的查询函数
// 支持前置/后置钩子
// 参数:
//
//	ctx: 请求上下文
//	mc: 中间件执行上下文
//	queryFn: 最终查询函数
func executeWithMiddlewares[R any](
	ctx context.Context,
	mc *middlewareContext[R],
	queryFn func(context.Context) (core.Result[R], error),
) (core.Result[R], error) {
	// 设置查询开始时间
	mc.onStartTime(time.Now())

	// 执行前置钩子
	if mc.beforeHook != nil {
		ctx = mc.beforeHook(ctx)
	}

	result, err := buildRunner[R](mc)(ctx, queryFn)
	invokeAfterHook[R](ctx, mc, result, err)
	return result, err
}

// executeCursorWithMiddlewares 执行游标查询模式下的中间件链和钩子
// 封装游标查询的完整生命周期：BeforeQueryHook → 分批获取（每批执行中间件链）→ AfterQueryHook
// 参数:
//
//	ctx: 请求上下文
//	mc: 中间件执行上下文
//	cursorQueryFn: 游标分批查询函数，接收 cursorValues 和 isFirstBatch 返回一批数据
//
// 返回:
//
//	iter.Seq2[*R, error]: 游标迭代器
func executeCursorWithMiddlewares[R any](
	ctx context.Context,
	mc *middlewareContext[R],
	cursorQueryFn cursorFetchBatch[R],
) iter.Seq2[*R, error] {
	// 游标字段默认值/合法性已在 prepareAndValidate 中统一处理。
	ctx, batchSize, initialCursorValues, runChain := prepareCursorPipeline[R](ctx, mc)

	// 包装 fetchBatch，使每批次查询经过中间件链
	wrappedFetch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*R, []any, int64, bool, error) {
		var nextCursorValues []any
		var batchTotal int64
		queryFn := func(ctx context.Context) (core.Result[R], error) {
			batch, nextCV, total, _, err := cursorQueryFn(ctx, cursorValues, isFirstBatch)
			nextCursorValues = nextCV
			batchTotal = total
			return &core.ListResult[R]{
				Items: batch,
				Total: resolveResultTotal(mc, batch, total),
			}, err
		}

		result, err := runChain(ctx, queryFn)
		if result == nil {
			return nil, nextCursorValues, batchTotal, false, err
		}
		list := result.GetItems()
		return list, nextCursorValues, batchTotal, false, err
	}

	// 用于接收首批次查询返回的总数（needTotal 时有效）
	var cursorTotal int64
	// 构建迭代器，并在迭代完成后执行后置钩子
	innerIter := buildCursorIterator[R](
		ctx,
		batchSize,
		initialCursorValues,
		mc.needPagination,
		wrappedFetch,
		&cursorTotal,
	)

	// 包装迭代器，在遍历结束后执行 AfterQueryHook
	return func(yield func(*R, error) bool) {
		var allResults []*R
		var lastErr error

		for item, err := range innerIter {
			if err != nil {
				lastErr = err
				if !yield(nil, err) {
					break
				}
				break
			}
			allResults = append(allResults, item)
			if !yield(item, nil) {
				break
			}
		}

		// 执行后置钩子
		invokeAfterHook[R](ctx, mc, &core.ListResult[R]{
			Items: allResults,
			Total: resolveResultTotal(mc, allResults, cursorTotal),
		}, lastErr)
	}
}

// executePageWithMiddlewares 执行单批次游标分页查询，返回结构化的分页结果
// 封装"单批次游标查询 + 中间件链 + 前置/后置钩子 + HasMore 判断"的完整生命周期
// 参数:
//
//	ctx: 请求上下文
//	mc: 中间件执行上下文
//	pageFetchFn: 游标分批查询函数，接收 cursorValues 和 isFirstBatch 返回一批数据
//
// 返回:
//
//	*CursorPageResult[R]: 游标分页结果
//	error: 错误信息
func executePageWithMiddlewares[R any](
	ctx context.Context,
	mc *middlewareContext[R],
	pageFetchFn cursorFetchBatch[R],
) (*core.CursorPageResult[R], error) {
	ctx, batchSize, initialCursorValues, runChain := prepareCursorPipeline[R](ctx, mc)

	// 单批次查询：先组装完整 CursorPageResult，再交给中间件链
	queryFn := func(ctx context.Context) (core.Result[R], error) {
		batch, nextCV, total, more, err := pageFetchFn(ctx, initialCursorValues, true)
		result := &core.CursorPageResult[R]{
			Items:            batch,
			Total:            resolveResultTotal(mc, batch, total),
			HasMore:          more,
			NextCursorValues: nextCV,
		}
		return result, err
	}

	result, err := runChain(ctx, queryFn)
	pageResult := cursorPageResultFromResult(result)
	normalizeCursorPageResult(pageResult, batchSize)
	// 执行后置钩子
	invokeAfterHook[R](ctx, mc, pageResult, err)
	if err != nil {
		return nil, err
	}

	return pageResult, nil
}

// invokeAfterHook 执行后置钩子的统一逻辑
func invokeAfterHook[R any](ctx context.Context, mc *middlewareContext[R], result core.Result[R], err error) {
	if mc.afterHook == nil {
		return
	}

	mc.afterHook(ctx, result, err)
}

// resolveResultTotal 根据中间件上下文的 needTotal 和实际查询结果决定最终的 Total 值
func resolveResultTotal[R any](mc *middlewareContext[R], list []*R, queryTotal int64) int64 {
	if mc.needTotal && queryTotal > 0 {
		return queryTotal
	}
	return int64(len(list))
}

// normalizeCursorPageResult 根据 batchSize 和实际返回的 Items 数量调整 HasMore 和 NextCursorValues 字段
func normalizeCursorPageResult[R any](result *core.CursorPageResult[R], batchSize int) {
	if result == nil {
		return
	}
	if !result.HasMore || len(result.Items) < batchSize {
		result.HasMore = false
		result.NextCursorValues = nil
	}
}

// cursorPageResultFromResult 根据通用 Result[R] 组装 *CursorPageResult[R]
func cursorPageResultFromResult[R any](result core.Result[R]) *core.CursorPageResult[R] {
	if result == nil {
		return nil
	}
	return &core.CursorPageResult[R]{
		Items:            result.GetItems(),
		Total:            result.GetTotal(),
		HasMore:          result.GetHasMore(),
		NextCursorValues: result.GetNextCursorValues(),
	}
}

// listResultFromResult 根据通用 Result[R] 组装 *ListResult[R]
func listResultFromResult[R any](result core.Result[R]) *core.ListResult[R] {
	if result == nil {
		return nil
	}
	return &core.ListResult[R]{
		Items: result.GetItems(),
		Total: result.GetTotal(),
	}
}
