package builder

import (
	"context"
	"io"
	"iter"
	"sync"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/fantasticbin/QueryBuilder/v2/util"
)

// List 查询列表功能结构
// 泛型参数:
//
//	R - 返回结果类型参数
type List[R any] struct {
	dataSource      core.DataSource    // 数据源类型
	data            *core.DBProxy      // 可选：默认数据实例
	querier         Querier[R]         // 可选：直接注入自定义 Querier（用于测试等场景）
	metaMu          sync.RWMutex       // 保护 metaQuerier / metaSnapshot 的并发读写
	metaQuerier     Querier[R]         // 直接注入且未克隆的自定义 Querier，用于 GetQueryMeta 委托
	metaSnapshot    core.QueryMeta     // 最近一次独立查询完成时的只读元信息快照
	hasMetaSnapshot bool               // 是否存在有效的只读元信息快照
	beforeHook      BeforeQueryHook    // 查询前置钩子
	afterHook       AfterQueryHook[R]  // 查询后置钩子
	middlewares     []Middleware[R]    // 中间件链
	scope           ScopeConfigurer[R] // 可选：构建器配置回调，用于自动设置 filter/sort
}

// NewList 创建一个未绑定数据源的 List
// 调用方可后续通过 SetDataSource/WithData 或 SetQuerier 提供查询后端
func NewList[R any]() *List[R] {
	return &List[R]{}
}

// NewListWithData 通过指定数据源类型和数据实例创建 List
// 内部会保留默认数据实例，并预创建一个元信息快照
// 后续每次 Query/QueryCursor/QueryPage 都会使用新的构建器，避免查询状态串场
func NewListWithData[R any](ds core.DataSource, data *core.DBProxy) *List[R] {
	querier := NewBuilder[R](ds, data)
	return &List[R]{
		dataSource:      ds,
		data:            data,
		metaSnapshot:    querier.GetQueryMeta(),
		hasMetaSnapshot: true,
	}
}

// SetDataSource 设置数据源类型
// 支持不同数据源的查询实现，如 Gorm、MongoDB、ElasticSearch
// 通过该方法指定数据源类型，查询时将自动创建对应的专属构建器
func (l *List[R]) SetDataSource(ds core.DataSource) *List[R] {
	l.dataSource = ds
	if l.querier == nil {
		l.metaMu.Lock()
		l.metaQuerier = nil
		l.metaSnapshot = core.QueryMeta{}
		l.hasMetaSnapshot = false
		l.metaMu.Unlock()
	}
	return l
}

// SetQuerier 直接注入自定义 Querier 实例
// 用于测试场景或需要自定义查询逻辑的场景
// 设置后将忽略 DataSource 配置，直接使用注入的 Querier
func (l *List[R]) SetQuerier(querier Querier[R]) *List[R] {
	l.querier = querier
	l.metaMu.Lock()
	l.metaQuerier = querier
	l.metaSnapshot = core.QueryMeta{}
	l.hasMetaSnapshot = false
	l.metaMu.Unlock()
	return l
}

// Use 添加查询中间件
func (l *List[R]) Use(middlewares Middleware[R]) *List[R] {
	l.middlewares = append(l.middlewares, middlewares)
	return l
}

// SetScope 设置构建器配置回调
// 通过 NewGormScope / NewMongoScope / NewElasticSearchScope 创建 ScopeConfigurer
// 在 Query 内部创建好构建器后自动调用，用于设置 filter/sort
func (l *List[R]) SetScope(scope ScopeConfigurer[R]) *List[R] {
	l.scope = scope
	return l
}

// SetBeforeQueryHook 设置查询前置钩子
func (l *List[R]) SetBeforeQueryHook(hook BeforeQueryHook) *List[R] {
	l.beforeHook = hook
	return l
}

// SetAfterQueryHook 设置查询后置钩子
func (l *List[R]) SetAfterQueryHook(hook AfterQueryHook[R]) *List[R] {
	l.afterHook = hook
	return l
}

// buildQuerier 为单次查询准备 Querier
// 对内置构建器使用 Clone 隔离可变查询状态，对自定义 Querier 保持原样以兼容测试和扩展实现；
// isolated 为 true 表示返回的是本次查询独占的新构建器/副本，可安全采集只读元信息快照
func (l *List[R]) buildQuerier(options BaseQueryListOptions) (Querier[R], bool) {
	var querier Querier[R]
	var isolated bool
	if l.querier != nil {
		querier, isolated = cloneQuerier(l.querier)
	} else {
		data := options.GetData()
		if data == nil {
			data = l.data
		}
		querier = NewBuilder[R](l.dataSource, data)
		isolated = true
	}
	l.applyBackendOptions(querier, options)
	return querier, isolated
}

