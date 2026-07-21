package agg

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/olivere/elastic/v7"
)

const (
	elasticRootAggregation   = "_querybuilder_aggregate"
	elasticBucketAggregation = "_querybuilder_groups"
	elasticOrderAggregation  = "_querybuilder_order"
	elasticCompositePageSize = 5000
)

// ElasticSearchBuilder 用于构建 Elasticsearch 聚合查询
// 泛型参数:
//
//	A: 聚合结果行 DTO 类型
type ElasticSearchBuilder[A any] struct {
	base[A]
	index  string
	filter elastic.Query
}

// NewElasticSearchBuilder 创建 Elasticsearch 聚合查询构建器
// 泛型参数:
//
//	A: 聚合结果行 DTO 类型
func NewElasticSearchBuilder[A any](
	data *core.DBProxy,
	index string,
) *ElasticSearchBuilder[A] {
	b := &ElasticSearchBuilder[A]{index: index}
	b.data = data
	b.dataSource = core.ElasticSearch
	b.needTotal = true
	b.setSelf(b)
	return b
}

// SetFilter 设置 Elasticsearch 数据过滤条件
func (b *ElasticSearchBuilder[A]) SetFilter(filter elastic.Query) *ElasticSearchBuilder[A] {
	b.filter = filter
	return b
}

// Clone 返回配置相互隔离的构建器副本
func (b *ElasticSearchBuilder[A]) Clone() *ElasticSearchBuilder[A] {
	cloned := &ElasticSearchBuilder[A]{index: b.index, filter: b.filter}
	b.base.cloneBase(&cloned.base)
	cloned.setSelf(cloned)
	return cloned
}

// Query 执行 Elasticsearch 聚合查询
func (b *ElasticSearchBuilder[A]) Query(ctx context.Context) (*Result[A], error) {
	if err := b.prepareElasticSearch(); err != nil {
		return nil, err
	}

	return b.execute(ctx, func(ctx context.Context) (*Result[A], error) {
		client, err := b.data.ElasticSearchClient()
		if err != nil {
			return nil, err
		}
		if len(b.spec.Groups) > 0 && (b.spec.Start > 0 || b.needTotal || b.needsElasticClientPostProcessing()) {
			return b.queryGroupedWithPagination(ctx, client)
		}

		root := b.buildAggregation()
		searchResult, err := client.Search().
			Index(b.index).
			Size(0).
			Aggregation(elasticRootAggregation, root).
			Do(ctx)
		if err != nil {
			return nil, fmt.Errorf("executing elasticsearch aggregate query: %w", err)
		}
		return b.decodeResult(searchResult)
	})
}

// Explain 返回生成的 Elasticsearch DSL，但不会执行查询
func (b *ElasticSearchBuilder[A]) Explain(context.Context) (string, error) {
	if err := b.prepareElasticSearch(); err != nil {
		return "", err
	}
	rootSource, err := b.buildAggregation().Source()
	if err != nil {
		return "", fmt.Errorf("building elasticsearch aggregate dsl: %w", err)
	}
	payload := map[string]any{
		"index": b.index,
		"size":  0,
		"aggregations": map[string]any{
			elasticRootAggregation: rootSource,
		},
		"pagination": map[string]any{
			"start":       b.spec.Start,
			"limit":       b.spec.Limit,
			"need_total":  b.needTotal,
			"total_limit": b.totalLimit,
		},
		"plan": b.Meta().Plan,
	}
	if b.needsElasticClientPostProcessing() {
		payload["client_post_processing"] = map[string]any{
			"full_scan": b.needsElasticFullClientPostProcessing(),
			"havings":   len(b.spec.Havings) > 0,
			"orders":    len(b.spec.Orders) > 0,
			"start":     b.spec.Start,
			"limit":     b.spec.Limit,
		}
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding elasticsearch aggregate dsl: %w", err)
	}
	return string(encoded), nil
}

// prepareElasticSearch 校验公共配置和 Elasticsearch 索引
func (b *ElasticSearchBuilder[A]) prepareElasticSearch() error {
	if err := b.prepare(); err != nil {
		return err
	}
	if b.index == "" {
		return ErrIndexNotConfigured
	}
	return nil
}

