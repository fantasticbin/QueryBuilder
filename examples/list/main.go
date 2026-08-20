package main

import (
	"context"
	"fmt"
	"log"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/examples/internal/demo"
	"gorm.io/gorm"
)

func main() {
	db, err := demo.Open()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	b := builder.NewGormBuilder[demo.User](builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db)))
	b.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", 1)
	}).SetSort(func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at DESC")
	})
	b.SetStart(0)
	b.SetLimit(10)
	b.SetNeedTotal(true)
	b.SetNeedPagination(true)
	b.SetFields("id", "name", "status")

	sql, err := b.Explain(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Explain:")
	fmt.Println(sql)

	result, err := b.QueryList(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("items=%d total=%d\n", len(result.Items), result.Total)
	for _, user := range result.Items {
		fmt.Printf("  id=%d name=%s status=%d\n", user.ID, user.Name, user.Status)
	}
}