// recordQueryMeta 在单次查询配置/执行完成后以并发安全方式记录元信息。
// 独占构建器保存值快照，避免并发 GetQueryMeta 读取仍在变更的构建器字段；
// 非独占自定义 Querier（如单测 mock）仅保留引用，不额外触发其 GetQueryMeta() 调用。
func (l *List[R]) recordQueryMeta(querier Querier[R], isolated bool) {
	if isolated {
		snapshot := querier.GetQueryMeta()
		l.metaMu.Lock()
		l.metaSnapshot = snapshot
		l.hasMetaSnapshot = true
		l.metaQuerier = nil
		l.metaMu.Unlock()
		return
	}
	l.metaMu.Lock()
	l.metaQuerier = querier
	l.hasMetaSnapshot = false
	l.metaMu.Unlock()
}

// cloneQuerier 在已知内置构建器上创建查询状态副本
// 未知 Querier 没有通用复制协议，直接返回原实例并标记 cloned=false
func cloneQuerier[R any](querier Querier[R]) (Querier[R], bool) {
	switch q := querier.(type) {
	case *GormBuilder[R]:
		return q.Clone(), true
	case *MongoBuilder[R]:
		return q.Clone(), true
	case *ElasticSearchBuilder[R]:
		return q.Clone(), true
	default:
		return querier, false
	}
}

// applyBackendOptions 应用通用 QueryOption 中承载的后端专属配置
func (l *List[R]) applyBackendOptions(querier Querier[R], options BaseQueryListOptions) {
	if es, ok := querier.(*ElasticSearchBuilder[R]); ok {
		if options.esIndex != "" {
			es.SetESIndex(options.esIndex)
		}
		if options.pitID != "" {
			es.SetPITID(options.pitID)
		}
		if options.pitKeepAlive > 0 {
			es.SetPitKeepAlive(options.pitKeepAlive)
		}
	}
}

// passQueryOption 传递查询选项
func (l *List[R]) passQueryOption(querier Querier[R], options BaseQueryListOptions, cursorMode, handleHookAndMiddleware bool) {
	// 配置通用参数
	querier.SetStart(options.GetStart())
	querier.SetLimit(options.GetLimit())
	querier.SetNeedTotal(options.GetNeedTotal())
	if totalLimit := options.GetTotalLimit(); totalLimit > 0 {
		if q, ok := querier.(interface {
			SetTotalLimit(uint32) Querier[R]
		}); ok {
			q.SetTotalLimit(totalLimit)
		}
	}
	querier.SetNeedPagination(options.GetNeedPagination())

	// 应用指定字段
	if fields := options.GetFields(); len(fields) > 0 {
		querier.SetFields(fields...)
	}

	if cursorMode {
		// 设置游标字段
		if cursorFields := options.GetCursorFields(); len(cursorFields) > 0 {
			querier.SetCursorField(cursorFields...)
		}
		// 设置游标初始值
		if cursorValues := options.GetCursorValues(); len(cursorValues) > 0 {
			querier.SetCursorValue(cursorValues...)
		}
	}

	// 应用 Scope 配置回调，自动设置 filter/sort
	if l.scope != nil {
		l.scope(querier)
	}

	if handleHookAndMiddleware {
		// 设置 Hook
		if l.beforeHook != nil {
			querier.SetBeforeQueryHook(l.beforeHook)
		}
		if l.afterHook != nil {
			querier.SetAfterQueryHook(l.afterHook)
		}

		// 添加中间件
		for _, m := range l.middlewares {
			querier.Use(m)
		}
	}
}

// recoverListError 作为 defer 的直接目标（必须由 defer 直接调用的函数执行 recover）
// 捕获 panic 并转换为 error 写入 *err；panic 时将 result 置为其类型的零值
func recoverListError[R any](prefix string, errPtr *error, resultPtr *R) {
	if r := recover(); r != nil {
		var zero R
		*errPtr = util.PanicToError(prefix, r)
		*resultPtr = zero
	}
}

// recoverCursorError 作为 defer 的直接目标，捕获 panic 并将游标迭代器替换为返回该错误的迭代器
func recoverCursorError[R any](prefix string, seqPtr *iter.Seq2[*R, error]) {
	if r := recover(); r != nil {
		err := util.PanicToError(prefix, r)
		*seqPtr = func(yield func(*R, error) bool) {
			yield(nil, err)
		}
	}
}

// Query 执行查询
// 该方法会根据传入的 QueryOption 选项执行查询
// 通过 DataSource 枚举值自动创建对应的专属查询构建器
// 调用方需在获取具体构建器后自行设置 filter/sort
func (l *List[R]) Query(
	ctx context.Context,
	opts ...QueryOption,
) (result *core.ListResult[R], err error) {
	// 捕获 NewBuilder 等可能产生的 panic，转换为 error 返回
	defer recoverListError("query panic recovered", &err, &result)

	options := LoadQueryOptions(opts...)
	querier, isolated := l.buildQuerier(options)
	l.passQueryOption(querier, options, false, true)
	defer l.recordQueryMeta(querier, isolated)
	return querier.QueryList(ctx)
}

