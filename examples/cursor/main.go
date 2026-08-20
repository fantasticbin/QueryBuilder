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
	data := builder.NewDBProxyWithAdapters(builder.NewGormAdapter(db))

	fmt.Println("== QueryCursor ==")
	stream := builder.NewGormBuilder[demo.User](data)
	stream.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", 1)
	})
	stream.SetCursorField("id")
	stream.SetLimit(2)
	// 全量流式：关掉分页才会一批接一批直到结束；不需要 COUNT
	stream.SetNeedPagination(false)
	stream.SetNeedTotal(false)

	n := 0
	for user, err := range stream.QueryCursor(ctx) {
		if err != nil {
			log.Fatal(err)
		}
		n++
		fmt.Printf("  stream id=%d name=%s\n", user.ID, user.Name)
	}
	fmt.Printf("streamed %d rows\n", n)

	fmt.Println("== QueryPage ==")
	pageBuilder := builder.NewGormBuilder[demo.User](data)
	pageBuilder.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", 1)
	})
	pageBuilder.SetCursorField("id")
	pageBuilder.SetLimit(2)
	// 单页加载更多：每次 QueryPage 只取一页，用 HasMore 而不是 Total
	pageBuilder.SetNeedPagination(true)
	pageBuilder.SetNeedTotal(false)

	pageNo := 1
	for {
		page, err := pageBuilder.QueryPage(ctx)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("page %d items=%d has_more=%v\n", pageNo, len(page.Items), page.HasMore)
		for _, user := range page.Items {
			fmt.Printf("  id=%d name=%s\n", user.ID, user.Name)
		}
		if !page.HasMore {
			break
		}
		pageBuilder.SetCursorValue(page.NextCursorValues...)
		pageNo++
	}
}
