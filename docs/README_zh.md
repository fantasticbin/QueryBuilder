[English](../README.md) | **中文**

---

# QueryBuilder

一个用于构建类型安全列表查询的 Go 库，支持多种数据源。利用 Go 1.26 自引用泛型约束特性，为 GORM 兼容数据库（如 MySQL/PostgreSQL/SQLite/SQL Server）、MongoDB、ElasticSearch 提供专属查询构建器——零类型断言、灵活中间件、统一查询接口。

---

## 特性

- **多数据源专属构建器**：提供 `GormBuilder`、`MongoBuilder`、`ElasticSearchBuilder`，各自拥有强类型的 `SetFilter` / `SetSort` 方法。
- **自引用泛型约束**：利用 Go 1.26 自引用泛型约束特性，实现类型安全的链式调用。
- **零类型断言**：所有 filter/sort 操作均为强类型，运行时无需任何 `any` 类型转换。
- **Scope 辅助函数**：提供 `SetScope` + `NewGormScope` / `NewMongoScope` / `NewElasticSearchScope`，在 `List` 模式下一行代码设置 filter/sort，无需手写中间件和类型断言。
- **统一 `Querier` 接口**：跨数据源的通用接口，统一分页、中间件和查询执行。
- **中间件管道**：在查询管道中插入自定义逻辑（耗时统计、日志记录、缓存等）。
- **内置缓存中间件**：开箱即用的 `CacheMiddleware`，提供可插拔的 `CacheProvider` 接口——自由对接任意缓存后端（Redis、内存缓存等）。
- **指定字段**：通过 `SetFields` 指定只返回部分字段，减少所有数据源的带宽和内存消耗。
- **查询钩子**：`BeforeQueryHook` 和 `AfterQueryHook`，用于轻量级的查询前后置逻辑（上下文注入、日志记录、指标统计等）。
- **查询元信息**：中间件可通过 `builder.GetQueryMeta()` 直接获取查询元数据——数据源类型、分页信息和查询开始时间无需通过 context 注入即可获取。
- **Dry Run / Explain**：每个构建器提供 `Explain` 方法，预览生成的查询语句（SQL、MongoDB filter、ES DSL），无需实际执行。
- **游标分页**：内置基于游标的分页查询 `QueryCursor`，返回 Go 1.23+ `iter.Seq2` 迭代器，支持对大数据集进行内存高效的流式遍历。支持 Gorm（行值表达式）、MongoDB（`$gt` 复合条件）和 ElasticSearch（`search_after` API）。
- **Clone 并发分叉**：每个构建器提供 `Clone()` 方法，创建当前查询配置的独立副本——支持安全的并发分叉查询，无共享可变状态。
- **分页控制**：支持开关分页，适用于数据导出等场景。
- **选项模式**：通过函数式选项灵活配置查询参数。
- **易于测试**：内置 `MockQuerier`，便于单元测试。

---

## 安装

```shell
go get github.com/fantasticbin/QueryBuilder
```

> **需要 Go 1.26+**（自引用泛型约束特性）。

---

## 架构

```
┌──────────────────────────────────────────────────────────┐
│                       Querier[R]                         │  ← 统一接口
│  Use / SetStart / SetLimit / SetNeedTotal /              │
│  SetNeedPagination / SetFields / SetBeforeQueryHook /    │
│  SetAfterQueryHook / SetCursorField / QueryList /        │
│  QueryCursor                                             │
└──────────┬──────────────┬──────────────┬─────────────────┘
           │              │              │
    ┌──────▼──┐     ┌─────▼────┐ ┌───────▼─────────┐
    │  Gorm   │     │  Mongo   │ │  ElasticSearch  │   ← 专属构建器
    │ Builder │     │ Builder  │ │     Builder     │
    └──────┬──┘     └─────┬────┘ └───────┬─────────┘
           │              │              │
    ┌──────▼──────────────▼──────────────▼──────────────────┐
    │                   builder[B, R]                       │   ← 公共基类（泛型）
    │  data / start / limit / fields / hooks / middlewares  │
    └───────────────────────────────────────────────────────┘
```

每个专属构建器通过 Go 1.26 自引用泛型嵌入私有的 `builder` 基类，继承通用的分页、指定字段、钩子和中间件逻辑，同时暴露各自强类型的 `SetFilter` / `SetSort` 和 `Explain`。

