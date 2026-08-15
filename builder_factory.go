package builder

import (
	"context"
	"fmt"
	"iter"
	"sync"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

// builderFactory 将任意结果类型的构造函数收成统一存储形态
type builderFactory func(data *core.DBProxy) any

var customBuilderFactories sync.Map

// RegisterBuilder 为非内置数据源注册查询构建器工厂
// 自定义源需要同时 RegisterAdapter（提供客户端）和 RegisterBuilder（提供 Querier），NewBuilder 才能真正执行查询
// 对 Gorm / MongoDB / ElasticSearch 调用会 panic，避免覆盖内置实现
// factory 为 nil 时删除该数据源已注册的工厂，便于测试复位
func RegisterBuilder[R any](ds core.DataSource, factory func(*core.DBProxy) Querier[R]) {
	if isBuiltInQueryDataSource(ds) {
		panic("builder: RegisterBuilder cannot override a built-in data source")
	}
	if factory == nil {
		customBuilderFactories.Delete(ds)
		return
	}
	customBuilderFactories.Store(ds, builderFactory(func(data *core.DBProxy) any {
		return factory(data)
	}))
}

// lookupCustomBuilder 查找并实例化自定义数据源的构建器
func lookupCustomBuilder[R any](ds core.DataSource, data *core.DBProxy) (Querier[R], bool) {
	value, ok := customBuilderFactories.Load(ds)
	if !ok {
		return nil, false
	}
	factory, ok := value.(builderFactory)
	if !ok || factory == nil {
		return nil, false
	}
	querier, ok := factory(data).(Querier[R])
	return querier, ok
}

// isBuiltInQueryDataSource 判断数据源是否已有内置 Builder
func isBuiltInQueryDataSource(ds core.DataSource) bool {
	switch ds {
	case Gorm, MongoDB, ElasticSearch:
		return true
	default:
		return false
	}
}

// unsupportedQuerier 表示没有内置实现、也未 RegisterBuilder 的数据源
// 配置方法保持可链式调用；执行方法返回 ErrDataSourceInvalid
type unsupportedQuerier[R any] struct {
	dataSource core.DataSource
}

func (q unsupportedQuerier[R]) err() error {
	return fmt.Errorf("%w: %s (%d)", ErrDataSourceInvalid, q.dataSource.String(), q.dataSource)
}

func (q unsupportedQuerier[R]) Use(Middleware[R]) Querier[R]                   { return q }
func (q unsupportedQuerier[R]) SetStart(uint32) Querier[R]                     { return q }
func (q unsupportedQuerier[R]) SetLimit(uint32) Querier[R]                     { return q }
func (q unsupportedQuerier[R]) SetNeedTotal(bool) Querier[R]                   { return q }
func (q unsupportedQuerier[R]) SetTotalLimit(uint32) Querier[R]                { return q }
func (q unsupportedQuerier[R]) SetNeedPagination(bool) Querier[R]              { return q }
func (q unsupportedQuerier[R]) SetFields(...string) Querier[R]                 { return q }
func (q unsupportedQuerier[R]) SetBeforeQueryHook(BeforeQueryHook) Querier[R]  { return q }
func (q unsupportedQuerier[R]) SetAfterQueryHook(AfterQueryHook[R]) Querier[R] { return q }
func (q unsupportedQuerier[R]) SetCursorField(...string) Querier[R]            { return q }
func (q unsupportedQuerier[R]) SetCursorValue(...any) Querier[R]               { return q }

func (q unsupportedQuerier[R]) GetQueryMeta() core.QueryMeta {
	return core.QueryMeta{DataSource: q.dataSource}
}

func (q unsupportedQuerier[R]) QueryList(context.Context) (*core.ListResult[R], error) {
	return nil, q.err()
}

func (q unsupportedQuerier[R]) QueryCursor(context.Context) iter.Seq2[*R, error] {
	return func(yield func(*R, error) bool) {
		yield(nil, q.err())
	}
}

func (q unsupportedQuerier[R]) QueryPage(context.Context) (*core.CursorPageResult[R], error) {
	return nil, q.err()
}

func (q unsupportedQuerier[R]) Explain(context.Context) (string, error) {
	return "", q.err()
}
