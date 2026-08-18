# QueryBuilder 技术点清单

本文对照仓库中的实现，梳理具有教学价值的语言特性、设计模式、查询能力与工程实践，并标注**项目出处**与**理论/社区出处**，便于阅读源码与深入研究。

> 参考入口：`README.md`、`docs/README_zh.md`，以及核心包 `builder` / `core` / `agg` / `middleware` / `util`。

---

## 目录

1. [项目概览](#1-项目概览)
2. [语言与 API 设计](#2-语言与-api-设计)
3. [设计模式](#3-设计模式)
4. [查询能力](#4-查询能力)
5. [工程化能力](#5-工程化能力)
6. [附录：技术点速查表](#6-附录技术点速查表)

---

## 1. 项目概览

**QueryBuilder** 是一个面向多数据源的类型安全列表/聚合查询库：在统一 `Querier[R]` 门面下，为 GORM、MongoDB、Elasticsearch 提供专属 Builder，并配套中间件、缓存、可观测性、游标分页与聚合 DSL。

| 维度 | 说明 | 出处 |
| --- | --- | --- |
| 语言版本 | Go **1.26+**（自引用泛型约束） | `go.mod`；README 安装说明 |
| 模块路径 | `github.com/fantasticbin/QueryBuilder/v2` | `go.mod` |
| 核心数据源 | GORM / MongoDB driver v2 / olivere elastic v7 | `go.mod` `require` |
| 测试与并发 | `testing` + `go.uber.org/mock` + `golang.org/x/sync/errgroup` | `go.mod`；`mock_querier.go`；`util/util.go` |

架构上可概括为：

```text
Querier[R]  ──►  GormBuilder / MongoBuilder / ElasticSearchBuilder
                     │
                     ▼
              builder[B, R]   （公共模板：分页、游标、钩子、中间件）
                     │
                     ▼
           DBProxy + DataSourceAdapter
```

出处：`README.md` 架构图；`builder.go` 中 `builder` 与 `Querier` 定义。

---

## 2. 语言与 API 设计

### 2.1 自引用泛型 / CRTP 风格的链式调用

**是什么**  
用「构建器类型自己作为类型参数」约束接口，使基类方法返回**具体子类型**而非接口，从而在无需类型断言的前提下保持流式 API（fluent API）。

**项目中的形态**

```go
type selfBuilder[B any] interface {
    self() B
}

type queryBuilder[B selfBuilder[B], R any] interface {
    selfBuilder[B]
    doQuery(ctx context.Context) ([]*R, int64, error)
    doCursorQuery(...) ([]*R, []any, int64, bool, error)
    cleanupCursorQuery(result *core.CursorPageResult[R], err error)
}

type builder[B queryBuilder[B, R], R any] struct { /* ... */ }
```

例如 `GormBuilder[R]` 内嵌 `builder[*GormBuilder[R], R]`，`SetLimit` 等返回 `B`（即 `*GormBuilder[R]`），可继续调用 `SetFilter`。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `builder.go`：`selfBuilder`、`queryBuilder`、`builder[B, R]` | C++ 的 **CRTP**（奇异递归模板模式，Curiously Recurring Template Pattern）；Go 1.18+ 泛型；Go 1.26 对自引用约束的强化（README 明确依赖） |
| `gorm_builder.go`：`GormBuilder` + `setSelf` | 同上；流式接口（Fluent Interface，Martin Fowler） |

### 2.2 Go 1.23+ 迭代器（`iter.Seq2`）

**是什么**  
标准库 `iter` 包提供的推送（push）风格序列：`iter.Seq2[V, E]` 适合「值 + 错误」成对产出（yield），用于流式消费大数据集，避免一次性装入内存。

**项目中的形态**  
`QueryCursor` 返回 `iter.Seq2[*R, error]`；`buildCursorIterator` 内循环「拉取一批 → 产出（yield）→ 更新游标」，并在每批前检查 `ctx.Err()`。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `cursor.go`：`buildCursorIterator` | Go 1.23 [Range over function / iterators](https://go.dev/blog/range-functions)；`iter` 包文档 |
| `builder.go`：`QuerierCursor.QueryCursor` | 同上；迭代器（Iterator）模式 |

### 2.3 函数选项模式（Functional Options）

**是什么**  
用 `func(*Options)` 配置默认结构体，避免过长的参数列表与布尔值陷阱（boolean trap），新增选项时不破坏既有调用方。

**项目中的形态**

```go
type QueryOption func(options *BaseQueryListOptions)

func LoadQueryOptions(opts ...QueryOption) BaseQueryListOptions { /* 先取默认值，再依次应用各选项 */ }
// WithLimit / WithFields / WithPITID 等选项函数
```

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `option.go`：`QueryOption`、`LoadQueryOptions`、`With*` | Rob Pike / Dave Cheney 推广的「函数选项」（[Functional Options](https://dave.cheney.net/2014/10/17/functional-options-for-friendly-apis)）；Uber Go Style Guide |

### 2.4 泛型结果抽象

**是什么**  
用接口统一「列表结果 / 游标页结果 / ES PIT 页结果」，中间件与缓存只依赖 `Result[R]`，不关心底层后端实现。

**项目中的形态**

- `Result[R]`：`GetItems` / `GetTotal` / `GetHasMore` / `GetNextCursorValues` / `GetResultKind`
- 实现：`ListResult[R]`、`CursorPageResult[R]`、`ESPITPageResult[R]`（内嵌游标结果并附加 `PitID`）

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `core/result.go` | 泛型接口；接口隔离原则（ISP）+ 策略统一返回值 |
| `middleware/cache.go`：按 `ResultKind` 序列化 | 同上 |

### 2.5 接口拆分与组合（接口隔离原则，ISP）

**是什么**  
把「列表 / 游标 / Explain / Meta」拆成小接口，再组合成 `Querier[R]`，调用方可以只依赖自己需要的能力。

```go
type QuerierList[R any] interface { QueryList(...) }
type QuerierCursor[R any] interface { QueryCursor(...); QueryPage(...) }
type QuerierExplain interface { Explain(...) }

type Querier[R any] interface {
    Use(...) Querier[R]
    // ... setters ...
    QuerierList[R]
    QuerierCursor[R]
    QuerierExplain
    core.QuerierMeta
}
```

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `builder.go`：`QuerierList` / `QuerierCursor` / `QuerierExplain` / `Querier` | SOLID：**接口隔离原则（ISP）**；Go 惯用的「小接口 + 组合」 |
| `core/meta.go`：`QuerierMeta` | 同上 |

### 2.6 结构体嵌入（组合优于继承，Composition over Inheritance）

**是什么**  
通过匿名嵌入复用字段与方法，而非类继承。

**项目中的形态**

- `builder` 内嵌 `queryConfig`、`cursorConfig`、`hookChain[R]`
- `GormBuilder` 内嵌 `builder[*GormBuilder[R], R]`
- `ESPITPageResult` 内嵌 `CursorPageResult`

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `builder.go`；`gorm_builder.go`；`core/result.go` | Go 官方 Effective Go 中的 Embedding；组合优于继承 |

### 2.7 哨兵错误（Sentinel Errors）与错误链

**是什么**  
包级 `var ErrXxx = errors.New(...)` 作为可判定的哨兵错误（sentinel error）；panic 恢复路径用自定义类型实现 `Unwrap`，保留 `errors.Is` / `errors.As` 的能力。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `builder.go`、`core/data.go`、`agg/spec.go` 中的 `Err*` | Go blog：[Working with Errors](https://go.dev/blog/go1.13-errors)；Dave Cheney 等关于哨兵错误（sentinel）与错误包装（wrapping）的讨论 |
| `util/util.go`：`PanicToError`、`panicError.Unwrap` | 同上；`runtime/debug.Stack` 用于并发路径上的调用栈 |

### 2.8 位掩码标志（Bitmask Flags）

**是什么**  
用 `1 << iota` 让每个常量占据独立比特位，多个特征可用 `|` 组合进一个整数，用 `&` 按位检测，替代「一堆 bool 字段」的结构体，节省空间且便于整体传递与序列化。

**项目中的形态**

```go
type PlanFlags uint64

const (
    PlanHasDistinctMetrics PlanFlags = 1 << iota
    PlanHasConditionalMetrics
    PlanHasDateGroups
    PlanUsesMongoFacet
    // ...
)

// 组合：plan.Flags |= PlanHasDateGroups
// 检测：plan.Has(PlanUsesMongoFacet)
func (f PlanFlags) Has(flag PlanFlags) bool {
    return flag != 0 && f&flag == flag
}
```

`AnalyzeSpec` 静态分析聚合规范后，把「是否有去重指标 / 是否走 Mongo facet / 是否需要客户端后处理」等执行特征一次性压缩进 `Plan.Flags`，供中间件与执行层按需探测；`PlanFlags` 直接作为 `Plan` 的字段随 JSON / BSON 序列化。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `agg/spec.go`：`PlanFlags`、`PlanFlags.Has`、`AnalyzeSpec` | Go 规范中的 [iota](https://go.dev/ref/spec#Iota)；标准库同类用法：`log.Logger` 的 flags、`os.FileMode` 位或组合；位标志（bit flags）惯用法 |

---

## 3. 设计模式

### 3.1 适配器模式（Adapter）

**意图**  
将 GORM / Mongo / ES 客户端统一为 `DataSourceAdapter`，由 `DBProxy` 统一注册并校验配置。

```go
type DataSourceAdapter interface {
    DataSource() DataSource
    IsConfigured() bool
}
// GormAdapter / MongoAdapter / ElasticSearchAdapter
// 另有 gormDBProvider 等内部提供方（provider）接口，用于强类型取回
```

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `core/data.go`；`builder.go` 中 `New*Adapter` / `NewDBProxyWithAdapters` | GoF **适配器（Adapter）**；六边形 / 端口适配器（Ports & Adapters）中的「出站适配器」 |

### 3.2 工厂方法（Factory Method）

**意图**  
按 `DataSource` 枚举创建对应的 Builder，对外只暴露 `Querier[R]`。

```go
func NewBuilder[R any](ds core.DataSource, data *core.DBProxy) Querier[R] {
    switch ds {
    case core.Gorm: return NewGormBuilder[R](data)
    case core.MongoDB: return NewMongoBuilder[R](data)
    case core.ElasticSearch: return NewElasticSearchBuilder[R](data, "")
    default: panic(...)
    }
}
```

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `builder.go`：`NewBuilder`；`list.go`：`buildQuerier` 内调用 | GoF **工厂方法（Factory Method）**；简单工厂变体 |

### 3.3 模板方法（Template Method）

**意图**  
基类 `builder` 固定算法骨架：校验 → 中间件 / 钩子 → 调用子类钩子方法 → 组装结果；子类只实现各后端差异。

| 骨架步骤 | 实现位置 |
| --- | --- |
| 校验分页/游标 | `prepareAndValidate`、`validateInitialCursorValues` |
| 中间件与 Hook | `middleware.go`：`executeWithMiddlewares` 等 |
| 后端查询 | 子类 `doQuery` / `doCursorQuery` |
| 游标收尾 | `cleanupCursorQuery`（如关闭 ES PIT） |

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `builder.go`：`QueryList` / `QueryCursor` / `QueryPage` | GoF **模板方法（Template Method）** |
| `gorm_builder.go` / `mongo_builder.go` / `elasticsearch_builder.go` 的 `doQuery` 等 | 同上 |

### 3.4 门面模式（Facade）

**意图**  
`List[R]` 对外提供更业务向的 `Query` / `QueryCursor` / `QueryPage` / `Explain`，内部负责数据源选择、选项（Option）应用、作用域（Scope）、钩子（Hook）、中间件与 panic 恢复。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `list.go`：`List[R]`、`NewList`、`NewListWithData` | GoF **门面（Facade）** |

### 3.5 原型（Prototype）/ 克隆（Clone）模式

**意图**  
`Clone()` 深拷贝查询配置（含字段、游标、中间件等切片副本），用于并发分叉，避免共享可变状态。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `gorm_builder.go` / `mongo_builder.go` / `elasticsearch_builder.go`：`Clone` | GoF **原型（Prototype）** |
| `builder.go`：`cloneBase`、`queryConfig.clone` 等 | 防御性拷贝（defensive copy） |
| `list.go`：`cloneQuerier` | 同上 |
| `agg` 包各 Builder 与 `Spec.Clone` | 同上 |

### 3.6 中间件 / 责任链（Chain of Responsibility）

**意图**  
洋葱模型：逆序包装 `next`，支持在查询前后插入缓存、观测与自定义逻辑；另有轻量级的 `BeforeQueryHook` / `AfterQueryHook` 钩子。

```go
type Middleware[R any] func(
    ctx context.Context,
    builder Querier[R],
    next func(context.Context) (core.Result[R], error),
) (core.Result[R], error)
```

`buildRunner` 在无中间件时直接短路，避免无谓的闭包分配（性能意识）。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `middleware.go`：`Middleware`、`buildRunner`、`executeWithMiddlewares` | HTTP 中间件传统（net/http、Chi、Gin）；GoF **责任链（Chain of Responsibility）**；洋葱架构（onion architecture）的管道思想 |
| `middleware/cache.go`、`middleware/observability.go` | 横切关注点（AOP 思想在 Go 中的中间件表达） |

### 3.7 策略模式（Strategy）与作用域（Scope）回调

**意图**  
`ScopeConfigurer` + `NewGormScope` / `NewMongoScope` / `NewElasticSearchScope` 把过滤 / 排序的配置策略注入 List 流程，调用方无需自行编写类型断言中间件。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `scope.go`；`list.go`：`SetScope` / `passQueryOption` | GoF **策略（Strategy）**；回调注入 |

### 3.8 建造者模式（Builder，查询 DSL 构建）

**意图**  
分步配置分页、字段、游标、过滤、排序后执行，与「最终查询对象」分离。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| 各 `*Builder` 与 `agg` 包 | GoF **建造者（Builder）**；与 2.1 的 CRTP 链式调用叠加 |

### 3.9 注册表 / 服务定位轻量版（DBProxy）

**意图**  
`DBProxy` 集中注册多个适配器，查询前统一执行 `IsConfigured` 校验。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `core/data.go` | 简化的注册表（Registry）；依赖倒置：业务依赖适配器抽象 |

---

## 4. 查询能力

### 4.1 偏移（Offset）分页、字段投影、总数上限

| 能力 | 说明 | 项目出处 | 领域出处 |
| --- | --- | --- | --- |
| Offset/Limit | `start` + `limit`，`limit` 为 0 时回落到默认值 | `option.go`：`effectiveLimit`；`queryConfig` | SQL `LIMIT/OFFSET`；常见 REST 列表 API |
| 字段投影 | `SetFields` / `WithFields` 减少 IO 与带宽 | `builder.go`；各后端 Select/Projection | SQL SELECT 列表；Mongo 投影（projection）；ES `_source` |
| Total 上限 | `SetTotalLimit` / `WithTotalLimit` 限制高成本 COUNT | `option.go`；`core.QueryMeta` | 大数据列表工程实践（近似总数 / 上限截断） |
| 关闭分页 | 导出等场景 | `SetNeedPagination` | 业务导出模式 |

### 4.2 游标（Cursor）分页

**要点**

- 支持单字段、多字段；`"+field"` / `"-field"` 表示排序方向
- `limit+1` 探测 `hasMore`，避免额外的 COUNT
- GORM：行值表达式（row value）等键集条件
- MongoDB：复合 `$gt` / 方向相关比较
- 默认决胜字段（tie-breaker）：`id` / `_id` / `_shard_doc`

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `cursor.go`：解析方向、`buildCursorIterator` | **键集分页（Keyset pagination）** / 定位查找（Seek Method，参考 [Use the Index, Luke](https://use-the-index-luke.com/no-offset) 等） |
| 各 builder 的 `doCursorQuery` | SQL / Mongo / ES 各自的 seek（向后查找）语义 |
| `QueryPage` → `CursorPageResult` | App「加载更多」API 形态 |

### 4.3 Elasticsearch 的 `search_after` + 时间点快照（Point-in-Time，PIT）

**要点**

- 游标遍历可用 PIT 固定索引快照，降低 refresh 导致的排序漂移
- `QueryPageWithPIT` 返回 `ESPITPageResult`（含 `PitID`，可跨请求续查）
- 内部自动创建 / 关闭 PIT，关闭时使用独立的超时上下文（timeout context），避免请求取消导致资源泄漏

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `elasticsearch_builder.go`：`QueryPageWithPIT`、`closePIT`、`SetPITID`、`SetPitKeepAlive` | Elasticsearch 官方 [Point in Time API](https://www.elastic.co/guide/en/elasticsearch/reference/current/point-in-time-api.html)、[search_after](https://www.elastic.co/guide/en/elasticsearch/reference/current/paginate-search-results.html) |
| `core/result.go`：`ESPITPageResult` | 同上 |
| `option.go`：`WithPITID` 等 | 同上 |

### 4.4 Explain / 试运行（Dry Run）

各 Builder 的 `Explain(ctx)`：GORM 输出 SQL，Mongo 输出查询 DSL，ES 输出 JSON DSL，**不执行**真实查询。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `QuerierExplain`；各 `*Builder.Explain` | SQL `EXPLAIN`；调试期的试运行（Dry Run）习惯 |

### 4.5 跨后端聚合 DSL（`agg` 包）

**要点**

- `Spec` 描述分组（GroupBy）、计数 / 求和 / 平均 / 最小 / 最大（Count/Sum/Avg/Min/Max）、条件指标、HAVING、排序、Limit
- 再翻译为 SQL / Mongo 聚合管道（Aggregation Pipeline）/ ES 复合聚合（Composite Aggregation）
- 字段名、表达式用正则校验，降低注入与非法 DSL 的风险
- 同样具备中间件、克隆（Clone）、Explain 能力

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `agg/spec.go`、`agg/builder.go`、`agg/gorm.go`、`agg/mongo.go`、`agg/elasticsearch.go` | 领域 DSL / 中间表示（IR）再代码生成（codegen）；SQL GROUP BY；Mongo `$group`；ES 聚合（aggregations） |
| `middleware/aggregate_*.go` | 聚合场景下横切能力的复用 |

---

## 5. 工程化能力

### 5.1 上下文（Context）传播与取消

所有查询与并发任务都接受 `context.Context`；游标循环中检查 `ctx.Err()`；`errgroup.WithContext` 在任一任务失败时取消其余任务。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| 各 `Query*` 签名；`cursor.go`；`util/util.go` | Go [context 包](https://pkg.go.dev/context)；官方博客 [Go Concurrency Patterns: Context](https://go.dev/blog/context) |

### 5.2 并发查询（数据 + 总数）

列表查询常并行执行「拉取数据」与「COUNT」，用 `util.WaitAndGoWithContext` 封装 `errgroup`，并捕获 goroutine 内的 panic、附带调用栈（stack）。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `util/util.go`：`WaitAndGoWithContext`、`GoWithNotify` | `golang.org/x/sync/errgroup`；扇出 / 扇入（fan-out / fan-in） |
| `gorm_builder.go` / `mongo_builder.go` / `elasticsearch_builder.go` 的 `doQuery` | 同上 |

### 5.3 可插拔缓存

| 组件 | 作用 | 出处 |
| --- | --- | --- |
| `CacheProvider` | 读取 / 写入（Get/Set），对接 Redis、内存等 | `middleware/cache.go` |
| `CacheKeyBuilder` | 自定义缓存键（key） | `middleware/cache_policy.go` |
| `DefaultCacheKeyBuilder` | 规范化 JSON 载荷 + **SHA1** 摘要 → `qb:cache:<hex>` | 同上 |
| `CacheKeyHints` | 过滤 / 排序 / 附加信息（如多租户标识） | 同上 |

### 5.4 可观测性中间件（厂商无关）

抽象出 `Attribute`、`QuerySpanStart`、`QueryEvent` 与信号类型（追踪 Trace / 指标 Metrics / 日志 Logger），不绑定 OpenTelemetry（OTel）/ Prometheus 等具体 SDK；观测回调中先隔离 panic，再重新抛出。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `middleware/observability.go` | OpenTelemetry 语义启发；三大支柱：日志（Logs）/ 指标（Metrics）/ 追踪（Traces）；依赖倒置 |

### 5.5 Panic 恢复（Recovery）分层策略

| 层级 | 行为 | 出处 |
| --- | --- | --- |
| `List` | `recoverListError` 将 panic 转为 error | `list.go` |
| 并发工具 | 恢复（recover）+ 调用栈（stack）→ error | `util/util.go` |
| 观测中间件 | 记录后重新抛出，避免吞掉严重错误 | `middleware/observability.go`（及相关测试） |

### 5.6 查询元信息快照（QueryMeta）

中间件通过 `GetQueryMeta()` 拿到数据源、分页、游标、PIT 标志、开始时间等**只读快照**（切片字段均为拷贝），无需往 context 中塞入私有键（key）。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `core/meta.go`；`builder.GetQueryMeta` | 避免滥用 context 传值（Go 社区惯例）；只读快照防止别名修改 |

### 5.7 测试体系

- 标准库 `testing` + 表驱动测试习惯（各 `*_test.go`）
- **gomock** 生成的泛型 `MockQuerier[R]`（`mock_querier.go`）
- 覆盖克隆（Clone）、游标、中间件、缓存、可观测性、聚合等场景

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `mock_querier.go`；`go.uber.org/mock` | 模拟（Mock）对象模式；[uber-go/mock](https://github.com/uber-go/mock)（原 golang/mock） |

### 5.8 性能与分配意识（小而实用的工程技巧）

| 技巧 | 说明 | 出处 |
| --- | --- | --- |
| 中间件空链短路 | 无中间件时不构建闭包链 | `middleware.go` 的 `buildRunner` |
| `strings.Builder` + `Grow` | 游标 SQL 片段拼接 | `util.BuildString` |
| `PanicToError` 避免 `fmt.Errorf` 反射装箱 | 降低 recover 路径上的分配 | `util/util.go` 注释 |
| 游标字段解析缓存 | GORM 模式（schema）解析结果缓存 | `gorm_builder.go` 字段 `cursorFieldCache` |

### 5.9 资源生命周期（PIT 关闭）

ES PIT 的关闭使用 `context.WithTimeout(context.Background(), ...)`，与请求上下文解耦，防止客户端取消后清理逻辑无法运行。

| 项目出处 | 理论/社区出处 |
| --- | --- |
| `elasticsearch_builder.go`：`closePIT` | `context.WithoutCancel` / Background 清理模式；资源获取 / 释放（acquire / release） |

---

## 6. 附录：技术点速查表

| 类别 | 技术点                               | 主要代码 |
| --- |--------------------------------------| --- |
| 语言 | 自引用泛型 CRTP                      | `builder.go` |
| 语言 | `iter.Seq2` 流式游标                 | `cursor.go` |
| 语言 | 函数选项（Functional Options）       | `option.go` |
| 语言 | 泛型结果抽象                         | `core/result.go` |
| 语言 | 小接口组合                           | `builder.go` `Querier*` |
| 语言 | 结构体嵌入                           | `builder` / `*Builder` / `ESPITPageResult` |
| 语言 | 哨兵错误与错误链                     | `builder.go`、`core/data.go`、`agg/spec.go` |
| 语言 | 位掩码标志（`1 << iota`）            | `agg/spec.go` `PlanFlags` |
| 模式 | 适配器（Adapter）                    | `core/data.go` |
| 模式 | 工厂（Factory）                      | `NewBuilder` |
| 模式 | 模板方法（Template Method）          | `QueryList` + `doQuery` |
| 模式 | 门面（Facade）                       | `list.go` |
| 模式 | 原型 / 克隆（Prototype/Clone）       | 各 `Clone()` |
| 模式 | 中间件 / 责任链（Middleware/CoR）    | `middleware.go` |
| 模式 | 策略 / 作用域（Strategy/Scope）      | `scope.go` |
| 模式 | 建造者（Builder）                    | 各 `*Builder` 与 `agg` 包 |
| 模式 | 注册表（Registry）                   | `core/data.go` |
| 查询 | 游标（Cursor）分页                   | `cursor.go` + 各 builder |
| 查询 | ES 时间点快照（PIT）+ `search_after` | `elasticsearch_builder.go` |
| 查询 | Explain                              | 各 Builder |
| 查询 | 聚合 DSL                             | `agg/*` |
| 工程 | 上下文（Context）/ errgroup          | `util/util.go` |
| 工程 | 可插拔缓存                           | `middleware/cache*.go` |
| 工程 | 可观测抽象                           | `middleware/observability.go` |
| 工程 | Panic → error                        | `list.go`、`util` |
| 工程 | gomock                               | `mock_querier.go` |

---

*本文档根据仓库当前实现整理；若 API 演进，请以源码与 README 为准。*
