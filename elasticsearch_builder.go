package builder

import (
	"context"
	"encoding/json"
	"errors"
	"iter"

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

// SetCursorField 设置游标分页排序字段（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetCursorField(fields ...string) Querier[R] {
	e.builder.SetCursorField(fields...)
	return e
}

// SetCursorValue 设置游标初始值（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) SetCursorValue(values ...any) Querier[R] {
	e.builder.SetCursorValue(values...)
	return e
}

// QueryList 执行 ElasticSearch 查询列表操作
func (e *ElasticSearchBuilder[R]) QueryList(ctx context.Context) ([]*R, int64, error) {
	return e.builder.executeWithMiddlewares(ctx, func(ctx context.Context) ([]*R, int64, error) {
		return e.doQuery(ctx)
	})
}

// QueryCursor 执行 ElasticSearch 游标分页查询，返回迭代器（实现 Querier 接口）
// 使用 ES 的 search_after API 进行分批查询
func (e *ElasticSearchBuilder[R]) QueryCursor(ctx context.Context) iter.Seq2[*R, error] {
	// ES 的 search_after 不使用通用的 buildCursorIterator，因为它需要直接使用 sort values
	return e.builder.executeCursorWithMiddlewares(ctx, e.doCursorQuery)
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
// 若已配置游标字段，将输出游标查询模式的首批查询 DSL
func (e *ElasticSearchBuilder[R]) Explain(ctx context.Context) (string, error) {
	if e.index == "" {
		return "", errors.New("elasticsearch index not configured")
	}

	// 如果配置了游标字段，展示游标查询模式的首批 DSL
	if len(e.builder.cursorFields) > 0 {
		return e.explainCursor(ctx)
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

// buildCursorSortService 为游标查询构建排序配置，应用到 SearchService 上
// 游标字段作为主排序，用户 sort 作为辅助排序，无排序时默认使用 _doc
func (e *ElasticSearchBuilder[R]) buildCursorSortService(searchService *elastic.SearchService) *elastic.SearchService {
	cursorFields := e.builder.cursorFields
	if len(cursorFields) > 0 {
		for _, field := range cursorFields {
			searchService = searchService.SortBy(elastic.NewFieldSort(field).Asc())
		}
	} else if len(e.sort) == 0 {
		// 未设置排序条件时默认使用 _doc 排序以获得最佳性能
		searchService = searchService.SortBy(elastic.NewFieldSort("_doc").Asc())
	}

	// 用户 sort 作为辅助排序
	for _, s := range e.sort {
		searchService = searchService.SortBy(s)
	}

	return searchService
}

// buildCursorSortSources 为游标查询构建排序条件的 Source 列表（用于 Explain）
func (e *ElasticSearchBuilder[R]) buildCursorSortSources() ([]any, error) {
	var sortList []any
	cursorFields := e.builder.cursorFields
	if len(cursorFields) > 0 {
		for _, field := range cursorFields {
			src, _ := elastic.NewFieldSort(field).Asc().Source()
			sortList = append(sortList, src)
		}
	} else if len(e.sort) == 0 {
		src, _ := elastic.NewFieldSort("_doc").Asc().Source()
		sortList = append(sortList, src)
	}
	for _, s := range e.sort {
		src, err := s.Source()
		if err != nil {
			return nil, err
		}
		sortList = append(sortList, src)
	}
	return sortList, nil
}

// explainCursor 返回游标查询模式的首批查询 DSL
func (e *ElasticSearchBuilder[R]) explainCursor(ctx context.Context) (string, error) {
	batchSize := int(e.builder.limit)
	if batchSize < 1 {
		batchSize = defaultLimit
	}

	filter := e.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}

	result := map[string]any{
		"mode":  "cursor",
		"index": e.index,
		"size":  batchSize,
	}

	// 序列化查询条件
	querySource, err := filter.Source()
	if err != nil {
		return "", err
	}
	result["query"] = querySource

	if len(e.builder.fields) > 0 {
		result["_source"] = e.builder.fields
	}

	sortList, err := e.buildCursorSortSources()
	if err != nil {
		return "", err
	}
	if len(sortList) > 0 {
		result["sort"] = sortList
	}

	result["cursor_fields"] = e.builder.cursorFields
	result["search_after"] = "auto (from last hit sort values)"

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// doCursorQuery 执行 ElasticSearch 游标分页的单批次查询
// 使用 search_after API，将上一批最后一条文档的 sort values 作为下一批的 search_after 参数
func (e *ElasticSearchBuilder[R]) doCursorQuery(ctx context.Context, cursorValues []any) ([]*R, []any, error) {
	if e.index == "" {
		return nil, nil, errors.New("elasticsearch index not configured")
	}

	batchSize := int(e.builder.limit)
	if batchSize < 1 {
		batchSize = defaultLimit
	}

	filter := e.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}

	searchService := e.builder.data.ElasticSearch.Search().
		Index(e.index).
		Query(filter).
		Size(batchSize)

	// 应用字段投影
	if len(e.builder.fields) > 0 {
		fsc := elastic.NewFetchSourceContext(true).Include(e.builder.fields...)
		searchService = searchService.FetchSourceContext(fsc)
	}

	// 构建排序
	searchService = e.buildCursorSortService(searchService)

	// 设置 search_after（仅在非首次查询时）
	if len(cursorValues) > 0 {
		searchService = searchService.SearchAfter(cursorValues...)
	}

	searchResult, err := searchService.Do(ctx)
	if err != nil {
		return nil, nil, err
	}

	var list []*R
	for _, hit := range searchResult.Hits.Hits {
		var item R
		if err := json.Unmarshal(hit.Source, &item); err != nil {
			return nil, nil, err
		}
		list = append(list, &item)
	}

	// 从最后一条 hit 的 Sort 字段提取 sort values 作为下一批的 search_after 参数
	var nextCursorValues []any
	if len(searchResult.Hits.Hits) > 0 {
		lastHit := searchResult.Hits.Hits[len(searchResult.Hits.Hits)-1]
		if lastHit.Sort != nil {
			nextCursorValues = make([]any, len(lastHit.Sort))
			for i, v := range lastHit.Sort {
				nextCursorValues[i] = v
			}
		}
	}

	return list, nextCursorValues, nil
}