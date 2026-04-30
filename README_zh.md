[English](README.md) | **中文**

---

# QueryBuilder

一个用于构建类型安全列表查询的 Go 库，支持多种数据源。利用 Go 1.26 自引用泛型约束特性，为 MySQL（GORM）、MongoDB、ElasticSearch 提供专属查询构建器——零类型断言、灵活中间件、统一查询接口。

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
- **查询元信息上下文**：查询执行前自动将 `QueryMeta` 注入到 context 中——中间件可获取数据源类型、分页信息和查询开始时间。
- **Dry Run / Explain**：每个构建器提供 `Explain` 方法，预览生成的查询语句（SQL、MongoDB filter、ES DSL），无需实际执行。
- **游标分页**：内置基于游标的分页查询 `QueryCursor`，返回 Go 1.23+ `iter.Seq2` 迭代器，支持对大数据集进行内存高效的流式遍历。支持 MySQL（行值表达式）、MongoDB（`$gt` 复合条件）和 ElasticSearch（`search_after` API）。
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
    list.SetDataSource(builder.MySQL)

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
list.SetDataSource(builder.MySQL)

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

| 数据源 | 实现方式 |
|--------|---------|
| MySQL (GORM) | `db.Select(fields...)` |
| MongoDB | `options.Find().SetProjection(bson.D{...})` |
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
b.Use(builder.CacheMiddleware[User](cache, 5*time.Minute, func(ctx context.Context) string {
    return fmt.Sprintf("users:list:%d:%d", start, limit)
}))

users, total, err := b.QueryList(ctx)
```

### 查询元信息上下文

查询元信息会在执行前自动注入到 context 中。中间件可通过 `QueryMetaFromContext` 获取：

```go
// 在中间件中使用
func MyMiddleware[R any]() builder.Middleware[R] {
    return func(ctx context.Context, q builder.Querier[R], next func(context.Context) ([]*R, int64, error)) ([]*R, int64, error) {
        meta := builder.QueryMetaFromContext(ctx)
        if meta != nil {
            log.Printf("数据源: %v, 起始: %d, 每页: %d, 字段: %v",
                meta.DataSource, meta.Start, meta.Limit, meta.Fields)
        }
        return next(ctx)
    }
}
```

`QueryMeta` 包含以下字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `DataSource` | `DataSource` | 数据源类型（MySQL/MongoDB/ElasticSearch） |
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
- MySQL 使用行值表达式（`WHERE (col1, col2) > (v1, v2)`），MongoDB 使用 `$gt` 复合条件，ElasticSearch 使用 `search_after` API。
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
list.SetDataSource(builder.MySQL)
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

ES 游标分页内部使用 `search_after` API。最后一条文档的 sort values 会自动作为下一批的 `search_after` 参数：

```go
b := builder.NewElasticSearchBuilder[Doc](
    builder.NewDBProxy(nil, nil, esClient), "my_index",
)
b.SetFilter(elastic.NewTermQuery("status", "active"))
b.SetCursorField("created_at")
b.SetSort(elastic.NewFieldSort("_id").Asc()) // 辅助排序
b.SetLimit(100)

for doc, err := range b.QueryCursor(ctx) {
    if err != nil {
        break
    }
    process(doc)
}
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
| `SetLimit(limit)` | 设置每页数据条数 |
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
| `SetESIndex(index)` | `ElasticSearchBuilder` | 设置/修改 ES 索引名 |
| `Explain(ctx)` | 所有构建器 | 预览生成的查询语句（Dry Run） |

---

## 支持的数据源

| 数据源           | 构建器 | Filter 类型 | Sort 类型 |
|---------------|--------|-------------|-----------|
| MySQL (GORM)  | `GormBuilder` | `GormScope` (`func(*gorm.DB) *gorm.DB`) | `GormScope` |
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