---

## 快速开始

### 1. 直接使用专属构建器（推荐）

直接使用专属构建器，获得完整的类型安全：

```go
package main

import (
    "context"
    "gorm.io/gorm"
    builder "github.com/fantasticbin/QueryBuilder"
)

func main() {
    ctx := context.Background()
    db := &gorm.DB{} // 你的 GORM 实例

    // 创建 GORM 构建器
    b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))

    // 设置强类型的 filter 和 sort（GormScope = func(*gorm.DB) *gorm.DB）
    b.SetFilter(func(db *gorm.DB) *gorm.DB {
        return db.Where("status = ?", 1)
    }).SetSort(func(db *gorm.DB) *gorm.DB {
        return db.Order("created_at DESC")
    })

    // 通过 Querier 接口配置分页参数
    b.SetStart(0)
    b.SetLimit(10)
    b.SetNeedTotal(true)
    b.SetNeedPagination(true)

    // 执行查询
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

### 2. 使用 List 与选项模式

适用于 protobuf 定义 filter/sort 结构的场景：

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
    list.SetDataSource(builder.Gorm)

    // 使用 SetScope 设置 filter 和 sort
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

## 进阶用法

### 中间件

在查询管道中插入自定义中间件：

```go
list := builder.NewList[model.User]()
list.SetDataSource(builder.Gorm)

// 添加耗时统计中间件
list.Use(func(
    ctx context.Context,
    b builder.Querier[model.User], // 底层构建器实例
    next func(context.Context) ([]*model.User, int64, error),
) ([]*model.User, int64, error) {
    start := time.Now()
    result, total, err := next(ctx)
    fmt.Printf("查询耗时 %v\n", time.Since(start))
    return result, total, err
})

result, total, err := list.Query(ctx, opts...)
```

### 指定字段

通过 `SetFields` 指定只返回部分字段，减少带宽和内存消耗：

```go
// 直接使用构建器
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFields("id", "name", "email")
users, total, err := b.QueryList(ctx)

// 通过 List 选项
result, total, err := list.Query(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithFields("id", "name", "email"),
)
```

指定字段在所有数据源中均可使用：

| 数据源           | 实现方式 |
|---------------|---------|
| Gorm          | `db.Select(fields...)` |
| MongoDB       | `options.Find().SetProjection(bson.D{...})` |
| Elasticsearch | `FetchSourceContext(true).Include(fields...)` |

### 查询钩子

通过 `BeforeQueryHook` 和 `AfterQueryHook` 实现轻量级的查询前后置逻辑：

```go
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))

// 前置钩子：向 context 注入 trace ID
b.SetBeforeQueryHook(func(ctx context.Context) context.Context {
    return context.WithValue(ctx, "trace_id", generateTraceID())
})

// 后置钩子：记录查询结果日志
b.SetAfterQueryHook(func(ctx context.Context, list []*User, total int64, err error) {
    if err != nil {
        log.Printf("查询失败: %v", err)
    } else {
        log.Printf("查询返回 %d 条记录, 总数: %d", len(list), total)
    }
})

users, total, err := b.QueryList(ctx)
```

### 超时控制

QueryBuilder 遵循 Go 标准的 `context` 模式进行超时控制——无需额外 API。只需使用 `context.WithTimeout` 或 `context.WithDeadline` 包装你的 context 即可：

```go
// 设置 3 秒查询超时
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})

users, total, err := b.QueryList(ctx)
if err != nil {
    // 超时时 err 可能为 context.DeadlineExceeded
    log.Printf("查询错误: %v", err)
}
```

该方式在所有数据源中表现一致——GORM、MongoDB 和 ElasticSearch 均原生支持 context 的取消和超时机制。你还可以结合中间件来记录慢查询：

```go
b.Use(func(ctx context.Context, q builder.Querier[User], next func(context.Context) ([]*User, int64, error)) ([]*User, int64, error) {
    start := time.Now()
    list, total, err := next(ctx)
    if duration := time.Since(start); duration > 2*time.Second {
        log.Printf("检测到慢查询: %v", duration)
    }
    return list, total, err
})
```

### Clone（并发分叉）

每个专属构建器提供 `Clone()` 方法，创建当前查询配置的完全独立副本。克隆实例与原实例不共享任何可变状态——对其中一个的修改不会影响另一个。

**核心要点：**
- 所有标量字段、切片（fields、cursorFields、cursorValues、middlewares）以及数据源专属的 filter/sort 均为深拷贝。
- 原始构建器**不是**并发安全的——不要在多个 goroutine 中对同一实例调用 `Set*` 方法。
- `Clone()` 之后，每个副本可以安全地在各自的 goroutine 中使用。

#### 基本用法

```go
// 构建一个"模板"，配置公共参数
base := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
base.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", "active")
}).SetSort(func(db *gorm.DB) *gorm.DB {
    return db.Order("id DESC")
}).SetFields("id", "name", "email").SetNeedTotal(true)

