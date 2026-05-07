package builder

import (
	"context"
	"sync"
	"testing"

	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"
)

// CloneTestEntity Clone 测试用实体
type CloneTestEntity struct {
	ID   uint32
	Name string
}

// --- GormBuilder Clone 状态隔离测试 ---

func TestGormBuilder_Clone_StateIsolation(t *testing.T) {
	original := NewGormBuilder[CloneTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	original.SetLimit(100)
	original.SetStart(20)
	original.SetFields("id", "name")
	original.SetCursorField("id")
	original.SetCursorValue(uint32(50))
	original.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", "active")
	})

	cloned := original.Clone()

	// 修改 clone 不影响 original
	cloned.SetLimit(200)
	cloned.SetStart(0)
	cloned.SetFields("id", "name", "age")
	cloned.SetCursorField("created_at")
	cloned.SetCursorValue(int64(999))

	// 验证 original 未被修改
	if original.builder.limit != 100 {
		t.Errorf("original limit should be 100, got %d", original.builder.limit)
	}
	if original.builder.start != 20 {
		t.Errorf("original start should be 20, got %d", original.builder.start)
	}
	if len(original.builder.fields) != 2 {
		t.Errorf("original fields should have 2 items, got %d", len(original.builder.fields))
	}
	if len(original.builder.cursorFields) != 1 || original.builder.cursorFields[0] != "id" {
		t.Errorf("original cursorFields should be [id], got %v", original.builder.cursorFields)
	}
	if len(original.builder.cursorValues) != 1 || original.builder.cursorValues[0] != uint32(50) {
		t.Errorf("original cursorValues should be [50], got %v", original.builder.cursorValues)
	}

	// 验证 cloned 已修改
	if cloned.builder.limit != 200 {
		t.Errorf("cloned limit should be 200, got %d", cloned.builder.limit)
	}
	if cloned.builder.start != 0 {
		t.Errorf("cloned start should be 0, got %d", cloned.builder.start)
	}
	if len(cloned.builder.fields) != 3 {
		t.Errorf("cloned fields should have 3 items, got %d", len(cloned.builder.fields))
	}
}

// --- MongoBuilder Clone 状态隔离测试 ---

