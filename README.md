**English** | [中文](docs/README_zh.md)

---

# QueryBuilder

A Go library for building type-safe list queries across multiple data sources. Leverages Go 1.26 self-referential generic constraints to provide dedicated query builders for GORM-Compatible DB (GORM, e.g. MySQL/PostgreSQL/SQLite/SQL Server), MongoDB, and ElasticSearch — with zero type assertions, flexible middleware, and a unified query interface.

---

## Features

- **Multi-DataSource Builders**: Dedicated `GormBuilder`, `MongoBuilder`, and `ElasticSearchBuilder` with strongly-typed `SetFilter` / `SetSort` methods.
- **Aggregate Query Builders**: The `agg` package provides dedicated builders for grouped statistics on GORM, MongoDB, and ElasticSearch, including `Count`, `CountDistinct`, `Sum`, `SumDistinct`, `Avg`, `Min`, `Max`, conditional metrics, HAVING filters, result ordering, typed result decoding, `Explain`, and aggregate middleware in `middleware`.
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
- **Bounded Total Count**: Cap expensive total-count queries with `WithTotalLimit` / `SetTotalLimit` while keeping exact counting as the default.
- **Options Pattern**: Flexible query configuration via functional options.
- **Easy to Test**: Built-in `MockQuerier` for convenient unit testing.

---

## Installation

```shell
go get github.com/fantasticbin/QueryBuilder/v2
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
    builder "github.com/fantasticbin/QueryBuilder/v2"
)

func main() {
    ctx := context.Background()
    db := &gorm.DB{} // your GORM instance

    // Create a GORM builder (defaults: limit=10, paginated, needTotal=true)
    b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))

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

### Data Source Adapter Registration

Register data sources through top-level `builder.NewDBProxyWithAdapters(...)` and the corresponding `builder.New*Adapter(...)`; adapter-only setup usually does not need an extra `core` import:

```go
gormData := builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))
mongoData := builder.NewDBProxyWithAdapters(builder.NewMongoAdapter(collection))
esData := builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(esClient))
```

`builder.NewDBProxy(db, mongo, es)` is kept only for backward compatibility with earlier versions. New code should use adapter registration via `builder.NewDBProxyWithAdapters(...)`; the compatibility constructor will be removed in a future release.

### Custom Data Source Builder Registration

For a non-built-in data source, register a **builder factory** with `RegisterBuilder` so that `NewBuilder` can construct your `Querier`. The client adapter is still registered on the `DBProxy` as usual.

```go
const MyDataSource core.DataSource = 42

func init() {
    // Register the Querier factory for your custom data source
    builder.RegisterBuilder[MyEntity](MyDataSource, func(data *core.DBProxy) builder.Querier[MyEntity] {
        return NewMyBuilder[MyEntity](data)
    })
}

// data carries the client adapter; NewBuilder dispatches to the registered factory
data := builder.NewDBProxyWithAdapters(NewMyAdapter(client))
b := builder.NewBuilder[MyEntity](MyDataSource, data)
```

- For a registered source, `NewBuilder` returns your custom builder; an **unregistered** source returns a `Querier` whose `Query*` / `Explain` yield `ErrDataSourceInvalid` (no panic).
- Built-in sources (`Gorm` / `MongoDB` / `ElasticSearch`) cannot be overridden — `RegisterBuilder` panics to protect them.
- Passing `factory == nil` deletes a previous registration, useful for test cleanup.
- A custom `Querier` used with `QueryCursor` + `CacheMiddleware` should implement `CursorValueOverlayer` so each batch's cursor is injected into `GetQueryMeta` (see Cache section).

### 2. Using List with Options Pattern

For scenarios with protobuf-defined filter/sort structures:

```go
package service

import (
    "context"
    pb "demo/api/user/v1"
    "demo/internal/model"
    builder "github.com/fantasticbin/QueryBuilder/v2"
    "github.com/fantasticbin/QueryBuilder/v2/core"
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
        builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(model.DB))),
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
b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
b.SetFields("id", "name", "email")
result, err := b.QueryList(ctx)