// Clone 后独立定制
page1 := base.Clone().SetStart(0).SetLimit(50)
page2 := base.Clone().SetStart(50).SetLimit(50)
```

#### 并发分叉查询（最佳实践）

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
        list, _, err := q.QueryList(ctx)
        if err != nil {
            log.Printf("page %d error: %v", idx, err)
            return
        }
        results[idx] = list
    }(i, page)
}
wg.Wait()
```

#### Clone 后使用不同过滤条件

```go
base := builder.NewMongoBuilder[Order](builder.NewDBProxy(nil, collection, nil))
base.SetFields("id", "user_id", "amount").SetLimit(20)

// 分叉为不同的过滤条件
pending := base.Clone().SetFilter(bson.D{{Key: "status", Value: "pending"}})
completed := base.Clone().SetFilter(bson.D{{Key: "status", Value: "completed"}})

go func() { pendingOrders, _, _ := pending.QueryList(ctx) }()
go func() { completedOrders, _, _ := completed.QueryList(ctx) }()
```

#### Clone 后追加不同中间件

```go
base := builder.NewGormBuilder[Product](builder.NewDBProxy(db, nil, nil))
base.SetFilter(filterScope).SetLimit(100)

// 每个 Clone 可以拥有独立的中间件栈
go func() {
    q := base.Clone()
    q.Use(cacheMiddleware)  // 此副本走缓存
    list, _, _ := q.QueryList(ctx)
}()

go func() {
    q := base.Clone()
    q.Use(metricsMiddleware) // 此副本收集指标
    list, _, _ := q.QueryList(ctx)
}()
```

#### 规则与反模式

| 规则 | 说明 |
|------|------|
| ✅ 先配置，再 Clone | 构建"模板" builder，然后通过 Clone 分叉 |
| ✅ 每个 goroutine 一个 Clone | 每个 goroutine 应独占自己的 Clone 副本 |
| ✅ Clone 是对 base 的只读操作 | 可以对同一个 base 多次调用 Clone（顺序调用） |
| ❌ 不要跨 goroutine 共享 builder | 不要在多个 goroutine 中对同一实例调用 Set 方法 |
| ❌ 不要在 base 被修改时并发 Clone | 确保 base 完全配置好后再进行并发 Clone 调用 |

### 缓存中间件

使用内置的 `CacheMiddleware` 缓存查询结果。实现 `CacheProvider` 接口以对接你的缓存后端：

```go
// CacheProvider 接口——可用 Redis、内存缓存等实现
type CacheProvider interface {
    Get(ctx context.Context, key string) ([]byte, bool)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration)
}
```

