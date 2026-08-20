package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/agg"
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
	keyBuilder := middleware.AggregateDefaultCacheKeyBuilder{
		Prefix: "orders",
		Hints:  middleware.CacheKeyHints{Filter: map[string]any{"status": "paid"}},
	}

	query := agg.NewGormBuilder[demo.Order, demo.SalesSummary](
		builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)),
	)
	query.GroupBy("region", "region").
		Count("order_count").
		Sum("amount", "amount_sum").
		SetLimit(100)
	query.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", "paid")
	})
	query.Use(middleware.AggregateObservabilityMiddleware[demo.SalesSummary](middleware.AggregateObservabilityOptions{
		Logger: middleware.AggregateLoggerFunc(func(_ context.Context, event middleware.AggregateEvent) {
			slog.Info("aggregate event",
				"op", event.Operation,
				"ok", event.Success,
				"rows", event.RowCount,
				"dur", event.Duration,
			)
		}),
	}))
	query.Use(middleware.AggregateCacheMiddlewareWithKeyBuilder[demo.SalesSummary](cache, time.Minute, keyBuilder))

	run := func(label string) {
		result, err := query.Query(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s groups=%d\n", label, result.Total)
		for _, row := range result.Rows {
			fmt.Printf("  region=%s orders=%d sum=%.1f\n", row.Region, row.Count, row.Amount)
		}
	}

	run("first")
	run("second (cache hit)")

	key := keyBuilder.Build(ctx, query.Meta())
	if err := middleware.DeleteCache(ctx, cache, key); err != nil {
		log.Fatal(err)
	}
	fmt.Println("deleted cache key", key)
}
