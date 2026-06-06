package middleware

import (
	"context"
	"iter"
	"testing"
	"time"

	builder "github.com/fantasticbin/QueryBuilder"
	"github.com/fantasticbin/QueryBuilder/core"
)

// --- mockCache 实现 CacheProvider ---

type mockCache struct{ store map[string][]byte }

func newMockCache() *mockCache { return &mockCache{store: map[string][]byte{}} }
func (m *mockCache) Get(_ context.Context, key string) ([]byte, bool) {
	v, ok := m.store[key]
	return v, ok
}
func (m *mockCache) Set(_ context.Context, key string, value []byte, _ time.Duration) {
	m.store[key] = value
}

// --- mockQuerier 实现 builder.Querier[R]，仅 GetQueryMeta() 返回可配置元信息 ---

type mockQuerier[R any] struct {
	meta core.QueryMeta
}

func (m *mockQuerier[R]) GetQueryMeta() core.QueryMeta { return m.meta }

// setter 桩（均返回自身，满足 Querier[R] 链式调用）
func (m *mockQuerier[R]) Use(_ builder.Middleware[R]) builder.Querier[R]                   { return m }
func (m *mockQuerier[R]) SetStart(_ uint32) builder.Querier[R]                             { return m }
func (m *mockQuerier[R]) SetLimit(_ uint32) builder.Querier[R]                             { return m }
func (m *mockQuerier[R]) SetNeedTotal(_ bool) builder.Querier[R]                           { return m }
func (m *mockQuerier[R]) SetNeedPagination(_ bool) builder.Querier[R]                      { return m }
func (m *mockQuerier[R]) SetFields(_ ...string) builder.Querier[R]                         { return m }
func (m *mockQuerier[R]) SetBeforeQueryHook(_ builder.BeforeQueryHook) builder.Querier[R]  { return m }
func (m *mockQuerier[R]) SetAfterQueryHook(_ builder.AfterQueryHook[R]) builder.Querier[R] { return m }
func (m *mockQuerier[R]) SetCursorField(_ ...string) builder.Querier[R]                    { return m }
func (m *mockQuerier[R]) SetCursorValue(_ ...any) builder.Querier[R]                       { return m }

// 查询方法桩
func (m *mockQuerier[R]) QueryList(_ context.Context) (*core.ListResult[R], error) {
	return nil, nil
}
func (m *mockQuerier[R]) QueryCursor(_ context.Context) iter.Seq2[*R, error] { return nil }
func (m *mockQuerier[R]) QueryPage(_ context.Context) (*core.CursorPageResult[R], error) {
	return nil, nil
}
func (m *mockQuerier[R]) Explain(_ context.Context) (string, error) { return "", nil }

// --- 测试辅助 ---

func baseMeta() core.QueryMeta {
	return core.QueryMeta{DataSource: core.Gorm, Start: 0, Limit: 20, NeedTotal: true, NeedPagination: true, Fields: []string{"id", "name"}}
}

type testUser struct {
	ID   int
	Name string
}

// ============================================================================
// CacheKeyBuilder 测试
// ============================================================================

func TestDefaultCacheKeyBuilderStability(t *testing.T) {
	ctx := context.Background()
	kb := DefaultCacheKeyBuilder{
		Prefix: "users",
		Hints:  CacheKeyHints{Filter: map[string]any{"status": "active"}, Sort: map[string]any{"id": "desc"}, Extra: map[string]any{"tenant_id": "t1"}},
	}
	meta := baseMeta()
	k1 := kb.Build(ctx, meta)
	k2 := kb.Build(ctx, meta)
	if k1 != k2 {
		t.Fatalf("expected stable key, got %s != %s", k1, k2)
	}
}

func TestDefaultCacheKeyBuilderIsolationByFilterAndSort(t *testing.T) {
	ctx := context.Background()
	meta := baseMeta()
	b1 := DefaultCacheKeyBuilder{Prefix: "users", Hints: CacheKeyHints{Filter: map[string]any{"status": "active"}, Sort: map[string]any{"id": "asc"}}}
	b2 := DefaultCacheKeyBuilder{Prefix: "users", Hints: CacheKeyHints{Filter: map[string]any{"status": "inactive"}, Sort: map[string]any{"id": "asc"}}}
	k1 := b1.Build(ctx, meta)
	k2 := b2.Build(ctx, meta)
	if k1 == k2 {
		t.Fatalf("expected keys to differ when filter changes")
	}
}

func TestDefaultCacheKeyBuilderPrefixIsolation(t *testing.T) {
	ctx := context.Background()
	meta := baseMeta()
	k1 := DefaultCacheKeyBuilder{Prefix: "users"}.Build(ctx, meta)
	k2 := DefaultCacheKeyBuilder{Prefix: "orders"}.Build(ctx, meta)
	if k1 == k2 {
		t.Fatalf("expected keys to differ for different prefix")
	}
}

func TestDefaultCacheKeyBuilderWithoutHints(t *testing.T) {
	ctx := context.Background()
	meta := baseMeta()
	kb := DefaultCacheKeyBuilder{Prefix: "users"}
	if kb.Build(ctx, meta) == "" {
		t.Fatalf("key should not be empty when hints empty")
	}
}

