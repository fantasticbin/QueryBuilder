package builder

import (
	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/bson"
	"gorm.io/gorm"
)

// ScopeConfigurer 构建器配置回调类型
// 用于 List.SetScope，在 Query 内部创建好构建器后自动调用
// 泛型参数:
//
//	R: 查询结果的实体类型
type ScopeConfigurer[R any] func(querier Querier[R])

// NewGormScope 创建一个 GORM 构建器的 ScopeConfigurer
// 用于 List.SetScope，在 Query 内部自动设置 filter 和 sort，无需手写中间件
// 参数:
//
//	filter - GORM 过滤条件（GormScope 类型），可为 nil
//	sort   - GORM 排序条件（GormScope 类型），可为 nil
func NewGormScope[R any](filter func(*gorm.DB) *gorm.DB, sort func(*gorm.DB) *gorm.DB) ScopeConfigurer[R] {
	return func(querier Querier[R]) {
		if gb, ok := querier.(*GormBuilder[R]); ok {
			if filter != nil {
				gb.SetFilter(filter)
			}
			if sort != nil {
				gb.SetSort(sort)
			}
		}
	}
}

// NewMongoScope 创建一个 MongoDB 构建器的 ScopeConfigurer
// 用于 List.SetScope，在 Query 内部自动设置 filter 和 sort，无需手写中间件
// 参数:
//
//	filter - MongoDB 过滤条件（bson.D 类型），可为 nil
//	sort   - MongoDB 排序条件（bson.D 类型），可为 nil
func NewMongoScope[R any](filter bson.D, sort bson.D) ScopeConfigurer[R] {
	return func(querier Querier[R]) {
		if mb, ok := querier.(*MongoBuilder[R]); ok {
			if filter != nil {
				mb.SetFilter(filter)
			}
			if sort != nil {
				mb.SetSort(sort)
			}
		}
	}
}

// NewElasticSearchScope 创建一个 ElasticSearch 构建器的 ScopeConfigurer
// 用于 List.SetScope，在 Query 内部自动设置 filter 和 sort，无需手写中间件
// 参数:
//
//	filter - ElasticSearch 过滤条件（elastic.Query 类型），可为 nil
//	sort   - ElasticSearch 排序条件（elastic.Sorter 切片），可为 nil
func NewElasticSearchScope[R any](filter elastic.Query, sort ...elastic.Sorter) ScopeConfigurer[R] {
	return func(querier Querier[R]) {
		if eb, ok := querier.(*ElasticSearchBuilder[R]); ok {
			if filter != nil {
				eb.SetFilter(filter)
			}
			if len(sort) > 0 {
				eb.SetSort(sort...)
			}
		}
	}
}
