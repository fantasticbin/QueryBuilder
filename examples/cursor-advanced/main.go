package main

import (
	"context"
	"fmt"
	"log"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/core"
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

	fmt.Println("== mixed direction: -created_at, id ==")
	mixed := newActiveCursor(data)
	mixed.SetCursorField("-created_at", "id")
	// 只投影业务字段：cursor 字段（created_at、id）会被自动补进 SELECT，游标推进不缺列
	mixed.SetFields("name")
	sql, err := mixed.Explain(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(sql)
	printCursor("mixed", mixed, ctx)

	// 不调用 SetCursorField 时，执行期自动注入默认唯一 tie-breaker（Gorm 为 id，
	// 见 README「Automatic Unique Tie-Breaker」），因此遍历顺序稳定为 id 升序
	fmt.Println("== auto tie-breaker (no SetCursorField → id) ==")
	auto := newActiveCursor(data)
	printCursor("auto", auto, ctx)

	fmt.Println("== early termination (break after 2) ==")
	early := newActiveCursor(data)
	early.SetCursorField("id")
	n := 0
	for user, err := range early.QueryCursor(ctx) {
		if err != nil {
			log.Fatal(err)
		}
		n++
		fmt.Printf("  id=%d name=%s\n", user.ID, user.Name)
		if n >= 2 {
			break
		}
	}
	fmt.Printf("stopped after %d rows\n", n)
}

func newActiveCursor(data *core.DBProxy) *builder.GormBuilder[demo.User] {
	b := builder.NewGormBuilder[demo.User](data)
	b.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", 1)
	})
	b.SetLimit(10)
	b.SetNeedPagination(false)
	b.SetNeedTotal(false)
	return b
}

func printCursor(label string, b *builder.GormBuilder[demo.User], ctx context.Context) {
	for user, err := range b.QueryCursor(ctx) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  %s id=%d created_at=%d name=%s\n", label, user.ID, user.CreatedAt, user.Name)
	}
}
