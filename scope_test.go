package builder

import (
	"context"
	"testing"

	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/bson"
	"gorm.io/gorm"
)

// TestSetScopeWithGorm 测试通过 List.SetScope + NewGormScope 设置 filter/sort
func TestSetScopeWithGorm(t *testing.T) {
	ctx := context.Background()

	t.Run("设置filter和sort", func(t *testing.T) {
		gormBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(gormBuilder)

		list.SetScope(NewGormScope[TestEntity](
			func(db *gorm.DB) *gorm.DB {
				return db.Where("name = ?", "Alice")
			},
			func(db *gorm.DB) *gorm.DB {
				return db.Order("id DESC")
			},
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if gb, ok := b.(*GormBuilder[TestEntity]); ok {
				if gb.filter == nil {
					t.Error("expected filter to be set via SetScope + NewGormScope")
				}
				if gb.sort == nil {
					t.Error("expected sort to be set via SetScope + NewGormScope")
				}
			} else {
				t.Error("expected builder to be *GormBuilder[TestEntity]")
			}
			return []*TestEntity{{ID: 1, Name: "Alice", Age: 25}}, 1, nil
		})

		result, total, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Errorf("expected total 1, got %d", total)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 result, got %d", len(result))
		}
	})

	t.Run("filter为nil", func(t *testing.T) {
		gormBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(gormBuilder)

		list.SetScope(NewGormScope[TestEntity](
			nil,
			func(db *gorm.DB) *gorm.DB {
				return db.Order("id DESC")
			},
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if gb, ok := b.(*GormBuilder[TestEntity]); ok {
				if gb.filter != nil {
					t.Error("expected filter to be nil")
				}
				if gb.sort == nil {
					t.Error("expected sort to be set")
				}
			}
			return []*TestEntity{}, 0, nil
		})

		_, _, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("sort为nil", func(t *testing.T) {
		gormBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(gormBuilder)

		list.SetScope(NewGormScope[TestEntity](
			func(db *gorm.DB) *gorm.DB {
				return db.Where("age > ?", 18)
			},
			nil,
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if gb, ok := b.(*GormBuilder[TestEntity]); ok {
				if gb.filter == nil {
					t.Error("expected filter to be set")
				}
				if gb.sort != nil {
					t.Error("expected sort to be nil")
				}
			}
			return []*TestEntity{}, 0, nil
		})

		_, _, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestSetScopeWithMongo 测试通过 List.SetScope + NewMongoScope 设置 filter/sort
func TestSetScopeWithMongo(t *testing.T) {
	ctx := context.Background()

	t.Run("设置filter和sort", func(t *testing.T) {
		mongoBuilder := NewMongoBuilder[TestEntity](NewDBProxy(nil, nil, nil))

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(mongoBuilder)

		list.SetScope(NewMongoScope[TestEntity](
			bson.D{{Key: "name", Value: "Alice"}},
			bson.D{{Key: "id", Value: -1}},
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if mb, ok := b.(*MongoBuilder[TestEntity]); ok {
				if mb.filter == nil {
					t.Error("expected filter to be set")
				}
				if len(mb.filter) != 1 || mb.filter[0].Key != "name" {
					t.Errorf("expected filter key 'name', got %v", mb.filter)
				}
				if mb.sort == nil {
					t.Error("expected sort to be set")
				}
				if len(mb.sort) != 1 || mb.sort[0].Key != "id" {
					t.Errorf("expected sort key 'id', got %v", mb.sort)
				}
			} else {
				t.Error("expected builder to be *MongoBuilder[TestEntity]")
			}
			return []*TestEntity{{ID: 1, Name: "Alice", Age: 25}}, 1, nil
		})

		result, total, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Errorf("expected total 1, got %d", total)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 result, got %d", len(result))
		}
	})

	t.Run("filter为nil", func(t *testing.T) {
		mongoBuilder := NewMongoBuilder[TestEntity](NewDBProxy(nil, nil, nil))

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(mongoBuilder)

		list.SetScope(NewMongoScope[TestEntity](
			nil,
			bson.D{{Key: "id", Value: 1}},
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if mb, ok := b.(*MongoBuilder[TestEntity]); ok {
				if mb.filter != nil {
					t.Error("expected filter to be nil")
				}
				if mb.sort == nil {
					t.Error("expected sort to be set")
				}
			}
			return []*TestEntity{}, 0, nil
		})

		_, _, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("sort为nil", func(t *testing.T) {
		mongoBuilder := NewMongoBuilder[TestEntity](NewDBProxy(nil, nil, nil))

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(mongoBuilder)

		list.SetScope(NewMongoScope[TestEntity](
			bson.D{{Key: "age", Value: bson.D{{Key: "$gt", Value: 18}}}},
			nil,
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if mb, ok := b.(*MongoBuilder[TestEntity]); ok {
				if mb.filter == nil {
					t.Error("expected filter to be set")
				}
				if mb.sort != nil {
					t.Error("expected sort to be nil")
				}
			}
			return []*TestEntity{}, 0, nil
		})

		_, _, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestSetScopeWithElasticSearch 测试通过 List.SetScope + NewElasticSearchScope 设置 filter/sort
func TestSetScopeWithElasticSearch(t *testing.T) {
	ctx := context.Background()

	t.Run("设置filter和sort", func(t *testing.T) {
		esBuilder := NewElasticSearchBuilder[TestEntity](NewDBProxy(nil, nil, nil), "test_index")

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(esBuilder)

		list.SetScope(NewElasticSearchScope[TestEntity](
			elastic.NewTermQuery("status", "active"),
			elastic.NewFieldSort("created_at").Order(false),
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if eb, ok := b.(*ElasticSearchBuilder[TestEntity]); ok {
				if eb.filter == nil {
					t.Error("expected filter to be set")
				}
				if len(eb.sort) != 1 {
					t.Errorf("expected 1 sort, got %d", len(eb.sort))
				}
			} else {
				t.Error("expected builder to be *ElasticSearchBuilder[TestEntity]")
			}
			return []*TestEntity{{ID: 1, Name: "Alice", Age: 25}}, 1, nil
		})

		result, total, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 1 {
			t.Errorf("expected total 1, got %d", total)
		}
		if len(result) != 1 {
			t.Errorf("expected 1 result, got %d", len(result))
		}
	})

	t.Run("filter为nil", func(t *testing.T) {
		esBuilder := NewElasticSearchBuilder[TestEntity](NewDBProxy(nil, nil, nil), "test_index")

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(esBuilder)

		list.SetScope(NewElasticSearchScope[TestEntity](
			nil,
			elastic.NewFieldSort("id").Order(true),
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if eb, ok := b.(*ElasticSearchBuilder[TestEntity]); ok {
				if eb.filter != nil {
					t.Error("expected filter to be nil")
				}
				if len(eb.sort) != 1 {
					t.Errorf("expected 1 sort, got %d", len(eb.sort))
				}
			}
			return []*TestEntity{}, 0, nil
		})

		_, _, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("无sort参数", func(t *testing.T) {
		esBuilder := NewElasticSearchBuilder[TestEntity](NewDBProxy(nil, nil, nil), "test_index")

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(esBuilder)

		list.SetScope(NewElasticSearchScope[TestEntity](
			elastic.NewMatchAllQuery(),
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if eb, ok := b.(*ElasticSearchBuilder[TestEntity]); ok {
				if eb.filter == nil {
					t.Error("expected filter to be set")
				}
				if len(eb.sort) != 0 {
					t.Errorf("expected 0 sort, got %d", len(eb.sort))
				}
			}
			return []*TestEntity{}, 0, nil
		})

		_, _, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("多个sort参数", func(t *testing.T) {
		esBuilder := NewElasticSearchBuilder[TestEntity](NewDBProxy(nil, nil, nil), "test_index")

		list := NewList[TestEntity, TestFilter, TestSort]()
		list.SetQuerier(esBuilder)

		list.SetScope(NewElasticSearchScope[TestEntity](
			elastic.NewTermQuery("status", "active"),
			elastic.NewFieldSort("created_at").Order(false),
			elastic.NewFieldSort("id").Order(true),
		))

		list.Use(func(
			ctx context.Context,
			b Querier[TestEntity],
			next func(context.Context) ([]*TestEntity, int64, error),
		) ([]*TestEntity, int64, error) {
			if eb, ok := b.(*ElasticSearchBuilder[TestEntity]); ok {
				if eb.filter == nil {
					t.Error("expected filter to be set")
				}
				if len(eb.sort) != 2 {
					t.Errorf("expected 2 sorts, got %d", len(eb.sort))
				}
			}
			return []*TestEntity{}, 0, nil
		})

		_, _, err := list.Query(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestSetScopeNil 测试 SetScope 为 nil 时不影响查询流程
func TestSetScopeNil(t *testing.T) {
	ctx := context.Background()

	gormBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

	list := NewList[TestEntity, TestFilter, TestSort]()
	list.SetQuerier(gormBuilder)

	// 不设置 scope
	list.Use(func(
		ctx context.Context,
		b Querier[TestEntity],
		next func(context.Context) ([]*TestEntity, int64, error),
	) ([]*TestEntity, int64, error) {
		if gb, ok := b.(*GormBuilder[TestEntity]); ok {
			if gb.filter != nil {
				t.Error("expected filter to be nil when no scope is set")
			}
			if gb.sort != nil {
				t.Error("expected sort to be nil when no scope is set")
			}
		}
		return []*TestEntity{}, 0, nil
	})

	_, _, err := list.Query(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSetScopeBeforeMiddleware 测试 SetScope 在中间件之前执行
func TestSetScopeBeforeMiddleware(t *testing.T) {
	ctx := context.Background()

	gormBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

	list := NewList[TestEntity, TestFilter, TestSort]()
	list.SetQuerier(gormBuilder)

	// 设置 scope
	list.SetScope(NewGormScope[TestEntity](
		func(db *gorm.DB) *gorm.DB {
			return db.Where("name = ?", "Alice")
		},
		nil,
	))

	// 中间件中验证 scope 已经被应用
	list.Use(func(
		ctx context.Context,
		b Querier[TestEntity],
		next func(context.Context) ([]*TestEntity, int64, error),
	) ([]*TestEntity, int64, error) {
		if gb, ok := b.(*GormBuilder[TestEntity]); ok {
			if gb.filter == nil {
				t.Error("expected filter to be set before middleware execution")
			}
		}
		return []*TestEntity{}, 0, nil
	})

	_, _, err := list.Query(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}