以下示例使用 [gcache](https://github.com/bluele/gcache)（支持 LRU、LFU、ARC 的内存缓存库）作为缓存后端：

```go
import (
    "context"
    "time"

    "github.com/bluele/gcache"
    builder "github.com/fantasticbin/QueryBuilder"
)

// GCacheProvider 使用 gcache 实现 builder.CacheProvider 接口
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

配合缓存中间件使用：

```go
cache := NewGCacheProvider(1000) // 1000 条目的 LRU 缓存

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.Use(builder.CacheMiddleware[User](cache, 5*time.Minute, func(ctx context.Context, b builder.Querier[User]) string {
    meta := b.GetQueryMeta()
    return fmt.Sprintf("users:list:%d:%d", meta.start, meta.limit)
}))

users, total, err := b.QueryList(ctx)
```

### 缓存键生成策略

为解决缓存键设计不统一、命中率不稳定的问题，QueryBuilder 提供了 `CacheKeyBuilder` 接口及开箱即用的 `DefaultCacheKeyBuilder` 默认实现。

#### CacheKeyBuilder 接口

```go
// CacheKeyBuilder 定义缓存键构建接口，业务方可覆写默认实现。
type CacheKeyBuilder interface {
    Build(ctx context.Context, meta QueryMeta) string
}
```

#### DefaultCacheKeyBuilder（默认实现）

默认实现从 `QueryMeta`（通过 `builder.GetQueryMeta()` 获取）和 `CacheKeyHints`（由 `DefaultCacheKeyBuilder` 自身持有）中提取以下维度构建缓存键：

| 维度 | 来源 | 说明 |
|------|------|------|
| `prefix` | `DefaultCacheKeyBuilder.Prefix` | 业务资源名（如 "users"、"orders"），隔离不同查询场景 |
| `datasource` | `QueryMeta.DataSource` | 数据源类型（Gorm/MongoDB/ElasticSearch） |
| `fields` | `QueryMeta.Fields` | 查询字段投影 |
| `pagination` | `QueryMeta` | 包含 start、limit、needTotal、needPagination、isCursorQuery、cursorFields |
| `filter` | `DefaultCacheKeyBuilder.Hints` | 业务过滤条件（map/struct/切片/标量） |
| `sort` | `DefaultCacheKeyBuilder.Hints` | 业务排序条件 |
| `extra` | `DefaultCacheKeyBuilder.Hints` | 扩展维度（如 tenant_id 等多租户隔离字段） |

最终将所有维度 JSON 序列化后取 SHA1 哈希，生成格式为 `qb:cache:<sha1hex>` 的固定长度缓存键。

`CacheKeyHints` 完全由 `DefaultCacheKeyBuilder` 自身管理——**不存储在构建器基类中，也不注入到 context 中**。这种设计保持了查询构建器的职责纯净，并避免了 `Clone` 并发场景下的数据混乱。

> ⚠️ **重要提示：** 使用 `DefaultCacheKeyBuilder` 时，**必须**提供 `Hints` 或 `HintsProvider`。如果两者都为 nil/空，生成的缓存键将不包含 filter/sort/extra 维度，这意味着**不同查询条件会共享相同的缓存键**，导致缓存串读。

#### 使用 CacheKeyHints

由于 filter/sort 等业务条件是数据源专属类型（GORM scope、bson.D、elastic.Query），无法从构建器中自动提取。在创建缓存中间件时，直接在 `DefaultCacheKeyBuilder` 中提供 `CacheKeyHints`：

```go
// Hints 直接在 DefaultCacheKeyBuilder 中提供
keyBuilder := builder.DefaultCacheKeyBuilder{
    Prefix: "users",
    Hints: builder.CacheKeyHints{
        Filter: map[string]any{"status": "active", "role": "admin"},
        Sort:   map[string]any{"created_at": "desc"},
        Extra:  map[string]any{"tenant_id": "tenant-123"},
    },
}
```

#### 使用 CacheMiddlewareWithKeyBuilder

```go
cache := NewGCacheProvider(1000)

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", "active")
})

// 使用 DefaultCacheKeyBuilder 并提供 Hints——缓存键由 QueryMeta + Hints 共同决定
b.Use(builder.CacheMiddlewareWithKeyBuilder[User](
    cache,
    5*time.Minute,
    builder.DefaultCacheKeyBuilder{
        Prefix: "users",
        Hints: builder.CacheKeyHints{
            Filter: map[string]any{"status": "active"},
            Sort:   map[string]any{"created_at": "desc"},
        },
    },
))

users, total, err := b.QueryList(ctx)
```

#### HintsProvider（动态 Hints 提供者）

当 hints 需要从 ctx 中动态获取时（如多租户隔离），使用 `HintsProvider`：

```go
b.Use(builder.CacheMiddlewareWithKeyBuilder[User](
    cache,
    5*time.Minute,
    builder.DefaultCacheKeyBuilder{
        Prefix: "users",
        HintsProvider: func(ctx context.Context) builder.CacheKeyHints {
            // 从 context 中动态提取租户信息
            return builder.CacheKeyHints{
                Filter: map[string]any{"status": "active"},
                Extra:  map[string]any{"tenant_id": extractTenantID(ctx)},
            }
        },
    },
))
```

> **优先级**：当 `Hints` 非空时，`HintsProvider` 不会被调用。`HintsProvider` 仅在 `Hints` 为空时作为兜底。

#### Clone 并发场景下的缓存隔离

由于 `CacheKeyHints` 由 `DefaultCacheKeyBuilder` 自身管理（而非构建器基类），每个 `Clone` 实例可以安全地使用各自的缓存中间件携带不同的 hints——无共享状态，无数据混乱：

```go
base := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
base.SetFields("id", "name", "email").SetNeedTotal(true)

