package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/fantasticbin/QueryBuilder/util"
	"github.com/olivere/elastic/v7"
)

// ElasticSearchBuilder ElasticSearch 专属查询构建器
// 泛型参数:
//
//	R: 查询结果的实体类型
type ElasticSearchBuilder[R any] struct {
	builder[*ElasticSearchBuilder[R], R]
	index        string           // ES 索引名，仅 ElasticSearch 构建器专属
	filter       elastic.Query    // ES 专属过滤条件
	sort         []elastic.Sorter // ES 专属排序条件
	pitKeepAlive time.Duration    // Point-in-Time 保持时间
	pitID        string           // 外部传入/内部更新的 PIT ID（用于跨请求分页）
}

// ESPITPageResult ES PIT 分页查询结果。
// PitID 与 CursorValues 可用于下一批查询。
type ESPITPageResult[R any] struct {
	List         []*R
	Total        int64
	HasMore      bool
	PitID        string
	CursorValues []any
}

// self 返回自身引用，实现 builderInterface 接口
func (e *ElasticSearchBuilder[R]) self() *ElasticSearchBuilder[R] {
	return e
}

// NewElasticSearchBuilder 创建 ElasticSearch 专属查询构建器实例
func NewElasticSearchBuilder[R any](data *DBProxy, index string) *ElasticSearchBuilder[R] {
	e := &ElasticSearchBuilder[R]{
		index:        index,
		pitKeepAlive: 0,
	}
	e.builder.data = data
	e.builder.dataSource = ElasticSearch
	e.builder.limit = defaultLimit
	e.builder.setSelf(e, e)
	return e
}

// Clone 复制当前 ElasticSearchBuilder 的查询配置，返回一个独立的新实例
// 新实例与原实例状态隔离，修改互不影响，适用于并发分叉查询场景
// 注意：原 ElasticSearchBuilder 非并发安全，请勿在多 goroutine 中共享同一实例进行写操作
func (e *ElasticSearchBuilder[R]) Clone() *ElasticSearchBuilder[R] {
	cloned := &ElasticSearchBuilder[R]{
		index:        e.index,
		filter:       e.filter,
		pitKeepAlive: e.pitKeepAlive,
		pitID:        e.pitID,
	}
	e.builder.cloneBase(&cloned.builder)
	cloned.builder.setSelf(cloned, cloned)

	// 深拷贝 sort 切片
	if e.sort != nil {
		cloned.sort = make([]elastic.Sorter, len(e.sort))
		copy(cloned.sort, e.sort)
	}
	return cloned
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

// SetPitKeepAlive 设置 Point-in-Time 查询的 keep alive 时间
func (e *ElasticSearchBuilder[R]) SetPitKeepAlive(keepAlive time.Duration) *ElasticSearchBuilder[R] {
	e.pitKeepAlive = keepAlive
	return e
}

// SetPITID 设置 Point-in-Time ID，用于跨请求分页场景续查。
func (e *ElasticSearchBuilder[R]) SetPITID(pitID string) *ElasticSearchBuilder[R] {
	e.pitID = pitID
	return e
}

// GetQueryMeta 返回当前查询元信息的只读快照（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) GetQueryMeta() QueryMeta {
	return e.builder.GetQueryMeta()
}

// QueryList 执行 ElasticSearch 查询列表操作
func (e *ElasticSearchBuilder[R]) QueryList(ctx context.Context) ([]*R, int64, error) {
	if err := e.builder.prepareAndValidate(); err != nil {
		return nil, 0, err
	}
	return e.builder.executeWithMiddlewares(ctx, func(ctx context.Context) ([]*R, int64, error) {
		return e.doQuery(ctx)
	})
}

// QueryCursor 执行 ElasticSearch 游标分页查询，返回迭代器（实现 Querier 接口）
// 使用 ES 的 search_after API 进行分批查询
func (e *ElasticSearchBuilder[R]) QueryCursor(ctx context.Context) iter.Seq2[*R, error] {
	if err := e.builder.prepareAndValidate(); err != nil {
		return func(yield func(*R, error) bool) {
			yield(nil, err)
		}
	}
	// ES 的 search_after 不使用通用的 buildCursorIterator，因为它需要直接使用 sort values
	var pitID string
	wrappedCursorQuery := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*R, []any, int64, error) {
		list, nextCursorValues, total, _, err := e.doCursorQuery(ctx, cursorValues, isFirstBatch, &pitID, false)
		return list, nextCursorValues, total, err
	}
	innerIter := e.builder.executeCursorWithMiddlewares(ctx, wrappedCursorQuery)
	return func(yield func(*R, error) bool) {
		defer func() {
			e.closePIT(pitID)
		}()

		for item, err := range innerIter {
			if !yield(item, err) {
				return
			}
		}
	}
}

