package agg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/fantasticbin/QueryBuilder/v2/util"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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

// mongoAggregateTotal 承载 $count 阶段输出的分组总数
type mongoAggregateTotal struct {
	Total int64 `bson:"total"`
}

// NewMongoBuilder 创建 MongoDB 聚合查询构建器
// 泛型参数:
//
//	A: 聚合结果行 DTO 类型
func NewMongoBuilder[A any](data *core.DBProxy) *MongoBuilder[A] {
	b := &MongoBuilder[A]{}
	b.data = data
	b.dataSource = core.MongoDB
	b.needTotal = true
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

		if len(b.spec.Groups) == 0 {
			rows, err := b.queryRows(ctx, collection)
			if err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				rows = append(rows, new(A))
			}
			return &Result[A]{Rows: rows, Total: 1}, nil
		}

		rows := make([]*A, 0)
		var total int64
		if err := util.WaitAndGoWithContext(ctx, func(ctx context.Context) error {
			var err error
			rows, err = b.queryRows(ctx, collection)
			return err
		}, func(ctx context.Context) error {
			if !b.needTotal {
				return nil
			}
			count, err := b.queryTotal(ctx, collection)
			if err != nil {
				return err
			}
			total = count
			return nil
		}); err != nil {
			return nil, err
		}
		return &Result[A]{Rows: rows, Total: total}, nil
	})
}

// queryRows 执行聚合数据页 pipeline 并解码结果行
func (b *MongoBuilder[A]) queryRows(ctx context.Context, collection *mongo.Collection) ([]*A, error) {
	cursor, err := collection.Aggregate(ctx, b.buildPipeline(), mongoAllowDiskUse())
	if err != nil {
		return nil, fmt.Errorf("executing mongodb aggregate query: %w", err)
	}
	rows := make([]*A, 0)
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("decoding mongodb aggregate result: %w", err)
	}
	return rows, nil
}

// queryTotal 统计聚合后、HAVING 后的分组行数
func (b *MongoBuilder[A]) queryTotal(ctx context.Context, collection *mongo.Collection) (int64, error) {
	cursor, err := collection.Aggregate(ctx, b.buildTotalPipeline(), mongoAllowDiskUse())
	if err != nil {
		return 0, fmt.Errorf("counting mongodb aggregate groups: %w", err)
	}
	totals := make([]mongoAggregateTotal, 0, 1)
	if err := cursor.All(ctx, &totals); err != nil {
		return 0, fmt.Errorf("decoding mongodb aggregate total: %w", err)
	}
	if len(totals) == 0 {
		return 0, nil
	}
	return totals[0].Total, nil
}

// mongoAllowDiskUse 返回允许磁盘溢出的聚合选项，避免大分组查询触发内存限制
func mongoAllowDiskUse() *options.AggregateOptionsBuilder {
	return options.Aggregate().SetAllowDiskUse(true)
}

// Explain 返回生成的 MongoDB 聚合管道，但不会执行查询
func (b *MongoBuilder[A]) Explain(context.Context) (string, error) {
	if err := b.prepare(); err != nil {
		return "", err
	}

	payload := bson.D{
		{Key: "pipeline", Value: b.buildPipeline()},
		{Key: "plan", Value: b.Meta().Plan},
	}
	if b.needTotal && len(b.spec.Groups) > 0 {
		payload = append(payload, bson.E{Key: "total_pipeline", Value: b.buildTotalPipeline()})
	}

	encoded, err := bson.MarshalExtJSON(payload, true, false)
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

// buildPipeline 根据聚合规范构建 MongoDB 数据页 pipeline
func (b *MongoBuilder[A]) buildPipeline() mongo.Pipeline {
	pipeline := b.buildPipelineBase()
	return b.appendHavingSortAndPagination(pipeline)
}

// buildTotalPipeline 根据聚合规范构建 MongoDB 总分组数 pipeline
func (b *MongoBuilder[A]) buildTotalPipeline() mongo.Pipeline {
	pipeline := b.appendHaving(b.buildPipelineBase())
	if b.totalLimit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: int64(b.totalLimit)}})
	}
	return append(pipeline, bson.D{{Key: "$count", Value: "total"}})
}

// buildPipelineBase 构建不含 HAVING、排序和分页的基础聚合 pipeline
func (b *MongoBuilder[A]) buildPipelineBase() mongo.Pipeline {
	pipeline := make(mongo.Pipeline, 0, 6)
	if match := b.buildMatch(); len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}
	return append(
		pipeline,
		bson.D{{Key: "$group", Value: b.buildMetricGroupStage(b.buildSourceGroupID(), b.spec.Metrics)}},
		bson.D{{Key: "$project", Value: b.buildProjection(b.spec.Metrics)}},
	)
}