func TestMongoBuilder_Clone_StateIsolation(t *testing.T) {
	original := NewMongoBuilder[CloneTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	original.SetLimit(50)
	original.SetFields("id", "name")
	original.SetFilter(bson.D{{Key: "status", Value: "active"}})
	original.SetSort(bson.D{{Key: "id", Value: -1}})

	cloned := original.Clone()

	// 修改 clone 不影响 original
	cloned.SetLimit(500)
	cloned.SetFields("id")
	cloned.SetFilter(bson.D{{Key: "type", Value: "premium"}})
	cloned.SetSort(bson.D{{Key: "created_at", Value: 1}})

	// 验证 original 未被修改
	if original.builder.limit != 50 {
		t.Errorf("original limit should be 50, got %d", original.builder.limit)
	}
	if len(original.builder.fields) != 2 {
		t.Errorf("original fields should have 2 items, got %d", len(original.builder.fields))
	}
	if len(original.filter) != 1 || original.filter[0].Key != "status" {
		t.Errorf("original filter should be [{status active}], got %v", original.filter)
	}
	if len(original.sort) != 1 || original.sort[0].Key != "id" {
		t.Errorf("original sort should be [{id -1}], got %v", original.sort)
	}

	// 验证 cloned 已修改
	if cloned.builder.limit != 500 {
		t.Errorf("cloned limit should be 500, got %d", cloned.builder.limit)
	}
	if len(cloned.filter) != 1 || cloned.filter[0].Key != "type" {
		t.Errorf("cloned filter should be [{type premium}], got %v", cloned.filter)
	}
}

// --- ElasticSearchBuilder Clone 状态隔离测试 ---

func TestElasticSearchBuilder_Clone_StateIsolation(t *testing.T) {
	original := NewElasticSearchBuilder[CloneTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "index_v1")
	original.SetLimit(30)
	original.SetFields("id", "name")
	original.SetFilter(elastic.NewTermQuery("status", "active"))
	original.SetSort(elastic.NewFieldSort("id").Order(true))

	cloned := original.Clone()

	// 修改 clone 不影响 original
	cloned.SetLimit(300)
	cloned.SetFields("id")
	cloned.SetESIndex("index_v2")
	cloned.SetFilter(elastic.NewTermQuery("type", "premium"))
	cloned.SetSort(elastic.NewFieldSort("created_at").Order(false))

	// 验证 original 未被修改
	if original.builder.limit != 30 {
		t.Errorf("original limit should be 30, got %d", original.builder.limit)
	}
	if len(original.builder.fields) != 2 {
		t.Errorf("original fields should have 2 items, got %d", len(original.builder.fields))
	}
	if original.index != "index_v1" {
		t.Errorf("original index should be 'index_v1', got %q", original.index)
	}
	if len(original.sort) != 1 {
		t.Errorf("original sort should have 1 item, got %d", len(original.sort))
	}

	// 验证 cloned 已修改
	if cloned.builder.limit != 300 {
		t.Errorf("cloned limit should be 300, got %d", cloned.builder.limit)
	}
	if cloned.index != "index_v2" {
		t.Errorf("cloned index should be 'index_v2', got %q", cloned.index)
	}
}

// --- Clone 中间件隔离测试 ---

func TestGormBuilder_Clone_MiddlewareIsolation(t *testing.T) {
	original := NewGormBuilder[CloneTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	original.Use(func(ctx context.Context, builder Querier[CloneTestEntity], next func(context.Context) ([]*CloneTestEntity, int64, error)) ([]*CloneTestEntity, int64, error) {
		return next(ctx)
	})

	cloned := original.Clone()

	// 向 clone 添加额外中间件
	cloned.Use(func(ctx context.Context, builder Querier[CloneTestEntity], next func(context.Context) ([]*CloneTestEntity, int64, error)) ([]*CloneTestEntity, int64, error) {
		return next(ctx)
	})

	// original 应只有 1 个中间件
	if len(original.builder.middlewares) != 1 {
		t.Errorf("original should have 1 middleware, got %d", len(original.builder.middlewares))
	}
	// cloned 应有 2 个中间件
	if len(cloned.builder.middlewares) != 2 {
		t.Errorf("cloned should have 2 middlewares, got %d", len(cloned.builder.middlewares))
	}
}

// --- Clone 链式调用兼容性测试 ---

func TestGormBuilder_Clone_ChainAPI(t *testing.T) {
	original := NewGormBuilder[CloneTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	original.SetLimit(100).SetFields("id", "name")

	// Clone 后继续链式调用
	cloned := original.Clone()
	cloned.SetLimit(200).SetStart(10).SetFields("id")

	if original.builder.limit != 100 {
		t.Errorf("original limit should be 100, got %d", original.builder.limit)
	}
	if cloned.builder.limit != 200 {
		t.Errorf("cloned limit should be 200, got %d", cloned.builder.limit)
	}
	if cloned.builder.start != 10 {
		t.Errorf("cloned start should be 10, got %d", cloned.builder.start)
	}
}

// --- 并发分叉查询测试（race 模式验证） ---

func TestGormBuilder_Clone_ConcurrentSafety(t *testing.T) {
	original := NewGormBuilder[CloneTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	original.SetLimit(100)
	original.SetFields("id", "name")
	original.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", "active")
	})

	// 使用中间件短路，避免真实数据库查询
	shortCircuit := func(ctx context.Context, builder Querier[CloneTestEntity], next func(context.Context) ([]*CloneTestEntity, int64, error)) ([]*CloneTestEntity, int64, error) {
		return []*CloneTestEntity{{ID: 1, Name: "test"}}, 1, nil
	}
	original.Use(shortCircuit)

	var wg sync.WaitGroup
	const goroutines = 10

	// 并发 Clone 并独立修改和查询
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cloned := original.Clone()
			cloned.SetLimit(uint32(idx + 1))
			cloned.SetStart(uint32(idx * 10))
			cloned.SetFields("id")

			result, total, err := cloned.QueryList(context.Background())
			if err != nil {
				t.Errorf("goroutine %d: unexpected error: %v", idx, err)
				return
			}
			if total != 1 {
				t.Errorf("goroutine %d: expected total 1, got %d", idx, total)
			}
			if len(result) != 1 {
				t.Errorf("goroutine %d: expected 1 result, got %d", idx, len(result))
			}
		}(i)
	}

	wg.Wait()

	// 验证 original 未被任何 goroutine 修改
	if original.builder.limit != 100 {
		t.Errorf("original limit should still be 100, got %d", original.builder.limit)
	}
	if original.builder.start != 0 {
		t.Errorf("original start should still be 0, got %d", original.builder.start)
	}
	if len(original.builder.fields) != 2 {
		t.Errorf("original fields should still have 2 items, got %d", len(original.builder.fields))
	}
}

