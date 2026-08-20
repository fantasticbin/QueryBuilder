package main

import (
	"context"
	"fmt"
	"log"
	"sync"

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

	// 先把公共配置写在模板上，再 Clone；不要在多个 goroutine 里对同一实例 Set*
	// offset 分页必须带确定性 ORDER BY，否则 LIMIT/OFFSET 结果不保证稳定
	base := builder.NewGormBuilder[demo.User](data)
	base.SetFields("id", "name", "status").SetNeedTotal(true).SetLimit(10)
	base.SetSort(func(db *gorm.DB) *gorm.DB {
		return db.Order("id ASC")
	})

	fmt.Println("== Clone with different filters ==")
	type fork struct {
		name   string
		status int
	}
	forks := []fork{{"active", 1}, {"inactive", 0}}
	results := make([]string, len(forks))
	var wg sync.WaitGroup
	for i, f := range forks {
		wg.Add(1)
		go func(idx int, f fork) {
			defer wg.Done()
			q := base.Clone().SetFilter(func(db *gorm.DB) *gorm.DB {
				return db.Where("status = ?", f.status)
			})
			result, err := q.QueryList(ctx)
			if err != nil {
				log.Printf("%s: %v", f.name, err)
				return
			}
			results[idx] = fmt.Sprintf("%s items=%d total=%d", f.name, len(result.Items), result.Total)
		}(i, f)
	}
	wg.Wait()
	for _, line := range results {
		fmt.Println(" ", line)
	}

	fmt.Println("== Clone with different pages ==")
	page1 := base.Clone()
	page1.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", 1)
	})
	page1.SetStart(0)
	page1.SetLimit(2)
	page2 := base.Clone()
	page2.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", 1)
	})
	page2.SetStart(2)
	page2.SetLimit(2)
	printPage("page1", page1, ctx)
	printPage("page2", page2, ctx)
}

func printPage(label string, q *builder.GormBuilder[demo.User], ctx context.Context) {
	result, err := q.QueryList(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("  %s items=%d total=%d\n", label, len(result.Items), result.Total)
	for _, user := range result.Items {
		fmt.Printf("    id=%d name=%s\n", user.ID, user.Name)
	}
}
