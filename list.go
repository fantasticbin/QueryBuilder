package builder

import (
	"context"
	"fmt"
)

// List 查询列表功能结构
// 泛型参数:
//
//	R - 返回结果类型参数
type List[R any] struct {
	dataSource  DataSource
	querier     Querier[R] // 可选：直接注入自定义 Querier（用于测试等场景）
	middlewares []Middleware[R]
	scope       ScopeConfigurer[R] // 可选：构建器配置回调，用于自动设置 filter/sort
}

func NewList[R any]() *List[R] {
	return &List[R]{}
}

// SetDataSource 设置数据源类型
// 支持不同数据源的查询实现，如 MySQL、MongoDB、ElasticSearch
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

// Query 执行查询
// 该方法会根据传入的 QueryOption 选项执行查询
// 通过 DataSource 枚举值自动创建对应的专属查询构建器
// 调用方需在获取具体构建器后自行设置 filter/sort
func (l *List[R]) Query(
	ctx context.Context,
	opts ...QueryOption,
) (result []*R, total int64, err error) {
	// 捕获 NewBuilder 等可能产生的 panic，转换为 error 返回
	defer func() {
		if r := recover(); r != nil {
			result = nil
			total = 0
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
	}

	// 通过 Querier 接口方法直接配置通用参数，无需类型断言
	querier.SetStart(options.GetStart())
	querier.SetLimit(options.GetLimit())
	querier.SetNeedTotal(options.GetNeedTotal())
	querier.SetNeedPagination(options.GetNeedPagination())

	// 应用字段投影
	if fields := options.GetFields(); len(fields) > 0 {
		querier.SetFields(fields...)
	}

	// 应用 Scope 配置回调，自动设置 filter/sort
	if l.scope != nil {
		l.scope(querier)
	}

	// 添加中间件
	for _, m := range l.middlewares {
		querier.Use(m)
	}

	return querier.QueryList(ctx)
}
