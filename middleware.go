package builder

import (
	"context"
	"iter"
	"time"
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
//	[]*R: 查询结果列表
//	int64: 总数
//	error: 错误信息
type Middleware[R any] func(
	ctx context.Context,
	builder Querier[R],
	next func(context.Context) ([]*R, int64, error),
) ([]*R, int64, error)

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
//	list: 查询结果列表
//	total: 总数
//	err: 错误信息
type AfterQueryHook[R any] func(ctx context.Context, list []*R, total int64, err error)

// middlewareRunner 中间件链执行器类型
// 接收 ctx 和查询函数，返回经过中间件链处理后的结果
type middlewareRunner[R any] func(ctx context.Context, queryFn func(context.Context) ([]*R, int64, error)) ([]*R, int64, error)

// middlewareProvider 中间件执行层数据提供者接口
// 由 builder 实现，供 newMiddlewareContext 通过接口约束获取数据，彻底解耦对 builder 实例的直接依赖
// 泛型参数:
//
//	R: 查询结果的实体类型
type middlewareProvider[R any] interface {
	getMiddlewares() []Middleware[R]
	getQuerierRef() Querier[R]
	getBeforeHook() BeforeQueryHook
	getAfterHook() AfterQueryHook[R]
	getNeedTotal() bool
	getNeedPagination() bool
	getLimit() uint32
	getCursorValues() []any
	getStart() uint32
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
	return &middlewareContext[R]{
		middlewares:    p.getMiddlewares(),
		querierRef:     p.getQuerierRef(),
		beforeHook:     p.getBeforeHook(),
		afterHook:      p.getAfterHook(),
		needTotal:      p.getNeedTotal(),
		needPagination: p.getNeedPagination(),
		limit:          p.getLimit(),
		cursorValues:   p.getCursorValues(),
		start:          p.getStart(),
		onStartTime:    p.setStartTime,
	}
}

// buildRunner 构建中间件链执行器
// 将中间件按逆序包装，返回 middlewareRunner，调用时传入 queryFn 即可执行完整中间件链
func buildRunner[R any](mc *middlewareContext[R]) middlewareRunner[R] {
	return func(ctx context.Context, queryFn func(context.Context) ([]*R, int64, error)) ([]*R, int64, error) {
		next := queryFn
		for i := len(mc.middlewares) - 1; i >= 0; i-- {
			next = func(mw Middleware[R], fn func(context.Context) ([]*R, int64, error)) func(context.Context) ([]*R, int64, error) {
				return func(ctx context.Context) ([]*R, int64, error) {
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
	queryFn func(context.Context) ([]*R, int64, error),
) ([]*R, int64, error) {
	// 设置查询开始时间
	mc.onStartTime(time.Now())

	// 执行前置钩子
	if mc.beforeHook != nil {
		ctx = mc.beforeHook(ctx)
	}

	list, total, err := buildRunner[R](mc)(ctx, queryFn)

	// 执行后置钩子
	if mc.afterHook != nil {
		mc.afterHook(ctx, list, total, err)
	}

	return list, total, err
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
		queryFn := func(ctx context.Context) ([]*R, int64, error) {
			batch, nextCV, total, _, err := cursorQueryFn(ctx, cursorValues, isFirstBatch)
			nextCursorValues = nextCV
			batchTotal = total
			return batch, int64(len(batch)), err
		}

		list, _, err := runChain(ctx, queryFn)
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
		invokeAfterHook[R](ctx, mc, allResults, cursorTotal, lastErr)
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
) (*CursorPageResult[R], error) {
	ctx, batchSize, initialCursorValues, runChain := prepareCursorPipeline[R](ctx, mc)

	// 单批次查询：直接包装 pageFetchFn 经过中间件链执行一次
	var nextCursorValues []any
	var batchTotal int64
	var hasMore bool
	queryFn := func(ctx context.Context) ([]*R, int64, error) {
		batch, nextCV, total, more, err := pageFetchFn(ctx, initialCursorValues, true)
		nextCursorValues = nextCV
		batchTotal = total
		hasMore = more
		return batch, int64(len(batch)), err
	}

	list, _, err := runChain(ctx, queryFn)
	// 执行后置钩子
	invokeAfterHook[R](ctx, mc, list, batchTotal, err)
	if err != nil {
		return nil, err
	}

	// 组装结果
	result := &CursorPageResult[R]{
		Items: list,
		Total: batchTotal,
	}

	// 使用各构建器通过 limit+1 探测精确返回的 hasMore
	// HasMore=false 时 NextCursorValues 保持 nil（零值）
	if hasMore {
		result.HasMore = true
		result.NextCursorValues = nextCursorValues
	}

	// 兜底：如果返回条数小于 batchSize，强制 HasMore=false
	if len(list) < batchSize {
		result.HasMore = false
		result.NextCursorValues = nil
	}

	return result, nil
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

// invokeAfterHook 执行后置钩子的统一逻辑
// 当 needTotal 为 true 且 batchTotal > 0 时使用 batchTotal 作为总数；否则使用 list 长度
func invokeAfterHook[R any](ctx context.Context, mc *middlewareContext[R], list []*R, batchTotal int64, err error) {
	if mc.afterHook == nil {
		return
	}
	total := int64(len(list))
	if mc.needTotal && batchTotal > 0 {
		total = batchTotal
	}

	mc.afterHook(ctx, list, total, err)
}
