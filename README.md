**English** | [中文](docs/README_zh.md)

---

# QueryBuilder

A Go library for building type-safe list queries across multiple data sources. Leverages Go 1.26 self-referential generic constraints to provide dedicated query builders for GORM-Compatible DB (GORM, e.g. MySQL/PostgreSQL/SQLite/SQL Server), MongoDB, and ElasticSearch — with zero type assertions, flexible middleware, and a unified query interface.

---

## Features

- **Multi-DataSource Builders**: Dedicated `GormBuilder`, `MongoBuilder`, and `ElasticSearchBuilder` with strongly-typed `SetFilter` / `SetSort` methods.
- **Self-Referential Generics**: Uses Go 1.26 self-referential generic constraints for type-safe fluent chaining.
- **Zero Type Assertions**: All filter/sort operations are fully typed — no `any` casts at runtime.
- **Scope Helpers**: Built-in `SetScope` + `NewGormScope` / `NewMongoScope` / `NewElasticSearchScope` — set filter/sort in one line under `List` mode, no manual middleware or type assertions needed.
- **Unified `Querier` Interface**: A common interface for pagination, middleware, and query execution across all data sources.
- **Middleware Pipeline**: Insert custom logic (timing, logging, caching, etc.) into the query pipeline.
- **Built-in Cache Middleware**: Out-of-the-box `CacheMiddleware` with a pluggable `CacheProvider` interface — bring your own cache backend (Redis, in-memory, etc.).
- **Field Selection**: Use `SetFields` to select only specific fields, reducing bandwidth and memory usage across all data sources.
- **Query Hooks**: `BeforeQueryHook` and `AfterQueryHook` for lightweight pre/post query logic (context injection, logging, metrics, etc.).
- **Query Meta**: Middleware can access `QueryMeta` directly via `builder.GetQueryMeta()` — data source type, pagination/cursor info, and query start time are available without context injection.
- **Dry Run / Explain**: Each builder provides an `Explain` method to preview the generated query (SQL, MongoDB filter, ES DSL) without executing it.
- **Cursor Pagination**: Built-in cursor-based pagination with `QueryCursor`, returning Go 1.23+ `iter.Seq2` iterators for memory-efficient streaming over large datasets. Supports Gorm (row value expressions), MongoDB (`$gt` compound conditions), and ElasticSearch (`search_after` API). Also provides `QueryPage` for single-batch cursor pagination, returning a structured `core.CursorPageResult` (items + has_more + next_cursor) — ideal for App "load more" or API-driven pagination. Supports the `search_after` + `Point-in-Time (PIT)` approach for full data iteration in ElasticSearch cursor scenarios, ensuring index snapshot consistency during iteration and avoiding unstable sorting caused by refresh operations. It can be automatically enabled via `SetNeedPagination(false)`, with the keep-alive duration configurable through `SetPitKeepAlive(...)`.
- **Clone for Concurrent Forking**: Each builder provides a `Clone()` method to create an independent copy of the current query configuration — enabling safe concurrent forked queries without shared state.
- **Pagination Control**: Toggle pagination on/off — useful for data export scenarios.
- **Options Pattern**: Flexible query configuration via functional options.
- **Easy to Test**: Built-in `MockQuerier` for convenient unit testing.

---

## Installation

```shell
go get github.com/fantasticbin/QueryBuilder
```

> **Requires Go 1.26+** (for self-referential generic constraints).

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                       Querier[R]                         │  ← Unified interface
│  Use / SetStart / SetLimit / SetNeedTotal /              │
│  SetNeedPagination / SetFields / SetBeforeQueryHook /    │
│  SetAfterQueryHook / SetCursorField / QueryList /        │
│  QueryCursor / QueryPage                                 │
└──────────┬──────────────┬──────────────┬─────────────────┘
           │              │              │
    ┌──────▼──┐     ┌─────▼────┐ ┌───────▼─────────┐
    │  Gorm   │     │  Mongo   │ │  ElasticSearch  │   ← Dedicated builders
    │ Builder │     │ Builder  │ │     Builder     │
    └──────┬──┘     └─────┬────┘ └───────┬─────────┘
           │              │              │
    ┌──────▼──────────────▼──────────────▼──────────────────┐
    │                   builder[B, R]                       │   ← Shared base (generics)
    │  data / start / limit / fields / hooks / middlewares  │
    └───────────────────────────────────────────────────────┘
```

Each dedicated builder embeds the private `builder` base via Go 1.26 self-referential generics, inheriting common pagination, field selection, hooks, and middleware logic while exposing its own strongly-typed `SetFilter` / `SetSort` and `Explain`.

---

## Quick Start

### 1. Direct Builder Usage (Recommended)

Use a dedicated builder directly for full type safety:

```go
package main

import (
    "context"
    "gorm.io/gorm"
    builder "github.com/fantasticbin/QueryBuilder"
)

func main() {
    ctx := context.Background()
    db := &gorm.DB{} // your GORM instance

    // Create a GORM builder
    b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))

    // Set strongly-typed filter & sort (GormScope = func(*gorm.DB) *gorm.DB)
    b.SetFilter(func(db *gorm.DB) *gorm.DB {
        return db.Where("status = ?", 1)
    }).SetSort(func(db *gorm.DB) *gorm.DB {
        return db.Order("created_at DESC")
    })

    // Configure pagination via Querier interface
    b.SetStart(0)
    b.SetLimit(10)
    b.SetNeedTotal(true)
    b.SetNeedPagination(true)

    // Execute query
    result, err := b.QueryList(ctx)
    if err != nil {
        panic(err)
    }

    _ = result.Items
    _ = result.Total
}