func TestMongoBuilder_Clone_ConcurrentSafety(t *testing.T) {
	original := NewMongoBuilder[CloneTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	original.SetLimit(50)
	original.SetFilter(bson.D{{Key: "status", Value: "active"}})

	shortCircuit := func(ctx context.Context, builder Querier[CloneTestEntity], next func(context.Context) ([]*CloneTestEntity, int64, error)) ([]*CloneTestEntity, int64, error) {
		return []*CloneTestEntity{{ID: 1, Name: "test"}}, 1, nil
	}
	original.Use(shortCircuit)

	var wg sync.WaitGroup
	const goroutines = 10

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cloned := original.Clone()
			cloned.SetLimit(uint32(idx + 1))
			cloned.SetFilter(bson.D{{Key: "type", Value: idx}})

			result, total, err := cloned.QueryList(context.Background())
			if err != nil {
				t.Errorf("goroutine %d: unexpected error: %v", idx, err)
				return
			}
			if total != 1 {
				t.Errorf("goroutine %d: expected total 1, got %d", idx, total)
			}
			if len(result) != 1 {
				t.Errorf("goroutine %d: expected 1 result, got %d", idx, len(result))
			}
		}(i)
	}

	wg.Wait()

	// 验证 original 未被修改
	if original.builder.limit != 50 {
		t.Errorf("original limit should still be 50, got %d", original.builder.limit)
	}
	if len(original.filter) != 1 || original.filter[0].Key != "status" {
		t.Errorf("original filter should still be [{status active}], got %v", original.filter)
	}
}

func TestElasticSearchBuilder_Clone_ConcurrentSafety(t *testing.T) {
	original := NewElasticSearchBuilder[CloneTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "test_index")
	original.SetLimit(30)
	original.SetFilter(elastic.NewTermQuery("status", "active"))

	shortCircuit := func(ctx context.Context, builder Querier[CloneTestEntity], next func(context.Context) ([]*CloneTestEntity, int64, error)) ([]*CloneTestEntity, int64, error) {
		return []*CloneTestEntity{{ID: 1, Name: "test"}}, 1, nil
	}
	original.Use(shortCircuit)

	var wg sync.WaitGroup
	const goroutines = 10

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			cloned := original.Clone()
			cloned.SetLimit(uint32(idx + 1))
			cloned.SetESIndex("index_" + string(rune('a'+idx)))

			result, total, err := cloned.QueryList(context.Background())
			if err != nil {
				t.Errorf("goroutine %d: unexpected error: %v", idx, err)
				return
			}
			if total != 1 {
				t.Errorf("goroutine %d: expected total 1, got %d", idx, total)
			}
			if len(result) != 1 {
				t.Errorf("goroutine %d: expected 1 result, got %d", idx, len(result))
			}
		}(i)
	}

	wg.Wait()

	// 验证 original 未被修改
	if original.builder.limit != 30 {
		t.Errorf("original limit should still be 30, got %d", original.builder.limit)
	}
	if original.index != "test_index" {
		t.Errorf("original index should still be 'test_index', got %q", original.index)
	}
}
