package builder

import (
	"context"
	"fmt"
	"iter"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

// List 查询列表功能结构
// 泛型参数:
//
//	R - 返回结果类型参数
type List[R any] struct {
	dataSource  DataSource         // 数据源类型
	querier     Querier[R]         // 可选：直接注入自定义 Querier（用于测试等场景）
	beforeHook  BeforeQueryHook    // 查询前置钩子
	afterHook   AfterQueryHook[R]  // 查询后置钩子
	middlewares []Middleware[R]    // 中间件链
	scope       ScopeConfigurer[R] // 可选：构建器配置回调，用于自动设置 filter/sort
}

func NewList[R any]() *List[R] {
	return &List[R]{}
}

// NewListWithData 通过指定数据源类型和数据实例创建 List
// 内部会预先创建对应的专属查询构建器并持有，后续 Query/QueryCursor 将复用该构建器
// 这种方式下可通过 GetQueryMeta() 在查询执行后获取元信息
func NewListWithData[R any](ds DataSource, data *DBProxy) *List[R] {
	return &List[R]{
		dataSource: ds,
		querier:    NewBuilder[R](ds, data),
	}
}

// SetDataSource 设置数据源类型
// 支持不同数据源的查询实现，如 Gorm、MongoDB、ElasticSearch
// 通过该方法指定数据源类型，查询时将自动创建对应的专属构建器
func (l *List[R]) SetDataSource(ds DataSource) *List[R] {
	l.dataSource = ds
	return l
}

// SetQuerier 直接注入自定义 Querier 实例
// 用于测试场景或需要自定义查询逻辑的场景
// 设置后将忽略 DataSource 配置，直接使用注入的 Querier
func (l *List[R]) SetQuerier(querier Querier[R]) *List[R] {
	l.querier = querier
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

// passQueryOption 传递查询选项
func (l *List[R]) passQueryOption(options BaseQueryListOptions, cursorMode, handleHookAndMiddleware bool) {
	// 配置通用参数
	l.querier.SetStart(options.GetStart())
	l.querier.SetLimit(options.GetLimit())
	l.querier.SetNeedTotal(options.GetNeedTotal())
	l.querier.SetNeedPagination(options.GetNeedPagination())

	// 应用指定字段
	if fields := options.GetFields(); len(fields) > 0 {
		l.querier.SetFields(fields...)
	}

	if cursorMode {
		// 设置游标字段
		if cursorFields := options.GetCursorFields(); len(cursorFields) > 0 {
			l.querier.SetCursorField(cursorFields...)
		}
		// 设置游标初始值
		if cursorValues := options.GetCursorValues(); len(cursorValues) > 0 {
			l.querier.SetCursorValue(cursorValues...)
		}
	}

	// 应用 Scope 配置回调，自动设置 filter/sort
	if l.scope != nil {
		l.scope(l.querier)
	}

	if handleHookAndMiddleware {
		// 设置 Hook
		if l.beforeHook != nil {
			l.querier.SetBeforeQueryHook(l.beforeHook)
		}
		if l.afterHook != nil {
			l.querier.SetAfterQueryHook(l.afterHook)
		}

		// 添加中间件
		for _, m := range l.middlewares {
			l.querier.Use(m)
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
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("query panic recovered: %v", r)
		}
	}()

	options := LoadQueryOptions(opts...)

	var querier Querier[R]
	if l.querier != nil {
		// 使用注入的自定义 Querier
		querier = l.querier
	} else {
		// 通过工厂函数创建对应的专属查询构建器
		querier = NewBuilder[R](l.dataSource, options.GetData())
		l.querier = querier
	}

	// 配置通用参数
	l.passQueryOption(options, false, true)
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
	defer func() {
		if r := recover(); r != nil {
			seq = func(yield func(*R, error) bool) {
				yield(nil, fmt.Errorf("query cursor panic recovered: %v", r))
			}
		}
	}()

	options := LoadQueryOptions(opts...)

	var querier Querier[R]
	if l.querier != nil {
		// 使用注入的自定义 Querier
		querier = l.querier
	} else {
		// 通过工厂函数创建对应的专属查询构建器
		querier = NewBuilder[R](l.dataSource, options.GetData())
		l.querier = querier
	}

	// 配置通用参数
	l.passQueryOption(options, true, true)
	return querier.QueryCursor(ctx)
}

// QueryPage 执行单批次游标分页查询，返回结构化的分页结果
// 该方法会根据传入的 QueryOption 选项执行单批次游标分页查询
// 返回当前页数据、是否有下一页、下一页游标值等信息
func (l *List[R]) QueryPage(
	ctx context.Context,
	opts ...QueryOption,
) (result *core.CursorPageResult[R], err error) {
	// 捕获 NewBuilder 等可能产生的 panic，转换为 error 返回
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("query page panic recovered: %v", r)
		}
	}()

	options := LoadQueryOptions(opts...)

	var querier Querier[R]
	if l.querier != nil {
		// 使用注入的自定义 Querier
		querier = l.querier
	} else {
		// 通过工厂函数创建对应的专属查询构建器
		querier = NewBuilder[R](l.dataSource, options.GetData())
		l.querier = querier
	}

	// 配置通用参数
	l.passQueryOption(options, true, true)
	return querier.QueryPage(ctx)
}

// Explain 返回构建器最终生成的查询语句（Dry Run 模式）
// 用于调试场景，不会实际执行查询
func (l *List[R]) Explain(ctx context.Context, opts ...QueryOption) (result string, err error) {
	// 捕获 NewBuilder 等可能产生的 panic，转换为 error 返回
	defer func() {
		if r := recover(); r != nil {
			result = ""
			err = fmt.Errorf("explain panic recovered: %v", r)
		}
	}()

	options := LoadQueryOptions(opts...)

	var querier Querier[R]
	if l.querier != nil {
		querier = l.querier
	} else {
		querier = NewBuilder[R](l.dataSource, options.GetData())
		l.querier = querier
	}

	// 配置通用参数
	var cursorMode bool
	if len(options.GetCursorFields()) > 0 {
		cursorMode = true
	}
	l.passQueryOption(options, cursorMode, false)

	return querier.Explain(ctx)
}

// GetQueryMeta 返回当前内部构建器的查询元信息快照
// 支持以下场景：
//   - 通过 NewListWithData 创建时，内部预先持有构建器实例
//   - 通过 SetQuerier 注入自定义 Querier 时
//   - 通过 Query/QueryCursor 执行后，内部自动创建的构建器会回填到 List 中
//
// 仅在首次调用 Query/QueryCursor 之前且未设置 Querier 时返回零值
func (l *List[R]) GetQueryMeta() QueryMeta {
	if l.querier != nil {
		return l.querier.GetQueryMeta()
	}
	return QueryMeta{}
}
