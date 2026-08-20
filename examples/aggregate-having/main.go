package main

import (
	"context"
	"fmt"
	"log"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/examples/internal/demo"
)

func main() {
	db, err := demo.Open()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	data := builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))

	fmt.Println("== CountIf / SumIf / Having / OrderByDesc ==")
	paid := agg.NewGormBuilder[demo.Order, demo.RegionPaidSummary](data)
	paid.GroupBy("region", "region").
		Count("total").
		CountIf("paid_total", "status = ?", "paid").
		SumIf("amount", "paid_amount", "status = ?", "paid").
		Having("paid_amount >= ?", 20).
		OrderByDesc("paid_amount").
		SetLimit(20)
	sql, err := paid.Explain(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(sql)
	result, err := paid.Query(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("groups=%d (HAVING paid_amount >= 20)\n", result.Total)
	for _, row := range result.Rows {
		fmt.Printf("  region=%s total=%d paid=%d amount=%.1f\n",
			row.Region, row.Total, row.PaidTotal, row.PaidAmount)
	}

	fmt.Println("== GroupByDate ==")
	byDay := agg.NewGormBuilder[demo.Order, demo.DaySummary](data)
	byDay.GroupByDate("created_at", "created_day", agg.TimeIntervalDay).
		Count("total").
		SetLimit(10)
	daySQL, err := byDay.Explain(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(daySQL)
	days, err := byDay.Query(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, row := range days.Rows {
		fmt.Printf("  day=%s total=%d\n", row.Day, row.Total)
	}
}
