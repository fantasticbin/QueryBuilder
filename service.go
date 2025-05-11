package builder

import (
	"context"
)

// Service 通用查询服务接口
type Service interface {
	GetFilter(context.Context) (any, error)
	GetSort() any
}

// BaseService 基础查询服务
// 泛型参数:
//
//	R - 返回结果类型参数
//	F - 过滤条件类型参数
//	S - 排序条件类型参数
type BaseService[R any, F any, S any] struct {
	builder builder[R]
	filter  *F
	sort    S
	service Service
}

// QueryList 执行列表查询
func (s *BaseService[R, F, S]) QueryList(ctx context.Context) ([]*R, int64, error) {
	if s.service == nil {
		return nil, 0, nil
	}

	// 执行查询
	return s.builder.
		SetFilter(s.service.GetFilter).
		SetSort(s.service.GetSort).
		QueryList(ctx)
}

// List 查询列表功能结构
// 泛型参数上同
type List[R any, F any, S any] struct {
	strategy    Strategy[R]
	middlewares []Middleware[R]
}

// SetStrategy 设置查询策略
// 支持不同数据源的查询实现，如MySQL、MongoDB等
// 通过该方法可自定义查询策略，已有策略可根据数据源自动选择
func (l *List[R, F, S]) SetStrategy(strategy Strategy[R]) *List[R, F, S] {
	l.strategy = strategy
	return l
}

// Use 添加查询中间件
func (l *List[R, F, S]) Use(middlewares Middleware[R]) *List[R, F, S] {
	l.middlewares = append(l.middlewares, middlewares)
	return l
}

// Query 执行查询
// 该方法会根据传入的Service实例和QueryOption选项执行查询
func (l *List[R, F, S]) Query(
	ctx context.Context,
	s Service,
	opts ...QueryOption[F, S],
) ([]*R, int64, error) {
	options := LoadQueryOptions(opts...)
	service := &BaseService[R, F, S]{
		builder: builder[R]{
			data:           options.GetData(),
			start:          options.GetStart(),
			limit:          options.GetLimit(),
			needTotal:      options.GetNeedTotal(),
			needPagination: options.GetNeedPagination(),
			strategy:       l.strategy,
		},
		filter:  options.GetFilter(),
		sort:    options.GetSort(),
		service: s,
	}

	for _, m := range l.middlewares {
		service.builder.Use(m)
	}

	return service.QueryList(ctx)
}
