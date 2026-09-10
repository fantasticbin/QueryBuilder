// redis-search 演示 RedisSearchBuilder（Redis Query Engine / FT.SEARCH）的列表查询，
// 重点覆盖 RedisFilter 的五个字段：
//
//	Query          查询表达式 DSL，空串退化为通配符 *
//	Params         参数化查询（配合 Query 中的 $param 占位符，防注入）
//	NumericFilters 数值范围过滤（FT.SEARCH FILTER 子句）
//	InKeys         限定参与检索的文档 Key（INKEYS 子句）
//	InFields       限定参与匹配的字段（INFIELDS 子句）
//
// 真实查询需要 Redis 8+（内置 Query Engine），建议：
//
//	docker run -d -p 6379:6379 redis:8.0
//	go run ./redis-search
//
// Redis 不可达时自动降级，仅演示第 9 节构建期校验链路（无需真实服务）。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/redis/go-redis/v9"
)

const indexName = "idx:qb:users"

// redisUser Redis JSON 文档实体
// json tag 即文档属性名；索引 schema 通过 JSONPath（如 $.name）引用并取别名
type redisUser struct {
	ID        uint32 `json:"id"`
	Name      string `json:"name"`
	City      string `json:"city"`
	Status    string `json:"status"`
	Age       int    `json:"age"`
	CreatedAt int64  `json:"created_at"`
}

func main() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	online := rdb.Ping(probeCtx).Err() == nil
	cancel()

	// RedisAdapter 只要求非 nil 客户端；连接失败推迟到查询期报错
	proxy := builder.NewDBProxyWithAdapters(builder.NewRedisAdapter(rdb))

	if !online {
		fmt.Printf("Redis 不可达（%s）：跳过真实查询，仅演示构建期校验链路。\n", addr)
		fmt.Println("提示：docker run -d -p 6379:6379 redis:8.0 启动后重跑可见完整示例。")
		fmt.Println()
		runValidationDemos(ctx, proxy)
		return
	}

	if err := seed(ctx, rdb); err != nil {
		log.Fatal(err)
	}

	// 1) Query：查询表达式（tag 精确匹配 + 布尔组合）
	fmt.Println("1) Query 查询表达式：@city:{shanghai} @status:{active}，按 age 降序")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetFilter(builder.RedisFilter{Query: "@city:{shanghai} @status:{active}"}).
		SetSort(builder.RedisSort{FieldName: "age", Desc: true}).
		QueryList(ctx))

	// 2) Params：$param 占位符 + 参数表，值不进查询字符串，防注入且免转义
	fmt.Println("2) Params 参数化查询：@city:{$city} @age:[$min $max]（city=shanghai，age∈[22,30]）")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetFilter(builder.RedisFilter{
			Query:  "@city:{$city} @age:[$min $max]",
			Params: map[string]any{"city": "shanghai", "min": 22, "max": 30},
		}).
		SetSort(builder.RedisSort{FieldName: "age", Desc: true}).
		QueryList(ctx))

	// 3) NumericFilters：结构化数值范围（FILTER 子句），与 Query 内的 @age:[a b] 写法二选一
	fmt.Println("3) NumericFilters：@status:{active} + FILTER age [25,35]")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetFilter(builder.RedisFilter{
			Query:          "@status:{active}",
			NumericFilters: []redis.FTSearchFilter{{FieldName: "age", Min: 25, Max: 35}},
		}).
		SetSort(builder.RedisSort{FieldName: "age", Desc: true}).
		QueryList(ctx))

	// 4) InKeys：检索范围限定到指定文档（类似 SQL 的 WHERE id IN (...)）
	fmt.Println("4) InKeys：通配符 * 限定在 user:1 / user:3 内")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetFilter(builder.RedisFilter{Query: "*", InKeys: []any{"user:1", "user:3"}}).
		SetSort(builder.RedisSort{FieldName: "age", Desc: true}).
		QueryList(ctx))

	// 5) InFields：只有 name 字段参与文本匹配
	fmt.Println("5) InFields：文本 alice 仅在 name 字段内匹配")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetFilter(builder.RedisFilter{Query: "alice", InFields: []any{"name"}}).
		SetSort(builder.RedisSort{FieldName: "id", Asc: true}).
		QueryList(ctx))

	// 6) 零值 RedisFilter：Query 空串退化为 *，全量分页 + 字段投影
	//    SetFields 传 JSON 属性名自动补 $. 前缀；需要 JSONPath 级投影（如 $.a[0].b）时改用 SetReturn
	fmt.Println("6) 零值 RedisFilter 退化为 *：created_at 降序取前 2 条，SetFields 投影 name/age")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetFilter(builder.RedisFilter{}).
		SetSort(builder.RedisSort{FieldName: "created_at", Desc: true}).
		SetStart(0).SetLimit(2).SetFields("name", "age").
		QueryList(ctx))

	// 7) 门面模式：RedisFilter 经 NewRedisSearchScope 注入，索引经 WithRedisIndex 指定
	fmt.Println("7) 门面模式：active + FILTER age [20,40]，age 降序取 3 条")
	list := builder.NewList[redisUser]()
	list.SetDataSource(builder.RedisSearch)
	list.SetScope(builder.NewRedisSearchScope[redisUser](
		builder.RedisFilter{
			Query:          "@status:{active}",
			NumericFilters: []redis.FTSearchFilter{{FieldName: "age", Min: 20, Max: 40}},
		},
		builder.RedisSort{FieldName: "age", Desc: true},
	))
	printList(list.Query(ctx,
		builder.WithData(proxy),
		builder.WithRedisIndex(indexName),
		builder.WithStart(0),
		builder.WithLimit(3),
	))

	// 8) Clone：模板 + 分叉；RedisFilter 深拷贝，两个查询互不影响
	fmt.Println("8) Clone：同一模板分叉出 active / inactive 两个查询")
	template := builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetFilter(builder.RedisFilter{Query: "@status:{active}"}).
		SetSort(builder.RedisSort{FieldName: "age", Desc: true})
	inactive := template.Clone().SetFilter(builder.RedisFilter{Query: "@status:{inactive}"})
	printList(template.QueryList(ctx))
	printList(inactive.QueryList(ctx))

	runValidationDemos(ctx, proxy)
}

