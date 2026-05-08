package builder

import (
	"context"
	"testing"
	"time"
)

type mockCache struct{ store map[string][]byte }

func newMockCache() *mockCache { return &mockCache{store: map[string][]byte{}} }
func (m *mockCache) Get(ctx context.Context, key string) ([]byte, bool) { v, ok := m.store[key]; return v, ok }
func (m *mockCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) { m.store[key] = value }

func baseMetaCtx() context.Context {
	return withQueryMeta(context.Background(), &QueryMeta{DataSource: MySQL, Start: 0, Limit: 20, NeedTotal: true, NeedPagination: true, Fields: []string{"id", "name"}})
}

func TestDefaultCacheKeyBuilderStability(t *testing.T) {
	ctx := WithCacheKeyHints(baseMetaCtx(), CacheKeyHints{Filter: map[string]any{"status": "active"}, Sort: map[string]any{"id": "desc"}, Extra: map[string]any{"tenant_id": "t1"}})
	builder := DefaultCacheKeyBuilder{Prefix: "users"}
	k1 := builder.Build(ctx)
	k2 := builder.Build(ctx)
	if k1 != k2 { t.Fatalf("expected stable key, got %s != %s", k1, k2) }
}

func TestDefaultCacheKeyBuilderIsolationByFilterAndSort(t *testing.T) {
	builder := DefaultCacheKeyBuilder{Prefix: "users"}
	k1 := builder.Build(WithCacheKeyHints(baseMetaCtx(), CacheKeyHints{Filter: map[string]any{"status": "active"}, Sort: map[string]any{"id": "asc"}}))
	k2 := builder.Build(WithCacheKeyHints(baseMetaCtx(), CacheKeyHints{Filter: map[string]any{"status": "inactive"}, Sort: map[string]any{"id": "asc"}}))
	if k1 == k2 { t.Fatalf("expected keys to differ when filter changes") }
}

func TestDefaultCacheKeyBuilderPrefixIsolation(t *testing.T) {
	ctx := baseMetaCtx()
	k1 := DefaultCacheKeyBuilder{Prefix: "users"}.Build(ctx)
	k2 := DefaultCacheKeyBuilder{Prefix: "orders"}.Build(ctx)
	if k1 == k2 { t.Fatalf("expected keys to differ for different prefix") }
}

func TestDefaultCacheKeyBuilderWithoutHints(t *testing.T) {
	ctx := baseMetaCtx()
	builder := DefaultCacheKeyBuilder{Prefix: "users"}
	if builder.Build(ctx) == "" { t.Fatalf("key should not be empty when hints missing") }
}

func TestDefaultCacheKeyBuilderOptionalHintsProvider(t *testing.T) {
	ctx := baseMetaCtx()
	builder := DefaultCacheKeyBuilder{Prefix: "users", OptionalHintsProvider: func(ctx context.Context) CacheKeyHints {
		return CacheKeyHints{Extra: map[string]any{"tenant_id": "auto"}}
	}}
	k1 := builder.Build(ctx)
	k2 := builder.Build(WithCacheKeyHints(ctx, CacheKeyHints{Extra: map[string]any{"tenant_id": "manual"}}))
	if k1 == k2 { t.Fatalf("context hints should override provider hints") }
}

func TestCacheMiddlewareWithDefaultKeyBuilderHit(t *testing.T) {
	cache := newMockCache()
	ctx := withQueryMeta(context.Background(), &QueryMeta{DataSource: MySQL, Start: 0, Limit: 10, NeedTotal: true, NeedPagination: true, Fields: []string{"id"}})
	ctx = WithCacheKeyHints(ctx, CacheKeyHints{Extra: map[string]any{"tenant_id": "tenant-a"}})
	calls := 0
	mw := CacheMiddlewareWithKeyBuilder[testUser](cache, time.Minute, DefaultCacheKeyBuilder{Prefix: "user-list"})
	next := func(ctx context.Context) ([]*testUser, int64, error) { calls++; return []*testUser{{ID: 1, Name: "A"}}, 1, nil }
	_, _, _ = mw(ctx, nil, next)
	_, _, _ = mw(ctx, nil, next)
	if calls != 1 { t.Fatalf("expected backend called once due to cache hit, got %d", calls) }
}

func TestCacheMiddlewareWithNilKeyBuilder(t *testing.T) {
	cache := newMockCache()
	ctx := baseMetaCtx()
	mw := CacheMiddlewareWithKeyBuilder[testUser](cache, time.Minute, nil)
	_, _, err := mw(ctx, nil, func(ctx context.Context) ([]*testUser, int64, error) { return []*testUser{{ID: 1}}, 1, nil })
	if err != nil { t.Fatalf("nil keyBuilder should not cause error: %v", err) }
}

type testUser struct { ID int; Name string }