type User struct {
    ID   uint32
    Name string
}
```

### 2. Using List with Options Pattern

For scenarios with protobuf-defined filter/sort structures:

```go
package service

import (
    "context"
    pb "demo/api/user/v1"
    "demo/internal/model"
    builder "github.com/fantasticbin/QueryBuilder"
    "github.com/fantasticbin/QueryBuilder/core"
)

func ListUser(ctx context.Context, req *pb.ListUserRequest) (*core.ListResult[model.User], error) {
    list := builder.NewList[model.User]()
    list.SetDataSource(builder.Gorm)

    // Use SetScope to set filter and sort
    list.SetScope(builder.NewGormScope[model.User](
        func(db *gorm.DB) *gorm.DB {
            return db.Where("name = ?", req.Filter.Name)
        },
        func(db *gorm.DB) *gorm.DB {
            return db.Order("created_at DESC")
        },
    ))

    result, err := list.Query(
        ctx,
        builder.WithData(builder.NewDBProxy(model.DB, nil, nil)),
        builder.WithStart(req.Start),
        builder.WithLimit(req.Limit),
    )
    if err != nil {
        return nil, err
    }

    return result, nil
}
```

---

## Advanced Usage

### Middleware

Insert custom middleware into the query pipeline:

```go
list := builder.NewList[model.User]()
list.SetDataSource(builder.Gorm)

// Add a timing middleware
list.Use(func(
    ctx context.Context,
    b builder.Querier[model.User], // the underlying builder instance
    next func(context.Context) (core.Result[model.User], error),
) (core.Result[model.User], error) {
    start := time.Now()
    result, err := next(ctx)
    fmt.Printf("query took %v\n", time.Since(start))
    return result, err
})

result, err := list.Query(ctx, opts...)
```

### Field Selection

Use `SetFields` to select only specific fields, reducing bandwidth and memory usage:

```go
// Direct builder usage
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFields("id", "name", "email")
result, err := b.QueryList(ctx)

// Via List options
result, err := list.Query(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithFields("id", "name", "email"),
)
```

Field selection works across all data sources:

| Data Source   | Implementation |
|---------------|---------------|
| Gorm          | `db.Select(fields...)` |
| MongoDB       | `options.Find().SetProjection(bson.D{...})` |
| Elasticsearch | `FetchSourceContext(true).Include(fields...)` |

### Query Hooks

Use `BeforeQueryHook` and `AfterQueryHook` for lightweight pre/post query logic:

```go
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))

// Before hook: inject trace ID into context
b.SetBeforeQueryHook(func(ctx context.Context) context.Context {
    return context.WithValue(ctx, "trace_id", generateTraceID())
})

// After hook: log query results
b.SetAfterQueryHook(func(ctx context.Context, result core.Result[User], err error) {
    if err != nil {
        log.Printf("query failed: %v", err)
    } else {
        log.Printf("query returned %d items, total: %d", len(result.GetItems()), result.GetTotal())
    }
})

result, err := b.QueryList(ctx)
```

### Timeout Control

QueryBuilder follows Go's standard `context` pattern for timeout control — no extra API needed. Simply wrap your context with `context.WithTimeout` or `context.WithDeadline`:

```go
// Set a 3-second timeout for the query
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})

result, err := b.QueryList(ctx)
if err != nil {
    // err may be context.DeadlineExceeded if the query times out
    log.Printf("query error: %v", err)
}
```

This works consistently across all data sources — GORM, MongoDB, and ElasticSearch all respect context cancellation and deadlines natively. You can also combine it with middleware to log slow queries:

```go
b.Use(func(ctx context.Context, q builder.Querier[User], next func(context.Context) (core.Result[User], error)) (core.Result[User], error) {
    start := time.Now()
    result, err := next(ctx)
    if duration := time.Since(start); duration > 2*time.Second {
        log.Printf("slow query detected: %v", duration)
    }
    return result, err
})
```

### Clone (Concurrent Forking)

Each dedicated builder provides a `Clone()` method that creates a fully independent copy of the current query configuration. The cloned instance shares no mutable state with the original — modifications to one will never affect the other.

**Key points:**
- All scalar fields, slices (fields, cursorFields, cursorValues, middlewares), and data-source-specific filters/sorts are deep-copied.
- The original builder is **not** concurrency-safe for writes — do not call `Set*` methods on the same instance from multiple goroutines.
- After `Clone()`, each copy can be safely used in its own goroutine.

#### Basic Usage

```go
// Build a "template" with common configuration
base := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
base.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", "active")
}).SetSort(func(db *gorm.DB) *gorm.DB {
    return db.Order("id DESC")
}).SetFields("id", "name", "email").SetNeedTotal(true)

// Clone and customize independently
page1 := base.Clone().SetStart(0).SetLimit(50)
page2 := base.Clone().SetStart(50).SetLimit(50)
```

#### Concurrent Forked Queries (Best Practice)

```go
base := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
base.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", "active")
}).SetFields("id", "name", "email").SetNeedTotal(true)

var wg sync.WaitGroup
pages := []struct{ start, limit uint32 }{
    {0, 100}, {100, 100}, {200, 100},
}
results := make([][]*User, len(pages))

