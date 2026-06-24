package agg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// MongoFilter 表示有序的 MongoDB 过滤文档
type MongoFilter = bson.D

// MongoBuilder 用于构建 MongoDB 聚合管道
type MongoBuilder[A any] struct {
	base[A]
	filter MongoFilter
}

const (
	mongoBaseFacet = "_base"
	mongoRowsField = "_rows"
)

// NewMongoBuilder 创建 MongoDB 聚合查询构建器
func NewMongoBuilder[A any](data *core.DBProxy, spec Spec) *MongoBuilder[A] {
	b := &MongoBuilder[A]{}
	b.data = data
	b.dataSource = core.MongoDB
	b.spec = normalizeSpec(spec)
	b.setSelf(b)
	return b
}

// SetFilter 设置 MongoDB 数据过滤条件
func (b *MongoBuilder[A]) SetFilter(filter MongoFilter) *MongoBuilder[A] {
	b.filter = cloneBSONDocument(filter)
	return b
}

// Clone 返回配置相互隔离的构建器副本
func (b *MongoBuilder[A]) Clone() *MongoBuilder[A] {
	cloned := &MongoBuilder[A]{filter: cloneBSONDocument(b.filter)}
	b.base.cloneBase(&cloned.base)
	cloned.setSelf(cloned)
	return cloned
}

// Query 执行 MongoDB 聚合管道
func (b *MongoBuilder[A]) Query(ctx context.Context) (*Result[A], error) {
	if err := b.prepare(); err != nil {
		return nil, err
	}

	return b.execute(ctx, func(ctx context.Context) (*Result[A], error) {
		collection, err := b.data.MongoCollection()
		if err != nil {
			return nil, err
		}

		cursor, err := collection.Aggregate(ctx, b.buildPipeline())
		if err != nil {
			return nil, fmt.Errorf("executing mongodb aggregate query: %w", err)
		}
		rows := make([]*A, 0)
		if err := cursor.All(ctx, &rows); err != nil {
			return nil, fmt.Errorf("decoding mongodb aggregate result: %w", err)
		}
		if len(b.spec.Groups) == 0 && len(rows) == 0 {
			rows = append(rows, new(A))
		}
		return &Result[A]{Rows: rows}, nil
	})
}

// Explain 返回生成的 MongoDB 聚合管道，但不会执行查询
func (b *MongoBuilder[A]) Explain(context.Context) (string, error) {
	if err := b.prepare(); err != nil {
		return "", err
	}

	encoded, err := bson.MarshalExtJSON(
		bson.D{{Key: "pipeline", Value: b.buildPipeline()}},
		true,
		false,
	)
	if err != nil {
		return "", fmt.Errorf("encoding mongodb aggregate pipeline: %w", err)
	}
	var formatted any
	if err := json.Unmarshal(encoded, &formatted); err != nil {
		return "", fmt.Errorf("formatting mongodb aggregate pipeline: %w", err)
	}
	pretty, err := json.MarshalIndent(formatted, "", "  ")
	if err != nil {
		return "", fmt.Errorf("formatting mongodb aggregate pipeline: %w", err)
	}
	return string(pretty), nil
}

// buildPipeline 根据聚合规范构建 MongoDB pipeline
func (b *MongoBuilder[A]) buildPipeline() mongo.Pipeline {
	if b.hasDistinctMetric() {
		return b.buildDistinctPipeline()
	}
	return b.buildSimplePipeline()
}

// buildSimplePipeline 构建不含去重指标的单阶段统计管道
func (b *MongoBuilder[A]) buildSimplePipeline() mongo.Pipeline {
	pipeline := make(mongo.Pipeline, 0, 5)
	if match := b.buildMatch(); len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}

	pipeline = append(
		pipeline,
		bson.D{{Key: "$group", Value: b.buildMetricGroupStage(b.buildSourceGroupID(), b.spec.Metrics)}},
		bson.D{{Key: "$project", Value: b.buildProjection(b.spec.Metrics)}},
	)
	return b.appendSortAndLimit(pipeline)
}