// buildMetricGroupStage 构建指标使用的 MongoDB $group 内容
// 条件指标用 $cond 折进同一阶段；去重指标用 $addToSet，避免 $facet 复制全量文档
func (b *MongoBuilder[A]) buildMetricGroupStage(groupID any, metrics []Metric) bson.D {
	groupStage := bson.D{{Key: "_id", Value: groupID}}
	for _, metric := range metrics {
		groupStage = append(groupStage, bson.E{
			Key:   metric.Alias,
			Value: b.mongoMetricAccumulator(metric),
		})
	}
	return groupStage
}

// mongoMetricAccumulator 将单个指标转换为 $group 累加器
func (b *MongoBuilder[A]) mongoMetricAccumulator(metric Metric) bson.D {
	if isDistinctMetric(metric) {
		return bson.D{{Key: "$addToSet", Value: b.mongoConditionalValue(metric, "$"+metric.Field, nil)}}
	}
	if metric.Func == Count {
		return bson.D{{Key: "$sum", Value: b.mongoConditionalValue(metric, 1, 0)}}
	}
	fieldRef := "$" + metric.Field
	operator := "$" + metric.Func.String()
	var otherwise any
	if metric.Func == Sum {
		otherwise = 0
	}
	return bson.D{{Key: operator, Value: b.mongoConditionalValue(metric, fieldRef, otherwise)}}
}

// mongoConditionalValue 无谓词时直接返回 when；否则返回 $cond 表达式：
// 谓词成立取 when，否则取 otherwise。谓词由字段存在性检查与字段条件（如有）组成
func (b *MongoBuilder[A]) mongoConditionalValue(metric Metric, when any, otherwise any) any {
	predicates := make(bson.A, 0, 2)
	if metric.Field != "" && (isDistinctMetric(metric) || metric.Func != Count) {
		predicates = append(predicates, mongoFieldPresentExpr(metric.Field))
	}
	if metric.Condition != nil {
		predicates = append(predicates, mongoConditionExpr(*metric.Condition))
	}
	if len(predicates) == 0 {
		return when
	}
	predicate := any(predicates[0])
	if len(predicates) > 1 {
		predicate = bson.D{{Key: "$and", Value: predicates}}
	}
	return bson.D{{Key: "$cond", Value: bson.A{predicate, when, otherwise}}}
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

// buildProjection 构建把分组键和指标别名展开到结果行的投影
func (b *MongoBuilder[A]) buildProjection(metrics []Metric) bson.D {
	projection := bson.D{{Key: "_id", Value: 0}}
	for _, group := range b.spec.Groups {
		projection = append(projection, bson.E{Key: group.Alias, Value: "$_id." + group.Alias})
	}
	for _, metric := range metrics {
		projection = append(projection, bson.E{Key: metric.Alias, Value: mongoProjectedMetric(metric)})
	}
	return projection
}

// mongoProjectedMetric 构建指标在 $project 阶段的取值表达式
// 去重指标在 $group 阶段得到的是数组，需先过滤 null 再取大小或求和
func mongoProjectedMetric(metric Metric) any {
	if isDistinctCount(metric) {
		return bson.D{{Key: "$size", Value: mongoFilterNulls("$" + metric.Alias)}}
	}
	if isDistinctSum(metric) {
		return bson.D{{Key: "$sum", Value: mongoFilterNulls("$" + metric.Alias)}}
	}
	return 1
}

// mongoFilterNulls 构建 $filter 表达式，剔除数组中的 null 元素
func mongoFilterNulls(ref string) bson.D {
	return bson.D{{Key: "$filter", Value: bson.D{
		{Key: "input", Value: ref},
		{Key: "cond", Value: bson.D{{Key: "$ne", Value: bson.A{"$$this", nil}}}},
	}}}
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
	case Exists, IsNotNull:
		return mongoFieldExistsAndNotNull(condition.Field)
	case NotExists, IsNull:
		return bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: condition.Field, Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: condition.Field, Value: nil}},
		}}}
	case Like:
		return bson.D{{Key: condition.Field, Value: bson.D{{Key: "$regex", Value: sqlLikePatternToRegexp(condition.Value.(string))}}}}
	case NotLike:
		return mergeMongoClauses(bson.A{
			mongoFieldExistsAndNotNull(condition.Field),
			bson.D{{Key: condition.Field, Value: bson.D{{Key: "$not", Value: bson.D{{Key: "$regex", Value: sqlLikePatternToRegexp(condition.Value.(string))}}}}}},
		})
	}

	// 其余算术比较操作符（Eq/Ne/Gt/Gte/Lt/Lte）落入默认分支，
	// 统一通过 mongoConditionExpression + mongoOperator 转换为 $expr 比较表达式。
	expression := bson.D{{Key: "$expr", Value: mongoConditionExpression(condition)}}
	if condition.Op != Ne {
		return expression
	}
	return mergeMongoClauses(bson.A{
		mongoFieldExistsAndNotNull(condition.Field),
		expression,
	})
}

