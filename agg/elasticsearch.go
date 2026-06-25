package agg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/olivere/elastic/v7"
)

const (
	elasticRootAggregation   = "_querybuilder_aggregate"
	elasticBucketAggregation = "_querybuilder_groups"
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

// buildAggregation 根据聚合规范构建 Elasticsearch filter 和 composite aggregation
func (b *ElasticSearchBuilder[A]) buildAggregation() *elastic.FilterAggregation {
	filter := b.filter
	if filter == nil {
		filter = elastic.NewMatchAllQuery()
	}
	root := elastic.NewFilterAggregation().Filter(filter)
	if len(b.spec.Groups) == 0 {
		for _, metric := range b.spec.Metrics {
			if metric.Func != Count || isDistinctCount(metric) {
				root = root.SubAggregation(metric.Alias, elasticMetric(metric))
			}
		}
		return root
	}

	sources := make([]elastic.CompositeAggregationValuesSource, 0, len(b.spec.Groups))
	for _, group := range b.spec.Groups {
		order := "asc"
		if group.Descending {
			order = "desc"
		}
		sources = append(
			sources,
			elastic.NewCompositeAggregationTermsValuesSource(group.Alias).
				Field(group.Field).
				MissingBucket(false).
				Order(order),
		)
	}
	buckets := elastic.NewCompositeAggregation().
		Sources(sources...).
		Size(int(b.spec.Limit))
	for _, metric := range b.spec.Metrics {
		if metric.Func != Count || isDistinctCount(metric) {
			buckets = buckets.SubAggregation(metric.Alias, elasticMetric(metric))
		}
	}
	return root.SubAggregation(elasticBucketAggregation, buckets)
}

// elasticMetric 将通用指标转换为 Elasticsearch 指标聚合
func elasticMetric(metric Metric) elastic.Aggregation {
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
		Params(map[string]interface{}{"field": metric.Field})
}

// decodeResult 将 Elasticsearch 聚合响应解码为类型安全的结果
func (b *ElasticSearchBuilder[A]) decodeResult(searchResult *elastic.SearchResult) (*Result[A], error) {
	if searchResult == nil {
		return nil, errors.New("decoding elasticsearch aggregate result: empty response")
	}
	root, ok := searchResult.Aggregations.Filter(elasticRootAggregation)
	if !ok || root == nil {
		return nil, errors.New("decoding elasticsearch aggregate result: root aggregation missing")
	}

	if len(b.spec.Groups) == 0 {
		row, err := b.decodeRow(nil, root.DocCount, root.Aggregations)
		if err != nil {
			return nil, err
		}
		return &Result[A]{Rows: []*A{row}}, nil
	}

	buckets, ok := root.Aggregations.Composite(elasticBucketAggregation)
	if !ok || buckets == nil {
		return nil, errors.New("decoding elasticsearch aggregate result: group aggregation missing")
	}
	rows := make([]*A, 0, len(buckets.Buckets))
	for _, bucket := range buckets.Buckets {
		row, err := b.decodeRow(bucket.Key, bucket.DocCount, bucket.Aggregations)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return &Result[A]{Rows: rows}, nil
}

// decodeRow 合并分组键、文档数量和指标值，并解码为调用方结果类型
func (b *ElasticSearchBuilder[A]) decodeRow(
	keys map[string]any,
	docCount int64,
	aggregations elastic.Aggregations,
) (*A, error) {
	values := make(map[string]any, len(b.spec.Groups)+len(b.spec.Metrics))
	for _, group := range b.spec.Groups {
		values[group.Alias] = keys[group.Alias]
	}
	for _, metric := range b.spec.Metrics {
		if metric.Func == Count && !isDistinctCount(metric) {
			values[metric.Alias] = docCount
			continue
		}
		value, err := elasticMetricValue(aggregations, metric)
		if err != nil {
			return nil, err
		}
		values[metric.Alias] = value
	}
	return decodeElasticRow[A](values)
}

// elasticMetricValue 读取单值指标聚合结果
func elasticMetricValue(aggregations elastic.Aggregations, metric Metric) (any, error) {
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
		return nil, fmt.Errorf("decoding elasticsearch aggregate result: metric %q missing", metric.Alias)
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
		return nil, fmt.Errorf("decoding elasticsearch aggregate result: metric %q missing", metric.Alias)
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