// elasticAggregationOptions 定义一次 Elasticsearch 聚合请求的分页与管道选项
type elasticAggregationOptions struct {
	size            int
	after           map[string]any
	includePipeline bool
}

// buildAggregation 根据聚合规范构建 Elasticsearch filter 和 composite aggregation
func (b *ElasticSearchBuilder[A]) buildAggregation() *elastic.FilterAggregation {
	size := int(b.spec.Limit)
	if b.needsElasticClientPostProcessing() {
		size = elasticCompositePageSize
	}
	return b.buildAggregationWithOptions(elasticAggregationOptions{
		size:            size,
		includePipeline: !b.needsElasticClientPostProcessing(),
	})
}

// buildAggregationWithOptions 根据聚合规范构建可分页的 Elasticsearch 聚合请求
func (b *ElasticSearchBuilder[A]) buildAggregationWithOptions(options elasticAggregationOptions) *elastic.FilterAggregation {
	filter := b.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}
	root := elastic.NewFilterAggregation().Filter(filter)
	if len(b.spec.Groups) == 0 {
		for _, metric := range b.spec.Metrics {
			if metric.Func != Count || isDistinctCount(metric) || metric.Condition != nil {
				root = root.SubAggregation(metric.Alias, elasticMetric(metric))
			}
		}
		return root
	}

	if options.size <= 0 {
		options.size = int(b.spec.Limit)
	}
	sources := make([]elastic.CompositeAggregationValuesSource, 0, len(b.spec.Groups))
	for _, group := range b.spec.Groups {
		order := b.elasticGroupOrder(group)
		sources = append(sources, elasticGroupSource(group, order))
	}
	buckets := elastic.NewCompositeAggregation().
		Sources(sources...).
		Size(options.size)
	if len(options.after) > 0 {
		buckets = buckets.AggregateAfter(options.after)
	}
	for _, metric := range b.spec.Metrics {
		if metric.Func != Count || isDistinctCount(metric) || metric.Condition != nil {
			buckets = buckets.SubAggregation(metric.Alias, elasticMetric(metric))
		}
	}
	if options.includePipeline {
		for i, having := range b.spec.Havings {
			buckets = buckets.SubAggregation(elasticHavingAggregationName(i), b.elasticHavingAggregation(having))
		}
		if b.elasticOrdersNeedClientPostProcessing() {
			buckets = buckets.SubAggregation(elasticOrderAggregation, b.elasticBucketSortAggregation())
		}
	}
	return root.SubAggregation(elasticBucketAggregation, buckets)
}

// elasticMetric 将通用指标转换为 Elasticsearch 指标聚合
func elasticMetric(metric Metric) elastic.Aggregation {
	if metric.Condition != nil {
		filter := elastic.NewFilterAggregation().Filter(elasticConditionQuery(*metric.Condition))
		if metricAggregation := elasticBaseMetric(metric); metricAggregation != nil {
			filter = filter.SubAggregation(metric.Alias, metricAggregation)
		}
		return filter
	}
	return elasticBaseMetric(metric)
}

// elasticBaseMetric 将无条件指标转换为 Elasticsearch 指标聚合
func elasticBaseMetric(metric Metric) elastic.Aggregation {
	switch metric.Func {
	case Count:
		if isDistinctCount(metric) {
			return elastic.NewCardinalityAggregation().Field(metric.Field)
		}
	case Sum:
		if isDistinctSum(metric) {
			return elasticSumDistinctMetric(metric)
		}
		return elastic.NewSumAggregation().Field(metric.Field)
	case Avg:
		return elastic.NewAvgAggregation().Field(metric.Field)
	case Min:
		return elastic.NewMinAggregation().Field(metric.Field)
	case Max:
		return elastic.NewMaxAggregation().Field(metric.Field)
	}
	return nil
}