// 每个 Clone 使用各自的缓存中间件，携带不同的 hints
go func() {
    q := base.Clone()
    q.SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "active") })
    q.Use(builder.CacheMiddlewareWithKeyBuilder[User](cache, 5*time.Minute,
        builder.DefaultCacheKeyBuilder{Prefix: "users", Hints: builder.CacheKeyHints{
            Filter: map[string]any{"status": "active"},
        }},
    ))
    list, _, _ := q.QueryList(ctx)
}()

go func() {
    q := base.Clone()
    q.SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "inactive") })
    q.Use(builder.CacheMiddlewareWithKeyBuilder[User](cache, 5*time.Minute,
        builder.DefaultCacheKeyBuilder{Prefix: "users", Hints: builder.CacheKeyHints{
            Filter: map[string]any{"status": "inactive"},
        }},
    ))
    list, _, _ := q.QueryList(ctx)
}()
```

#### 自定义 CacheKeyBuilder

业务方可实现 `CacheKeyBuilder` 接口完全覆写默认的 key 生成逻辑：

```go
type MyCacheKeyBuilder struct{}

func (b MyCacheKeyBuilder) Build(ctx context.Context, meta builder.QueryMeta) string {
    tenantID := extractTenantID(ctx)
    return fmt.Sprintf("my-app:%s:%v:%d:%d", tenantID, meta.DataSource, meta.Start, meta.Limit)
}

b.Use(builder.CacheMiddlewareWithKeyBuilder[User](cache, 5*time.Minute, MyCacheKeyBuilder{}))
```

#### 设计说明

- **稳定性**：相同查询条件始终生成相同的缓存键（`encoding/json` 对 map key 按字典序排序）。
- **防御性**：对不可 JSON 序列化的值（如函数、channel）自动降级为字符串表示，避免 key 空串碰撞。
- **兜底机制**：JSON 序列化失败时使用 `fmt.Sprintf` 格式化，确保 key 不为空。
- **空结果缓存**：查询结果为空时仍会写入缓存，防止缓存穿透。
- **Clone 安全**：每个 Clone 实例使用各自的 `DefaultCacheKeyBuilder`（携带独立的 `Hints`），确保无共享可变状态。

### 查询元信息

中间件可通过 `builder` 参数的 `GetQueryMeta()` 方法直接获取查询元数据——无需通过 context 传递：

```go
// 在中间件中——直接从 builder 获取 meta
func MyMiddleware[R any]() builder.Middleware[R] {
    return func(ctx context.Context, q builder.Querier[R], next func(context.Context) ([]*R, int64, error)) ([]*R, int64, error) {
        meta := q.GetQueryMeta()
        log.Printf("数据源: %v, 起始: %d, 每页: %d, 字段: %v",
            meta.DataSource, meta.Start, meta.Limit, meta.Fields)
        return next(ctx)
    }
}
```

#### 为什么不再将 QueryMeta 注入到 Context 中？

在早期版本中，`QueryMeta` 会在执行前自动注入到 context 中，中间件通过 `QueryMetaFromContext(ctx)` 获取。这种方式在 `Clone` 功能完善后存在关键局限性：

- 当使用 `Clone` 进行并发分叉查询时，多个构建器实例可能共享同一个父 context。如果 `QueryMeta` 存储在 context 中，不同 Clone 实例的并发写入会导致共享 context 数据混乱。
- 新方式（`builder.GetQueryMeta()`）确保每个构建器实例返回各自独立的元数据快照——无共享状态，无数据竞争。

#### 在中间件中将 Meta 存入 Context（如有需要）

如果你需要将 `QueryMeta` 传递到更深层的调用中（例如传递给无法访问 builder 的 repository 函数），可以通过一个简单的中间件实现：

```go
// 定义 context key
type queryMetaKeyType struct{}
var queryMetaKey = queryMetaKeyType{}

