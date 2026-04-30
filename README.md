**English** | [中文](README_zh.md)

---

# QueryBuilder

A Go library for building type-safe list queries across multiple data sources. Leverages Go 1.26 self-referential generic constraints to provide dedicated query builders for MySQL (GORM), MongoDB, and ElasticSearch — with zero type assertions, flexible middleware, and a unified query interface.

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
- **Query Meta Context**: Automatically injects `QueryMeta` into context before query execution — middleware can access data source type, pagination info, and query start time.
- **Dry Run / Explain**: Each builder provides an `Explain` method to preview the generated query (SQL, MongoDB filter, ES DSL) without executing it.
- **Cursor Pagination**: Built-in cursor-based pagination with `QueryCursor`, returning Go 1.23+ `iter.Seq2` iterators for memory-efficient streaming over large datasets. Supports MySQL (row value expressions), MongoDB (`$gt` compound conditions), and ElasticSearch (`search_after` API).
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
│  QueryCursor                                             │
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
    users, total, err := b.QueryList(ctx)
    if err != nil {
        panic(err)
    }

    _ = users
    _ = total
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
)

func ListUser(ctx context.Context, req *pb.ListUserRequest) ([]*model.User, int64, error) {
    list := builder.NewList[model.User]()
    list.SetDataSource(builder.MySQL)

    // Use SetScope to set filter and sort
    list.SetScope(builder.NewGormScope[model.User](
        func(db *gorm.DB) *gorm.DB {
            return db.Where("name = ?", req.Filter.Name)
        },
        func(db *gorm.DB) *gorm.DB {
            return db.Order("created_at DESC")
        },
    ))

    result, total, err := list.Query(
        ctx,
        builder.WithData(builder.NewDBProxy(model.DB, nil, nil)),
        builder.WithStart(req.Start),
        builder.WithLimit(req.Limit),
    )
    if err != nil {
        return nil, 0, err
    }

    return result, total, nil
}
```

---

## Advanced Usage

### Middleware

Insert custom middleware into the query pipeline:

```go
list := builder.NewList[model.User]()
list.SetDataSource(builder.MySQL)

// Add a timing middleware
list.Use(func(
    ctx context.Context,
    b builder.Querier[model.User], // the underlying builder instance
    next func(context.Context) ([]*model.User, int64, error),
) ([]*model.User, int64, error) {
    start := time.Now()
    result, total, err := next(ctx)
    fmt.Printf("query took %v\n", time.Since(start))
    return result, total, err
})

result, total, err := list.Query(ctx, opts...)
```

### Field Selection

Use `SetFields` to select only specific fields, reducing bandwidth and memory usage:

```go
// Direct builder usage
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFields("id", "name", "email")
users, total, err := b.QueryList(ctx)

// Via List options
result, total, err := list.Query(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithFields("id", "name", "email"),
)
```

Field selection works across all data sources:

| Data Source | Implementation |
|-------------|---------------|
| MySQL (GORM) | `db.Select(fields...)` |
| MongoDB | `options.Find().SetProjection(bson.D{...})` |
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
b.SetAfterQueryHook(func(ctx context.Context, list []*User, total int64, err error) {
    if err != nil {
        log.Printf("query failed: %v", err)
    } else {
        log.Printf("query returned %d items, total: %d", len(list), total)
    }
})

users, total, err := b.QueryList(ctx)
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

users, total, err := b.QueryList(ctx)
if err != nil {
    // err may be context.DeadlineExceeded if the query times out
    log.Printf("query error: %v", err)
}
```

This works consistently across all data sources — GORM, MongoDB, and ElasticSearch all respect context cancellation and deadlines natively. You can also combine it with middleware to log slow queries:

```go
b.Use(func(ctx context.Context, q builder.Querier[User], next func(context.Context) ([]*User, int64, error)) ([]*User, int64, error) {
    start := time.Now()
    list, total, err := next(ctx)
    if duration := time.Since(start); duration > 2*time.Second {
        log.Printf("slow query detected: %v", duration)
    }
    return list, total, err
})
```

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
    builder "github.com/fantasticbin/QueryBuilder"
)

// GCacheProvider implements builder.CacheProvider using gcache
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
b.Use(builder.CacheMiddleware[User](cache, 5*time.Minute, func(ctx context.Context) string {
    return fmt.Sprintf("users:list:%d:%d", start, limit)
}))