// buildDistinctPipeline 构建包含去重指标的精确统计管道
func (b *MongoBuilder[A]) buildDistinctPipeline() mongo.Pipeline {
	pipeline := make(mongo.Pipeline, 0, 9)
	if match := b.buildMatch(); len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}

	facets := bson.D{{Key: mongoBaseFacet, Value: b.buildBaseFacetPipeline()}}
	facetArrays := bson.A{"$" + mongoBaseFacet}
	for index, metric := range b.spec.Metrics {
		if !isDistinctMetric(metric) {
			continue
		}
		facetName := fmt.Sprintf("_distinct_%d", index)
		facets = append(facets, bson.E{Key: facetName, Value: b.buildDistinctMetricFacetPipeline(metric)})
		facetArrays = append(facetArrays, "$"+facetName)
	}

	pipeline = append(
		pipeline,
		bson.D{{Key: "$facet", Value: facets}},
		bson.D{{Key: "$project", Value: bson.D{{Key: mongoRowsField, Value: bson.D{{Key: "$concatArrays", Value: facetArrays}}}}}},
		bson.D{{Key: "$unwind", Value: "$" + mongoRowsField}},
		bson.D{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$" + mongoRowsField}}}},
	)

	mergeGroup := bson.D{{Key: "_id", Value: b.buildOutputGroupID()}}
	for _, metric := range b.spec.Metrics {
		value := any("$" + metric.Alias)
		operator := "$max"
		if isDistinctMetric(metric) {
			value = bson.D{{Key: "$ifNull", Value: bson.A{"$" + metric.Alias, 0}}}
			operator = "$sum"
		}
		mergeGroup = append(mergeGroup, bson.E{
			Key: metric.Alias,
			Value: bson.D{{
				Key:   operator,
				Value: value,
			}},
		})
	}

	pipeline = append(
		pipeline,
		bson.D{{Key: "$group", Value: mergeGroup}},
		bson.D{{Key: "$project", Value: b.buildProjection(b.spec.Metrics)}},
	)
	return b.appendSortAndLimit(pipeline)
}

// buildBaseFacetPipeline 构建保留原始统计语义的基础分支
func (b *MongoBuilder[A]) buildBaseFacetPipeline() mongo.Pipeline {
	metrics := make([]Metric, 0, len(b.spec.Metrics))
	for _, metric := range b.spec.Metrics {
		if !isDistinctMetric(metric) {
			metrics = append(metrics, metric)
		}
	}
	return mongo.Pipeline{
		bson.D{{Key: "$group", Value: b.buildMetricGroupStage(b.buildSourceGroupID(), metrics)}},
		bson.D{{Key: "$project", Value: b.buildProjection(metrics)}},
	}
}

// buildDistinctMetricFacetPipeline 构建单个去重指标分支的两阶段分组
func (b *MongoBuilder[A]) buildDistinctMetricFacetPipeline(metric Metric) mongo.Pipeline {
	firstGroupID := any(bson.D{{Key: "value", Value: "$" + metric.Field}})
	secondGroupID := any(nil)
	if len(b.spec.Groups) > 0 {
		firstGroupID = bson.D{
			{Key: "group", Value: b.buildSourceGroupID()},
			{Key: "value", Value: "$" + metric.Field},
		}
		secondGroupID = "$_id.group"
	}

	accumulator := bson.D{{Key: "$sum", Value: 1}}
	if isDistinctSum(metric) {
		accumulator = bson.D{{Key: "$sum", Value: "$_id.value"}}
	}

	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: b.buildMetricMatch(metric)}},
		bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: firstGroupID}}}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: secondGroupID},
			{Key: metric.Alias, Value: accumulator},
		}}},
		bson.D{{Key: "$project", Value: b.buildProjection([]Metric{metric})}},
	}
}

// buildMetricGroupStage 构建普通指标使用的 MongoDB $group 内容
func (b *MongoBuilder[A]) buildMetricGroupStage(groupID any, metrics []Metric) bson.D {
	groupStage := bson.D{{Key: "_id", Value: groupID}}
	for _, metric := range metrics {
		if isDistinctMetric(metric) {
			continue
		}
		operator := "$" + metric.Func.String()
		value := any(1)
		if metric.Func == Count {
			operator = "$sum"
		} else {
			value = "$" + metric.Field
		}
		groupStage = append(groupStage, bson.E{
			Key: metric.Alias,
			Value: bson.D{{
				Key:   operator,
				Value: value,
			}},
		})
	}
	return groupStage
}

// buildSourceGroupID 构建基于原始文档字段的分组键
func (b *MongoBuilder[A]) buildSourceGroupID() any {
	if len(b.spec.Groups) == 0 {
		return nil
	}
	keys := make(bson.D, 0, len(b.spec.Groups))
	for _, group := range b.spec.Groups {
		keys = append(keys, bson.E{Key: group.Alias, Value: "$" + group.Field})
	}
	return keys
}

