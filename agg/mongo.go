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
// 泛型参数:
//
//	A: 聚合结果行 DTO 类型
type MongoBuilder[A any] struct {
	base[A]
	filter MongoFilter
}

const (
	mongoBaseFacet = "_base"
	mongoRowsField = "_rows"
)

// NewMongoBuilder 创建 MongoDB 聚合查询构建器
// 泛型参数:
//
//	A: 聚合结果行 DTO 类型
func NewMongoBuilder[A any](data *core.DBProxy) *MongoBuilder[A] {
	b := &MongoBuilder[A]{}
	b.data = data
	b.dataSource = core.MongoDB
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
		bson.D{
			{Key: "pipeline", Value: b.buildPipeline()},
			{Key: "plan", Value: b.Meta().Plan},
		},
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
	if b.hasFacetMetric() {
		return b.buildFacetPipeline()
	}
	return b.buildSimplePipeline()
}

// buildSimplePipeline 构建不含去重或条件指标的单阶段统计管道
func (b *MongoBuilder[A]) buildSimplePipeline() mongo.Pipeline {
	pipeline := make(mongo.Pipeline, 0, 6)
	if match := b.buildMatch(); len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}

	pipeline = append(
		pipeline,
		bson.D{{Key: "$group", Value: b.buildMetricGroupStage(b.buildSourceGroupID(), b.spec.Metrics)}},
		bson.D{{Key: "$project", Value: b.buildProjection(b.spec.Metrics)}},
	)
	return b.appendHavingSortAndLimit(pipeline)
}

// buildFacetPipeline 构建包含去重或条件指标的统计管道
func (b *MongoBuilder[A]) buildFacetPipeline() mongo.Pipeline {
	pipeline := make(mongo.Pipeline, 0, 10)
	if match := b.buildMatch(); len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}

	facets := bson.D{{Key: mongoBaseFacet, Value: b.buildBaseFacetPipeline()}}
	facetArrays := bson.A{"$" + mongoBaseFacet}
	for index, metric := range b.spec.Metrics {
		if !requiresMetricFacet(metric) {
			continue
		}
		facetName := mongoMetricFacetName(index, metric)
		facets = append(facets, bson.E{Key: facetName, Value: b.buildMetricFacetPipeline(metric)})
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
		if requiresMetricFacet(metric) && usesSummingMerge(metric) {
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
	return b.appendHavingSortAndLimit(pipeline)
}

// buildBaseFacetPipeline 构建保留原始统计语义的基础分支
func (b *MongoBuilder[A]) buildBaseFacetPipeline() mongo.Pipeline {
	metrics := make([]Metric, 0, len(b.spec.Metrics))
	for _, metric := range b.spec.Metrics {
		if !requiresMetricFacet(metric) {
			metrics = append(metrics, metric)
		}
	}
	return mongo.Pipeline{
		bson.D{{Key: "$group", Value: b.buildMetricGroupStage(b.buildSourceGroupID(), metrics)}},
		bson.D{{Key: "$project", Value: b.buildProjection(metrics)}},
	}
}

// buildMetricFacetPipeline 构建单个需要独立分支计算的指标管道
func (b *MongoBuilder[A]) buildMetricFacetPipeline(metric Metric) mongo.Pipeline {
	if isDistinctMetric(metric) {
		return b.buildDistinctMetricFacetPipeline(metric)
	}

	pipeline := make(mongo.Pipeline, 0, 3)
	if metric.Condition != nil {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: b.buildConditionMatch(*metric.Condition)}})
	}
	return append(
		pipeline,
		bson.D{{Key: "$group", Value: b.buildMetricGroupStage(b.buildSourceGroupID(), []Metric{metric})}},
		bson.D{{Key: "$project", Value: b.buildProjection([]Metric{metric})}},
	)
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
		keys = append(keys, bson.E{Key: group.Alias, Value: b.mongoGroupExpression(group)})
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

// buildMetricMatch 构建单个去重指标字段的非空过滤条件
func (b *MongoBuilder[A]) buildMetricMatch(metric Metric) bson.D {
	clauses := bson.A{bson.D{{
		Key: metric.Field,
		Value: bson.D{
			{Key: "$exists", Value: true},
			{Key: "$ne", Value: nil},
		},
	}}}
	if metric.Condition != nil {
		clauses = append(clauses, b.buildConditionMatch(*metric.Condition))
	}
	return mergeMongoClauses(clauses)
}

// buildConditionMatch 构建字段级条件过滤文档
func (b *MongoBuilder[A]) buildConditionMatch(condition Condition) bson.D {
	switch condition.Op {
	case In:
		values, _ := conditionListValues(condition.Value)
		return bson.D{{Key: condition.Field, Value: bson.D{{Key: "$in", Value: bson.A(values)}}}}
	case NotIn:
		values, _ := conditionListValues(condition.Value)
		return mergeMongoClauses(bson.A{
			mongoFieldExistsAndNotNull(condition.Field),
			bson.D{{Key: condition.Field, Value: bson.D{{Key: "$nin", Value: bson.A(values)}}}},
		})
	case Between:
		start, end, _ := conditionRangeValues(condition.Value)
		return bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$gte", Value: bson.A{"$" + condition.Field, start}}},
			bson.D{{Key: "$lte", Value: bson.A{"$" + condition.Field, end}}},
		}}}}}
	case Exists:
		return bson.D{{Key: condition.Field, Value: bson.D{{Key: "$exists", Value: true}}}}
	case NotExists:
		return bson.D{{Key: condition.Field, Value: bson.D{{Key: "$exists", Value: false}}}}
	case IsNull:
		return bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: condition.Field, Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: condition.Field, Value: nil}},
		}}}
	case IsNotNull:
		return mongoFieldExistsAndNotNull(condition.Field)
	case Like:
		return bson.D{{Key: condition.Field, Value: bson.D{{Key: "$regex", Value: sqlLikePatternToRegexp(condition.Value.(string))}}}}
	case NotLike:
		return mergeMongoClauses(bson.A{
			mongoFieldExistsAndNotNull(condition.Field),
			bson.D{{Key: condition.Field, Value: bson.D{{Key: "$not", Value: bson.D{{Key: "$regex", Value: sqlLikePatternToRegexp(condition.Value.(string))}}}}}},
		})
	}

	expression := bson.D{{Key: "$expr", Value: mongoConditionExpression(condition)}}
	if condition.Op != Ne {
		return expression
	}
	return mergeMongoClauses(bson.A{
		mongoFieldExistsAndNotNull(condition.Field),
		expression,
	})
}

