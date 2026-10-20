package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/fantasticbin/QueryBuilder/v2/util"
	"github.com/olivere/elastic/v7"
)

const (
	esPITCloseTimeout = 3 * time.Second
	// esMaxResultWindow 关闭分页时 QueryList 单次返回的最大条数
	// 对应 Elasticsearch 索引默认的 index.max_result_window（from + size 上限 10000）
	// 未显式设置 size 时 ES 默认只返回 10 条，因此关闭分页时显式设置为该上限，尽量与 Gorm/Mongo 返回全部数据的行为保持一致
	// 数据量超过 10000 条时请使用 QueryCursor（PIT + search_after）进行全量遍历
	esMaxResultWindow = 10000
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
	cursorPitID  string           // QueryCursor/QueryPage 内部临时 PIT ID
}

// self 返回自身引用，实现 builderInterface 接口
func (e *ElasticSearchBuilder[R]) self() *ElasticSearchBuilder[R] {
	return e
}

// NewElasticSearchBuilder 创建 ElasticSearch 专属查询构建器实例
func NewElasticSearchBuilder[R any](data *core.DBProxy, index string) *ElasticSearchBuilder[R] {
	e := &ElasticSearchBuilder[R]{
		index:        index,
		pitKeepAlive: 0,
	}
	e.builder.data = data
	e.builder.dataSource = core.ElasticSearch
	e.builder.applyQueryConfigDefaults()
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

// SetTotalLimit 设置总数统计上限，0 表示精确统计（实现 Querier 扩展配置）
func (e *ElasticSearchBuilder[R]) SetTotalLimit(totalLimit uint32) Querier[R] {
	e.builder.SetTotalLimit(totalLimit)
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

// SetPITID 设置 Point-in-Time ID，用于跨请求分页场景续查
func (e *ElasticSearchBuilder[R]) SetPITID(pitID string) *ElasticSearchBuilder[R] {
	e.pitID = pitID
	return e
}

// GetQueryMeta 返回当前查询元信息的只读快照（实现 Querier 接口）
func (e *ElasticSearchBuilder[R]) GetQueryMeta() core.QueryMeta {
	return e.builder.GetQueryMeta()
}

// doCursorQuery 执行 ElasticSearch 游标分页的单批次查询
func (e *ElasticSearchBuilder[R]) doCursorQuery(
	ctx context.Context,
	cursorValues []any,
	isFirstBatch bool,
	probeHasMore bool,
) ([]*R, []any, int64, bool, error) {
	if probeHasMore {
		// QueryPage 使用与 GORM/Mongo 相同的初始游标解析（显式 cursorValues 优先，否则 start）
		isFirstBatch = len(cursorValues) == 0
	}
	return e.doElasticCursorQuery(ctx, cursorValues, isFirstBatch, probeHasMore, &e.cursorPitID)
}

// cleanupCursorQuery 清理 QueryCursor/QueryPage 内部自动管理的 PIT
// QueryPage 在 HasMore=true 且成功时保留 PIT，供同一 Builder 实例续页
// QueryCursor 结束时传入 nil result，始终关闭
func (e *ElasticSearchBuilder[R]) cleanupCursorQuery(result *core.CursorPageResult[R], err error) {
	if err == nil && result != nil && result.HasMore {
		return
	}
	e.closePIT(e.cursorPitID)
	e.cursorPitID = ""
}

// QueryPageWithPIT 执行基于 PIT + search_after 的单批次分页查询
// 该方法仅关注 ES 对接语义：接收/返回 pitID 与 cursorValues，便于业务层自行封装分页协议
func (e *ElasticSearchBuilder[R]) QueryPageWithPIT(ctx context.Context) (*core.ESPITPageResult[R], error) {
	e.builder.beginQueryMode(true)
	e.builder.isPITQuery = true
	defer func() {
		e.builder.isPITQuery = false
		e.builder.finishCursorQuery()
	}()

	if err := e.builder.prepareAndValidate(); err != nil {
		return nil, err
	}
	if e.index == "" {
		return nil, ErrESIndexNotConfigured
	}
	if len(e.builder.cursorValues) > 0 && e.pitID == "" {
		return nil, ErrPITCursorWithoutPITID
	}

	oldNeedPagination := e.builder.needPagination
	oldPitID := e.pitID
	defer func() {
		e.builder.needPagination = oldNeedPagination
		e.pitID = oldPitID
	}()

	// PIT + search_after 本质是分页查询，确保 QueryMeta 和执行语义一致
	e.builder.needPagination = true
	isFirstBatch := len(e.builder.cursorValues) == 0

	var resultPitID string
	chainResult, err := newMiddlewareContext[R](&e.builder).executeWithMiddlewares(ctx, func(ctx context.Context) (core.Result[R], error) {
		batchList, batchNextCursorValues, batchTotal, batchHasMore, queryErr := e.doElasticCursorQuery(
			ctx,
			e.builder.cursorValues,
			isFirstBatch,
			true,
			&e.pitID,
		)
		if queryErr != nil {
			return nil, queryErr
		}
		resultPitID = e.pitID

		return &core.CursorPageResult[R]{
			Items:            batchList,
			Total:            resolveQueryTotal(e.builder.needTotal, len(batchList), batchTotal),
			HasMore:          batchHasMore,
			NextCursorValues: batchNextCursorValues,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	pageResult := cursorPageResultFromResult(chainResult)
	if pageResult == nil {
		pageResult = &core.CursorPageResult[R]{}
	}

	result := &core.ESPITPageResult[R]{
		CursorPageResult: core.CursorPageResult[R]{
			Items:            pageResult.Items,
			Total:            pageResult.Total,
			HasMore:          pageResult.HasMore,
			NextCursorValues: pageResult.NextCursorValues,
		},
		PitID: resultPitID,
	}

	if !result.HasMore {
		result.PitID = ""
		result.NextCursorValues = nil
	}

	return result, nil
}

// doQuery 执行实际的 ElasticSearch 查询逻辑
func (e *ElasticSearchBuilder[R]) doQuery(ctx context.Context) (list []*R, total int64, err error) {
	// 检查 Elasticsearch 索引配置
	if e.index == "" {
		return nil, 0, ErrESIndexNotConfigured
	}
	client, err := e.builder.data.ElasticSearchClient()
	if err != nil {
		return nil, 0, err
	}

	filter := e.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}

	// 并行执行数据查询和总数统计操作，任一失败时取消同组任务
	if err = util.WaitAndGoWithContext(ctx, func(ctx context.Context) error {
		searchService := client.Search().
			Index(e.index).
			Query(filter)

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
			searchService = searchService.From(int(e.builder.start)).Size(int(effectiveLimit(e.builder.limit)))
		} else {
			// 关闭分页时与 Gorm 一致忽略 start/limit，显式设置 size 避免 ES 默认只返回 10 条
			searchService = searchService.Size(esMaxResultWindow)
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
	}, func(ctx context.Context) error {
		if !e.builder.needTotal {
			return nil
		}

		count, err := e.countTotal(ctx, filter)
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

// countTotal 执行 Elasticsearch 总数统计；配置 totalLimit 时使用 track_total_hits 上限统计
func (e *ElasticSearchBuilder[R]) countTotal(ctx context.Context, filter elastic.Query) (int64, error) {
	client, err := e.builder.data.ElasticSearchClient()
	if err != nil {
		return 0, err
	}

	if e.builder.totalLimit == 0 {
		return client.Count().
			Index(e.index).
			Query(filter).
			Do(ctx)
	}

	searchResult, err := client.Search().
		Index(e.index).
		Query(filter).
		Size(0).
		TrackTotalHits(int(e.builder.totalLimit)).
		Do(ctx)
	if err != nil {
		return 0, err
	}
	if searchResult == nil || searchResult.Hits == nil || searchResult.Hits.TotalHits == nil {
		return 0, nil
	}
	return searchResult.Hits.TotalHits.Value, nil
}

// Explain 返回 ElasticSearch 构建器最终生成的查询 DSL（Dry Run 模式）
// 用于调试场景，不会实际执行查询
// 若已配置游标字段，将输出游标查询模式的首批查询 DSL
func (e *ElasticSearchBuilder[R]) Explain(ctx context.Context) (string, error) {
	if e.index == "" {
		return "", ErrESIndexNotConfigured
	}

	// 如果配置了游标字段，展示游标查询模式的首批 DSL
	if len(e.builder.cursorFields) > 0 {
		return e.explainCursor(ctx)
	}

	filter := e.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}

	result := map[string]any{
		"index": e.index,
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
		result["from"] = e.builder.start
		result["size"] = effectiveLimit(e.builder.limit)
	} else {
		result["size"] = esMaxResultWindow
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
		for _, cursorField := range e.builder.getParsedCursorFields() {
			fs := elastic.NewFieldSort(cursorField.Field).Asc()
			if !cursorField.Asc {
				fs = fs.Desc()
			}
			searchService = searchService.SortBy(fs)
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

// cursorSortKeyCount 返回游标查询中显式声明的排序键数量
// 与 buildCursorSortService 保持一致：游标字段 + 用户辅助排序，二者皆空时为默认的 _doc
// PIT 查询下 ES 会在此之后自动追加隐式 _shard_doc tiebreaker，其 sort 值不计入该数量
func (e *ElasticSearchBuilder[R]) cursorSortKeyCount() int {
	n := len(e.builder.cursorFields) + len(e.sort)
	if n == 0 {
		return 1
	}
	return n
}

// resolveCursorPosition 根据游标值数量决定续查定位方式
//   - 游标值数量 >= 显式排序键数量（游标字段 + 辅助排序；内部续查时可能额外带隐式 _shard_doc）：
//     与 sort 一一对齐，直接作为 search_after
//   - 存在辅助排序且仅提供游标字段数量的值（如 SetCursorValue(lastID) 或 SetStart 转换的单字段游标）：
//     search_after 要求与 sort 等长无法直接使用，转为"严格位于游标字段元组之后"的过滤条件，
//     语义与 Gorm 行值比较 / Mongo $gt 复合条件一致，要求游标字段组合唯一；缺失游标字段的文档不会被返回
//
// 返回用于搜索的 query（count 仍使用原始 filter）与 search_after 值
func (e *ElasticSearchBuilder[R]) resolveCursorPosition(filter elastic.Query, cursorValues []any) (elastic.Query, []any, error) {
	if len(cursorValues) == 0 {
		return filter, nil, nil
	}
	cursorFields := e.builder.getParsedCursorFields()
	if len(e.sort) == 0 || len(cursorFields) == 0 || len(cursorValues) != len(cursorFields) {
		return filter, cursorValues, nil
	}

	afterQuery, err := buildESCursorAfterQuery(cursorFields, cursorValues)
	if err != nil {
		return nil, nil, err
	}
	// 使用 must 保留用户 filter 的相关性评分（辅助排序可能为 _score），游标条件放入 filter 上下文
	return elastic.NewBoolQuery().Must(filter).Filter(afterQuery), nil, nil
}

// esPseudoSortFields 无法用于 range/term 过滤的 ES 排序伪字段
var esPseudoSortFields = map[string]struct{}{"_shard_doc": {}, "_doc": {}, "_score": {}}

// buildESCursorAfterQuery 构建"严格位于游标元组之后"的词典序过滤条件
// 形如 (f0 > v0) OR (f0 = v0 AND f1 > v1) ...，降序字段使用 <
func buildESCursorAfterQuery(fields []cursorSortField, values []any) (elastic.Query, error) {
	clauses := make([]elastic.Query, 0, len(fields))
	for i, field := range fields {
		if _, pseudo := esPseudoSortFields[field.Field]; pseudo {
			return nil, fmt.Errorf("%w: cursor field %q cannot be used as a filter; "+
				"pass the full NextCursorValues (cursor fields + auxiliary sorts)", ErrCursorMismatch, field.Field)
		}
		rangeQuery := elastic.NewRangeQuery(field.Field)
		if field.Asc {
			rangeQuery = rangeQuery.Gt(values[i])
		} else {
			rangeQuery = rangeQuery.Lt(values[i])
		}
		if i == 0 {
			clauses = append(clauses, rangeQuery)
			continue
		}
		clause := elastic.NewBoolQuery()
		for j := range i {
			clause = clause.Filter(elastic.NewTermQuery(fields[j].Field, values[j]))
		}
		clauses = append(clauses, clause.Filter(rangeQuery))
	}
	if len(clauses) == 1 {
		return clauses[0], nil
	}
	return elastic.NewBoolQuery().Should(clauses...).MinimumNumberShouldMatch(1), nil
}

// trimImplicitTiebreaker 裁剪 sort values 中 ES PIT 自动追加的隐式 _shard_doc tiebreaker
// 使 QueryPage / QueryPageWithPIT 返回的 NextCursorValues 与显式声明的排序键一一对应，
// 可直接通过 SetCursorValue 传回续查。若用户已显式声明 _shard_doc，ES 不会再追加，此处为 no-op
// 注意：裁剪后续页仅依赖显式排序键定位，排序键组合须唯一（如业务时间 + 唯一 ID），否则同值记录可能被跳过
func trimImplicitTiebreaker(values []any, keyCount int) []any {
	if keyCount <= 0 || len(values) <= keyCount {
		return values
	}
	return values[:keyCount]
}

// buildCursorSortSources 为游标查询构建排序条件的 Source 列表（用于 Explain）
func (e *ElasticSearchBuilder[R]) buildCursorSortSources() ([]any, error) {
	var sortList []any
	cursorFields := e.builder.cursorFields
	if len(cursorFields) > 0 {
		for _, cursorField := range e.builder.getParsedCursorFields() {
			fs := elastic.NewFieldSort(cursorField.Field).Asc()
			if !cursorField.Asc {
				fs = fs.Desc()
			}
			src, _ := fs.Source()
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
	batchSize := int(effectiveLimit(e.builder.limit))

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

// pitKeepAliveString 将 PIT keep alive 配置转换为 Elasticsearch 接受的时间字符串
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

// closePIT 关闭 Point-in-Time，释放 ES 资源
func (e *ElasticSearchBuilder[R]) closePIT(pitID string) {
	if pitID == "" {
		return
	}
	client, err := e.builder.data.ElasticSearchClient()
	if err != nil {
		return
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), esPITCloseTimeout)
	defer cancel()
	_, _ = client.ClosePointInTime(pitID).Do(closeCtx)
}

// doElasticCursorQuery 执行 ElasticSearch 游标分页的单批次查询
// 使用 search_after API，将上一批最后一条文档的 sort values 作为下一批的 search_after 参数
// isFirstBatch 为 true 时，若 needTotal 也为 true，则并行执行 Count 查询
func (e *ElasticSearchBuilder[R]) doElasticCursorQuery(
	ctx context.Context,
	cursorValues []any,
	isFirstBatch, forcePIT bool,
	pitID *string,
) (list []*R, nextCursorValues []any, total int64, hasMore bool, err error) {
	if e.index == "" {
		return nil, nil, 0, false, ErrESIndexNotConfigured
	}
	client, err := e.builder.data.ElasticSearchClient()
	if err != nil {
		return nil, nil, 0, false, err
	}

	batchSize := int(effectiveLimit(e.builder.limit))

	filter := e.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}

	querySize := batchSize
	if forcePIT {
		// PIT 分页场景通过多取 1 条记录来判断是否还有下一页，避免 len(list)==limit 时误判
		querySize = batchSize + 1
	}

	// 解析游标定位方式：完整 sort values 走 search_after；辅助排序存在且仅给出游标字段值时转为过滤条件
	searchQuery, searchAfter, err := e.resolveCursorPosition(filter, cursorValues)
	if err != nil {
		return nil, nil, 0, false, err
	}

	searchService := client.Search().
		Query(searchQuery).
		Size(querySize)

	usePIT := forcePIT || !e.builder.needPagination
	openedPITByThisCall := false

	defer func() {
		if err != nil && openedPITByThisCall && pitID != nil && *pitID != "" {
			e.closePIT(*pitID)
			*pitID = ""
		}
	}()

	if usePIT {
		if pitID == nil {
			return nil, nil, 0, false, ErrPITIDNil
		}
		if *pitID == "" {
			openResp, err := client.OpenPointInTime(e.index).
				KeepAlive(e.pitKeepAliveString()).
				Do(ctx)
			if err != nil {
				return nil, nil, 0, false, err
			}
			*pitID = openResp.Id
			openedPITByThisCall = true
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

	// 设置 search_after（仅在非首次查询且游标值与排序键对齐时）
	if len(searchAfter) > 0 {
		searchService = searchService.SearchAfter(searchAfter...)
	}

	var searchResult *elastic.SearchResult
	if err = util.WaitAndGoWithContext(ctx, func(ctx context.Context) error {
		var err error
		searchResult, err = searchService.Do(ctx)
		if err != nil {
			return err
		}

		if usePIT && *pitID != "" && searchResult.PitId != "" {
			*pitID = searchResult.PitId
		}

		return nil
	}, func(ctx context.Context) error {
		// 首批次且需要总数时，并行执行数据查询和 Count 查询
		if !isFirstBatch || !e.builder.needTotal {
			return nil
		}

		count, err := e.countTotal(ctx, filter)
		if err != nil {
			return err
		}

		total = count
		return nil
	}); err != nil {
		return nil, nil, 0, false, err
	}

	hits := searchResult.Hits.Hits
	hasMore = forcePIT && len(hits) > batchSize

	effectiveHits := hits
	if hasMore {
		effectiveHits = hits[:batchSize]
	}

	list = make([]*R, 0, len(effectiveHits))
	for _, hit := range effectiveHits {
		var item R
		if err := json.Unmarshal(hit.Source, &item); err != nil {
			return nil, nil, 0, false, err
		}
		list = append(list, &item)
	}

	// 从最后一条有效 hit 的 Sort 字段提取 sort values 作为下一批的 search_after 参数
	if len(effectiveHits) > 0 {
		lastHit := effectiveHits[len(effectiveHits)-1]
		if lastHit.Sort != nil {
			nextCursorValues = make([]any, len(lastHit.Sort))
			for i, v := range lastHit.Sort {
				nextCursorValues[i] = v
			}
		}
	}

	// 单页 API（QueryPage / QueryPageWithPIT）返回给调用方的游标需可直接 SetCursorValue 传回，
	// 因此裁剪隐式 tiebreaker；QueryCursor 全量遍历在内部续查，保留完整 sort values 以保证同值记录不丢失
	if forcePIT {
		nextCursorValues = trimImplicitTiebreaker(nextCursorValues, e.cursorSortKeyCount())
	}

	if forcePIT && !hasMore && *pitID != "" {
		e.closePIT(*pitID)
		*pitID = ""
	}

	return list, nextCursorValues, total, hasMore, nil
}