// elasticSumDistinctMetric 构建去重求和脚本聚合
func elasticSumDistinctMetric(metric Metric) elastic.Aggregation {
	return elastic.NewScriptedMetricAggregation().
		InitScript(elastic.NewScript("state.values = [:]")).
		MapScript(elastic.NewScript("if (doc.containsKey(params.field) && !doc[params.field].empty) { def value = doc[params.field].value; state.values[value] = true; }")).
		CombineScript(elastic.NewScript("return state.values")).
		ReduceScript(elastic.NewScript("double sum = 0; def values = [:]; for (state in states) { if (state == null) { continue; } for (entry in state.entrySet()) { def value = entry.getKey(); if (value != null && !values.containsKey(value)) { values[value] = true; sum += value; } } } return sum")).
		Params(map[string]any{"field": metric.Field})
}

// elasticConditionQuery 将通用字段条件转换为 Elasticsearch 查询
func elasticConditionQuery(condition Condition) elastic.Query {
	switch condition.Op {
	case Eq:
		return elastic.NewTermQuery(condition.Field, condition.Value)
	case Ne:
		return elastic.NewBoolQuery().Must(elastic.NewExistsQuery(condition.Field)).MustNot(elastic.NewTermQuery(condition.Field, condition.Value))
	case Gt:
		return elastic.NewRangeQuery(condition.Field).Gt(condition.Value)
	case Gte:
		return elastic.NewRangeQuery(condition.Field).Gte(condition.Value)
	case Lt:
		return elastic.NewRangeQuery(condition.Field).Lt(condition.Value)
	case Lte:
		return elastic.NewRangeQuery(condition.Field).Lte(condition.Value)
	case In:
		values, _ := conditionListValues(condition.Value)
		return elastic.NewTermsQuery(condition.Field, values...)
	case NotIn:
		values, _ := conditionListValues(condition.Value)
		return elastic.NewBoolQuery().Must(elastic.NewExistsQuery(condition.Field)).MustNot(elastic.NewTermsQuery(condition.Field, values...))
	case Between:
		start, end, _ := conditionRangeValues(condition.Value)
		return elastic.NewRangeQuery(condition.Field).Gte(start).Lte(end)
	case Exists, IsNotNull:
		return elastic.NewExistsQuery(condition.Field)
	case NotExists, IsNull:
		return elastic.NewBoolQuery().MustNot(elastic.NewExistsQuery(condition.Field))
	case Like:
		return elastic.NewWildcardQuery(condition.Field, sqlLikePatternToWildcard(condition.Value.(string)))
	case NotLike:
		return elastic.NewBoolQuery().Must(elastic.NewExistsQuery(condition.Field)).MustNot(elastic.NewWildcardQuery(condition.Field, sqlLikePatternToWildcard(condition.Value.(string))))
	default:
		return elastic.NewTermQuery(condition.Field, condition.Value)
	}
}

// elasticGroupOrder 返回 composite 分组源使用的排序方向
func (b *ElasticSearchBuilder[A]) elasticGroupOrder(group Group) string {
	descending := group.Descending
	for _, order := range b.spec.Orders {
		if strings.EqualFold(order.Alias, group.Alias) {
			descending = order.Descending
			break
		}
	}
	if descending {
		return "desc"
	}
	return "asc"
}

// elasticBucketSortAggregation 构建用于显式结果排序的 bucket_sort 聚合
func (b *ElasticSearchBuilder[A]) elasticBucketSortAggregation() elastic.Aggregation {
	bucketSort := elastic.NewBucketSortAggregation().Size(int(b.spec.Limit))
	for _, order := range b.spec.Orders {
		bucketSort = bucketSort.Sort(b.elasticBucketSortField(order.Alias), !order.Descending)
	}
	return bucketSort
}

// elasticBucketSortField 返回 bucket_sort 引用的分组键或指标路径
func (b *ElasticSearchBuilder[A]) elasticBucketSortField(alias string) string {
	if b.hasGroupAlias(alias) {
		return "_key." + alias
	}
	return b.elasticHavingBucketPath(alias)
}