for i, page := range pages {
    wg.Add(1)
    go func(idx int, p struct{ start, limit uint32 }) {
        defer wg.Done()
        q := base.Clone().SetStart(p.start).SetLimit(p.limit)
        result, err := q.QueryList(ctx)
        if err != nil {
            log.Printf("page %d error: %v", idx, err)
            return
        }
        results[idx] = result.Items
    }(i, page)
}
wg.Wait()
```

#### Clone with Different Filters

```go
base := builder.NewMongoBuilder[Order](builder.NewDBProxy(nil, collection, nil))
base.SetFields("id", "user_id", "amount").SetLimit(20)

// Fork into different filter conditions
pending := base.Clone().SetFilter(bson.D{{Key: "status", Value: "pending"}})
completed := base.Clone().SetFilter(bson.D{{Key: "status", Value: "completed"}})

go func() { pendingOrders, _ := pending.QueryList(ctx) }()
go func() { completedOrders, _ := completed.QueryList(ctx) }()
```

#### Clone with Additional Middleware

```go
base := builder.NewGormBuilder[Product](builder.NewDBProxy(db, nil, nil))
base.SetFilter(filterScope).SetLimit(100)

// Each clone can have its own middleware stack
go func() {
    q := base.Clone()
    q.Use(cacheMiddleware)  // this clone uses cache
    result, _ := q.QueryList(ctx)
}()

go func() {
    q := base.Clone()
    q.Use(metricsMiddleware) // this clone collects metrics
    result, _ := q.QueryList(ctx)
}()
```

#### Rules & Anti-Patterns

| Rule | Description |
|------|-------------|
| ✅ Configure first, then Clone | Build a "template" builder, then fork via Clone |
| ✅ One Clone per goroutine | Each goroutine should own its Clone exclusively |
| ✅ Clone is a read operation on base | Safe to call Clone multiple times on the same base (sequentially) |
| ❌ Don't share a builder across goroutines | Never call Set methods on the same instance from multiple goroutines |
| ❌ Don't Clone concurrently from a mutating base | Ensure the base is fully configured before any concurrent Clone calls |

### Cache Middleware

Use the built-in `CacheMiddleware` to cache query results. Implement the `CacheProvider` interface with your preferred cache backend:

```go
// CacheProvider interface — implement with Redis, in-memory cache, etc.
type CacheProvider interface {
    Get(ctx context.Context, key string) ([]byte, bool)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration)
}
```

Here is an example using [gcache](https://github.com/bluele/gcache) (an in-memory cache library supporting LRU, LFU, ARC) as the cache backend:

```go
import (
    "context"
    "time"

    "github.com/bluele/gcache"
    "github.com/fantasticbin/QueryBuilder/middleware"
)

// GCacheProvider implements middleware.CacheProvider using gcache
type GCacheProvider struct {
    cache gcache.Cache
}

func NewGCacheProvider(size int) *GCacheProvider {
    return &GCacheProvider{
        cache: gcache.New(size).LRU().Build(),
    }
}

func (g *GCacheProvider) Get(ctx context.Context, key string) ([]byte, bool) {
    val, err := g.cache.Get(key)
    if err != nil {
        return nil, false
    }
    data, ok := val.([]byte)
    return data, ok
}

func (g *GCacheProvider) Set(ctx context.Context, key string, value []byte, ttl time.Duration) {
    _ = g.cache.SetWithExpire(key, value, ttl)
}
```

Use it with the cache middleware:

```go
cache := NewGCacheProvider(1000) // LRU cache with 1000 entries

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.Use(middleware.CacheMiddleware[User](cache, 5*time.Minute, func(ctx context.Context, b core.QuerierMeta) string {
    meta := b.GetQueryMeta()
    return fmt.Sprintf("users:list:%d:%d", meta.Start, meta.Limit)
}))

result, err := b.QueryList(ctx)
```

### Cache Key Strategy

For production use, manually constructing cache keys (like `fmt.Sprintf("users:list:%d:%d", start, limit)`) is error-prone and hard to maintain. QueryBuilder provides a built-in **Cache Key Strategy** system with a `CacheKeyBuilder` interface and a ready-to-use default implementation.

#### CacheKeyBuilder Interface

```go
// CacheKeyBuilder defines the cache key building interface.
// Implement this to customize key generation logic.
type CacheKeyBuilder interface {
    Build(ctx context.Context, meta QueryMeta) string
}
```

#### DefaultCacheKeyBuilder

The `DefaultCacheKeyBuilder` generates deterministic, collision-resistant cache keys by hashing a canonical JSON payload that includes:

| Dimension | Source | Description |
|-----------|--------|-------------|
| `prefix` | `DefaultCacheKeyBuilder.Prefix` | Business resource name (e.g. `"users"`, `"orders"`) |
| `datasource` | `QueryMeta` | Data source type (Gorm/MongoDB/ES) |
| `fields` | `QueryMeta` | Field projection list |
| `pagination` | `QueryMeta` | start, limit, needTotal, needPagination, isCursorQuery, cursorFields |
| `filter` | `DefaultCacheKeyBuilder.Hints` | Query filter conditions |
| `sort` | `DefaultCacheKeyBuilder.Hints` | Sort conditions |
| `extra` | `DefaultCacheKeyBuilder.Hints` | Additional dimensions (e.g. tenant_id) |

The final key format is `qb:cache:<sha1hex>` — fixed length, safe for Redis and other backends.

`CacheKeyHints` is managed entirely by `DefaultCacheKeyBuilder` — it is **not** stored in the builder base class or injected into context. This design keeps the query builder's responsibilities clean and avoids data corruption in concurrent `Clone` scenarios.

> ⚠️ **Important:** When using `DefaultCacheKeyBuilder`, you **must** provide either `Hints` or `HintsProvider`. If both are nil/empty, the generated cache key will not include filter/sort/extra dimensions, meaning **different query conditions will share the same cache key**, leading to incorrect cache hits.

#### Using CacheKeyHints

Since filter/sort are data-source-specific types (GORM scope, bson.D, elastic.Query), they cannot be automatically extracted from the builder. Provide `CacheKeyHints` directly in the `DefaultCacheKeyBuilder` when creating the cache middleware:

```go
// Hints are provided directly in DefaultCacheKeyBuilder
keyBuilder := middleware.DefaultCacheKeyBuilder{
    Prefix: "users",
    Hints: middleware.CacheKeyHints{
        Filter: map[string]any{"status": "active", "role": "admin"},
        Sort:   map[string]any{"created_at": "desc"},
        Extra:  map[string]any{"tenant_id": "tenant-123"},
    },
}
```

#### Using CacheMiddlewareWithKeyBuilder

```go
cache := NewGCacheProvider(1000)

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ? AND role = ?", "active", "admin")
})