// buildOutputGroupID 构建基于中间结果字段的分组键
func (b *MongoBuilder[A]) buildOutputGroupID() any {
	if len(b.spec.Groups) == 0 {
		return nil
	}
	keys := make(bson.D, 0, len(b.spec.Groups))
	for _, group := range b.spec.Groups {
		keys = append(keys, bson.E{Key: group.Alias, Value: "$" + group.Alias})
	}
	return keys
}

// buildProjection 构建把分组键和指标别名展开到结果行的投影
func (b *MongoBuilder[A]) buildProjection(metrics []Metric) bson.D {
	projection := bson.D{{Key: "_id", Value: 0}}
	for _, group := range b.spec.Groups {
		projection = append(projection, bson.E{Key: group.Alias, Value: "$_id." + group.Alias})
	}
	for _, metric := range metrics {
		projection = append(projection, bson.E{Key: metric.Alias, Value: 1})
	}
	return projection
}

// buildMetricMatch 构建单个指标字段的非空过滤条件
func (b *MongoBuilder[A]) buildMetricMatch(metric Metric) bson.D {
	return bson.D{{
		Key: metric.Field,
		Value: bson.D{
			{Key: "$exists", Value: true},
			{Key: "$ne", Value: nil},
		},
	}}
}

// appendSortAndLimit 为分组结果追加排序和数量限制
func (b *MongoBuilder[A]) appendSortAndLimit(pipeline mongo.Pipeline) mongo.Pipeline {
	if len(b.spec.Groups) == 0 {
		return pipeline
	}
	sort := make(bson.D, 0, len(b.spec.Groups))
	for _, group := range b.spec.Groups {
		direction := 1
		if group.Descending {
			direction = -1
		}
		sort = append(sort, bson.E{Key: group.Alias, Value: direction})
	}
	return append(
		pipeline,
		bson.D{{Key: "$sort", Value: sort}},
		bson.D{{Key: "$limit", Value: int64(b.spec.Limit)}},
	)
}

// hasDistinctMetric 判断当前规范是否包含去重指标
func (b *MongoBuilder[A]) hasDistinctMetric() bool {
	for _, metric := range b.spec.Metrics {
		if isDistinctMetric(metric) {
			return true
		}
	}
	return false
}

// buildMatch 合并业务过滤条件与分组字段非空条件
func (b *MongoBuilder[A]) buildMatch() bson.D {
	clauses := make(bson.A, 0, len(b.spec.Groups)+1)
	if len(b.filter) > 0 {
		clauses = append(clauses, cloneBSONDocument(b.filter))
	}
	for _, group := range b.spec.Groups {
		clauses = append(clauses, bson.D{{
			Key: group.Field,
			Value: bson.D{
				{Key: "$exists", Value: true},
				{Key: "$ne", Value: nil},
			},
		}})
	}
	if len(clauses) == 0 {
		return bson.D{}
	}
	if len(clauses) == 1 {
		match, _ := clauses[0].(bson.D)
		return match
	}
	return bson.D{{Key: "$and", Value: clauses}}
}

// cloneBSONDocument 递归复制 BSON 有序文档，避免 Clone 或调用方后续修改嵌套过滤条件
func cloneBSONDocument(document bson.D) bson.D {
	if document == nil {
		return nil
	}
	cloned := make(bson.D, len(document))
	for i, element := range document {
		cloned[i] = bson.E{
			Key:   element.Key,
			Value: cloneBSONValue(element.Value),
		}
	}
	return cloned
}

// cloneBSONArray 递归复制 BSON 有序数组
func cloneBSONArray(array bson.A) bson.A {
	if array == nil {
		return nil
	}
	cloned := make(bson.A, len(array))
	for i, value := range array {
		cloned[i] = cloneBSONValue(value)
	}
	return cloned
}

// cloneBSONValue 递归复制 BSON 值
func cloneBSONValue(value any) any {
	switch typed := value.(type) {
	case bson.D:
		return cloneBSONDocument(typed)
	case bson.A:
		return cloneBSONArray(typed)
	case bson.M:
		if typed == nil {
			return bson.M(nil)
		}
		cloned := make(bson.M, len(typed))
		for key, item := range typed {
			cloned[key] = cloneBSONValue(item)
		}
		return cloned
	case []any:
		if typed == nil {
			return []any(nil)
		}
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneBSONValue(item)
		}
		return cloned
	case map[string]any:
		if typed == nil {
			return map[string]any(nil)
		}
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = cloneBSONValue(item)
		}
		return cloned
	default:
		return value
	}
}
