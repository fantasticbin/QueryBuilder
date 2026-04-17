package builder

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fantasticbin/QueryBuilder/util"
	"github.com/olivere/elastic/v7"
)

// ElasticSearchBuilder ElasticSearch 专属查询构建器
// 泛型参数:
//
//	R: 查询结果的实体类型
type ElasticSearchBuilder[R any] struct {
	builder[*ElasticSearchBuilder[R], R]
	index  string           // ES 索引名，仅 ElasticSearch 构建器专属
	filter elastic.Query    // ES 专属过滤条件
	sort   []elastic.Sorter // ES 专属排序条件
}

// self 返回自身引用，实现 builderInterface 接口
func (e *ElasticSearchBuilder[R]) self() *ElasticSearchBuilder[R] {
	return e
}

// NewElasticSearchBuilder 创建 ElasticSearch 专属查询构建器实例
func NewElasticSearchBuilder[R any](data *DBProxy, index string) *ElasticSearchBuilder[R] {
	e := &ElasticSearchBuilder[R]{
		index: index,
	}
	e.builder.data = data
	e.builder.dataSource = ElasticSearch
	e.builder.setSelf(e, e)
	return e
}

// SetESIndex 设置 Elasticsearch 索引名（仅 ElasticSearch 构建器专属方法）
// 返回 *ElasticSearchBuilder[R]，支持链式调用
func (e *ElasticSearchBuilder[R]) SetESIndex(index string) *ElasticSearchBuilder[R] {
	e.index = index
	return e
}

// SetFilter 设置 ElasticSearch 过滤条件
func (e *ElasticSearchBuilder[R]) SetFilter(filter elastic.Query) *ElasticSearchBuilder[R] {
	e.filter = filter
	return e
}

// SetSort 设置 ElasticSearch 排序条件
func (e *ElasticSearchBuilder[R]) SetSort(sort ...elastic.Sorter) *ElasticSearchBuilder[R] {
	e.sort = sort
	return e
}

// Use 添加中间件（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) Use(middleware Middleware[R]) Querier[R] {
	e.builder.Use(middleware)
	return e
}

// SetStart 设置分页起始位置（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetStart(start uint32) Querier[R] {
	e.builder.SetStart(start)
	return e
}

// SetLimit 设置每页数据条数（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetLimit(limit uint32) Querier[R] {
	e.builder.SetLimit(limit)
	return e
}

// SetNeedTotal 设置是否需要查询总数（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetNeedTotal(needTotal bool) Querier[R] {
	e.builder.SetNeedTotal(needTotal)
	return e
}

// SetNeedPagination 设置是否需要分页（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetNeedPagination(needPagination bool) Querier[R] {
	e.builder.SetNeedPagination(needPagination)
	return e
}

// SetFields 设置查询字段投影（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetFields(fields ...string) Querier[R] {
	e.builder.SetFields(fields...)
	return e
}

// SetBeforeQueryHook 设置查询前置钩子（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetBeforeQueryHook(hook BeforeQueryHook) Querier[R] {
	e.builder.SetBeforeQueryHook(hook)
	return e
}

// SetAfterQueryHook 设置查询后置钩子（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetAfterQueryHook(hook AfterQueryHook[R]) Querier[R] {
	e.builder.SetAfterQueryHook(hook)
	return e
}

// QueryList 执行 ElasticSearch 查询列表操作
func (e *ElasticSearchBuilder[R]) QueryList(ctx context.Context) ([]*R, int64, error) {
	return e.builder.executeWithMiddlewares(ctx, func(ctx context.Context) ([]*R, int64, error) {
		return e.doQuery(ctx)
	})
}

// doQuery 执行实际的 ElasticSearch 查询逻辑
func (e *ElasticSearchBuilder[R]) doQuery(ctx context.Context) (list []*R, total int64, err error) {
	// 检查 Elasticsearch 索引配置
	if e.index == "" {
		return nil, 0, errors.New("elasticsearch index not configured")
	}

	if e.filter == nil {
		e.filter = elastic.NewMatchAllQuery()
	}

	// 使用 WaitAndGo 并行执行数据查询和总数统计操作
	if err = util.WaitAndGo(func() error {
		searchService := e.builder.data.ElasticSearch.Search().
			Index(e.index).
			Query(e.filter)

		// 应用字段投影
		if len(e.builder.fields) > 0 {
			fsc := elastic.NewFetchSourceContext(true).Include(e.builder.fields...)
			searchService = searchService.FetchSourceContext(fsc)
		}

		// 处理排序
		for _, s := range e.sort {
			searchService = searchService.SortBy(s)
		}

		if e.builder.needPagination {
			if e.builder.limit < 1 {
				e.builder.limit = defaultLimit
			}
			searchService = searchService.From(int(e.builder.start)).Size(int(e.builder.limit))
		}

		searchResult, err := searchService.Do(ctx)
		if err != nil {
			return err
		}

		// 解析查询结果
		for _, hit := range searchResult.Hits.Hits {
			var item R
			if err := json.Unmarshal(hit.Source, &item); err != nil {
				return err
			}
			list = append(list, &item)
		}

		return nil
	}, func() error {
		if !e.builder.needTotal {
			return nil
		}

		countService := e.builder.data.ElasticSearch.Count().
			Index(e.index).
			Query(e.filter)
		count, err := countService.Do(ctx)
		if err != nil {
			return err
		}

		total = count

		return nil
	}); err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// Explain 返回 ElasticSearch 构建器最终生成的查询 DSL（Dry Run 模式）
// 用于调试场景，不会实际执行查询
func (e *ElasticSearchBuilder[R]) Explain(ctx context.Context) (string, error) {
	if e.index == "" {
		return "", errors.New("elasticsearch index not configured")
	}

	if e.filter == nil {
		e.filter = elastic.NewMatchAllQuery()
	}

	result := map[string]any{
		"index": e.index,
	}

	// 序列化查询条件
	querySource, err := e.filter.Source()
	if err != nil {
		return "", err
	}
	result["query"] = querySource

	if len(e.builder.fields) > 0 {
		result["_source"] = e.builder.fields
	}

	if len(e.sort) > 0 {
		var sortList []any
		for _, s := range e.sort {
			src, err := s.Source()
			if err != nil {
				return "", err
			}
			sortList = append(sortList, src)
		}
		result["sort"] = sortList
	}

	if e.builder.needPagination {
		if e.builder.limit < 1 {
			e.builder.limit = defaultLimit
		}
		result["from"] = e.builder.start
		result["size"] = e.builder.limit
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}