// Use DefaultCacheKeyBuilder with Hints — keys are derived from QueryMeta + Hints
b.Use(middleware.CacheMiddlewareWithKeyBuilder[User](
    cache,
    5*time.Minute,
    middleware.DefaultCacheKeyBuilder{
        Prefix: "users",
        Hints: middleware.CacheKeyHints{
            Filter: map[string]any{"status": "active", "role": "admin"},
            Sort:   map[string]any{"created_at": "desc"},
        },
    },
))

result, err := b.QueryList(ctx)
```

#### HintsProvider (Dynamic Hints)

For scenarios where hints need to be dynamically resolved (e.g., multi-tenant isolation from context), use `HintsProvider`:

```go
b.Use(middleware.CacheMiddlewareWithKeyBuilder[User](
    cache,
    5*time.Minute,
    middleware.DefaultCacheKeyBuilder{
        Prefix: "users",
        HintsProvider: func(ctx context.Context) middleware.CacheKeyHints {
            // Dynamically extract tenant from context
            return middleware.CacheKeyHints{
                Filter: map[string]any{"status": "active"},
                Extra:  map[string]any{"tenant_id": extractTenantID(ctx)},
            }
        },
    },
))
```

> **Priority:** When `Hints` is non-empty, `HintsProvider` will not be called. `HintsProvider` only serves as a fallback when `Hints` is empty.

#### Clone with Different Cache Keys

Since `CacheKeyHints` is managed by `DefaultCacheKeyBuilder` (not by the builder base class), each `Clone` instance can safely use its own cache middleware with different hints — no shared state, no data corruption:

```go
base := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
base.SetFields("id", "name", "email").SetNeedTotal(true)

// Each clone uses its own cache middleware with different hints
go func() {
    q := base.Clone()
    q.SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "active") })
    q.Use(middleware.CacheMiddlewareWithKeyBuilder[User](cache, 5*time.Minute,
        middleware.DefaultCacheKeyBuilder{Prefix: "users", Hints: middleware.CacheKeyHints{
            Filter: map[string]any{"status": "active"},
        }},
    ))
    result, _ := q.QueryList(ctx)
}()

go func() {
    q := base.Clone()
    q.SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "inactive") })
    q.Use(middleware.CacheMiddlewareWithKeyBuilder[User](cache, 5*time.Minute,
        middleware.DefaultCacheKeyBuilder{Prefix: "users", Hints: middleware.CacheKeyHints{
            Filter: map[string]any{"status": "inactive"},
        }},
    ))
    result, _ := q.QueryList(ctx)
}()
```

#### Custom CacheKeyBuilder

Implement the `CacheKeyBuilder` interface for full control over key generation:

```go
type MyCacheKeyBuilder struct{}

func (b MyCacheKeyBuilder) Build(ctx context.Context, meta core.QueryMeta) string {
    tenantID := extractTenantID(ctx)
    return fmt.Sprintf("myapp:%s:%v:%d:%d", tenantID, meta.DataSource, meta.Start, meta.Limit)
}

// Use with CacheMiddlewareWithKeyBuilder
b.Use(middleware.CacheMiddlewareWithKeyBuilder[User](cache, 5*time.Minute, MyCacheKeyBuilder{}))
```

#### Key Stability & Isolation Guarantees

- **Stable**: Same inputs always produce the same key (`encoding/json` sorts map keys lexicographically, ensuring deterministic serialization + SHA1).
- **Isolated**: Different prefix / filter / sort / pagination / extra values produce different keys.
- **Defensive**: Non-serializable values (functions, channels) are gracefully degraded to string representations, avoiding empty-key collisions.
- **Fallback**: Falls back to `fmt.Sprintf` formatting when JSON serialization fails, ensuring the key is never empty.
- **Empty-result caching**: Empty query results are still cached to prevent cache penetration.
- **Clone-safe**: Each Clone instance uses its own `DefaultCacheKeyBuilder` with independent `Hints`, ensuring no shared mutable state.

> ⚠️ **Note:** `CacheMiddleware` / `CacheMiddlewareWithKeyBuilder` **do not apply to `ElasticSearchBuilder.QueryPageWithPIT`**. `QueryPageWithPIT` is a dedicated one-page PIT + `search_after` API and does not go through the list middleware chain; additionally, each page depends on evolving PIT state (`pit_id`, `cursor_values`), so middleware-level cache reuse may return stale or out-of-order pages.

### Query Meta

Middleware can access query metadata directly via the `builder` parameter's `GetQueryMeta()` method — no context injection needed:

```go
// Inside a middleware — access meta directly from builder
func MyMiddleware[R any]() builder.Middleware[R] {
    return func(ctx context.Context, q builder.Querier[R], next func(context.Context) (core.Result[R], error)) (core.Result[R], error) {
        meta := q.GetQueryMeta()
        log.Printf("DataSource: %v, Start: %d, Limit: %d, Fields: %v",
            meta.DataSource, meta.Start, meta.Limit, meta.Fields)
        return next(ctx)
    }
}
```

#### Why Not Inject QueryMeta into Context?

In earlier versions, `QueryMeta` was automatically injected into the context before execution, and middleware accessed it via `QueryMetaFromContext(ctx)`. This approach has a critical limitation with the `Clone` feature:

- When using `Clone` for concurrent forked queries, multiple builder instances may share the same parent context. If `QueryMeta` is stored in context, concurrent writes from different clones would corrupt the shared context data.
- The new approach (`builder.GetQueryMeta()`) ensures each builder instance returns its own independent metadata snapshot — no shared state, no data races.

#### Storing Meta in Context (If Needed)

If you need `QueryMeta` available in context for downstream layers (e.g., passing to repository functions that don't have access to the builder), you can achieve this with a simple middleware:

```go
// Define a context key
type queryMetaKeyType struct{}
var queryMetaKey = queryMetaKeyType{}

