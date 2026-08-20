# QueryBuilder examples

Minimal programs that follow the README. They use an in-memory SQLite database (pure Go, no CGO, no extra services).

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

`explain` only calls `Explain` for MongoDB and ElasticSearch (no live cluster). GORM examples execute real queries against SQLite.

Seeded data lives in `internal/demo`.