func TestDefaultCacheKeyBuilderHintsProvider(t *testing.T) {
	ctx := context.Background()
	meta := baseMeta()
	// 无静态 Hints 时使用 HintsProvider
	b1 := DefaultCacheKeyBuilder{Prefix: "users", HintsProvider: func(ctx context.Context) CacheKeyHints {
		return CacheKeyHints{Extra: map[string]any{"tenant_id": "auto"}}
	}}
	// 有静态 Hints 时忽略 HintsProvider
	b2 := DefaultCacheKeyBuilder{Prefix: "users", Hints: CacheKeyHints{Extra: map[string]any{"tenant_id": "manual"}}, HintsProvider: func(ctx context.Context) CacheKeyHints {
		return CacheKeyHints{Extra: map[string]any{"tenant_id": "auto"}}
	}}
	k1 := b1.Build(ctx, meta)
	k2 := b2.Build(ctx, meta)
	if k1 == k2 {
		t.Fatalf("static hints should override provider hints")
	}
}

// ============================================================================
// CacheMiddleware 集成测试
// ============================================================================

func TestCacheMiddlewareWithDefaultKeyBuilderHit(t *testing.T) {
	cache := newMockCache()
	mq := &mockQuerier[testUser]{meta: core.QueryMeta{DataSource: core.Gorm, Start: 0, Limit: 10, NeedTotal: true, NeedPagination: true, Fields: []string{"id"}}}

	ctx := context.Background()
	calls := 0
	keyBuilder := DefaultCacheKeyBuilder{Prefix: "user-list", Hints: CacheKeyHints{Extra: map[string]any{"tenant_id": "tenant-a"}}}
	mw := CacheMiddlewareWithKeyBuilder[testUser](cache, time.Minute, keyBuilder)
	next := func(ctx context.Context) (core.Result[testUser], error) {
		calls++
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1, Name: "A"}}, Total: 1}, nil
	}
	_, _ = mw(ctx, mq, next)
	_, _ = mw(ctx, mq, next)
	if calls != 1 {
		t.Fatalf("expected backend called once due to cache hit, got %d", calls)
	}
}

func TestCacheMiddlewareWithNilKeyBuilder(t *testing.T) {
	cache := newMockCache()
	mq := &mockQuerier[testUser]{meta: baseMeta()}

	ctx := context.Background()
	mw := CacheMiddlewareWithKeyBuilder[testUser](cache, time.Minute, nil)
	_, err := mw(ctx, mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})
	if err != nil {
		t.Fatalf("nil keyBuilder should not cause error: %v", err)
	}
}

func TestCacheMiddlewarePreservesCursorPageResult(t *testing.T) {
	cache := newMockCache()
	mq := &mockQuerier[testUser]{meta: baseMeta()}

	ctx := context.Background()
	calls := 0
	mw := CacheMiddlewareWithKeyBuilder[testUser](cache, time.Minute, DefaultCacheKeyBuilder{Prefix: "user-page"})
	next := func(ctx context.Context) (core.Result[testUser], error) {
		calls++
		return &core.CursorPageResult[testUser]{
			Items:            []*testUser{{ID: 1, Name: "A"}},
			Total:            3,
			HasMore:          true,
			NextCursorValues: []any{1},
		}, nil
	}

	_, _ = mw(ctx, mq, next)
	result, err := mw(ctx, mq, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected backend called once due to cache hit, got %d", calls)
	}
	page, ok := result.(*core.CursorPageResult[testUser])
	if !ok {
		t.Fatalf("expected cursor page result from cache, got %T", result)
	}
	if !page.HasMore || len(page.NextCursorValues) != 1 || page.NextCursorValues[0].(float64) != 1 {
		t.Fatalf("cursor page metadata was not preserved: %+v", page)
	}
}

// TestCloneCacheIsolation 验证 Clone 后各实例使用不同 DefaultCacheKeyBuilder 互不干扰
func TestCloneCacheIsolation(t *testing.T) {
	cache := newMockCache()
	ctx := context.Background()
	meta := baseMeta()

	// 模拟两个 Clone 实例各自使用不同 hints 的 keyBuilder
	kb1 := DefaultCacheKeyBuilder{Prefix: "users", Hints: CacheKeyHints{Filter: map[string]any{"status": "active"}}}
	kb2 := DefaultCacheKeyBuilder{Prefix: "users", Hints: CacheKeyHints{Filter: map[string]any{"status": "inactive"}}}

	k1 := kb1.Build(ctx, meta)
	k2 := kb2.Build(ctx, meta)

	if k1 == k2 {
		t.Fatalf("different hints should produce different cache keys")
	}

	// 验证各自缓存独立
	cache.Set(ctx, k1, []byte(`{"list":[{"ID":1}],"total":1}`), time.Minute)
	if _, ok := cache.Get(ctx, k2); ok {
		t.Fatalf("cache for k1 should not be accessible via k2")
	}
}