// Middleware that injects QueryMeta into context
func MetaToCtxMiddleware[R any]() builder.Middleware[R] {
    return func(ctx context.Context, q builder.Querier[R], next func(context.Context) (core.Result[R], error)) (core.Result[R], error) {
        ctx = context.WithValue(ctx, queryMetaKey, q.GetQueryMeta())
        return next(ctx)
    }
}

// Usage
b.Use(MetaToCtxMiddleware[User]())

// Retrieve in downstream code
func getMetaFromCtx(ctx context.Context) (builder.QueryMeta, bool) {
    meta, ok := ctx.Value(queryMetaKey).(builder.QueryMeta)
    return meta, ok
}
```

This approach is safe for `Clone` scenarios because each clone's middleware pipeline runs independently with its own context.

`QueryMeta` contains:

| Field | Type | Description |
|-------|------|-------------|
| `DataSource` | `DataSource` | Data source type (Gorm/MongoDB/ElasticSearch) |
| `Start` | `uint32` | Pagination offset |
| `Limit` | `uint32` | Page size |
| `NeedTotal` | `bool` | Whether total count is requested |
| `NeedPagination` | `bool` | Whether pagination is enabled |
| `Fields` | `[]string` | Field projection list |
| `IsCursorQuery` | `bool` | Whether this is a cursor query |
| `CursorFields` | `[]string` | Cursor pagination sort fields |
| `CursorValues` | `[]any` | Initial cursor values passed by the caller for resume/app pagination scenarios |
| `StartTime` | `time.Time` | Query start timestamp |

### Dry Run / Explain

Each dedicated builder provides an `Explain` method to preview the generated query without executing it:

```go
// GORM — returns SQL statement
gormBuilder := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
gormBuilder.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})
sql, err := gormBuilder.Explain(ctx)
// Output: SELECT * FROM `users` WHERE status = ? | args: [1]

// MongoDB — returns JSON filter/sort/projection
mongoBuilder := builder.NewMongoBuilder[Doc](builder.NewDBProxy(nil, collection, nil))
mongoBuilder.SetFilter(bson.D{{Key: "status", Value: "active"}})
jsonStr, err := mongoBuilder.Explain(ctx)

// ElasticSearch — returns Query DSL JSON
esBuilder := builder.NewElasticSearchBuilder[Doc](builder.NewDBProxy(nil, nil, esClient), "my_index")
esBuilder.SetFilter(elastic.NewTermQuery("status", "active"))
dsl, err := esBuilder.Explain(ctx)
```

### Mock Testing

Use the built-in `MockQuerier` for unit testing:

```go
func TestListUser(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    // Create mock
    mockQuerier := builder.NewMockQuerier[model.User](ctrl)

    // Set expectations
    mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().
        QueryList(gomock.Any()).
        Return(&core.ListResult[model.User]{Items: []*model.User{{ID: 1, Name: "Alice"}}, Total: 1}, nil)

    // Inject mock
    list := builder.NewList[model.User]()
    list.SetQuerier(mockQuerier)

    result, err := list.Query(ctx, opts...)
    // assert result...
}
```

### Cursor Pagination

Use `QueryCursor` for memory-efficient streaming over large datasets. It returns a Go 1.23+ `iter.Seq2[*R, error]` iterator that fetches data in batches internally using cursor-based pagination.

**How it works:**
- Each batch is fetched using cursor conditions (not OFFSET), ensuring consistent performance regardless of data depth.
- Gorm uses row value expressions (`WHERE (col1, col2) > (v1, v2)`), MongoDB uses `$gt` compound conditions, and ElasticSearch uses the `search_after` API.
- Cursor values are automatically extracted from the last record of each batch — no manual cursor management needed.
- Supports single-field and multi-field cursors.

#### Cursor Sort Direction (ASC/DESC Mixed)

`SetCursorField(...)` supports direction prefixes per field:

- `field` or `+field`: ASC
- `-field`: DESC

Examples:

```go
// Single-field descending cursor
b.SetCursorField("-id")

