# QueryBuilder examples

Minimal programs that follow the README. Most use an in-memory SQLite database (pure Go, no CGO, no extra services); `redis-search` is the only one that needs a live Redis for full output.

Requires Go 1.26+.

```shell
cd examples
go run ./list
go run ./list-options
go run ./clone
go run ./cursor
go run ./cursor-advanced
go run ./aggregate
go run ./aggregate-having
go run ./aggregate-middleware
go run ./middleware
go run ./explain
go run ./redis-search
```

| Directory | README section | What it does |
| --- | --- | --- |
| `list` | Quick Start / Direct Builder | `GormBuilder` list query, field projection, `Explain` |
| `list-options` | List with Options Pattern | `NewList` + `NewGormScope` + `WithStart` / `WithLimit` |
| `clone` | Clone (Concurrent Forking) | template + Clone; concurrent filters; independent pages |
| `cursor` | Cursor Pagination | `QueryCursor` stream and `QueryPage` load-more |
| `cursor-advanced` | mixed direction / tie-breaker / early `break` | `-created_at,id`, auto `id`, stop after 2 rows |
| `aggregate` | Aggregate Statistics | grouped `Count` / `CountDistinct` / `Sum` |
| `aggregate-having` | Conditional Metrics, HAVING, `GroupByDate` | `CountIf` / `SumIf` / `Having` / `OrderByDesc` |
| `aggregate-middleware` | Aggregate cache + observability | `AggregateCacheMiddleware` hit + slog |
| `middleware` | Cache / Observability | in-memory cache, slog events, `DeleteCache` |
| `explain` | Dry Run / Explain + SetSpec | same `agg.Spec` on GORM, MongoDB, ElasticSearch |
| `redis-search` | RedisSearch Builder / `RedisFilter` | `RedisSearchBuilder` list query covering all `RedisFilter` fields; facade `NewRedisSearchScope`; `Clone`; build-time validation |

`explain` only calls `Explain` for MongoDB and ElasticSearch (no live cluster). GORM examples execute real queries against SQLite.

`redis-search` executes real `FT.SEARCH` queries and needs Redis 8+ with the Query Engine:

```shell
docker run -d -p 6379:6379 redis:8.0
REDIS_ADDR=localhost:6379 go run ./redis-search   # REDIS_ADDR 缺省为 localhost:6379
```

Without a reachable Redis the example degrades to the build-time validation demos (sort / index constraints), which need no server.

Seeded data lives in `internal/demo`; the Redis example seeds its own JSON documents under `user:*`.