// hasGroupAlias 判断别名是否引用分组输出
func (b *ElasticSearchBuilder[A]) hasGroupAlias(alias string) bool {
	for _, group := range b.spec.Groups {
		if strings.EqualFold(group.Alias, alias) {
			return true
		}
	}
	return false
}

// elasticHavingAggregation 构建用于 HAVING 过滤的 bucket_selector 聚合
func (b *ElasticSearchBuilder[A]) elasticHavingAggregation(having Having) elastic.Aggregation {
	return elastic.NewBucketSelectorAggregation().
		AddBucketsPath("value", b.elasticHavingBucketPath(having.Alias)).
		Script(elastic.NewScript("params.value "+elasticScriptOperator(having.Op)+" params.threshold").Param("threshold", having.Value))
}

// elasticHavingBucketPath 返回 bucket_selector 引用的指标路径
func (b *ElasticSearchBuilder[A]) elasticHavingBucketPath(alias string) string {
	metric, ok := b.metricByAlias(alias)
	if !ok {
		return alias
	}
	if metric.Func == Count && !isDistinctCount(metric) {
		if metric.Condition != nil {
			return alias + ">_count"
		}
		return "_count"
	}
	if metric.Condition != nil {
		return alias + ">" + alias
	}
	return alias
}

// metricByAlias 按别名查找指标配置
func (b *ElasticSearchBuilder[A]) metricByAlias(alias string) (Metric, bool) {
	for _, metric := range b.spec.Metrics {
		if strings.EqualFold(metric.Alias, alias) {
			return metric, true
		}
	}
	return Metric{}, false
}

// elasticHavingAggregationName 返回 HAVING pipeline 聚合的内部名称
func elasticHavingAggregationName(index int) string {
	return fmt.Sprintf("_having_%d", index)
}

// elasticScriptOperator 将通用比较操作符转换为 painless 脚本操作符
func elasticScriptOperator(op Operator) string {
	switch op {
	case Eq:
		return "=="
	case Ne:
		return "!="
	case Gt:
		return ">"
	case Gte:
		return ">="
	case Lt:
		return "<"
	case Lte:
		return "<="
	default:
		return "=="
	}
}

// needsElasticClientPostProcessing 判断是否需要在客户端完成完整分组后的后处理
func (b *ElasticSearchBuilder[A]) needsElasticClientPostProcessing() bool {
	return len(b.spec.Groups) > 0 && (len(b.spec.Havings) > 0 || b.needsElasticFullClientPostProcessing())
}

// needsElasticFullClientPostProcessing 判断是否必须读取完整 bucket 集合后才能排序或截断
func (b *ElasticSearchBuilder[A]) needsElasticFullClientPostProcessing() bool {
	return len(b.spec.Groups) > 0 && b.elasticOrdersNeedClientPostProcessing()
}

// elasticOrdersNeedClientPostProcessing 判断显式排序是否无法由 composite source 顺序表达
func (b *ElasticSearchBuilder[A]) elasticOrdersNeedClientPostProcessing() bool {
	return elasticOrdersNeedClientPostProcessingSpec(b.spec)
}

// queryGroupedWithPagination 分页读取 composite bucket，并按需要统计聚合后的分组行数
func (b *ElasticSearchBuilder[A]) queryGroupedWithPagination(ctx context.Context, client *elastic.Client) (*Result[A], error) {
	if b.needsElasticFullClientPostProcessing() {
		return b.queryGroupedWithFullPostProcessing(ctx, client)
	}
	return b.queryGroupedInCompositeOrder(ctx, client)
}

// queryGroupedWithFullPostProcessing 读取完整 bucket 集合后执行 HAVING、排序和分页
func (b *ElasticSearchBuilder[A]) queryGroupedWithFullPostProcessing(ctx context.Context, client *elastic.Client) (*Result[A], error) {
	rowValues := make([]map[string]any, 0)
	var after map[string]any
	for {
		values, nextAfter, hasMore, err := b.fetchElasticCompositeRowValues(ctx, client, after)
		if err != nil {
			return nil, err
		}
		rowValues = append(rowValues, values...)
		if !hasMore {
			break
		}
		after = nextAfter
	}

	rowValues = b.postProcessElasticRowValues(rowValues)
	total := b.elasticTotal(len(rowValues))
	pageValues := b.paginateElasticRowValues(rowValues)
	result, err := decodeElasticRows[A](pageValues)
	if err != nil {
		return nil, err
	}
	result.Total = total
	return result, nil
}

