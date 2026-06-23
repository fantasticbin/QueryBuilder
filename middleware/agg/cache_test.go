package agg

import (
	"context"
	"testing"
	"time"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type memoryCache struct {
	values map[string][]byte
}

func newMemoryCache() *memoryCache {
	return &memoryCache{values: make(map[string][]byte)}
}

func (c *memoryCache) Get(_ context.Context, key string) ([]byte, bool) {
	value, ok := c.values[key]
	return value, ok
}

func (c *memoryCache) Set(_ context.Context, key string, value []byte, _ time.Duration) {
	c.values[key] = append([]byte{}, value...)
}

type cacheRow struct {
	Total int64 `json:"total" bson:"total"`
}

func TestCacheMiddlewareHit(t *testing.T) {
	t.Parallel()

	cache := newMemoryCache()
	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[cacheRow](data, queryagg.Spec{
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	builder.Use(CacheWithKeyBuilder[cacheRow](
		cache,
		time.Minute,
		DefaultCacheKeyBuilder{
			Prefix: "orders",
			Hints:  CacheKeyHints{Filter: map[string]any{"status": "paid"}},
		},
	))
	queryCount := 0
	builder.Use(func(
		context.Context,
		queryagg.Querier[cacheRow],
		queryagg.Handler[cacheRow],
	) (*queryagg.Result[cacheRow], error) {
		queryCount++
		return &queryagg.Result[cacheRow]{Rows: []*cacheRow{{Total: 7}}}, nil
	})

	for range 2 {
		result, err := builder.Query(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Rows) != 1 || result.Rows[0].Total != 7 {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
	if queryCount != 1 {
		t.Fatalf("expected one backend query, got %d", queryCount)
	}
}

func TestDefaultCacheKeyBuilderIsolation(t *testing.T) {
	t.Parallel()

	builder := DefaultCacheKeyBuilder{Prefix: "orders"}
	base := queryagg.Meta{
		DataSource: core.MongoDB,
		Spec:       queryagg.Spec{Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}}},
	}
	baseKey := builder.Build(context.Background(), base)
	changed := base
	changed.Spec.Metrics = []queryagg.Metric{{Func: queryagg.Sum, Field: "amount", Alias: "total"}}
	changedKey := builder.Build(context.Background(), changed)
	if baseKey == changedKey {
		t.Fatal("different aggregate specs must not share a cache key")
	}
	if baseKey != builder.Build(context.Background(), base) {
		t.Fatal("equivalent aggregate metadata must produce a stable key")
	}
}