// runValidationDemos 构建期校验链路：以下错误在连接 Redis 之前返回，离线也能演示
func runValidationDemos(ctx context.Context, proxy *core.DBProxy) {
	fmt.Println("9) 构建期校验（无需 Redis 服务，fail fast）：")

	fmt.Println("   - 缺少排序：无 SORTBY 的分页结果非确定，直接拒绝")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).QueryList(ctx))

	fmt.Println("   - 多字段排序：FT.SEARCH SORTBY 仅支持单属性")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetSort(
			builder.RedisSort{FieldName: "age", Desc: true},
			builder.RedisSort{FieldName: "id", Asc: true},
		).
		QueryList(ctx))

	fmt.Println("   - 排序方向冲突：Asc 与 Desc 互斥")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, indexName).
		SetSort(builder.RedisSort{FieldName: "age", Asc: true, Desc: true}).
		QueryList(ctx))

	fmt.Println("   - 索引未配置")
	printList(builder.NewRedisSearchBuilder[redisUser](proxy, "").
		SetSort(builder.RedisSort{FieldName: "age"}).
		QueryList(ctx))
}

// seed 幂等重建索引并写入示例 JSON 文档
func seed(ctx context.Context, rdb *redis.Client) error {
	// 重建索引（幂等）：ON JSON + PREFIX user:，只索引 user:* 前缀的 JSON 文档
	_ = rdb.FTDropIndex(ctx, indexName).Err()
	err := rdb.FTCreate(ctx, indexName, &redis.FTCreateOptions{
		OnJSON: true,
		Prefix: []any{"user:"},
	},
		&redis.FieldSchema{FieldName: "$.id", As: "id", FieldType: redis.SearchFieldTypeNumeric, Sortable: true},
		&redis.FieldSchema{FieldName: "$.name", As: "name", FieldType: redis.SearchFieldTypeText},
		&redis.FieldSchema{FieldName: "$.city", As: "city", FieldType: redis.SearchFieldTypeTag},
		&redis.FieldSchema{FieldName: "$.status", As: "status", FieldType: redis.SearchFieldTypeTag},
		&redis.FieldSchema{FieldName: "$.age", As: "age", FieldType: redis.SearchFieldTypeNumeric, Sortable: true},
		&redis.FieldSchema{FieldName: "$.created_at", As: "created_at", FieldType: redis.SearchFieldTypeNumeric, Sortable: true},
	).Err()
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	users := []redisUser{
		{ID: 1, Name: "Alice", City: "shanghai", Status: "active", Age: 28, CreatedAt: 1700000100},
		{ID: 2, Name: "Bob", City: "beijing", Status: "active", Age: 35, CreatedAt: 1700000200},
		{ID: 3, Name: "Carol", City: "shanghai", Status: "inactive", Age: 25, CreatedAt: 1700000300},
		{ID: 4, Name: "Dave", City: "shenzhen", Status: "active", Age: 31, CreatedAt: 1700000400},
		{ID: 5, Name: "Eve", City: "shanghai", Status: "active", Age: 22, CreatedAt: 1700000500},
		{ID: 6, Name: "Frank", City: "beijing", Status: "inactive", Age: 40, CreatedAt: 1700000600},
	}
	for i := range users {
		key := fmt.Sprintf("user:%d", users[i].ID)
		if err := rdb.JSONSet(ctx, key, "$", &users[i]).Err(); err != nil {
			return fmt.Errorf("seed %s: %w", key, err)
		}
	}
	return nil
}

// printList 打印查询结果；QueryList / Query 的 (result, err) 可直接作为实参传入
func printList(result *core.ListResult[redisUser], err error) {
	if err != nil {
		fmt.Printf("   -> error: %v\n", err)
		return
	}
	fmt.Printf("   -> items=%d total=%d\n", len(result.Items), result.Total)
	for _, u := range result.Items {
		fmt.Printf("      id=%d name=%-6s city=%-9s status=%-8s age=%d created_at=%d\n",
			u.ID, u.Name, u.City, u.Status, u.Age, u.CreatedAt)
	}
}