// queryGroupedInCompositeOrder 按 composite 顺序跳过 offset 并收集当前页
func (b *ElasticSearchBuilder[A]) queryGroupedInCompositeOrder(ctx context.Context, client *elastic.Client) (*Result[A], error) {
	pageValues := make([]map[string]any, 0, int(b.spec.Limit))
	var total int64
	var seen uint64
	start := uint64(b.spec.Start)
	limit := int(b.spec.Limit)
	var after map[string]any

	for {
		values, nextAfter, hasMore, err := b.fetchElasticCompositeRowValues(ctx, client, after)
		if err != nil {
			return nil, err
		}
		values = b.filterElasticRowValues(values)
		for _, row := range values {
			if b.shouldCountElasticTotal(total) {
				total++
			}
			if seen >= start && len(pageValues) < limit {
				pageValues = append(pageValues, row)
			}
			seen++
		}
		if len(pageValues) >= limit && b.elasticTotalSatisfied(total) {
			break
		}
		if !hasMore {
			break
		}
		after = nextAfter
	}

	result, err := decodeElasticRows[A](pageValues)
	if err != nil {
		return nil, err
	}
	if b.needTotal {
		result.Total = total
	}
	return result, nil
}

// fetchElasticCompositeRowValues 读取一页 composite bucket 并解码为中间行值
func (b *ElasticSearchBuilder[A]) fetchElasticCompositeRowValues(
	ctx context.Context,
	client *elastic.Client,
	after map[string]any,
) ([]map[string]any, map[string]any, bool, error) {
	root := b.buildAggregationWithOptions(elasticAggregationOptions{
		size:  elasticCompositePageSize,
		after: after,
	})
	searchResult, err := client.Search().
		Index(b.index).
		Size(0).
		Aggregation(elasticRootAggregation, root).
		Do(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("executing elasticsearch aggregate query: %w", err)
	}

	buckets, err := b.decodeCompositeBuckets(searchResult)
	if err != nil {
		return nil, nil, false, err
	}
	values, err := b.decodeCompositeRowValues(buckets.Buckets)
	if err != nil {
		return nil, nil, false, err
	}
	hasMore := len(buckets.Buckets) > 0 && len(buckets.AfterKey) > 0
	return values, buckets.AfterKey, hasMore, nil
}

// shouldCountElasticTotal 判断是否仍需增加 bounded total 计数
func (b *ElasticSearchBuilder[A]) shouldCountElasticTotal(total int64) bool {
	return b.needTotal && (b.totalLimit == 0 || total < int64(b.totalLimit))
}

// elasticTotalSatisfied 判断 total 统计是否已满足当前配置
func (b *ElasticSearchBuilder[A]) elasticTotalSatisfied(total int64) bool {
	return !b.needTotal || (b.totalLimit > 0 && total >= int64(b.totalLimit))
}

// elasticTotal 返回按 totalLimit 截断后的 total 值
func (b *ElasticSearchBuilder[A]) elasticTotal(rowCount int) int64 {
	if !b.needTotal {
		return 0
	}
	total := int64(rowCount)
	if b.totalLimit > 0 && total > int64(b.totalLimit) {
		return int64(b.totalLimit)
	}
	return total
}

// postProcessElasticRowValues 在完整分组结果上执行 HAVING 和排序
func (b *ElasticSearchBuilder[A]) postProcessElasticRowValues(rows []map[string]any) []map[string]any {
	rows = b.filterElasticRowValues(rows)
	b.sortElasticRowValues(rows)
	return rows
}

