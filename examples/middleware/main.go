package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/fantasticbin/QueryBuilder/v2/examples/internal/demo"
	"github.com/fantasticbin/QueryBuilder/v2/middleware"
	"gorm.io/gorm"
)

func main() {
	db, err := demo.Open()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	cache := demo.NewMemoryCache()
	keyBuilder := middleware.DefaultCacheKeyBuilder{
		Prefix: "users",
		Hints: middleware.CacheKeyHints{
			Filter: map[string]any{"status": 1},
			Sort:   map[string]any{"created_at": "desc"},
		},
	}

	b := builder.NewGormBuilder[demo.User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
	b.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", 1)
	})
	b.SetAfterQueryHook(func(_ context.Context, result core.Result[demo.User], err error) {
		if err != nil {
			slog.Error("query failed", "err", err)
			return
		}
		slog.Info("after hook", "items", len(result.GetItems()), "total", result.GetTotal())
	})
	b.Use(middleware.ObservabilityMiddleware[demo.User](middleware.ObservabilityOptions{
		Logger: middleware.QueryLoggerFunc(func(_ context.Context, event middleware.QueryEvent) {
			slog.Info("query event",
				"op", event.Operation,
				"ok", event.Success,
				"items", event.ItemCount,
				"dur", event.Duration,
			)
		}),
	}))
	b.Use(middleware.CacheMiddlewareWithKeyBuilder[demo.User](cache, time.Minute, keyBuilder))

	run := func(label string) {
		result, err := b.QueryList(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s items=%d total=%d\n", label, len(result.Items), result.Total)
	}

	run("first")
	run("second (cache hit)")

	key := keyBuilder.Build(ctx, b.GetQueryMeta())
	if err := middleware.DeleteCache(ctx, cache, key); err != nil {
		log.Fatal(err)
	}
	fmt.Println("deleted cache key", key)
}
