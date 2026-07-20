package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type aggregateCacheRow struct {
	Total int64 `json:"total" bson:"total"`
}

func TestAggregateCacheMiddlewareHit(t *testing.T) {
	t.Parallel()

	cache := newMockCache()
	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[aggregateCacheRow](data)
	builder.SetSpec(queryagg.Spec{
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	builder.Use(AggregateCacheMiddlewareWithKeyBuilder[aggregateCacheRow](
		cache,
		time.Minute,
		AggregateDefaultCacheKeyBuilder{
			Prefix: "orders",
			Hints:  CacheKeyHints{Filter: map[string]any{"status": "paid"}},
		},
	))
	queryCount := 0
	builder.Use(func(
		context.Context,
		queryagg.Querier[aggregateCacheRow],
		queryagg.Handler[aggregateCacheRow],
	) (*queryagg.Result[aggregateCacheRow], error) {
		queryCount++
		return &queryagg.Result[aggregateCacheRow]{Rows: []*aggregateCacheRow{{Total: 7}}, Total: 42}, nil
	})

	for range 2 {
		result, err := builder.Query(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Rows) != 1 || result.Rows[0].Total != 7 || result.Total != 42 {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
	if queryCount != 1 {
		t.Fatalf("expected one backend query, got %d", queryCount)
	}
}

func TestAggregateDefaultCacheKeyBuilderIsolation(t *testing.T) {
	t.Parallel()

	builder := AggregateDefaultCacheKeyBuilder{Prefix: "orders"}
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

	changedStart := base
	changedStart.Start = 10
	changedStart.Spec.Start = 10
	if baseKey == builder.Build(context.Background(), changedStart) {
		t.Fatal("different aggregate page starts must not share a cache key")
	}

	changedNeedTotal := base
	changedNeedTotal.NeedTotal = true
	if baseKey == builder.Build(context.Background(), changedNeedTotal) {
		t.Fatal("different aggregate total settings must not share a cache key")
	}

	changedTotalLimit := base
	changedTotalLimit.TotalLimit = 1000
	if baseKey == builder.Build(context.Background(), changedTotalLimit) {
		t.Fatal("different aggregate total limits must not share a cache key")
	}

	if baseKey != builder.Build(context.Background(), base) {
		t.Fatal("equivalent aggregate metadata must produce a stable key")
	}
}

func TestAggregateDefaultCacheKeyBuilderNoRedundantPaginationKeys(t *testing.T) {
	t.Parallel()

	builder := AggregateDefaultCacheKeyBuilder{Prefix: "orders"}
	meta := queryagg.Meta{
		DataSource: core.MongoDB,
		Spec: queryagg.Spec{
			Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
			Start:   5,
			Limit:   20,
		},
		Start:      5,
		NeedTotal:  true,
		TotalLimit: 100,
	}
	fromBuilder := builder.Build(context.Background(), meta)

	// Rebuild the canonical payload WITHOUT the redundant pagination.start/limit
	// to prove Build no longer carries them — Spec already serializes Start/Limit.
	payload := map[string]any{
		"prefix":     "orders",
		"datasource": meta.DataSource.String(),
		"spec":       meta.Spec,
		"pagination": map[string]any{
			"need_total":  meta.NeedTotal,
			"total_limit": meta.TotalLimit,
		},
	}
	sum := sha256.Sum256([]byte(canonicalCachePayload(payload)))
	expected := "qb:agg:cache:" + hex.EncodeToString(sum[:])
	if fromBuilder != expected {
		t.Fatal("cache key still depends on redundant pagination.start/limit")
	}
}