// paginateElasticRowValues 对已经完成过滤和排序的聚合结果应用偏移分页
func (b *ElasticSearchBuilder[A]) paginateElasticRowValues(rows []map[string]any) []map[string]any {
	start := int(b.spec.Start)
	if start >= len(rows) {
		return nil
	}
	end := min(start+int(b.spec.Limit), len(rows))
	return rows[start:end]
}

// filterElasticRowValues 执行聚合后的 HAVING 过滤
func (b *ElasticSearchBuilder[A]) filterElasticRowValues(rows []map[string]any) []map[string]any {
	if len(b.spec.Havings) == 0 {
		return rows
	}
	filtered := rows[:0]
	for _, row := range rows {
		if b.elasticRowMatchesHavings(row) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

// elasticRowMatchesHavings 判断一行聚合结果是否满足全部 HAVING 条件
func (b *ElasticSearchBuilder[A]) elasticRowMatchesHavings(row map[string]any) bool {
	for _, having := range b.spec.Havings {
		if !elasticCompareNumeric(row[having.Alias], having.Op, having.Value) {
			return false
		}
	}
	return true
}

// sortElasticRowValues 按显式排序规则排序聚合结果
func (b *ElasticSearchBuilder[A]) sortElasticRowValues(rows []map[string]any) {
	if len(b.spec.Orders) == 0 {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, order := range b.spec.Orders {
			comparison := elasticCompareValues(rows[i][order.Alias], rows[j][order.Alias])
			if comparison == 0 {
				continue
			}
			if order.Descending {
				return comparison > 0
			}
			return comparison < 0
		}
		return false
	})
}

// elasticCompareNumeric 按通用操作符比较两个数值
func elasticCompareNumeric(left any, op Operator, right any) bool {
	leftNumber, ok := elasticNumber(left)
	if !ok {
		return false
	}
	rightNumber, ok := elasticNumber(right)
	if !ok {
		return false
	}
	switch op {
	case Eq:
		return leftNumber == rightNumber
	case Ne:
		return leftNumber != rightNumber
	case Gt:
		return leftNumber > rightNumber
	case Gte:
		return leftNumber >= rightNumber
	case Lt:
		return leftNumber < rightNumber
	case Lte:
		return leftNumber <= rightNumber
	default:
		return false
	}
}

// elasticCompareValues 返回排序比较结果，数值按数值比，其他值按字符串比
func elasticCompareValues(left any, right any) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	leftNumber, leftOK := elasticNumber(left)
	rightNumber, rightOK := elasticNumber(right)
	if leftOK && rightOK {
		switch {
		case leftNumber < rightNumber:
			return -1
		case leftNumber > rightNumber:
			return 1
		default:
			return 0
		}
	}
	leftString := fmt.Sprint(left)
	rightString := fmt.Sprint(right)
	switch {
	case leftString < rightString:
		return -1
	case leftString > rightString:
		return 1
	default:
		return 0
	}
}

// elasticNumber 将常见数值类型转换为 float64 以便 HAVING 和排序比较
func elasticNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

// decodeResult 将一次 Elasticsearch 聚合响应解码为类型安全的结果，并对该次响应返回的
// buckets 做收尾式的分页与总数统计。
//
// 注意：真正的多页遍历与精确总数由上层 queryGroupedWithCompositeOrder /
// queryGroupedWithFullPostProcessing 负责，它们不会调用本方法。在本方法被常规 Query
// 路径直接调用的简单分支（Start==0 且 !needTotal 且无需客户端后处理）下，本方法尾部的
// elasticTotal / paginateElasticRowValues 实为 no-op：单次请求的 buckets 已经按 size 收口，
// 且 needTotal 为 false 时 Total 恒为 0。本方法主要是为直接构造 SearchResult 的单测服务。
func (b *ElasticSearchBuilder[A]) decodeResult(searchResult *elastic.SearchResult) (*Result[A], error) {
	root, err := elasticRootBucket(searchResult)
	if err != nil {
		return nil, err
	}

	if len(b.spec.Groups) == 0 {
		row, err := b.decodeRow(nil, root.DocCount, root.Aggregations)
		if err != nil {
			return nil, err
		}
		return &Result[A]{Rows: []*A{row}, Total: 1}, nil
	}

	buckets, err := b.decodeCompositeBuckets(searchResult)
	if err != nil {
		return nil, err
	}
	rowValues, err := b.decodeCompositeRowValues(buckets.Buckets)
	if err != nil {
		return nil, err
	}
	if b.needsElasticClientPostProcessing() {
		rowValues = b.postProcessElasticRowValues(rowValues)
	}

	total := b.elasticTotal(len(rowValues))
	rowValues = b.paginateElasticRowValues(rowValues)
	result, err := decodeElasticRows[A](rowValues)
	if err != nil {
		return nil, err
	}
	result.Total = total
	return result, nil
}