// 将 QueryMeta 注入到 context 的中间件
func MetaToCtxMiddleware[R any]() builder.Middleware[R] {
    return func(ctx context.Context, q builder.Querier[R], next func(context.Context) ([]*R, int64, error)) ([]*R, int64, error) {
        ctx = context.WithValue(ctx, queryMetaKey, q.GetQueryMeta())
        return next(ctx)
    }
}

// 使用
b.Use(MetaToCtxMiddleware[User]())

// 在下游代码中获取
func getMetaFromCtx(ctx context.Context) (builder.QueryMeta, bool) {
    meta, ok := ctx.Value(queryMetaKey).(builder.QueryMeta)
    return meta, ok
}
```

这种方式对 `Clone` 场景是安全的，因为每个 Clone 的中间件管道独立运行，拥有各自的 context。

`QueryMeta` 包含以下字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `DataSource` | `DataSource` | 数据源类型（Gorm/MongoDB/ElasticSearch） |
| `Start` | `uint32` | 分页起始位置 |
| `Limit` | `uint32` | 每页数据条数 |
| `NeedTotal` | `bool` | 是否需要查询总数 |
| `NeedPagination` | `bool` | 是否需要分页 |
| `Fields` | `[]string` | 指定字段列表 |
| `IsCursorQuery` | `bool` | 是否为游标查询模式 |
| `CursorFields` | `[]string` | 游标分页排序字段列表 |
| `StartTime` | `time.Time` | 查询开始时间 |

### Dry Run / Explain

每个专属构建器提供 `Explain` 方法，预览生成的查询语句，不会实际执行：

```go
// GORM — 返回 SQL 语句
gormBuilder := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
gormBuilder.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})
sql, err := gormBuilder.Explain(ctx)
// 输出: SELECT * FROM `users` WHERE status = ? | args: [1]

// MongoDB — 返回 JSON 格式的 filter/sort/projection
mongoBuilder := builder.NewMongoBuilder[Doc](builder.NewDBProxy(nil, collection, nil))
mongoBuilder.SetFilter(bson.D{{Key: "status", Value: "active"}})
jsonStr, err := mongoBuilder.Explain(ctx)

// ElasticSearch — 返回 Query DSL JSON
esBuilder := builder.NewElasticSearchBuilder[Doc](builder.NewDBProxy(nil, nil, esClient), "my_index")
esBuilder.SetFilter(elastic.NewTermQuery("status", "active"))
dsl, err := esBuilder.Explain(ctx)
```

### Mock 测试

使用内置的 `MockQuerier` 进行单元测试：

```go
func TestListUser(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    // 创建 Mock
    mockQuerier := builder.NewMockQuerier[model.User](ctrl)

    // 设置期望
    mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
    mockQuerier.EXPECT().
        QueryList(gomock.Any()).
        Return([]*model.User{{ID: 1, Name: "Alice"}}, int64(1), nil)

    // 注入 Mock
    list := builder.NewList[model.User]()
    list.SetQuerier(mockQuerier)

    result, total, err := list.Query(ctx, opts...)
    // 断言结果...
}
```

### Elasticsearch 构建器

`ElasticSearchBuilder` 在构造时接收索引名，也可以通过 `SetESIndex` 方法动态修改：

```go
// 构造时传入索引名
esBuilder := builder.NewElasticSearchBuilder[Doc](
    builder.NewDBProxy(nil, nil, esClient),
    "my_index",
)

// 也可以通过 SetESIndex 动态修改索引名（支持链式调用）
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

### 游标分页

使用 `QueryCursor` 对大数据集进行内存高效的流式遍历。它返回 Go 1.23+ `iter.Seq2[*R, error]` 迭代器，内部自动基于游标条件分批获取数据。

**工作原理：**
- 每批数据通过游标条件（而非 OFFSET）获取，无论数据深度如何都能保持稳定性能。
- Gorm 使用行值表达式（`WHERE (col1, col2) > (v1, v2)`），MongoDB 使用 `$gt` 复合条件，ElasticSearch 使用 `search_after` API。
- 游标值从每批最后一条记录中自动提取——无需手动管理游标。
- 支持单字段和多字段游标。

#### 直接使用构建器

```go
ctx := context.Background()
db := &gorm.DB{} // 你的 GORM 实例

b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})

// 设置游标字段——建议使用有索引的列以获得最佳性能
b.SetCursorField("id")
// SetLimit 控制每批获取的数据条数（默认：10）
b.SetLimit(100)

// QueryCursor 返回 iter.Seq2 迭代器
for user, err := range b.QueryCursor(ctx) {
    if err != nil {
        log.Printf("游标查询错误: %v", err)
        break
    }
    process(user)
}
```

