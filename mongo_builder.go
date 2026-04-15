package builder

import (
	"context"

	"github.com/fantasticbin/QueryBuilder/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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

// QueryList 执行 MongoDB 查询列表操作
func (m *MongoBuilder[R]) QueryList(ctx context.Context) ([]*R, int64, error) {
	return m.builder.executeWithMiddlewares(ctx, func(ctx context.Context) ([]*R, int64, error) {
		return m.doQuery(ctx)
	})
}

// doQuery 执行实际的 MongoDB 查询逻辑
func (m *MongoBuilder[R]) doQuery(ctx context.Context) (list []*R, total int64, err error) {
	if m.filter == nil {
		m.filter = bson.D{}
	}

	// 使用 WaitAndGo 并行执行数据查询和总数统计操作
	if err = util.WaitAndGo(func() error {
		findOpt := options.Find().SetSort(m.sort)
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