// elasticRootBucket 读取根 filter 聚合结果
func elasticRootBucket(searchResult *elastic.SearchResult) (*elastic.AggregationSingleBucket, error) {
	if searchResult == nil {
		return nil, ErrAggEmptyResponse
	}
	root, ok := searchResult.Aggregations.Filter(elasticRootAggregation)
	if !ok || root == nil {
		return nil, ErrAggRootAggMissing
	}
	return root, nil
}

// decodeCompositeBuckets 读取 composite 分组聚合结果
func (b *ElasticSearchBuilder[A]) decodeCompositeBuckets(searchResult *elastic.SearchResult) (*elastic.AggregationBucketCompositeItems, error) {
	root, err := elasticRootBucket(searchResult)
	if err != nil {
		return nil, err
	}
	buckets, ok := root.Aggregations.Composite(elasticBucketAggregation)
	if !ok || buckets == nil {
		return nil, ErrAggGroupAggMissing
	}
	return buckets, nil
}

// decodeCompositeRowValues 将 composite buckets 解码为中间 map 结果
func (b *ElasticSearchBuilder[A]) decodeCompositeRowValues(buckets []*elastic.AggregationBucketCompositeItem) ([]map[string]any, error) {
	rows := make([]map[string]any, 0, len(buckets))
	for _, bucket := range buckets {
		values, err := b.decodeRowValues(bucket.Key, bucket.DocCount, bucket.Aggregations)
		if err != nil {
			return nil, err
		}
		rows = append(rows, values)
	}
	return rows, nil
}

// decodeRow 合并分组键、文档数量和指标值，并解码为调用方结果类型
func (b *ElasticSearchBuilder[A]) decodeRow(
	keys map[string]any,
	docCount int64,
	aggregations elastic.Aggregations,
) (*A, error) {
	values, err := b.decodeRowValues(keys, docCount, aggregations)
	if err != nil {
		return nil, err
	}
	return decodeElasticRow[A](values)
}

// decodeRowValues 合并分组键、文档数量和指标值为中间 map 结果
func (b *ElasticSearchBuilder[A]) decodeRowValues(
	keys map[string]any,
	docCount int64,
	aggregations elastic.Aggregations,
) (map[string]any, error) {
	values := make(map[string]any, len(b.spec.Groups)+len(b.spec.Metrics))
	for _, group := range b.spec.Groups {
		values[group.Alias] = keys[group.Alias]
	}
	for _, metric := range b.spec.Metrics {
		if metric.Func == Count && !isDistinctCount(metric) && metric.Condition == nil {
			values[metric.Alias] = docCount
			continue
		}
		value, err := elasticMetricValue(aggregations, metric)
		if err != nil {
			return nil, err
		}
		values[metric.Alias] = value
	}
	return values, nil
}

// elasticMetricValue 读取单值指标聚合结果
func elasticMetricValue(aggregations elastic.Aggregations, metric Metric) (any, error) {
	if metric.Condition != nil {
		return elasticConditionalMetricValue(aggregations, metric)
	}
	return elasticBaseMetricValue(aggregations, metric)
}

// elasticConditionalMetricValue 读取 filter 聚合包装的条件指标结果
func elasticConditionalMetricValue(aggregations elastic.Aggregations, metric Metric) (any, error) {
	filter, ok := aggregations.Filter(metric.Alias)
	if !ok || filter == nil {
		return nil, fmt.Errorf("%w (alias %q)", ErrAggMetricMissing, metric.Alias)
	}
	if metric.Func == Count && !isDistinctCount(metric) {
		return filter.DocCount, nil
	}
	return elasticBaseMetricValue(filter.Aggregations, metric)
}