// Mixed-direction multi-field cursor
b.SetCursorField("-created_at", "id") // created_at DESC, id ASC
```

> Note: For multi-field cursors, Gorm uses row-value comparison when all cursor fields share the same direction (all ASC or all DESC), and falls back to lexicographic OR conditions for mixed directions.

#### Automatic Unique Tie-Breaker

When cursor mode is used without explicitly calling `SetCursorField(...)`, QueryBuilder automatically appends a default unique tie-breaker field by data source:

- Gorm/SQL: `id`
- MongoDB: `_id`
- ElasticSearch: `_shard_doc`

This keeps cursor pagination deterministic and avoids missing cursor-field configuration errors.

> ⚠️ **Important:** auto-append only injects the default field name.  
> You must ensure that field is actually sortable/available in your model/index:
> - For Gorm/SQL, if the model/table does not expose a sortable `id` column, query execution will return a SQL error.
> - For ElasticSearch, `_shard_doc` is mainly intended for stable deep pagination in PIT/search context; for strict business ordering, still prefer explicit business sort fields + unique tie-breaker.

#### Direct Builder Usage

```go
ctx := context.Background()
db := &gorm.DB{} // your GORM instance

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})

// Set cursor field(s) — must be indexed columns for best performance
b.SetCursorField("id")
// SetLimit controls the batch size (default: 10)
b.SetLimit(100)

// QueryCursor returns an iter.Seq2 iterator
for user, err := range b.QueryCursor(ctx) {
    if err != nil {
        log.Printf("cursor error: %v", err)
        break
    }
    process(user)
}
```

#### Multi-Field Cursor

For composite sorting scenarios (e.g., `created_at` + `id`):

```go
b := builder.NewGormBuilder[Order](builder.NewDBProxy(db, nil, nil))
b.SetCursorField("created_at", "id") // multi-field cursor
b.SetLimit(50)

for order, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    exportOrder(order)
}
```

#### Using List with Options Pattern

```go
list := builder.NewList[User]()
list.SetDataSource(builder.Gorm)
list.SetScope(builder.NewGormScope[User](
    func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", 1) },
    nil, // no custom sort — cursor fields handle ordering
))

for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithLimit(100),
) {
    if err != nil {
        break
    }
    process(user)
}
```

#### MongoDB Cursor Pagination

```go
b := builder.NewMongoBuilder[Doc](builder.NewDBProxy(nil, collection, nil))
b.SetFilter(bson.D{{Key: "status", Value: "active"}})
b.SetCursorField("created_at", "_id")
b.SetLimit(100)

for doc, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(doc)
}
```

#### ElasticSearch Cursor Pagination

ES cursor pagination uses the `search_after` API internally. Sort values from the last hit are automatically used as the next batch's `search_after` parameter:

```go
b := builder.NewElasticSearchBuilder[Doc](
    builder.NewDBProxy(nil, nil, esClient), "my_index",
)
b.SetFilter(elastic.NewTermQuery("status", "active"))
b.SetCursorField("created_at")
b.SetSort(elastic.NewFieldSort("_id").Asc()) // auxiliary sort
b.SetLimit(100)
b.SetNeedPagination(false)         // In ES cursor mode, disabling pagination will automatically enable PIT
b.SetPitKeepAlive(2 * time.Minute) // Optional: Configure the PIT keep_alive duration (default: 1 minute)

for doc, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(doc)
}
```

#### Setting Initial Cursor Position

By default, cursor pagination starts from the beginning of the dataset. You can specify an initial cursor position to resume from a specific point.

**Option A: Reuse `start` as initial cursor value** — suitable for single-field numeric cursors:

```go
// Direct builder usage
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetCursorField("id")
b.SetStart(100) // Start from id > 100
b.SetLimit(10)

for user, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(user) // Returns users with id > 100
}

// Via List options
for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithStart(100), // Start from id > 100
    builder.WithLimit(10),
) {
    if err != nil {
        break
    }
    process(user)
}
```

**Option B: `SetCursorValue` / `WithCursorValue`** — for multi-field cursors or non-numeric cursor values:

```go
// Direct builder usage — multi-field cursor
b := builder.NewGormBuilder[Order](builder.NewDBProxy(db, nil, nil))
b.SetCursorField("created_at", "id")
b.SetCursorValue(int64(1700000000), uint32(500)) // Resume from (created_at > 1700000000, id > 500)
b.SetLimit(10)

for order, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(order)
}

