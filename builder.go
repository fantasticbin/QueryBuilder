package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"
)

// DataSource 数据源类型枚举
type DataSource int

const (
	// MySQL 数据源
	MySQL DataSource = iota
	// MongoDB 数据源
	MongoDB
	// ElasticSearch 数据源
	ElasticSearch
)

// DBProxy 数据实例结构
type DBProxy struct {
	DB            *gorm.DB
	Mongodb       *mongo.Collection // 需提前指定.Database("db_name").Collection("collection_name")
	ElasticSearch *elastic.Client
	// redis...
}

// NewDBProxy 创建数据实例
func NewDBProxy(db *gorm.DB, mongodb *mongo.Collection, elasticsearch *elastic.Client) *DBProxy {
	return &DBProxy{
		DB:            db,
		Mongodb:       mongodb,
		ElasticSearch: elasticsearch,
	}
}

// queryBuilder 构建器接口约束，利用 Go 1.26 自引用泛型约束特性
// 泛型参数:
//
//	B: 具体构建器类型（自引用）
//	R: 查询结果的实体类型
type queryBuilder[B any, R any] interface {
	// self 返回具体构建器自身引用，用于链式调用返回具体子类型
	self() B
	// QueryList 执行查询列表操作，由各专属构建器各自实现
	QueryList(ctx context.Context) ([]*R, int64, error)
}

// Querier 通用查询接口，作为工厂函数的返回类型
// 泛型参数:
//
//	R: 查询结果的实体类型
type Querier[R any] interface {
	// Use 添加中间件
	Use(middleware Middleware[R]) Querier[R]
	// SetStart 设置分页起始位置
	SetStart(start uint32) Querier[R]
	// SetLimit 设置每页数据条数
	SetLimit(limit uint32) Querier[R]
	// SetNeedTotal 设置是否需要查询总数
	SetNeedTotal(needTotal bool) Querier[R]
	// SetNeedPagination 设置是否需要分页
	SetNeedPagination(needPagination bool) Querier[R]
	// SetFields 设置查询字段投影，指定只返回部分字段
	SetFields(fields ...string) Querier[R]
	// SetBeforeQueryHook 设置查询前置钩子
	SetBeforeQueryHook(hook BeforeQueryHook) Querier[R]
	// SetAfterQueryHook 设置查询后置钩子
	SetAfterQueryHook(hook AfterQueryHook[R]) Querier[R]
	// QueryList 执行查询列表操作
	QueryList(ctx context.Context) ([]*R, int64, error)
}

// builder 查询构建器公共模板基类，使用自引用泛型约束
// 泛型参数:
//
//	B: 具体构建器类型（自引用，满足 queryBuilder 约束）
//	R: 查询结果的实体类型
type builder[B queryBuilder[B, R], R any] struct {
	data           *DBProxy
	dataSource     DataSource // 数据源类型，用于查询元信息
	start          uint32
	limit          uint32
	needTotal      bool
	needPagination bool
	fields         []string          // 查询字段投影
	beforeHook     BeforeQueryHook   // 查询前置钩子
	afterHook      AfterQueryHook[R] // 查询后置钩子
	middlewares    []Middleware[R]   // 中间件链

	selfRef    B          // 存储具体子类型引用，用于链式调用返回具体子类型
	querierRef Querier[R] // 存储 Querier 接口引用，避免中间件执行时的类型断言
}

// setSelf 设置具体子类型引用，供子类型构造时调用
// querier 参数同时保存 Querier[R] 接口引用，避免中间件执行时需要类型断言
func (b *builder[B, R]) setSelf(self B, querier Querier[R]) {
	b.selfRef = self
	b.querierRef = querier
}

// Use 添加中间件
// 返回具体子类型，支持类型安全的链式调用
func (b *builder[B, R]) Use(middleware Middleware[R]) B {
	b.middlewares = append(b.middlewares, middleware)
	return b.selfRef
}

// SetStart 设置分页起始位置
func (b *builder[B, R]) SetStart(start uint32) B {
	b.start = start
	return b.selfRef
}

// SetLimit 设置每页数据条数
func (b *builder[B, R]) SetLimit(limit uint32) B {
	b.limit = limit
	return b.selfRef
}

// SetNeedTotal 设置是否需要查询总数
func (b *builder[B, R]) SetNeedTotal(needTotal bool) B {
	b.needTotal = needTotal
	return b.selfRef
}

// SetNeedPagination 设置是否需要分页
func (b *builder[B, R]) SetNeedPagination(needPagination bool) B {
	b.needPagination = needPagination
	return b.selfRef
}

// SetFields 设置查询字段投影，指定只返回部分字段
func (b *builder[B, R]) SetFields(fields ...string) B {
	b.fields = fields
	return b.selfRef
}

// SetBeforeQueryHook 设置查询前置钩子
func (b *builder[B, R]) SetBeforeQueryHook(hook BeforeQueryHook) B {
	b.beforeHook = hook
	return b.selfRef
}

// SetAfterQueryHook 设置查询后置钩子
func (b *builder[B, R]) SetAfterQueryHook(hook AfterQueryHook[R]) B {
	b.afterHook = hook
	return b.selfRef
}

// executeWithMiddlewares 执行中间件链并调用最终查询逻辑
// 由各专属构建器在 QueryList 中调用，传入最终的查询函数
// 支持超时控制和前置/后置钩子
func (b *builder[B, R]) executeWithMiddlewares(
	ctx context.Context,
	queryFn func(context.Context) ([]*R, int64, error),
) ([]*R, int64, error) {
	// 注入查询元信息到 context
	ctx = withQueryMeta(ctx, &QueryMeta{
		DataSource:     b.dataSource,
		Start:          b.start,
		Limit:          b.limit,
		NeedTotal:      b.needTotal,
		NeedPagination: b.needPagination,
		Fields:         b.fields,
		StartTime:      time.Now(),
	})

	// 执行前置钩子
	if b.beforeHook != nil {
		ctx = b.beforeHook(ctx)
	}

	next := queryFn

	for i := len(b.middlewares) - 1; i >= 0; i-- {
		next = func(mw Middleware[R], fn func(context.Context) ([]*R, int64, error)) func(context.Context) ([]*R, int64, error) {
			return func(ctx context.Context) ([]*R, int64, error) {
				return mw(ctx, b.querierRef, fn)
			}
		}(b.middlewares[i], next)
	}

	list, total, err := next(ctx)

	// 执行后置钩子
	if b.afterHook != nil {
		b.afterHook(ctx, list, total, err)
	}

	return list, total, err
}

// NewBuilder 通用工厂函数，根据 DataSource 枚举值创建对应的专属查询构建器
// 返回 Querier[R] 通用查询接口
func NewBuilder[R any](ds DataSource, data *DBProxy) Querier[R] {
	switch ds {
	case MySQL:
		return NewGormBuilder[R](data)
	case MongoDB:
		return NewMongoBuilder[R](data)
	case ElasticSearch:
		return NewElasticSearchBuilder[R](data, "")
	default:
		panic(fmt.Sprintf("unsupported data source: %d", ds))
	}
}