// elasticBaseMetricValue 读取无条件指标聚合结果
func elasticBaseMetricValue(aggregations elastic.Aggregations, metric Metric) (any, error) {
	if isDistinctSum(metric) {
		return elasticScriptedMetricValue(aggregations, metric)
	}

	var value *elastic.AggregationValueMetric
	var ok bool
	switch metric.Func {
	case Count:
		if isDistinctCount(metric) {
			value, ok = aggregations.Cardinality(metric.Alias)
		}
	case Sum:
		value, ok = aggregations.Sum(metric.Alias)
	case Avg:
		value, ok = aggregations.Avg(metric.Alias)
	case Min:
		value, ok = aggregations.Min(metric.Alias)
	case Max:
		value, ok = aggregations.Max(metric.Alias)
	}
	if !ok || value == nil {
		return nil, fmt.Errorf("%w (alias %q)", ErrAggMetricMissing, metric.Alias)
	}
	if value.Value == nil {
		if isDistinctCount(metric) {
			return int64(0), nil
		}
		return nil, nil
	}
	if isDistinctCount(metric) {
		return int64(*value.Value), nil
	}
	return *value.Value, nil
}

// elasticScriptedMetricValue 读取脚本指标聚合结果
func elasticScriptedMetricValue(aggregations elastic.Aggregations, metric Metric) (any, error) {
	value, ok := aggregations.ScriptedMetric(metric.Alias)
	if !ok || value == nil {
		return nil, fmt.Errorf("%w (alias %q)", ErrAggMetricMissing, metric.Alias)
	}
	if value.Value == nil {
		return float64(0), nil
	}
	return elasticNumericValue(value.Value, metric.Alias)
}

// elasticNumericValue 将 Elasticsearch 脚本指标结果转换为数值
func elasticNumericValue(value any, alias string) (float64, error) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, fmt.Errorf("decoding elasticsearch aggregate result: metric %q value: %w", alias, err)
		}
		return parsed, nil
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case int32:
		return float64(typed), nil
	case uint:
		return float64(typed), nil
	case uint64:
		return float64(typed), nil
	case uint32:
		return float64(typed), nil
	default:
		return 0, fmt.Errorf("decoding elasticsearch aggregate result: metric %q value is not numeric", alias)
	}
}

// decodeElasticRows 通过 JSON 标签将多行聚合值映射到结果 DTO
func decodeElasticRows[A any](values []map[string]any) (*Result[A], error) {
	rows := make([]*A, 0, len(values))
	for _, value := range values {
		row, err := decodeElasticRow[A](value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return &Result[A]{Rows: rows}, nil
}

// decodeElasticRow 通过 JSON 标签将聚合值映射到结果 DTO
// 泛型参数:
//
//	A: 聚合结果行 DTO 类型
func decodeElasticRow[A any](values map[string]any) (*A, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encoding elasticsearch aggregate row: %w", err)
	}
	row := new(A)
	if err := json.Unmarshal(encoded, row); err != nil {
		return nil, fmt.Errorf("decoding elasticsearch aggregate row: %w", err)
	}
	return row, nil
}

// elasticGroupSource 构建 composite 分组源
func elasticGroupSource(group Group, order string) elastic.CompositeAggregationValuesSource {
	if group.Interval == "" {
		return elastic.NewCompositeAggregationTermsValuesSource(group.Alias).
			Field(group.Field).
			MissingBucket(false).
			Order(order)
	}
	source := elastic.NewCompositeAggregationDateHistogramValuesSource(group.Alias).
		Field(group.Field).
		CalendarInterval(string(group.Interval)).
		Format("strict_date_optional_time").
		MissingBucket(false).
		Order(order)
	if group.TimeZone != "" {
		source = source.TimeZone(group.TimeZone)
	}
	return source
}
