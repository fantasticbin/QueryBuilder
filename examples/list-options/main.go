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
	list := builder.NewList[demo.User]()
	list.SetDataSource(builder.Gorm)
	list.SetScope(builder.NewGormScope[demo.User](
		func(db *gorm.DB) *gorm.DB {
			return db.Where("status = ?", 1)
		},
		func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at DESC")
		},
	))

	result, err := list.Query(
		ctx,
		builder.WithData(builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))),
		builder.WithStart(0),
		builder.WithLimit(10),
		builder.WithFields("id", "name"),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("items=%d total=%d\n", len(result.Items), result.Total)
	for _, user := range result.Items {
		fmt.Printf("  id=%d name=%s\n", user.ID, user.Name)
	}
}
