package builder

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/mongo"
	"gorm.io/gorm"
)

// DBProxy 数据实例结构
type DBProxy struct {
	db      *gorm.DB
	mongodb *mongo.Collection // 需提前指定.Database("db_name").Collection("collection_name")
	// redis、elasticsearch...
}

// NewDBProxy 创建数据实例
func NewDBProxy(db *gorm.DB, mongodb *mongo.Collection) *DBProxy {
	return &DBProxy{
		db:      db,
		mongodb: mongodb,
	}
}

// builder 查询构建器，使用泛型支持多种实体类型
// 泛型参数:
//
//	R: 查询结果的实体类型
type builder[R any] struct {
	data           *DBProxy
	start          uint32
	limit          uint32
	needTotal      bool
	needPagination bool
	strategy       Strategy[R]     // 查询策略
	middlewares    []Middleware[R] // 中间件链

	filter func(context.Context) (any, error)
	sort   func() any
}

// SetFilter 设置过滤条件生成函数
// 返回支持链式调用的构建器实例
func (b *builder[R]) SetFilter(filter func(context.Context) (any, error)) *builder[R] {
	b.filter = filter
	return b
}

// SetSort 设置排序条件生成函数
// 返回支持链式调用的构建器实例
func (b *builder[R]) SetSort(sort func() any) *builder[R] {
	b.sort = sort
	return b
}

// SetStrategy 设置查询列表策略
// 返回支持链式调用的构建器实例
func (b *builder[R]) SetStrategy(strategy Strategy[R]) *builder[R] {
	b.strategy = strategy
	return b
}

// Use 添加中间件
// 返回支持链式调用的构建器实例
func (b *builder[R]) Use(middleware Middleware[R]) *builder[R] {
	b.middlewares = append(b.middlewares, middleware)
	return b
}

// getQueryStrategy 获取查询列表策略
// 如果没有设置策略，则根据数据源自动选择策略
func (b *builder[R]) getQueryStrategy() (Strategy[R], error) {
	if b.strategy != nil {
		return b.strategy, nil
	}
	if b.data == nil {
		return nil, errors.New("no data source provided")
	}

	switch {
	case b.data.db != nil:
		return NewQueryGormListStrategy[R](), nil
	case b.data.mongodb != nil:
		return NewQueryMongoListStrategy[R](), nil
	default:
		return nil, errors.New("query strategy not set and no valid DB found")
	}
}

// QueryList 执行查询列表操作
// 返回值与中间件类型相同，list []R 查询结果列表
func (b *builder[R]) QueryList(ctx context.Context) ([]*R, int64, error) {
	// 尝试自动推断策略类型
	strategy, err := b.getQueryStrategy()
	if err != nil {
		return nil, 0, err
	}

	// 构建中间件链
	next := func(ctx context.Context) ([]*R, int64, error) {
		return strategy.QueryList(ctx, b)
	}

	for i := len(b.middlewares) - 1; i >= 0; i-- {
		next = func(mw Middleware[R], fn func(context.Context) ([]*R, int64, error)) func(context.Context) ([]*R, int64, error) {
			return func(ctx context.Context) ([]*R, int64, error) {
				return mw(ctx, b, fn)
			}
		}(b.middlewares[i], next)
	}

	return next(ctx)
}
