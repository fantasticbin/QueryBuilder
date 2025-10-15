package builder

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fantasticbin/QueryBuilder/util"
	"github.com/olivere/elastic/v7"
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
	if err := util.WaitAndGo(func() error {
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
	if err := util.WaitAndGo(func() error {
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

// QueryElasticsearchListStrategy Elasticsearch 查询策略实现
type QueryElasticsearchListStrategy[R any] struct{}

// NewQueryElasticsearchListStrategy 创建 Elasticsearch 查询策略实例
func NewQueryElasticsearchListStrategy[R any]() *QueryElasticsearchListStrategy[R] {
	return &QueryElasticsearchListStrategy[R]{}
}

// QueryList 实现 Elasticsearch 查询逻辑
func (s *QueryElasticsearchListStrategy[R]) QueryList(
	ctx context.Context,
	builder *builder[R],
) (list []*R, total int64, err error) {
	// 检查 Elasticsearch 索引配置
	if builder.esIndex == "" {
		return nil, 0, errors.New("elasticsearch index not configured")
	}

	queryOpt, err := builder.filter(ctx)
	if err != nil {
		return nil, 0, err
	}

	sortOpt := builder.sort()
	// 验证过滤条件的类型有效性
	query, ok := queryOpt.(elastic.Query)
	if !ok {
		return nil, 0, errors.New("invalid query option")
	}

	// 使用 WaitAndGo 并行执行数据查询和总数统计操作
	if err := util.WaitAndGo(func() error {
		searchService := builder.data.elasticsearch.Search().
			Index(builder.esIndex).
			Query(query)

		// 处理排序
		if sortOpt != nil {
			switch sort := sortOpt.(type) {
			case elastic.Sorter:
				searchService = searchService.SortBy(sort)
			case []elastic.Sorter:
				for _, s := range sort {
					searchService = searchService.SortBy(s)
				}
			default:
				return errors.New("invalid sort option: must be elastic.Sorter or []elastic.Sorter")
			}
		}

		if builder.needPagination {
			if builder.limit < 1 {
				builder.limit = defaultLimit
			}
			searchService = searchService.From(int(builder.start)).Size(int(builder.limit))
		}

		searchResult, err := searchService.Do(ctx)
		if err != nil {
			return err
		}

		// 解析查询结果
		for _, hit := range searchResult.Hits.Hits {
			var item R
			if err := json.Unmarshal(hit.Source, &item); err != nil {
				return err
			}
			list = append(list, &item)
		}

		return nil
	}, func() error {
		if !builder.needTotal {
			return nil
		}

		countService := builder.data.elasticsearch.Count().
			Index(builder.esIndex).
			Query(query)
		count, err := countService.Do(ctx)
		if err != nil {
			return err
		}

		total = count

		return nil
	}); err != nil {
		return nil, 0, err
	}

	return list, total, nil
}
