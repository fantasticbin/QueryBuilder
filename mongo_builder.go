package builder

import (
	"context"
	"encoding/json"
	"iter"

	"github.com/fantasticbin/QueryBuilder/util"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoFilter MongoDB 过滤条件类型（bson.D 有序文档）
type MongoFilter = bson.D

// MongoSort MongoDB 排序条件类型（bson.D 有序文档）
type MongoSort = bson.D

// MongoBuilder MongoDB 专属查询构建器
// 泛型参数:
//
//	R: 查询结果的实体类型
type MongoBuilder[R any] struct {
	builder[*MongoBuilder[R], R]
	filter MongoFilter // MongoDB 专属过滤条件
	sort   MongoSort   // MongoDB 专属排序条件
}

// self 返回自身引用，实现 builderInterface 接口
func (m *MongoBuilder[R]) self() *MongoBuilder[R] {
	return m
}

// NewMongoBuilder 创建 MongoDB 专属查询构建器实例
func NewMongoBuilder[R any](data *DBProxy) *MongoBuilder[R] {
	m := &MongoBuilder[R]{}
	m.builder.data = data
	m.builder.dataSource = MongoDB
	m.builder.setSelf(m, m)
	return m
}

// SetFilter 设置 MongoDB 过滤条件
func (m *MongoBuilder[R]) SetFilter(filter MongoFilter) *MongoBuilder[R] {
	m.filter = filter
	return m
}

// SetSort 设置 MongoDB 排序条件
func (m *MongoBuilder[R]) SetSort(sort MongoSort) *MongoBuilder[R] {
	m.sort = sort
	return m
}

// Use 添加中间件（实现 Querier 接口）
func (m *MongoBuilder[R]) Use(middleware Middleware[R]) Querier[R] {
	m.builder.Use(middleware)
	return m
}

// SetStart 设置分页起始位置（实现 Querier 接口）
func (m *MongoBuilder[R]) SetStart(start uint32) Querier[R] {
	m.builder.SetStart(start)
	return m
}

// SetLimit 设置每页数据条数（实现 Querier 接口）
func (m *MongoBuilder[R]) SetLimit(limit uint32) Querier[R] {
	m.builder.SetLimit(limit)
	return m
}

// SetNeedTotal 设置是否需要查询总数（实现 Querier 接口）
func (m *MongoBuilder[R]) SetNeedTotal(needTotal bool) Querier[R] {
	m.builder.SetNeedTotal(needTotal)
	return m
}

// SetNeedPagination 设置是否需要分页（实现 Querier 接口）
func (m *MongoBuilder[R]) SetNeedPagination(needPagination bool) Querier[R] {
	m.builder.SetNeedPagination(needPagination)
	return m
}

// SetFields 设置查询字段投影（实现 Querier 接口）
func (m *MongoBuilder[R]) SetFields(fields ...string) Querier[R] {
	m.builder.SetFields(fields...)
	return m
}

// SetBeforeQueryHook 设置查询前置钩子（实现 Querier 接口）
func (m *MongoBuilder[R]) SetBeforeQueryHook(hook BeforeQueryHook) Querier[R] {
	m.builder.SetBeforeQueryHook(hook)
	return m
}

// SetAfterQueryHook 设置查询后置钩子（实现 Querier 接口）
func (m *MongoBuilder[R]) SetAfterQueryHook(hook AfterQueryHook[R]) Querier[R] {
	m.builder.SetAfterQueryHook(hook)
	return m
}

// SetCursorField 设置游标分页排序字段（实现 Querier 接口）
func (m *MongoBuilder[R]) SetCursorField(fields ...string) Querier[R] {
	m.builder.SetCursorField(fields...)
	return m
}

// SetCursorValue 设置游标初始值（实现 Querier 接口）
func (m *MongoBuilder[R]) SetCursorValue(values ...any) Querier[R] {
	m.builder.SetCursorValue(values...)
	return m
}

// QueryList 执行 MongoDB 查询列表操作
func (m *MongoBuilder[R]) QueryList(ctx context.Context) ([]*R, int64, error) {
	return m.builder.executeWithMiddlewares(ctx, func(ctx context.Context) ([]*R, int64, error) {
		return m.doQuery(ctx)
	})
}

// QueryCursor 执行 MongoDB 游标分页查询，返回迭代器（实现 Querier 接口）
func (m *MongoBuilder[R]) QueryCursor(ctx context.Context) iter.Seq2[*R, error] {
	return m.builder.executeCursorWithMiddlewares(ctx, m.doCursorQuery)
}

// doQuery 执行实际的 MongoDB 查询逻辑
func (m *MongoBuilder[R]) doQuery(ctx context.Context) (list []*R, total int64, err error) {
	if m.filter == nil {
		m.filter = bson.D{}
	}

	// 使用 WaitAndGo 并行执行数据查询和总数统计操作
	if err = util.WaitAndGo(func() error {
		findOpt := options.Find().SetSort(m.sort)

		// 应用字段投影
		if len(m.builder.fields) > 0 {
			projection := bson.D{}
			for _, f := range m.builder.fields {
				projection = append(projection, bson.E{Key: f, Value: 1})
			}
			findOpt.SetProjection(projection)
		}

		if m.builder.needPagination {
			if m.builder.limit < 1 {
				m.builder.limit = defaultLimit
			}
			findOpt.SetSkip(int64(m.builder.start)).SetLimit(int64(m.builder.limit))
		}

		cursor, err := m.builder.data.Mongodb.Find(ctx, m.filter, findOpt)
		if err != nil {
			return err
		}
		defer func(cursor *mongo.Cursor, ctx context.Context) {
			_ = cursor.Close(ctx)
		}(cursor, ctx)

		return cursor.All(ctx, &list)
	}, func() error {
		if !m.builder.needTotal {
			return nil
		}

		total, err = m.builder.data.Mongodb.CountDocuments(ctx, m.filter)
		if err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// Explain 返回 MongoDB 构建器最终生成的查询条件（Dry Run 模式）
// 用于调试场景，不会实际执行查询
// 若已配置游标字段，将输出游标查询模式的首批查询 DSL
func (m *MongoBuilder[R]) Explain(ctx context.Context) (string, error) {
	// 如果配置了游标字段，展示游标查询模式的首批 DSL
	if len(m.builder.cursorFields) > 0 {
		return m.explainCursor(ctx)
	}

	if m.filter == nil {
		m.filter = bson.D{}
	}

	result := map[string]any{
		"filter": m.filter,
	}

	if m.sort != nil {
		result["sort"] = m.sort
	}

	if len(m.builder.fields) > 0 {
		projection := bson.D{}
		for _, f := range m.builder.fields {
			projection = append(projection, bson.E{Key: f, Value: 1})
		}
		result["projection"] = projection
	}

	if m.builder.needPagination {
		if m.builder.limit < 1 {
			m.builder.limit = defaultLimit
		}
		result["skip"] = m.builder.start
		result["limit"] = m.builder.limit
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// buildCursorSort 构建游标查询的排序条件（游标字段排序为主，用户 sort 去重追加）
func (m *MongoBuilder[R]) buildCursorSort() bson.D {
	sortDoc := bson.D{}
	for _, field := range m.builder.cursorFields {
		sortDoc = append(sortDoc, bson.E{Key: field, Value: 1}) // 升序
	}
	// 追加用户 sort 中的其他字段（排除已在游标字段中的）
	if m.sort != nil {
		cursorFieldSet := make(map[string]bool, len(m.builder.cursorFields))
		for _, f := range m.builder.cursorFields {
			cursorFieldSet[f] = true
		}
		for _, s := range m.sort {
			if !cursorFieldSet[s.Key] {
				sortDoc = append(sortDoc, s)
			}
		}
	}
	return sortDoc
}

// buildCursorProjection 构建游标查询的字段投影
func (m *MongoBuilder[R]) buildCursorProjection() bson.D {
	if len(m.builder.fields) == 0 {
		return nil
	}
	projection := bson.D{}
	for _, f := range m.builder.fields {
		projection = append(projection, bson.E{Key: f, Value: 1})
	}
	return projection
}

// explainCursor 返回游标查询模式的首批查询 DSL
func (m *MongoBuilder[R]) explainCursor(ctx context.Context) (string, error) {
	batchSize := int(m.builder.limit)
	if batchSize < 1 {
		batchSize = defaultLimit
	}

	filter := m.filter
	if filter == nil {
		filter = bson.D{}
	}

	result := map[string]any{
		"mode":          "cursor",
		"cursor_fields": m.builder.cursorFields,
		"filter":        filter,
		"sort":          m.buildCursorSort(),
		"limit":         batchSize,
	}

	if projection := m.buildCursorProjection(); projection != nil {
		result["projection"] = projection
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// doCursorQuery 执行 MongoDB 游标分页的单批次查询
// 构建多字段复合游标条件
func (m *MongoBuilder[R]) doCursorQuery(ctx context.Context, cursorValues []any) ([]*R, []any, error) {
	batchSize := int(m.builder.limit)
	if batchSize < 1 {
		batchSize = defaultLimit
	}

	// 构建过滤条件
	filter := m.filter
	if filter == nil {
		filter = bson.D{}
	}

	// 构建游标条件（仅在非首次查询时添加）
	if len(cursorValues) > 0 {
		cursorFields := m.builder.cursorFields
		var cursorCondition bson.D

		if len(cursorFields) == 1 {
			// 单字段：{"field": {"$gt": value}}
			cursorCondition = bson.D{{Key: cursorFields[0], Value: bson.D{{Key: "$gt", Value: cursorValues[0]}}}}
		} else {
			// 多字段复合游标条件：
			// {"$or": [{"a": {"$gt": v1}}, {"a": v1, "b": {"$gt": v2}}]}
			var orConditions bson.A
			for i := 0; i < len(cursorFields); i++ {
				cond := bson.D{}
				// 前面的字段等于对应的游标值
				for j := 0; j < i; j++ {
					cond = append(cond, bson.E{Key: cursorFields[j], Value: cursorValues[j]})
				}
				// 当前字段大于对应的游标值
				cond = append(cond, bson.E{Key: cursorFields[i], Value: bson.D{{Key: "$gt", Value: cursorValues[i]}}})
				orConditions = append(orConditions, cond)
			}
			cursorCondition = bson.D{{Key: "$or", Value: orConditions}}
		}

		// 将游标条件与用户 filter 组合（$and）
		if len(filter) > 0 {
			filter = bson.D{{Key: "$and", Value: bson.A{filter, cursorCondition}}}
		} else {
			filter = cursorCondition
		}
	}

	findOpt := options.Find().SetSort(m.buildCursorSort()).SetLimit(int64(batchSize))

	// 应用字段投影
	if projection := m.buildCursorProjection(); projection != nil {
		findOpt.SetProjection(projection)
	}

	cursor, err := m.builder.data.Mongodb.Find(ctx, filter, findOpt)
	if err != nil {
		return nil, nil, err
	}
	defer func(cursor *mongo.Cursor, ctx context.Context) {
		_ = cursor.Close(ctx)
	}(cursor, ctx)

	var list []*R
	if err := cursor.All(ctx, &list); err != nil {
		return nil, nil, err
	}

	// 返回 nil 作为 nextCursorValues，由 buildCursorIterator 通过反射提取
	return list, nil, nil
}