#### 多字段游标

适用于复合排序场景（如 `created_at` + `id`）：

```go
b := builder.NewGormBuilder[Order](builder.NewDBProxy(db, nil, nil))
b.SetCursorField("created_at", "id") // 多字段游标
b.SetLimit(50)

for order, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    exportOrder(order)
}
```

#### 使用 List 与选项模式

```go
list := builder.NewList[User]()
list.SetDataSource(builder.Gorm)
list.SetScope(builder.NewGormScope[User](
    func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", 1) },
    nil, // 无需自定义排序——游标字段自动处理排序
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

#### MongoDB 游标分页

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

#### ElasticSearch 游标分页

ES 默认使用 `search_after + PIT (Point in Time)` 进行深分页，在跨请求分页期间保持索引快照一致、避免 refresh 导致排序不稳定；也可退化为纯 `search_after` 分页（关闭 PIT）。

```go
b := builder.NewElasticSearchBuilder[Doc](
    builder.NewDBProxy(nil, nil, esClient), "my_index",
)
b.SetFilter(elastic.NewTermQuery("status", "active"))
b.SetCursorField("created_at")
b.SetSort(elastic.NewFieldSort("_id").Asc()) // 辅助排序
b.SetPITKeepAlive("2m")                       // 可选，默认 "1m"
b.SetLimit(100)

for doc, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(doc)
}
```

如需回退为纯 `search_after`，可显式关闭 PIT：

```go
b.SetPITEnabled(false)
```

#### 设置游标初始位置

默认情况下，游标分页从数据集的起始位置开始。你可以指定一个初始游标位置，从特定位置恢复遍历——适用于客户端驱动的分页场景（如移动端的"加载更多"）。

**方案A：复用 `start` 作为初始游标值** —— 适用于单字段数值型游标：

```go
// 直接使用构建器
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetCursorField("id")
b.SetStart(100) // 从 id > 100 开始
b.SetLimit(10)

for user, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(user) // 返回 id > 100 的用户
}

// 通过 List 选项
for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithStart(100), // 从 id > 100 开始
    builder.WithLimit(10),
) {
    if err != nil {
        break
    }
    process(user)
}
```

**方案B：`SetCursorValue` / `WithCursorValue`** —— 适用于多字段游标或非数值型游标值：

```go
// 直接使用构建器——多字段游标
b := builder.NewGormBuilder[Order](builder.NewDBProxy(db, nil, nil))
b.SetCursorField("created_at", "id")
b.SetCursorValue(int64(1700000000), uint32(500)) // 从 (created_at > 1700000000, id > 500) 恢复
b.SetLimit(10)

for order, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(order)
}

// 通过 List 选项
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

> **优先级**：当同时设置了 `SetCursorValue` 和 `SetStart` 时，`SetCursorValue` 优先。

#### 游标模式下的分页控制

`needPagination` 和 `needTotal` 在游标查询模式下同样生效：

| 选项 | 默认值 | 游标模式下的行为 |
|------|--------|-----------------|
| `needPagination` | `true` | 为 `true` 时，只获取**单批次**数据（相当于一页）。为 `false` 时，自动分批遍历整个数据集直到耗尽。 |
| `needTotal` | `true` | 为 `true` 时，在**首批次**查询时并行执行 Count 查询获取总数。总数通过 `AfterQueryHook` 传递。为 `false` 时，完全跳过 Count 查询。 |

**单页游标查询**（App 端"加载更多"场景）：

```go
// 获取一页数据并返回总数——适用于移动端"加载更多"分页
for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithCursorValue(uint32(lastSeenID)),
    builder.WithLimit(20),
    builder.WithNeedPagination(true),  // 只取单批次
    builder.WithNeedTotal(true),       // 并行获取总数
) {
    if err != nil {
        break
    }
    process(user)
}
```

**全量遍历不查总数**（数据导出场景）：

