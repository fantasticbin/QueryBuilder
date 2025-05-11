package builder

import (
	"context"
	"errors"
	"github.com/fantasticbin/QueryBuilder/utils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"gorm.io/gorm"
)

// Strategy 查询列表策略
type Strategy[R any] interface {
	QueryList(context.Context, *builder[R]) ([]*R, int64, error)
}

// QueryGormListStrategy GORM 查询策略实现
type QueryGormListStrategy[R any] struct{}

// NewQueryGormListStrategy 创建 GORM 查询策略实例
func NewQueryGormListStrategy[R any]() *QueryGormListStrategy[R] {
	return &QueryGormListStrategy[R]{}
}

// QueryList 实现 GORM 查询逻辑
func (s *QueryGormListStrategy[R]) QueryList(
	ctx context.Context,
	builder *builder[R],
) (list []*R, total int64, err error) {
	filterScope, err := builder.filter(ctx)
	if err != nil {
		return nil, 0, err
	}

	sortScope := builder.sort()
	// 验证过滤条件和排序条件的类型有效性
	for _, scope := range []any{filterScope, sortScope} {
		if _, ok := scope.(func(*gorm.DB) *gorm.DB); !ok {
			return nil, 0, errors.New("invalid scope")
		}
	}

	// 使用 WaitAndGo 并行执行数据查询和总数统计操作
	if err := utils.WaitAndGo(func() error {
		query := builder.data.db.WithContext(ctx).
			Model(&list).
			Scopes(filterScope.(func(*gorm.DB) *gorm.DB), sortScope.(func(*gorm.DB) *gorm.DB))

		if builder.needPagination {
			if builder.limit < 1 {
				builder.limit = defaultLimit
			}
			query = query.Offset(int(builder.start)).Limit(int(builder.limit))
		}

		return query.Find(&list).Error
	}, func() error {
		if !builder.needTotal {
			return nil
		}

		return builder.data.db.WithContext(ctx).
			Model(&list).
			Scopes(filterScope.(func(*gorm.DB) *gorm.DB)).
			Count(&total).
			Error
	}); err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// QueryMongoListStrategy MongoDB 查询策略实现
type QueryMongoListStrategy[R any] struct{}

// NewQueryMongoListStrategy 创建 MongoDB 查询策略实例
func NewQueryMongoListStrategy[R any]() *QueryMongoListStrategy[R] {
	return &QueryMongoListStrategy[R]{}
}

// QueryList 实现 MongoDB 查询逻辑
func (s *QueryMongoListStrategy[R]) QueryList(
	ctx context.Context,
	builder *builder[R],
) (list []*R, total int64, err error) {
	filterOpt, err := builder.filter(ctx)
	if err != nil {
		return nil, 0, err
	}

	sortOpt := builder.sort()
	// 验证过滤条件和排序条件的类型有效性
	for _, opt := range []any{filterOpt, sortOpt} {
		_, mOk := opt.(bson.M)
		_, dOk := opt.(bson.D)
		if !mOk && !dOk {
			return nil, 0, errors.New("invalid option")
		}
	}

	// 使用 WaitAndGo 并行执行数据查询和总数统计操作
	if err := utils.WaitAndGo(func() error {
		findOpt := options.Find().SetSort(sortOpt)
		if builder.needPagination {
			if builder.limit < 1 {
				builder.limit = defaultLimit
			}
			findOpt.SetSkip(int64(builder.start)).SetLimit(int64(builder.limit))
		}

		cursor, err := builder.data.mongodb.Find(ctx, filterOpt, findOpt)
		if err != nil {
			return err
		}
		defer func(cursor *mongo.Cursor, ctx context.Context) {
			_ = cursor.Close(ctx)
		}(cursor, ctx)

		return cursor.All(ctx, &list)
	}, func() error {
		if !builder.needTotal {
			return nil
		}

		total, err = builder.data.mongodb.CountDocuments(ctx, filterOpt)
		if err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, 0, err
	}

	return list, total, nil
}
