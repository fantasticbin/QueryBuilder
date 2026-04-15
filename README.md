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
- **Pagination Control**: Toggle pagination on/off — useful for data export scenarios.
- **Options Pattern**: Flexible query configuration via functional options and an `OptionBuilder` helper.
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
┌─────────────────────────────────────────────────┐
│                  Querier[R]                     │  ← Unified interface
│  Use / SetStart / SetLimit / SetNeedTotal /     │
│  SetNeedPagination / QueryList                  │
└──────────┬──────────┬──────────┬────────────────┘
           │          │          │
    ┌──────▼──┐ ┌─────▼────┐ ┌───▼─────────────┐
    │  Gorm   │ │  Mongo   │ │  ElasticSearch  │   ← Dedicated builders
    │ Builder │ │ Builder  │ │     Builder     │
    └──────┬──┘ └─────┬────┘ └──┬──────────────┘
           │          │         │
    ┌──────▼──────────▼─────────▼────────────────┐
    │              builder[B, R]                 │   ← Shared base (generics)
    │  data / start / limit / middlewares / ...  │
    └────────────────────────────────────────────┘
```

Each dedicated builder embeds the private `builder` base via Go 1.26 self-referential generics, inheriting common pagination and middleware logic while exposing its own strongly-typed `SetFilter` / `SetSort`.

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
    // Define type aliases to simplify generic type inference
    type filter = pb.ListUserFilter
    type sort = pb.QueryUserListSort

    list := builder.NewList[model.User, filter, sort]()
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
        builder.WithData[filter, sort](builder.NewDBProxy(model.DB, nil, nil)),
        builder.WithStart[filter, sort](req.Start),
        builder.WithLimit[filter, sort](req.Limit),
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
list := builder.NewList[model.User, filter, sort]()
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

### Mock Testing

Use the built-in `MockQuerier` for unit testing:

```go
func TestListUser(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    type filter = pb.ListUserFilter
    type sort = pb.QueryUserListSort

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
    list := builder.NewList[model.User, filter, sort]()
    list.SetQuerier(mockQuerier)

    result, total, err := list.Query(ctx, opts...)
    // assert result...
}
```

### Option Builder Helper

Use the built-in `OptionBuilder` to simplify option setup with type inference:

```go
opts := builder.NewOptionBuilder[filter, sort]().
    WithData(builder.NewDBProxy(model.DB, nil, nil)).
    WithStart(req.GetStart()).
    WithLimit(req.GetLimit()).
    LoadOptions()

list := builder.NewList[model.User, filter, sort]()
list.SetDataSource(builder.MySQL)

// Use SetScope to set filter and sort
list.SetScope(builder.NewGormScope[model.User](
    func(db *gorm.DB) *gorm.DB {
        return db.Where("name = ?", req.GetFilter().GetName())
    },
    func(db *gorm.DB) *gorm.DB {
        return db.Order("created_at DESC")
    },
))

result, total, err := list.Query(ctx, opts...)
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

## Supported Data Sources

| Data Source | Builder | Filter Type | Sort Type |
|-------------|---------|-------------|-----------|
| MySQL (GORM) | `GormBuilder` | `GormScope` (`func(*gorm.DB) *gorm.DB`) | `GormScope` |
| MongoDB | `MongoBuilder` | `MongoFilter` (`bson.D`) | `MongoSort` (`bson.D`) |
| Elasticsearch | `ElasticSearchBuilder` | `elastic.Query` | `...elastic.Sorter` |

---

## Contributing

Issues and Pull Requests are welcome!

---

## License

MIT License

---

## Contact

For questions or suggestions, please open an Issue or contact the author.