```go
// 流式遍历全部记录，不查总数——适用于批处理/数据导出
for user, err := range list.QueryCursor(ctx,
    builder.WithData(builder.NewDBProxy(db, nil, nil)),
    builder.WithCursorField("id"),
    builder.WithLimit(500),
    builder.WithNeedPagination(false), // 遍历所有批次
    builder.WithNeedTotal(false),      // 跳过 Count 查询
) {
    if err != nil {
        break
    }
    export(user)
}
```

> **性能提示：** 对于不需要总数的大数据集遍历场景，设置 `needTotal(false)` 可以避免一次昂贵的 `COUNT(*)` / `CountDocuments` / `Count` 查询。

#### 提前终止

由于 `QueryCursor` 返回标准的 Go 迭代器，你可以随时使用 `break` 终止遍历：

```go
count := 0
for user, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    count++
    if count >= 1000 {
        break // 取到 1000 条后停止
    }
}
```

#### 游标查询与 Explain

配置了游标字段后，`Explain` 会输出游标查询模式的首批查询语句：

```go
b := builder.NewGormBuilder[User](builder.NewDBProxy(db, nil, nil))
b.SetFilter(func(db *gorm.DB) *gorm.DB {
    return db.Where("status = ?", 1)
})
b.SetCursorField("id")
b.SetLimit(100)

sql, err := b.Explain(ctx)
// 输出: [CursorQuery] SELECT * FROM `users` WHERE status = ? ORDER BY id ASC LIMIT 100 | args: [1] | cursor_fields: [id]
```

### Scope 辅助函数

在 `List` 模式下，通过 `List.SetScope` 配合 Scope 辅助函数设置 filter/sort，无需手写中间件签名和类型断言：

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

| 辅助函数 | 适用构建器 | filter 参数类型 | sort 参数类型 |
|---------|-----------|----------------|---------------|
| `NewGormScope` | `GormBuilder` | `func(*gorm.DB) *gorm.DB` | `func(*gorm.DB) *gorm.DB` |
| `NewMongoScope` | `MongoBuilder` | `bson.D` | `bson.D` |
| `NewElasticSearchScope` | `ElasticSearchBuilder` | `elastic.Query` | `...elastic.Sorter` |

filter 或 sort 参数传 `nil` 时将被忽略，不会影响查询流程。

---

## API 参考

### Querier 接口

| 方法 | 说明 |
|------|------|
| `Use(middleware)` | 添加中间件到查询管道 |
| `SetStart(start)` | 设置分页起始位置 |
| `SetLimit(limit)` | 设置每页数据条数（最大值：5000） |
| `SetNeedTotal(bool)` | 设置是否需要查询总数 |
| `SetNeedPagination(bool)` | 设置是否需要分页 |
| `SetFields(fields...)` | 设置指定字段 |
| `SetBeforeQueryHook(hook)` | 设置查询前置钩子 |
| `SetAfterQueryHook(hook)` | 设置查询后置钩子 |
| `SetCursorField(fields...)` | 设置游标分页排序字段 |
| `SetCursorValue(values...)` | 设置游标初始值（用于从指定位置恢复遍历） |
| `QueryList(ctx)` | 执行查询 |
| `QueryCursor(ctx)` | 执行游标分页查询，返回 `iter.Seq2` 迭代器 |

### 构建器专属方法

| 方法 | 适用构建器 | 说明 |
|------|-----------|------|
| `SetFilter(...)` | 所有构建器 | 设置数据源专属过滤条件 |
| `SetSort(...)` | 所有构建器 | 设置数据源专属排序条件 |
| `Clone()` | 所有构建器 | 创建独立副本，用于并发分叉查询 |
| `SetESIndex(index)` | `ElasticSearchBuilder` | 设置/修改 ES 索引名 |
| `Explain(ctx)` | 所有构建器 | 预览生成的查询语句（Dry Run） |

---

## 支持的数据源

| 数据源           | 构建器 | Filter 类型 | Sort 类型 |
|---------------|--------|-------------|-----------|
| Gorm          | `GormBuilder` | `GormScope` (`func(*gorm.DB) *gorm.DB`) | `GormScope` |
| MongoDB       | `MongoBuilder` | `MongoFilter` (`bson.D`) | `MongoSort` (`bson.D`) |
| ElasticSearch | `ElasticSearchBuilder` | `elastic.Query` | `...elastic.Sorter` |

---

## 贡献

欢迎提交 Issue 和 PR！

---

## License

MIT License

---

## 联系

如有问题或建议，请提交 Issue 或联系作者。