// Via List options
for order, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("created_at", "id"),
    builder.WithCursorValue(int64(1700000000), uint32(500)),
    builder.WithLimit(10),
) {
    if err != nil {
        break
    }
    process(order)
}
```

> **Priority**: When both `SetCursorValue` and `SetStart` are set, `SetCursorValue` takes precedence.

#### Pagination Control in Cursor Mode

`needPagination` and `needTotal` also apply to cursor queries:

| Option | Default | Behavior in Cursor Mode |
|--------|---------|------------------------|
| `needPagination` | `true` | When `true`, only fetches a **single batch** (equivalent to one page). When `false`, iterates through the entire dataset in batches until exhausted. |
| `needTotal` | `true` | When `true`, executes a **parallel Count query** on the first batch to retrieve the total count. The total is passed to `AfterQueryHook`. When `false`, skips the Count query entirely. |

**Single-page cursor query** (fetch one batch only):

```go
// Fetch one page of data with total count
for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithCursorValue(uint32(lastSeenID)),
    builder.WithLimit(20),
    builder.WithNeedPagination(true),  // single batch only
    builder.WithNeedTotal(true),       // get total count in parallel
) {
    if err != nil {
        break
    }
    process(user)
}
```

> **Tip:** For single-page cursor pagination scenarios (e.g., API-driven "load more"), consider using [`QueryPage`](#querypage-single-batch-cursor-pagination) instead — it returns a structured `core.CursorPageResult` with `HasMore` and `NextCursorValues`, which is more convenient for building paginated API responses.

**Full traversal without counting** (data export):

```go
// Stream all records without counting — best for batch processing / export
for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithLimit(500),
    builder.WithNeedPagination(false), // iterate all batches
    builder.WithNeedTotal(false),      // skip Count query
) {
    if err != nil {
        break
    }
    export(user)
}
```

> **Performance tip:** Set `needTotal(false)` for large-dataset traversals where total count is unnecessary — this avoids an expensive `COUNT(*)` / `CountDocuments` / `Count` query.

#### QueryPage (Single-Batch Cursor Pagination)

`QueryPage` is a dedicated API for single-batch cursor pagination that returns a structured `core.CursorPageResult` — ideal for App-style "load more" or API-driven pagination where you need `items + next_cursor + has_more` in one call.

**Key differences from `QueryCursor`:**

| Aspect | `QueryCursor` | `QueryPage` |
|--------|--------------|-------------|
| Return type | `iter.Seq2[*R, error]` (iterator) | `*core.CursorPageResult[R]` (struct) |
| Use case | Full traversal / streaming | Single-page fetch |
| HasMore detection | Implicit (empty batch = done) | Explicit (`limit+1` probing) |
| Cursor management | Automatic (internal) | Manual (caller persists `NextCursorValues`) |

**`core.CursorPageResult` structure:**

| Field | Type | Description |
|-------|------|-------------|
| `Items` | `[]*R` | Current page data |
| `Total` | `int64` | Total count (only when `needTotal=true`) |
| `HasMore` | `bool` | Whether more data exists after this page |
| `NextCursorValues` | `[]any` | Cursor values for next page (nil when `HasMore=false`) |

##### Direct Builder Usage

```go
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})
b.SetCursorField("id")
b.SetLimit(20)

// First page
page, err := b.QueryPage(ctx)
if err != nil {
    return err
}
// page.Items: current page data
// page.HasMore: whether there's a next page
// page.NextCursorValues: pass to SetCursorValue for next page

// Next page: set cursor values from previous response
if page.HasMore {
    b.SetCursorValue(page.NextCursorValues...)
    nextPage, err := b.QueryPage(ctx)
    // ...
}
```

##### Using List with Options Pattern

```go
list := builder.NewList[User]()
list.SetDataSource(builder.Gorm)
list.SetScope(builder.NewGormScope[User](
    func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", 1) },
    nil,
))

// First page
page, err := list.QueryPage(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithLimit(20),
)

// Next page with cursor values
nextPage, err := list.QueryPage(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithCursorValue(page.NextCursorValues...),
    builder.WithLimit(20),
)
```

##### MongoDB QueryPage

```go
b := builder.NewMongoBuilder[Doc](builder.NewDBProxy(nil, collection, nil))
b.SetFilter(bson.D{{Key: "status", Value: "active"}})
b.SetCursorField("created_at", "_id")
b.SetLimit(20)

page, err := b.QueryPage(ctx)
if err != nil {
    return err
}

// Next page
if page.HasMore {
    b.SetCursorValue(page.NextCursorValues...)
    nextPage, _ := b.QueryPage(ctx)
}
```

##### ElasticSearch QueryPage

For ElasticSearch, `QueryPage` internally manages PIT (Point-in-Time) lifecycle automatically — no manual PIT handling needed:

```go
b := builder.NewElasticSearchBuilder[Doc](
    builder.NewDBProxy(nil, nil, esClient), "my_index",
)
b.SetFilter(elastic.NewTermQuery("status", "active"))
b.SetCursorField("created_at", "_id")
b.SetLimit(20)

page, err := b.QueryPage(ctx)
// PIT is automatically opened and closed when HasMore=false
```

> **Note:** For scenarios where you need explicit PIT control (e.g., cross-request pagination with client-managed PIT ID), use `QueryPageWithPIT` instead — see [Elasticsearch Cross-Request Pagination](#elasticsearch-cross-request-pagination-pit--search_after) below.

#### Early Termination

Since `QueryCursor` returns a standard Go iterator, you can use `break` to stop at any time:

```go
count := 0
for user, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    count++
    if count >= 1000 {
        break // stop after 1000 records
    }
}
```

#### Cursor Query with Explain

When cursor fields are configured, `Explain` outputs the cursor query mode's first-batch query:

```go
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})
b.SetCursorField("id")
b.SetLimit(100)

sql, err := b.Explain(ctx)
// Output: [CursorQuery] SELECT * FROM `users` WHERE status = ? ORDER BY id ASC LIMIT 100 | args: [1] | cursor_fields: [id]
```

#### Elasticsearch Cross-Request Pagination (PIT + `search_after`)

For Elasticsearch, classic `from + size` pagination may become unstable across requests when index refresh/updates happen between page calls (possible duplicates/missing items).

`ElasticSearchBuilder` now provides a PIT-based single-page API for this scenario:

- `SetPITID(pitID)` to continue a PIT session.
- `SetCursorValue(...)` to continue from last page cursor.
- `QueryPageWithPIT(ctx)` to fetch one page and return `core.ESPITPageResult`.

**`core.ESPITPageResult` structure** (embeds `core.CursorPageResult` — inherits all its fields: `Items`, `Total`, `HasMore`, `NextCursorValues`):

| Field | Type | Description |
|-------|------|-------------|
| *(inherited)* | | All fields from `core.CursorPageResult` (see [above](#querypage-single-batch-cursor-pagination)) |
| `PitID` | `string` | Point-in-Time ID for next request (empty when `HasMore=false`) |

```go
es := builder.NewElasticSearchBuilder[Doc](builder.NewDBProxy(nil, nil, esClient), "my_index")
es.SetFilter(elastic.NewMatchAllQuery()).
   SetCursorField("created_at", "id").
   SetLimit(20)