// Via List options
result, err := list.Query(ctx,
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
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
b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))

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

b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
base := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
base := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
base := builder.NewMongoBuilder[Order](builder.NewDBProxyWithAdapters(builder.NewMongoAdapter(collection)))
base.SetFields("id", "user_id", "amount").SetLimit(20)

// Fork into different filter conditions
pending := base.Clone().SetFilter(bson.D{{Key: "status", Value: "pending"}})
completed := base.Clone().SetFilter(bson.D{{Key: "status", Value: "completed"}})

go func() { pendingOrders, _ := pending.QueryList(ctx) }()
go func() { completedOrders, _ := completed.QueryList(ctx) }()
```

#### Clone with Additional Middleware

```go
base := builder.NewGormBuilder[Product](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
    "github.com/fantasticbin/QueryBuilder/v2/middleware"
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

b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
b.Use(middleware.CacheMiddleware[User](cache, 5*time.Minute, func(ctx context.Context, q builder.Querier[User]) string {
    meta := q.GetQueryMeta()
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
| `pagination` | `QueryMeta` | start, limit, needTotal, totalLimit, needPagination, isCursorQuery, isPITQuery, cursorFields, cursorValues |
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

b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
base := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
- **Isolated**: Different prefix / filter / sort / pagination / extra values produce different keys. Pagination includes `cursorValues`, so `QueryPage` pages and `QueryCursor` batches do not share a key.
- **Defensive**: Non-serializable values (functions, channels) are gracefully degraded to string representations, avoiding empty-key collisions.
- **Fallback**: Falls back to `fmt.Sprintf` formatting when JSON serialization fails, ensuring the key is never empty.
- **Empty-result caching**: Empty query results are still cached to prevent cache penetration.
- **Clone-safe**: Each Clone instance uses its own `DefaultCacheKeyBuilder` with independent `Hints`, ensuring no shared mutable state.
- **Typed cursors**: Cached `NextCursorValues` keep their original Go types (`int64`, `uint32`, `time.Time`, …). Legacy untyped JSON numbers restore as `int64` when they are whole numbers.
- **Custom Querier**: `QueryCursor` batches are cached per cursor value. A custom `Querier` must implement the `CursorValueOverlayer` interface (`OverlayCursorValues`) so each batch's cursor is injected into `GetQueryMeta`; otherwise all batches share the initial cursor and cache keys are not isolated by page (silent degradation).

> **Note:** `CacheMiddleware` / `CacheMiddlewareWithKeyBuilder` automatically bypass `ElasticSearchBuilder.QueryPageWithPIT`. PIT pages depend on evolving `pit_id` and `cursor_values`, so the built-in cache middleware skips cache read/write and calls the next query handler directly. Other middleware, including `ObservabilityMiddleware`, still runs for PIT queries. `QueryCursor` batches are cached as `CursorPageResult` so a hit can restore `NextCursorValues` and continue iteration.

### Observability Middleware

Use the built-in `ObservabilityMiddleware` to connect query execution with your logging, metrics, and tracing stack. The middleware has no vendor dependency: QueryBuilder only emits stable events and attributes, while your application decides how to adapt them to `log`, zap, Prometheus, OpenTelemetry, or any other backend.

```go
import (
    "context"
    "log"
    "time"

    builder "github.com/fantasticbin/QueryBuilder/v2"
    "github.com/fantasticbin/QueryBuilder/v2/core"
    "github.com/fantasticbin/QueryBuilder/v2/middleware"
)

obs := middleware.ObservabilityMiddleware[User](middleware.ObservabilityOptions{
    Logger: middleware.QueryLoggerFunc(func(ctx context.Context, event middleware.QueryEvent) {
        log.Printf(
            "operation=%s success=%t duration=%s items=%d total=%d error_type=%s",
            event.Operation,
            event.Success,
            event.Duration,
            event.ItemCount,
            event.Total,
            event.ErrorType,
        )
    }),
    Metrics: middleware.QueryMetricsFunc(func(ctx context.Context, event middleware.QueryEvent) {
        queryDuration.WithLabelValues(event.Operation, event.ErrorType).Observe(event.Duration.Seconds())
        queryItems.WithLabelValues(event.Operation).Observe(float64(event.ItemCount))
    }),
    LoggerFilter: func(ctx context.Context, event middleware.QueryEvent) bool {
        return !event.Success || event.Duration > 2*time.Second
    },
    TraceFilter: func(ctx context.Context, meta core.QueryMeta) bool {
        return meta.DataSource == builder.Gorm
    },
    AttributeProvider: func(ctx context.Context, meta core.QueryMeta) []middleware.Attribute {
        return []middleware.Attribute{
            {Key: "tenant_id", Value: tenantIDFromContext(ctx)},
            {Key: "resource", Value: "users"},
        }
    },
})

b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
b.Use(obs)
result, err := b.QueryList(ctx)
```

Tracing is also adapter-based. For example, an application can bridge `QueryTracer` to OpenTelemetry without QueryBuilder importing OpenTelemetry itself:

```go
type otelQueryTracer struct {
    tracer trace.Tracer
}

func (t otelQueryTracer) StartQuery(ctx context.Context, start middleware.QuerySpanStart) (context.Context, middleware.QuerySpan) {
    ctx, span := t.tracer.Start(ctx, start.Operation)
    for _, attr := range start.Attributes {
        span.SetAttributes(attribute.String(attr.Key, fmt.Sprint(attr.Value)))
    }
    return ctx, otelQuerySpan{span: span}
}

type otelQuerySpan struct {
    span trace.Span
}

func (s otelQuerySpan) EndQuery(ctx context.Context, event middleware.QueryEvent) {
    if event.Error != nil {
        s.span.RecordError(event.Error)
    }
    s.span.SetAttributes(
        attribute.Bool("querybuilder.success", event.Success),
        attribute.Int("querybuilder.item_count", event.ItemCount),
    )
    s.span.End()
}
```

Default attributes only include low-sensitive query dimensions such as data source, query mode, pagination flags, start/limit, result kind, success, and error type. QueryBuilder does not automatically expose filter/sort or cursor values; add business dimensions explicitly through `AttributeProvider` when they are safe and useful.

Behavior notes:

- Configure `LoggerFilter`, `MetricsFilter`, and `TraceFilter` to avoid emitting every signal for every query. For example, keep metrics full-fidelity, log only errors/slow queries, and sample traces by context or data source.
- Configure `SignalOrder` to change completion dispatch order. The default remains `trace -> metrics -> logger`; omitted known signals are appended in default order, while duplicate or unknown values are ignored.
- When `Logger`, `Metrics`, and `Tracer` are all nil, the middleware bypasses observability work and calls the next handler directly.
- Observer adapters are best-effort: panics from `Logger`, `Metrics`, `Tracer`, `AttributeProvider`, `OperationNameBuilder`, or `ErrorClassifier` are isolated and do not interrupt the query.
- Default operation names are `querybuilder.<DataSource>.list`, `querybuilder.<DataSource>.cursor`, and `querybuilder.ElasticSearch.pit_cursor` for PIT + `search_after`.
- `QueryCursor` emits one event per fetched batch as `ResultKindCursorPage` (the batch carries `NextCursorValues` for cache resume). `QueryPage` and `QueryPageWithPIT` emit one event per returned page.
- Validation/configuration errors that happen before the middleware pipeline starts are not emitted by this middleware. If you need full API-entry observability, record those call-site errors at your service boundary as well.
- `DefaultErrorClassifier` returns stable names for context cancellation and deadline errors: `context_canceled` and `context_deadline_exceeded`.

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
func getMetaFromCtx(ctx context.Context) (core.QueryMeta, bool) {
    meta, ok := ctx.Value(queryMetaKey).(core.QueryMeta)
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
| `TotalLimit` | `uint32` | Total count cap; `0` means exact count |
| `NeedPagination` | `bool` | Whether pagination is enabled |
| `Fields` | `[]string` | Field projection list |
| `IsCursorQuery` | `bool` | Whether this is a cursor query |
| `IsPITQuery` | `bool` | Whether this is an Elasticsearch PIT + `search_after` query |
| `CursorFields` | `[]string` | Cursor pagination sort fields |
| `CursorValues` | `[]any` | Initial cursor values passed by the caller for resume/app pagination scenarios |
| `StartTime` | `time.Time` | Query start timestamp |

### Dry Run / Explain

Each dedicated builder provides an `Explain` method to preview the generated query without executing it:

```go
// GORM — returns SQL statement
gormBuilder := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
gormBuilder.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})
sql, err := gormBuilder.Explain(ctx)
// Output: SELECT * FROM `users` WHERE status = ? | args: [1]

// MongoDB — returns JSON filter/sort/projection
mongoBuilder := builder.NewMongoBuilder[Doc](builder.NewDBProxyWithAdapters(builder.NewMongoAdapter(collection)))
mongoBuilder.SetFilter(bson.D{{Key: "status", Value: "active"}})
jsonStr, err := mongoBuilder.Explain(ctx)

// ElasticSearch — returns Query DSL JSON
esBuilder := builder.NewElasticSearchBuilder[Doc](builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(esClient)), "my_index")
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

b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
b := builder.NewGormBuilder[Order](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
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
b := builder.NewMongoBuilder[Doc](builder.NewDBProxyWithAdapters(builder.NewMongoAdapter(collection)))
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
    builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(esClient)), "my_index",
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
b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
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
b := builder.NewGormBuilder[Order](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
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
| `totalLimit` | `0` | When greater than `0`, caps the total-count query. If returned `Total` equals the cap, treat it as `cap+` rather than an exact count. |

**Single-page cursor query** (fetch one batch only):

```go
// Fetch one page of data with total count
for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
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
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
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

#### Bounded Total Count

Exact total counts can dominate latency on large datasets. Keep `needTotal=true` when the UI still needs a total-like value, but configure a cap with `WithTotalLimit(n)`:

```go
result, err := list.Query(ctx,
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
    builder.WithLimit(20),
    builder.WithNeedTotal(true),
    builder.WithTotalLimit(10000),
)
// If result.Total == 10000, display it as "10000+" or treat it as a capped total.
```

The default `totalLimit=0` preserves exact-count behavior. When capped counting is enabled:

- Gorm uses a limited subquery count (`SELECT COUNT(*) FROM (SELECT 1 ... LIMIT n)`).
- MongoDB uses `CountDocuments` with `CountOptions.Limit`.
- ElasticSearch uses `size=0` with `track_total_hits=n`.

This option applies to `QueryList`, `QueryCursor` first-batch totals, `QueryPage`, and `QueryPageWithPIT`.

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
b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
    builder.WithCursorField("id"),
    builder.WithLimit(20),
)

// Next page with cursor values
nextPage, err := list.QueryPage(ctx,
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
    builder.WithCursorField("id"),
    builder.WithCursorValue(page.NextCursorValues...),
    builder.WithLimit(20),
)
```

##### MongoDB QueryPage

```go
b := builder.NewMongoBuilder[Doc](builder.NewDBProxyWithAdapters(builder.NewMongoAdapter(collection)))
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
    builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(esClient)), "my_index",
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
b := builder.NewGormBuilder[User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
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

The same flow can be used through `List` options:

```go
list := builder.NewList[Doc]()
list.SetDataSource(builder.ElasticSearch)
list.SetScope(builder.NewElasticSearchScope[Doc](elastic.NewMatchAllQuery()))

page, err := list.QueryPageWithPIT(ctx,
    builder.WithData(builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(esClient))),
    builder.WithESIndex("my_index"),
    builder.WithCursorField("created_at", "id"),
    builder.WithPITID(prevPitID),
    builder.WithCursorValue(prevCursorValues...),
    builder.WithPitKeepAlive(time.Minute),
    builder.WithLimit(20),
)
```

**`core.ESPITPageResult` structure** (embeds `core.CursorPageResult` — inherits all its fields: `Items`, `Total`, `HasMore`, `NextCursorValues`):

| Field | Type | Description |
|-------|------|-------------|
| *(inherited)* | | All fields from `core.CursorPageResult` (see [above](#querypage-single-batch-cursor-pagination)) |
| `PitID` | `string` | Point-in-Time ID for next request (empty when `HasMore=false`) |

```go
es := builder.NewElasticSearchBuilder[Doc](builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(esClient)), "my_index")
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
- `QueryPageWithPIT` goes through the middleware pipeline and is reported by `ObservabilityMiddleware` as `querybuilder.ElasticSearch.pit_cursor`; the built-in cache middleware bypasses PIT pages automatically.

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

### Aggregate Statistics

Use `agg` when a normal list query is not enough and you need a small summary table: orders by region, total sales, average amount, unique buyers, and similar report-style numbers. You can reuse a complete `agg.Spec` with `SetSpec`, or start with an empty spec and build it through the chain methods.

#### Basic Usage

This example summarizes paid orders by region. It counts all orders, counts unique buyers, sums the paid amount, and sums unique amount values:

```go
import (
    "context"
    "fmt"

    builder "github.com/fantasticbin/QueryBuilder/v2"
    "github.com/fantasticbin/QueryBuilder/v2/agg"
    "gorm.io/gorm"
)

type Order struct {
    ID         uint64
    Region     string
    CustomerID uint64
    Amount     float64
    Status     string
}

type SalesSummary struct {
    Region          string  `gorm:"column:region" bson:"region" json:"region"`
    Count           int64   `gorm:"column:order_count" bson:"order_count" json:"order_count"`
    BuyerCount      int64   `gorm:"column:buyer_count" bson:"buyer_count" json:"buyer_count"`
    UniqueAmountSum float64 `gorm:"column:unique_amount_sum" bson:"unique_amount_sum" json:"unique_amount_sum"`
    Amount          float64 `gorm:"column:amount_sum" bson:"amount_sum" json:"amount_sum"`
}

func summarize(ctx context.Context, db *gorm.DB) error {
    data := builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))
    query := agg.NewGormBuilder[Order, SalesSummary](data)
    query.GroupBy("region", "region").
        Count("order_count").
        CountDistinct("customer_id", "buyer_count").
        Sum("amount", "amount_sum").
        SumDistinct("amount", "unique_amount_sum").
        SetStart(0).
        SetLimit(100)
    query.SetFilter(func(db *gorm.DB) *gorm.DB {
        return db.Where("status = ?", "paid")
    })

    result, err := query.Query(ctx)
    if err != nil {
        return err
    }
    fmt.Println("groups:", result.Total)
    for _, row := range result.Rows {
        fmt.Println(row.Region, row.Count, row.BuyerCount, row.UniqueAmountSum, row.Amount)
    }
    return nil
}
```

`GroupBy` adds the fields you want to summarize by. `Count`, `CountDistinct`, `Sum`, `SumDistinct`, `Avg`, `Min`, and `Max` add the numbers you want in the result. Each alias must match a tag on the result struct, so `buyer_count` above is decoded into `SalesSummary.BuyerCount`. Add `gorm`, `bson`, and `json` tags when one result type is shared across multiple data sources.

Groups are sorted in ascending order by default. Use `GroupByDesc` or `AddGroup(agg.Group{...})` with `Descending: true` when a group key should be sorted descending.

Grouped aggregate results use offset pagination: `SetStart(start)` skips groups after filtering, HAVING, and ordering, while `SetLimit(limit)` controls the page size. The default grouped page size remains 100 and the maximum `Limit` is 5000. `Result.Total` is the number of grouped result rows after HAVING, not the number of source records; call `SetNeedTotal(false)` to skip the total query, or `SetTotalLimit(n)` to cap expensive total counting. Aggregate queries without `Groups` still return one summary row, ignore pagination, and report `Total=1`.

> **API symmetry with list/cursor queries:** The aggregate builders deliberately expose the **same** pagination API as the list and cursor builders — `SetStart` / `SetLimit` / `SetNeedTotal` / `SetTotalLimit` — and `Result.Total` mirrors `core.ListResult.Total`. This lets a shared paginated UI component (offset + total footer) drive both list and aggregate views without branching. The default `needTotal=true` matches the list-query default, and the aggregate cache/observability middleware keys on the same pagination dimensions (`start`, `limit`, `needTotal`, `totalLimit`) so cache keys and metrics stay consistent across both.

> ⚠️ **Elasticsearch full-scan risk:** Because `needTotal` defaults to `true`, Elasticsearch grouped queries that combine HAVING filters **and** non-prefix/metric ordering must collect **all** composite buckets to compute an exact `Total` — the builder degrades from "collect until the page is full" into a **full bucket scan**. For high-cardinality groupings this can be expensive. Prefer `SetNeedTotal(false)` to skip the total entirely, or `SetTotalLimit(n)` to cap it (a capped `Total` reads as `n+` rather than an exact count). `Explain` reports `full_scan` in `client_post_processing` when all buckets must be collected.

Time buckets are available with `GroupByDate` or `GroupByDateWithTimeZone` when a date/time field should be truncated before grouping:

```go
query.GroupByDateWithTimeZone("created_at", "created_day", agg.TimeIntervalDay, "Asia/Shanghai")
```

#### Switching Data Sources

Builders start with an empty `Spec` so the common path is fluent configuration. When a `Spec` is built elsewhere, it can still be reused with `SetSpec`:

```go
spec := agg.Spec{
    Groups: []agg.Group{{Field: "region", Alias: "region"}},
    Metrics: []agg.Metric{
        {Func: agg.Count, Alias: "order_count"},
        {Func: agg.Count, Field: "customer_id", Alias: "buyer_count", Distinct: true},
        {Func: agg.Sum, Field: "amount", Alias: "unique_amount_sum", Distinct: true},
    },
}

gormQuery := agg.NewGormBuilder[Order, SalesSummary](data)
gormQuery.SetSpec(spec)

mongoQuery := agg.NewMongoBuilder[SalesSummary](data)
mongoQuery.SetSpec(spec)

esQuery := agg.NewElasticSearchBuilder[SalesSummary](data, "orders")
esQuery.SetSpec(spec)
```

All three builders provide `SetFilter`, `Clone`, `Use`, `Query`, and `Explain`. `SetFilter` stays native to the data source: GORM uses `func(*gorm.DB) *gorm.DB`, MongoDB uses `bson.D`, and Elasticsearch uses `elastic.Query`.

#### Supported Functions

| Function | Builder method | Purpose | `Field` |
|----------|----------------|---------|---------|
| `agg.Count` | `.Count(alias)` | Count matching records | Omit |
| `agg.Count` with `Distinct: true` | `.CountDistinct(field, alias)` | Count unique non-null field values | Required |
| `agg.Sum` | `.Sum(field, alias)` | Calculate a sum | Required |
| `agg.Sum` with `Distinct: true` | `.SumDistinct(field, alias)` | Sum unique non-null field values | Required |
| `agg.Avg` | `.Avg(field, alias)` | Calculate an average | Required |
| `agg.Min` | `.Min(field, alias)` | Find the minimum | Required |
| `agg.Max` | `.Max(field, alias)` | Find the maximum | Required |

At least one metric is required, and aliases must be unique across groups and metrics. Field names may be regular identifiers or dotted paths such as `amount` and `customer.region`; raw expressions are not accepted.

#### Conditional Metrics, HAVING, and Ordering

Use GORM-like comparison expressions with the `*If` methods to compute metrics over only the records that match a simple field comparison:

```go
query.GroupBy("region", "region").
    Count("total").
    CountIf("paid_total", "status = ?", "paid").
    SumIf("amount", "paid_amount", "status = ?", "paid").
    Having("paid_amount >= ?", 10000).
    OrderByDesc("paid_amount").
    SetLimit(20)
```

`CountIf`, `CountDistinctIf`, `SumIf`, `SumDistinctIf`, `AvgIf`, `MinIf`, and `MaxIf` accept validated field predicates such as `status = ?`, `status IN ?`, `amount BETWEEN ? AND ?`, `name LIKE ?`, `deleted_at IS NULL`, and `archived_at EXISTS`. Supported predicate operators are `=`, `==`, `!=`, `<>`, `>`, `>=`, `<`, `<=`, `IN`, `NOT IN`, `BETWEEN`, `LIKE`, `NOT LIKE`, `EXISTS`, `NOT EXISTS`, `IS NULL`, and `IS NOT NULL`. Use `agg.Range{Start: ..., End: ...}` for `BETWEEN`, pass a non-empty slice for `IN`/`NOT IN`, and use `MetricWhere(fn, field, alias, agg.Condition{...})` when a typed condition is clearer than an expression string.

`OrderBy` and `OrderByDesc` accept either a group alias or a metric alias. `Having` filters grouped results by metric alias and uses the same comparison expression form, with numeric comparison values. For Elasticsearch grouped `Having`, the builder pages composite buckets and filters client-side before applying offset and limit. Ordering stays in composite source order when it is a prefix of the group aliases; metric ordering or non-prefix group ordering requires collecting all buckets before sorting and paging. `Explain` marks client work with `client_post_processing` and uses `full_scan` to show whether all buckets must be collected.

#### Inspecting the Query

Call `Explain` when you want to see what will be sent to the data source. It returns SQL, a MongoDB aggregation pipeline, or Elasticsearch DSL without executing the query:

```go
statement, err := query.Explain(ctx)
```

For distinct counts and sums, GORM emits `COUNT(DISTINCT field)` or `SUM(DISTINCT field)`, and MongoDB uses exact two-stage `$group` branches. Elasticsearch uses approximate `cardinality` for distinct counts and `scripted_metric` for distinct sums; high-cardinality distinct sums keep unique values in aggregation state, so use them only on bounded numeric fields.

`Explain` output also includes a compact `PlanFlags` bitmask for date groups, conditional/distinct metrics, MongoDB facet usage, Elasticsearch scripted metrics, and Elasticsearch client-side post-processing. The same summary is available from `Meta().Plan`; use `Plan.Has(flag)` for individual checks. `AggregateObservabilityMiddleware` still records expanded aggregate attributes, including group, metric, HAVING, and order counts derived from the spec.

#### Current Limits

- Grouped queries support offset pagination with `Start + Limit`; grouped page size defaults to 100 and `Limit` cannot exceed 5000
- A query without `Groups` returns one summary row, ignores pagination, and reports `Total=1`
- Records with a null or missing group field are excluded; null metric values are ignored
- Distinct is currently supported only for `Count` and `Sum`
- HAVING currently filters metric aliases only and accepts numeric comparison values; grouped cursor pagination is not supported yet
- Field predicates are intentionally structured and validated; arbitrary raw SQL, MongoDB, or Elasticsearch expressions are not accepted
- Cache and observability middleware for aggregate queries lives in `middleware` as `AggregateCacheMiddleware` and `AggregateObservabilityMiddleware`

---

## API Reference

### Querier Interface

| Method | Description |
|--------|-------------|
| `Use(middleware)` | Add middleware to the query pipeline |
| `SetStart(start)` | Set pagination offset |
| `SetLimit(limit)` | Set page size (max: 5000) |
| `SetNeedTotal(bool)` | Toggle total count query |
| `SetTotalLimit(limit)` | Cap total counting; `0` keeps exact counting |
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

### List QueryOptions

| Option | Description |
|--------|-------------|
| `WithData(data)` | Set the data proxy for this query |
| `WithStart(start)` | Set pagination offset |
| `WithLimit(limit)` | Set page size |
| `WithNeedTotal(bool)` | Toggle total count query |
| `WithTotalLimit(limit)` | Cap total counting; `0` keeps exact counting |
| `WithNeedPagination(bool)` | Toggle pagination |
| `WithFields(fields...)` | Set field selection |
| `WithCursorField(fields...)` | Set cursor pagination sort fields |
| `WithCursorValue(values...)` | Set initial cursor values |
| `WithESIndex(index)` | Set Elasticsearch index when using `List` |
| `WithPITID(pitID)` | Continue an Elasticsearch PIT pagination session |
| `WithPitKeepAlive(duration)` | Set Elasticsearch PIT keep-alive duration |

### Aggregate Builder Methods

Methods on the `agg.Builder` interface (also available on `agg.SpecBuilder` for building a standalone `Spec`). All configuration methods are chainable.

#### Execution & Meta

| Method | Description |
|--------|-------------|
| `Query(ctx)` | Execute the aggregate query, returns `*agg.Result[A]` |
| `Explain(ctx)` | Preview the generated aggregate query (Dry Run) |
| `Meta()` | Return aggregate query meta info (data source, spec, plan flags, etc.) |

#### Pipeline & Spec

| Method | Description |
|--------|-------------|
| `Use(middleware)` | Add aggregate middleware to the pipeline |
| `SetBeforeHook(hook)` | Set pre-execution hook |
| `SetAfterHook(hook)` | Set post-execution hook |
| `ConfigureSpec(options...)` | Batch-configure the spec via `SpecOption` functions |
| `SetSpec(spec)` | Replace the entire aggregate spec |

#### Grouping

| Method | Description |
|--------|-------------|
| `SetGroups(groups...)` | Replace all group-by fields |
| `AddGroup(group)` | Append a full group configuration |
| `GroupBy(field, alias)` | Append an ascending group-by field |
| `GroupByDesc(field, alias)` | Append a descending group-by field |
| `GroupByDate(field, alias, interval)` | Append an ascending time-bucket group-by field |
| `GroupByDateWithTimeZone(field, alias, interval, timeZone)` | Append a time-bucket group-by field with a time zone |

#### Metrics

| Method | Description |
|--------|-------------|
| `SetMetrics(metrics...)` | Replace all metrics |
| `AddMetric(metric)` | Append a full metric configuration |
| `Metric(fn, field, alias)` | Append a metric with the given aggregate function |
| `MetricIf(fn, field, alias, expression, value)` | Append a conditional metric using a `"field = ?"` expression |
| `MetricWhere(fn, field, alias, condition)` | Append a conditional metric using a typed `Condition` |
| `Count(alias)` | Count matching records |
| `CountIf(alias, expression, value)` | Count records matching the condition |
| `CountDistinct(field, alias)` | Count unique non-null field values |
| `CountDistinctIf(field, alias, expression, value)` | Count unique field values matching the condition |
| `Sum(field, alias)` | Calculate a sum |
| `SumIf(field, alias, expression, value)` | Sum values matching the condition |
| `SumDistinct(field, alias)` | Sum unique non-null field values |
| `SumDistinctIf(field, alias, expression, value)` | Sum unique field values matching the condition |
| `Avg(field, alias)` | Calculate an average |
| `AvgIf(field, alias, expression, value)` | Average values matching the condition |
| `Min(field, alias)` | Find the minimum |
| `MinIf(field, alias, expression, value)` | Minimum of values matching the condition |
| `Max(field, alias)` | Find the maximum |
| `MaxIf(field, alias, expression, value)` | Maximum of values matching the condition |

#### Ordering, HAVING, Limit & Pagination

| Method | Description |
|--------|-------------|
| `SetOrders(orders...)` | Replace all result ordering rules |
| `AddOrder(order)` | Append a result ordering rule |
| `OrderBy(alias)` | Append an ascending ordering rule (by metric/group alias) |
| `OrderByDesc(alias)` | Append a descending ordering rule |
| `SetHavings(havings...)` | Replace all post-aggregation filters |
| `AddHaving(having)` | Append a post-aggregation filter |
| `Having(expression, value)` | Append a HAVING filter using an `"alias >= ?"` expression |
| `SetLimit(limit)` | Set the max number of returned groups |
| `SetStart(start)` | Set the pagination offset for grouped results |
| `SetNeedTotal(bool)` | Toggle returning the total group count |
| `SetTotalLimit(limit)` | Cap total counting; `0` keeps exact counting |

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