// appendHavingSortAndLimit 为分组结果追加 HAVING、排序和数量限制
func (b *MongoBuilder[A]) appendHavingSortAndLimit(pipeline mongo.Pipeline) mongo.Pipeline {
	if len(b.spec.Havings) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: b.buildHavingMatch()}})
	}
	if len(b.spec.Groups) == 0 {
		return pipeline
	}
	sort := make(bson.D, 0, len(b.spec.Groups))
	for _, order := range effectiveOrders(b.spec) {
		direction := 1
		if order.Descending {
			direction = -1
		}
		sort = append(sort, bson.E{Key: order.Alias, Value: direction})
	}
	return append(
		pipeline,
		bson.D{{Key: "$sort", Value: sort}},
		bson.D{{Key: "$limit", Value: int64(b.spec.Limit)}},
	)
}

// buildHavingMatch 构建聚合后指标过滤文档
func (b *MongoBuilder[A]) buildHavingMatch() bson.D {
	clauses := make(bson.A, 0, len(b.spec.Havings))
	for _, having := range b.spec.Havings {
		clauses = append(clauses, b.buildHavingClause(having))
	}
	return mergeMongoClauses(clauses)
}

// buildHavingClause 构建单个 HAVING 过滤条件
func (b *MongoBuilder[A]) buildHavingClause(having Having) bson.D {
	comparison := bson.D{{
		Key: having.Alias,
		Value: bson.D{{
			Key:   mongoOperator(having.Op),
			Value: having.Value,
		}},
	}}
	if having.Op != Ne {
		return comparison
	}
	return mergeMongoClauses(bson.A{
		bson.D{{
			Key: having.Alias,
			Value: bson.D{
				{Key: "$exists", Value: true},
				{Key: "$ne", Value: nil},
			},
		}},
		comparison,
	})
}

// hasFacetMetric 判断当前规范是否包含需要独立分支计算的指标
func (b *MongoBuilder[A]) hasFacetMetric() bool {
	for _, metric := range b.spec.Metrics {
		if requiresMetricFacet(metric) {
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
	return mergeMongoClauses(clauses)
}

// mongoMetricFacetName 返回需要独立分支计算的指标 facet 名称
func mongoMetricFacetName(index int, metric Metric) string {
	if isDistinctMetric(metric) {
		return fmt.Sprintf("_distinct_%d", index)
	}
	return fmt.Sprintf("_conditional_%d", index)
}

// usesSummingMerge 判断 facet 合并阶段是否应累加指标值
func usesSummingMerge(metric Metric) bool {
	return metric.Func == Count || metric.Func == Sum
}

// mongoConditionExpression 构建 MongoDB $expr 条件表达式
func mongoConditionExpression(condition Condition) bson.D {
	return bson.D{{
		Key:   mongoOperator(condition.Op),
		Value: bson.A{"$" + condition.Field, condition.Value},
	}}
}

// mongoOperator 将通用比较操作符转换为 MongoDB 表达式操作符
func mongoOperator(op Operator) string {
	switch op {
	case Eq:
		return "$eq"
	case Ne:
		return "$ne"
	case Gt:
		return "$gt"
	case Gte:
		return "$gte"
	case Lt:
		return "$lt"
	case Lte:
		return "$lte"
	default:
		return "$eq"
	}
}

// mergeMongoClauses 合并多个 MongoDB 过滤条件
func mergeMongoClauses(clauses bson.A) bson.D {
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

// mongoGroupExpression 构建 MongoDB 分组键表达式
func (b *MongoBuilder[A]) mongoGroupExpression(group Group) any {
	if group.Interval == "" {
		return "$" + group.Field
	}
	dateTrunc := bson.D{
		{Key: "date", Value: "$" + group.Field},
		{Key: "unit", Value: string(group.Interval)},
	}
	if group.Interval == TimeIntervalWeek {
		dateTrunc = append(dateTrunc, bson.E{Key: "startOfWeek", Value: "monday"})
	}
	if group.TimeZone != "" {
		dateTrunc = append(dateTrunc, bson.E{Key: "timezone", Value: group.TimeZone})
	}
	return bson.D{{Key: "$dateTrunc", Value: dateTrunc}}
}

func mongoFieldExistsAndNotNull(field string) bson.D {
	return bson.D{{
		Key: field,
		Value: bson.D{
			{Key: "$exists", Value: true},
			{Key: "$ne", Value: nil},
		},
	}}
}
