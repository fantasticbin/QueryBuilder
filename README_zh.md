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
- **分页控制**：支持开关分页，适用于数据导出等场景。
- **选项模式**：通过函数式选项和 `OptionBuilder` 辅助工具灵活配置查询参数。
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
┌─────────────────────────────────────────────────┐
│                  Querier[R]                     │  ← 统一接口
│  Use / SetStart / SetLimit / SetNeedTotal /     │
│  SetNeedPagination / QueryList                  │
└──────────┬──────────┬──────────┬────────────────┘
           │          │          │
    ┌──────▼──┐ ┌─────▼────┐ ┌───▼─────────────┐
    │  Gorm   │ │  Mongo   │ │  ElasticSearch  │   ← 专属构建器
    │ Builder │ │ Builder  │ │     Builder     │
    └──────┬──┘ └─────┬────┘ └──┬──────────────┘
           │          │         │
    ┌──────▼──────────▼─────────▼────────────────┐
    │             builder[B, R]                  │   ← 公共基类（泛型）
    │  data / start / limit / middlewares / ...  │
    └────────────────────────────────────────────┘
```

每个专属构建器通过 Go 1.26 自引用泛型嵌入私有的 `builder` 基类，继承通用的分页和中间件逻辑，同时暴露各自强类型的 `SetFilter` / `SetSort`。

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
    // 定义类型别名，简化泛型类型的推断
    type filter = pb.ListUserFilter
    type sort = pb.QueryUserListSort

    list := builder.NewList[model.User, filter, sort]()
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

## 进阶用法

### 中间件

在查询管道中插入自定义中间件：

```go
list := builder.NewList[model.User, filter, sort]()
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

### Mock 测试

使用内置的 `MockQuerier` 进行单元测试：

```go
func TestListUser(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    type filter = pb.ListUserFilter
    type sort = pb.QueryUserListSort

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
    list := builder.NewList[model.User, filter, sort]()
    list.SetQuerier(mockQuerier)

    result, total, err := list.Query(ctx, opts...)
    // 断言结果...
}
```

### 入参辅助工具

使用内置的 `OptionBuilder` 简化入参设置，支持类型推断：

```go
opts := builder.NewOptionBuilder[filter, sort]().
    WithData(builder.NewDBProxy(model.DB, nil, nil)).
    WithStart(req.GetStart()).
    WithLimit(req.GetLimit()).
    LoadOptions()

list := builder.NewList[model.User, filter, sort]()
list.SetDataSource(builder.MySQL)

// 使用 SetScope 设置 filter 和 sort
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

## 支持的数据源

| 数据源 | 构建器 | Filter 类型 | Sort 类型 |
|--------|--------|-------------|-----------|
| MySQL (GORM) | `GormBuilder` | `GormScope` (`func(*gorm.DB) *gorm.DB`) | `GormScope` |
| MongoDB | `MongoBuilder` | `MongoFilter` (`bson.D`) | `MongoSort` (`bson.D`) |
| Elasticsearch | `ElasticSearchBuilder` | `elastic.Query` | `...elastic.Sorter` |

---

## 贡献

欢迎提交 Issue 和 PR！

---

## License

MIT License

---

## 联系

如有问题或建议，请提交 Issue 或联系作者。