// QueryCursor 执行游标分页查询，返回 iter.Seq2 迭代器
// 该方法会根据传入的 QueryOption 选项执行游标分页查询
// 通过 DataSource 枚举值自动创建对应的专属查询构建器
func (l *List[R]) QueryCursor(
	ctx context.Context,
	opts ...QueryOption,
) (seq iter.Seq2[*R, error]) {
	// 捕获 NewBuilder 等可能产生的 panic，转换为返回错误的迭代器
	defer recoverCursorError[R]("query cursor panic recovered", &seq)

	options := LoadQueryOptions(opts...)
	querier, isolated := l.buildQuerier(options)
	l.passQueryOption(querier, options, true, true)
	seq = querier.QueryCursor(ctx)
	l.recordQueryMeta(querier, isolated)
	return seq
}

// QueryPage 执行单批次游标分页查询，返回结构化的分页结果
// 该方法会根据传入的 QueryOption 选项执行单批次游标分页查询
// 返回当前页数据、是否有下一页、下一页游标值等信息
func (l *List[R]) QueryPage(
	ctx context.Context,
	opts ...QueryOption,
) (result *core.CursorPageResult[R], err error) {
	// 捕获 NewBuilder 等可能产生的 panic，转换为 error 返回
	defer recoverListError("query page panic recovered", &err, &result)

	options := LoadQueryOptions(opts...)
	querier, isolated := l.buildQuerier(options)
	// List.QueryPage 每次都使用一次性构建器/副本，返回后无法复用其内部 PIT，
	// 关闭实现了 io.Closer 的构建器（如 ElasticSearchBuilder）以避免 PIT 句柄泄漏。
	if isolated {
		if closer, ok := querier.(io.Closer); ok {
			defer func() { _ = closer.Close() }()
		}
	}
	l.passQueryOption(querier, options, true, true)
	defer l.recordQueryMeta(querier, isolated)
	return querier.QueryPage(ctx)
}

// QueryPageWithPIT 执行 Elasticsearch PIT + search_after 单批次分页查询
// 该方法仅支持 ElasticSearchBuilder，用于需要跨请求维持 PIT ID 的分页场景
func (l *List[R]) QueryPageWithPIT(
	ctx context.Context,
	opts ...QueryOption,
) (result *core.ESPITPageResult[R], err error) {
	defer recoverListError("query page with pit panic recovered", &err, &result)

	options := LoadQueryOptions(opts...)
	querier, isolated := l.buildQuerier(options)
	es, ok := querier.(*ElasticSearchBuilder[R])
	if !ok {
		return nil, ErrPITRequiresESBuilder
	}

	l.passQueryOption(es, options, true, true)
	defer l.recordQueryMeta(es, isolated)
	return es.QueryPageWithPIT(ctx)
}

// Explain 返回构建器最终生成的查询语句（Dry Run 模式）
// 用于调试场景，不会实际执行查询
func (l *List[R]) Explain(ctx context.Context, opts ...QueryOption) (result string, err error) {
	// 捕获 NewBuilder 等可能产生的 panic，转换为 error 返回
	defer recoverListError("explain panic recovered", &err, &result)

	options := LoadQueryOptions(opts...)
	querier, isolated := l.buildQuerier(options)

	// 配置通用参数
	var cursorMode bool
	if len(options.GetCursorFields()) > 0 {
		cursorMode = true
	}
	l.passQueryOption(querier, options, cursorMode, false)
	defer l.recordQueryMeta(querier, isolated)

	return querier.Explain(ctx)
}

// GetQueryMeta 返回当前内部构建器的查询元信息快照
// 支持以下场景：
//   - 通过 NewListWithData 创建时，内部预先持有构建器快照
//   - 通过 SetQuerier 注入自定义 Querier 时
//   - 通过 Query/QueryCursor/QueryPage 执行后，以并发安全方式记录最近一次查询的元信息快照
//
// 仅在首次调用 Query/QueryCursor 之前且未设置 Querier 时返回零值
func (l *List[R]) GetQueryMeta() core.QueryMeta {
	l.metaMu.RLock()
	if l.hasMetaSnapshot {
		meta := cloneQueryMeta(l.metaSnapshot)
		l.metaMu.RUnlock()
		return meta
	}
	querier := l.metaQuerier
	l.metaMu.RUnlock()
	if querier != nil {
		return querier.GetQueryMeta()
	}
	return core.QueryMeta{}
}

// cloneQueryMeta 返回 QueryMeta 中切片字段的防御性副本
func cloneQueryMeta(meta core.QueryMeta) core.QueryMeta {
	if meta.Fields != nil {
		fields := make([]string, len(meta.Fields))
		copy(fields, meta.Fields)
		meta.Fields = fields
	}
	if meta.CursorFields != nil {
		cursorFields := make([]string, len(meta.CursorFields))
		copy(cursorFields, meta.CursorFields)
		meta.CursorFields = cursorFields
	}
	if meta.CursorValues != nil {
		cursorValues := make([]any, len(meta.CursorValues))
		copy(cursorValues, meta.CursorValues)
		meta.CursorValues = cursorValues
	}
	return meta
}