// QueryPageWithPIT 执行基于 PIT + search_after 的单批次分页查询。
// 该方法仅关注 ES 对接语义：接收/返回 pitID 与 cursorValues，便于业务层自行封装分页协议。
func (e *ElasticSearchBuilder[R]) QueryPageWithPIT(ctx context.Context) (*ESPITPageResult[R], error) {
	if err := e.builder.prepareAndValidate(); err != nil {
		return nil, err
	}
	if e.index == "" {
		return nil, errors.New("elasticsearch index not configured")
	}

	isFirstBatch := len(e.builder.cursorValues) == 0
	list, nextCursorValues, total, hasMore, err := e.doCursorQuery(ctx, e.builder.cursorValues, isFirstBatch, &e.pitID, true)
	if err != nil {
		return nil, err
	}

	result := &ESPITPageResult[R]{
		List:         list,
		Total:        total,
		HasMore:      hasMore,
		PitID:        e.pitID,
		CursorValues: nextCursorValues,
	}
	if !hasMore {
		result.CursorValues = nil
	}

	return result, nil
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
			if e.builder.limit == 0 {
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
		if e.builder.limit == 0 {
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
	if batchSize == 0 {
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

func (e *ElasticSearchBuilder[R]) pitKeepAliveString() string {
	d := e.pitKeepAlive
	if d <= 0 {
		d = time.Minute
	}

	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return fmt.Sprintf("%dms", int(d/time.Millisecond))
}

func (e *ElasticSearchBuilder[R]) closePIT(pitID string) {
	if e.builder.needPagination || pitID == "" {
		return
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = e.builder.data.ElasticSearch.ClosePointInTime(pitID).Do(closeCtx)
}

// doCursorQuery 执行 ElasticSearch 游标分页的单批次查询
// 使用 search_after API，将上一批最后一条文档的 sort values 作为下一批的 search_after 参数
// isFirstBatch 为 true 时，若 needTotal 也为 true，则并行执行 Count 查询
func (e *ElasticSearchBuilder[R]) doCursorQuery(
	ctx context.Context,
	cursorValues []any,
	isFirstBatch bool,
	pitID *string,
	forcePIT bool,
) ([]*R, []any, int64, bool, error) {
	if e.index == "" {
		return nil, nil, 0, false, errors.New("elasticsearch index not configured")
	}

	batchSize := int(e.builder.limit)
	if batchSize == 0 {
		batchSize = defaultLimit
	}

	filter := e.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}

	querySize := batchSize
	if forcePIT {
		// PIT 分页场景通过多取 1 条记录来判断是否还有下一页，避免 len(list)==limit 时误判。
		querySize = batchSize + 1
	}
	searchService := e.builder.data.ElasticSearch.Search().
		Query(filter).
		Size(querySize)

	usePIT := forcePIT || !e.builder.needPagination
	if usePIT {
		if *pitID == "" {
			openResp, err := e.builder.data.ElasticSearch.OpenPointInTime(e.index).
				KeepAlive(e.pitKeepAliveString()).
				Do(ctx)
			if err != nil {
				return nil, nil, 0, false, err
			}
			*pitID = openResp.Id
		}
		searchService = searchService.PointInTime(elastic.NewPointInTimeWithKeepAlive(*pitID, e.pitKeepAliveString()))
	} else {
		searchService = searchService.Index(e.index)
	}

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

	var list []*R
	var total int64
	var searchResult *elastic.SearchResult

	if err := util.WaitAndGo(func() error {
		var err error
		searchResult, err = searchService.Do(ctx)
		if err != nil {
			return err
		}
		if usePIT && *pitID != "" && searchResult.PitId != "" {
			*pitID = searchResult.PitId
		}

		for _, hit := range searchResult.Hits.Hits {
			var item R
			if err := json.Unmarshal(hit.Source, &item); err != nil {
				return err
			}
			list = append(list, &item)
		}
		return nil
	}, func() error {
		// 首批次且需要总数时，并行执行数据查询和 Count 查询
		if !isFirstBatch || !e.builder.needTotal {
			return nil
		}

		countService := e.builder.data.ElasticSearch.Count().
			Index(e.index).
			Query(filter)
		count, err := countService.Do(ctx)
		if err != nil {
			return err
		}
		total = count
		return nil
	}); err != nil {
		return nil, nil, 0, false, err
	}

	hasMore := forcePIT && len(searchResult.Hits.Hits) > batchSize
	effectiveHits := searchResult.Hits.Hits
	if hasMore {
		effectiveHits = effectiveHits[:batchSize]
		list = list[:batchSize]
	}

	// 从最后一条 hit 的 Sort 字段提取 sort values 作为下一批的 search_after 参数
	var nextCursorValues []any
	if len(effectiveHits) > 0 {
		lastHit := effectiveHits[len(effectiveHits)-1]
		if lastHit.Sort != nil {
			nextCursorValues = make([]any, len(lastHit.Sort))
			for i, v := range lastHit.Sort {
				nextCursorValues[i] = v
			}
		}
	}

	return list, nextCursorValues, total, hasMore, nil
}
