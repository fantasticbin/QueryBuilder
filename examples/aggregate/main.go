package main

import (
	"context"
	"fmt"
	"log"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/examples/internal/demo"
	"gorm.io/gorm"
)

func main() {
	db, err := demo.Open()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	data := builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))
	query := agg.NewGormBuilder[demo.Order, demo.SalesSummary](data)
	query.GroupBy("region", "region").
		Count("order_count").
		CountDistinct("customer_id", "buyer_count").
		Sum("amount", "amount_sum").
		SumDistinct("amount", "unique_amount_sum").
		SetStart(0).
		SetLimit(100)
	query.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", "paid")
	})

	sql, err := query.Explain(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Explain:")
	fmt.Println(sql)

	result, err := query.Query(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("groups=%d\n", result.Total)
	for _, row := range result.Rows {
		fmt.Printf("  region=%s orders=%d buyers=%d unique_sum=%.1f sum=%.1f\n",
			row.Region, row.Count, row.BuyerCount, row.UniqueAmountSum, row.Amount)
	}

	fmt.Println("AnalyzeSpec notes:")
	plan := agg.AnalyzeSpec(builder.Gorm, query.Meta().Spec)
	if len(plan.Notes) == 0 {
		fmt.Println("  (none)")
	}
	for _, note := range plan.Notes {
		fmt.Println(" ", note)
	}
}