// mongoConditionExpr 将字段条件转换为可放进 $cond 的布尔表达式
func mongoConditionExpr(condition Condition) any {
	field := "$" + condition.Field
	switch condition.Op {
	case In:
		values, _ := conditionListValues(condition.Value)
		return bson.D{{Key: "$in", Value: bson.A{field, bson.A(values)}}}
	case NotIn:
		values, _ := conditionListValues(condition.Value)
		return bson.D{{Key: "$and", Value: bson.A{
			mongoFieldPresentExpr(condition.Field),
			bson.D{{Key: "$not", Value: bson.D{{Key: "$in", Value: bson.A{field, bson.A(values)}}}}},
		}}}
	case Between:
		start, end, _ := conditionRangeValues(condition.Value)
		return bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$gte", Value: bson.A{field, start}}},
			bson.D{{Key: "$lte", Value: bson.A{field, end}}},
		}}}
	case Exists, IsNotNull:
		return mongoFieldPresentExpr(condition.Field)
	case NotExists, IsNull:
		return bson.D{{Key: "$not", Value: mongoFieldPresentExpr(condition.Field)}}
	case Like:
		// 与 SQL 一致：NULL LIKE 不成立，不用 $ifNull 把 null 当成空串
		return bson.D{{Key: "$and", Value: bson.A{
			mongoFieldPresentExpr(condition.Field),
			mongoRegexMatchExpr(field, condition.Value.(string)),
		}}}
	case NotLike:
		// 与 SQL 一致：NULL NOT LIKE 也不成立，输入同样用 $toString(field)
		return bson.D{{Key: "$and", Value: bson.A{
			mongoFieldPresentExpr(condition.Field),
			bson.D{{Key: "$not", Value: mongoRegexMatchExpr(field, condition.Value.(string))}},
		}}}
	case Ne:
		return bson.D{{Key: "$and", Value: bson.A{
			mongoFieldPresentExpr(condition.Field),
			bson.D{{Key: "$ne", Value: bson.A{field, condition.Value}}},
		}}}
	default:
		return mongoConditionExpression(condition)
	}
}

// mongoRegexMatchExpr 构建 $regexMatch 表达式，将字段值转为字符串后按模式匹配
func mongoRegexMatchExpr(fieldRef, pattern string) bson.D {
	return bson.D{{Key: "$regexMatch", Value: bson.D{
		{Key: "input", Value: bson.D{{Key: "$toString", Value: fieldRef}}},
		{Key: "regex", Value: sqlLikePatternToRegexp(pattern)},
	}}}
}

// mongoFieldPresentExpr 构建字段存在且非 null 的 $expr 布尔表达式
func mongoFieldPresentExpr(field string) bson.D {
	ref := "$" + field
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$ne", Value: bson.A{bson.D{{Key: "$type", Value: ref}}, "missing"}}},
		bson.D{{Key: "$ne", Value: bson.A{ref, nil}}},
	}}}
}

// appendHaving 为分组结果追加 HAVING 过滤
func (b *MongoBuilder[A]) appendHaving(pipeline mongo.Pipeline) mongo.Pipeline {
	if len(b.spec.Havings) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: b.buildHavingMatch()}})
	}
	return pipeline
}

// appendHavingSortAndPagination 为分组结果追加 HAVING、排序和偏移分页
func (b *MongoBuilder[A]) appendHavingSortAndPagination(pipeline mongo.Pipeline) mongo.Pipeline {
	pipeline = b.appendHaving(pipeline)
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
	pipeline = append(pipeline, bson.D{{Key: "$sort", Value: sort}})
	if b.spec.Start > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$skip", Value: int64(b.spec.Start)}})
	}
	return append(pipeline, bson.D{{Key: "$limit", Value: int64(b.spec.Limit)}})
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

// mongoFieldExistsAndNotNull 构建字段存在且非 null 的匹配文档
func mongoFieldExistsAndNotNull(field string) bson.D {
	return bson.D{{
		Key: field,
		Value: bson.D{
			{Key: "$exists", Value: true},
			{Key: "$ne", Value: nil},
		},
	}}
}
