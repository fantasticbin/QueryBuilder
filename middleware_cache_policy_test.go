package builder

import (
	"context"
	"testing"
	"time"
)

type mockCache struct {
	store map[string][]byte
}

func newMockCache() *mockCache { return &mockCache{store: map[string][]byte{}} }
func (m *mockCache) Get(ctx context.Context, key string) ([]byte, bool) { v, ok := m.store[key]; return v, ok }
func (m *mockCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) { m.store[key] = value }

func TestDefaultCacheKeyBuilderStability(t *testing.T) {
	ctx := context.Background()
	ctx = withQueryMeta(ctx, &QueryMeta{DataSource: MySQL, Start: 0, Limit: 20, NeedTotal: true, NeedPagination: true, Fields: []string{"id", "name"}})
	ctx = WithCacheKeyHints(ctx, CacheKeyHints{Filter: map[string]any{"status": "active"}, Sort: map[string]any{"id": "desc"}, Extra: map[string]any{"tenant_id": "t1"}})

	builder := DefaultCacheKeyBuilder{Prefix: "users"}
	k1 := builder.Build(ctx)
	k2 := builder.Build(ctx)
	if k1 != k2 {
		t.Fatalf("expected stable key, got %s != %s", k1, k2)
	}
}

func TestCacheMiddlewareWithDefaultKeyBuilderHit(t *testing.T) {
	cache := newMockCache()
	ctx := context.Background()
	ctx = withQueryMeta(ctx, &QueryMeta{DataSource: MySQL, Start: 0, Limit: 10, NeedTotal: true, NeedPagination: true, Fields: []string{"id"}})
	ctx = WithCacheKeyHints(ctx, CacheKeyHints{Extra: map[string]any{"tenant_id": "tenant-a"}})

	calls := 0
	mw := CacheMiddlewareWithKeyBuilder[testUser](cache, time.Minute, DefaultCacheKeyBuilder{Prefix: "user-list"})
	next := func(ctx context.Context) ([]*testUser, int64, error) {
		calls++
		return []*testUser{{ID: 1, Name: "A"}}, 1, nil
	}

	_, _, err := mw(ctx, nil, next)
	if err != nil { t.Fatal(err) }
	_, _, err = mw(ctx, nil, next)
	if err != nil { t.Fatal(err) }

	if calls != 1 {
		t.Fatalf("expected backend called once due to cache hit, got %d", calls)
	}
}

type testUser struct {
	ID   int
	Name string
}
