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
	pipeline := make(mongo.Pipeline, 0, 5)
	if match := b.buildMatch(); len(match) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}

	groupID := any(nil)
	if len(b.spec.Groups) > 0 {
		keys := make(bson.D, 0, len(b.spec.Groups))
		for _, group := range b.spec.Groups {
			keys = append(keys, bson.E{Key: group.Alias, Value: "$" + group.Field})
		}
		groupID = keys
	}
	groupStage := bson.D{{Key: "_id", Value: groupID}}
	for _, metric := range b.spec.Metrics {
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
	pipeline = append(pipeline, bson.D{{Key: "$group", Value: groupStage}})

	projection := bson.D{{Key: "_id", Value: 0}}
	for _, group := range b.spec.Groups {
		projection = append(projection, bson.E{Key: group.Alias, Value: "$_id." + group.Alias})
	}
	for _, metric := range b.spec.Metrics {
		projection = append(projection, bson.E{Key: metric.Alias, Value: 1})
	}
	pipeline = append(pipeline, bson.D{{Key: "$project", Value: projection}})

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
	pipeline = append(
		pipeline,
		bson.D{{Key: "$sort", Value: sort}},
		bson.D{{Key: "$limit", Value: int64(b.spec.Limit)}},
	)
	return pipeline
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