users, total, err := b.QueryList(ctx)
```

### Query Meta Context

Query metadata is automatically injected into the context before execution. Middleware can access it via `QueryMetaFromContext`:

```go
// Inside a middleware
func MyMiddleware[R any]() builder.Middleware[R] {
    return func(ctx context.Context, q builder.Querier[R], next func(context.Context) ([]*R, int64, error)) ([]*R, int64, error) {
        meta := builder.QueryMetaFromContext(ctx)
        if meta != nil {
            log.Printf("DataSource: %v, Start: %d, Limit: %d, Fields: %v",
                meta.DataSource, meta.Start, meta.Limit, meta.Fields)
        }
        return next(ctx)
    }
}
```

`QueryMeta` contains:

| Field | Type | Description |
|-------|------|-------------|
| `DataSource` | `DataSource` | Data source type (MySQL/MongoDB/ElasticSearch) |
| `Start` | `uint32` | Pagination offset |
| `Limit` | `uint32` | Page size |
| `NeedTotal` | `bool` | Whether total count is requested |
| `NeedPagination` | `bool` | Whether pagination is enabled |
| `Fields` | `[]string` | Field projection list |
| `IsCursorQuery` | `bool` | Whether this is a cursor query |
| `CursorFields` | `[]string` | Cursor pagination sort fields |
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
        Return([]*model.User{{ID: 1, Name: "Alice"}}, int64(1), nil)

    // Inject mock
    list := builder.NewList[model.User]()
    list.SetQuerier(mockQuerier)

    result, total, err := list.Query(ctx, opts...)
    // assert result...
}
```

### Elasticsearch Builder

The `ElasticSearchBuilder` accepts an index name in its constructor. You can also change it later via the `SetESIndex` method:

```go
// Pass index name at construction time
esBuilder := builder.NewElasticSearchBuilder[Doc](
    builder.NewDBProxy(nil, nil, esClient),
    "my_index",
)

// Or change/set the index later via SetESIndex (supports chaining)
esBuilder.SetESIndex("another_index")

esBuilder.
    SetFilter(elastic.NewTermQuery("status", "active")).
    SetSort(elastic.NewFieldSort("created_at").Order(false))

esBuilder.SetStart(0)
esBuilder.SetLimit(20)
esBuilder.SetNeedTotal(true)
esBuilder.SetNeedPagination(true)

docs, total, err := esBuilder.QueryList(ctx)
```

### Cursor Pagination

Use `QueryCursor` for memory-efficient streaming over large datasets. It returns a Go 1.23+ `iter.Seq2[*R, error]` iterator that fetches data in batches internally using cursor-based pagination.

**How it works:**
- Each batch is fetched using cursor conditions (not OFFSET), ensuring consistent performance regardless of data depth.
- MySQL uses row value expressions (`WHERE (col1, col2) > (v1, v2)`), MongoDB uses `$gt` compound conditions, and ElasticSearch uses the `search_after` API.
- Cursor values are automatically extracted from the last record of each batch — no manual cursor management needed.
- Supports single-field and multi-field cursors.

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
list.SetDataSource(builder.MySQL)
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

for doc, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(doc)
}
```

#### Setting Initial Cursor Position

By default, cursor pagination starts from the beginning of the dataset. You can specify an initial cursor position to resume from a specific point — useful for client-driven pagination (e.g., "load more" in mobile apps).

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
| `SetLimit(limit)` | Set page size |
| `SetNeedTotal(bool)` | Toggle total count query |
| `SetNeedPagination(bool)` | Toggle pagination |
| `SetFields(fields...)` | Set field selection |
| `SetBeforeQueryHook(hook)` | Set pre-query hook |
| `SetAfterQueryHook(hook)` | Set post-query hook |
| `SetCursorField(fields...)` | Set cursor pagination sort fields |
| `SetCursorValue(values...)` | Set initial cursor values (for resuming from a specific position) |
| `QueryList(ctx)` | Execute the query |
| `QueryCursor(ctx)` | Execute cursor pagination query, returns `iter.Seq2` iterator |

### Builder-Specific Methods

| Method | Available On | Description |
|--------|-------------|-------------|
| `SetFilter(...)` | All builders | Set data source specific filter |
| `SetSort(...)` | All builders | Set data source specific sort |
| `SetESIndex(index)` | `ElasticSearchBuilder` | Set/change ES index name |
| `Explain(ctx)` | All builders | Preview generated query (Dry Run) |

---

## Supported Data Sources

| Data Source   | Builder | Filter Type | Sort Type |
|---------------|---------|-------------|-----------|
| MySQL (GORM)  | `GormBuilder` | `GormScope` (`func(*gorm.DB) *gorm.DB`) | `GormScope` |
| MongoDB       | `MongoBuilder` | `MongoFilter` (`bson.D`) | `MongoSort` (`bson.D`) |
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