// next request: restore values from previous response
es.SetPITID(prevPitID).SetCursorValue(prevCursorValues...)

page, err := es.QueryPageWithPIT(ctx)
if err != nil {
    return err
}
// persist page.PitID + page.NextCursorValues for next page
```

Integration recommendations:

- PIT has a keep-alive window; if PIT is expired/invalid, restart from first page and issue a new PIT.
- Keep a stable sort key (for example: business timestamp + unique id) to make `search_after` deterministic.
- `HasMore` is computed via `limit+1` probing; use it as a paging hint and still rely on returned cursor/token as source of truth.

Backend API contract reference (business layer):

- Request:
  - `page_size`: integer
  - `page_token`: opaque string (optional, empty for first page)
- Response:
  - `items`: array
  - `next_page_token`: opaque string (optional, empty when no more data)
  - `has_more`: boolean

Recommended `page_token` strategy:

1. Build payload: `{"pit_id":"...","cursor_values":[...],"exp":...,"v":1}`.
2. Serialize JSON and Base64URL encode.
3. Add integrity protection (HMAC signature) or encryption (AES-GCM) depending on your security requirements.
4. Validate version/expiration/signature on each request before calling `SetPITID` + `SetCursorValue`.

### Scope Helpers

Under `List` mode, use `List.SetScope` with Scope helpers to set filter/sort — no manual middleware signatures or type assertions:

```go
// MySQL (GORM)
list.SetScope(builder.NewGormScope[model.User](
    func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", 1) },
    func(db *gorm.DB) *gorm.DB { return db.Order("created_at DESC") },
))

// MongoDB
list.SetScope(builder.NewMongoScope[model.Doc](
    bson.D{{Key: "status", Value: "active"}},
    bson.D{{Key: "created_at", Value: -1}},
))

// ElasticSearch
list.SetScope(builder.NewElasticSearchScope[model.Doc](
    elastic.NewTermQuery("status", "active"),
    elastic.NewFieldSort("created_at").Order(false),
))
```

| Helper | Builder | filter Type | sort Type |
|--------|---------|-------------|-----------|
| `NewGormScope` | `GormBuilder` | `func(*gorm.DB) *gorm.DB` | `func(*gorm.DB) *gorm.DB` |
| `NewMongoScope` | `MongoBuilder` | `bson.D` | `bson.D` |
| `NewElasticSearchScope` | `ElasticSearchBuilder` | `elastic.Query` | `...elastic.Sorter` |

Passing `nil` for filter or sort will be ignored and won't affect the query flow.

---

## API Reference

### Querier Interface

| Method | Description |
|--------|-------------|
| `Use(middleware)` | Add middleware to the query pipeline |
| `SetStart(start)` | Set pagination offset |
| `SetLimit(limit)` | Set page size (max: 5000) |
| `SetNeedTotal(bool)` | Toggle total count query |
| `SetNeedPagination(bool)` | Toggle pagination |
| `SetFields(fields...)` | Set field selection |
| `SetBeforeQueryHook(hook)` | Set pre-query hook |
| `SetAfterQueryHook(hook)` | Set post-query hook |
| `SetCursorField(fields...)` | Set cursor pagination sort fields |
| `SetCursorValue(values...)` | Set initial cursor values (for resuming from a specific position) |
| `QueryList(ctx)` | Execute the query, returns `*core.ListResult` |
| `QueryCursor(ctx)` | Execute cursor pagination query, returns `iter.Seq2` iterator |
| `QueryPage(ctx)` | Execute single-batch cursor pagination, returns `*core.CursorPageResult` (items + has_more + next_cursor) |

### Builder-Specific Methods

| Method | Available On | Description |
|--------|-------------|-------------|
| `SetFilter(...)` | All builders | Set data source specific filter |
| `SetSort(...)` | All builders | Set data source specific sort |
| `Clone()` | All builders | Create an independent copy for concurrent forking |
| `SetESIndex(index)` | `ElasticSearchBuilder` | Set/change ES index name |
| `SetPitKeepAlive(keepAlive)` | `ElasticSearchBuilder` | Set PIT (Point-in-Time) keep-alive duration |
| `SetPITID(pitID)` | `ElasticSearchBuilder` | Set PIT ID for cross-request pagination resumption |
| `QueryPageWithPIT(ctx)` | `ElasticSearchBuilder` | Execute single-batch PIT-based pagination, returns `*core.ESPITPageResult` |
| `Explain(ctx)` | All builders | Preview generated query (Dry Run) |

---

## Supported Data Sources

| Data Source  | Builder | Filter Type | Sort Type |
|--------------|---------|-------------|-----------|
| Gorm         | `GormBuilder` | `GormScope` (`func(*gorm.DB) *gorm.DB`) | `GormScope` |
| MongoDB      | `MongoBuilder` | `MongoFilter` (`bson.D`) | `MongoSort` (`bson.D`) |
| ElasticSearch | `ElasticSearchBuilder` | `elastic.Query` | `...elastic.Sorter` |

---

## Contributing

Issues and Pull Requests are welcome!

---

## License

MIT License

---

## Contact

For questions or suggestions, please open an Issue or contact the author.
