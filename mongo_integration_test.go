//go:build integration

// MongoDB 常规查询（不含 agg）真实集成测试，需要可访问的 MongoDB 实例
// 未设置 MONGO_URI 时自动跳过；每次运行创建独立库/集合并在结束后删除
// 运行: MONGO_URI=mongodb://127.0.0.1:27017 go test -tags integration -race -v -run TestMongoIntegration .
package builder_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type mongoDoc struct {
	ID        int      `bson:"id"`
	Name      string   `bson:"name"`
	City      string   `bson:"city"`
	Age       int      `bson:"age"`
	Status    string   `bson:"status"`
	Tags      []string `bson:"tags,omitempty"`
	Bio       string   `bson:"bio"`
	Email     string   `bson:"email,omitempty"`
	Score     float64  `bson:"score"`
	CreatedAt string   `bson:"created_at"`
}

const mongoTotalDocs = 25

var mongoCities = []string{"beijing", "shanghai", "shenzhen"}

func makeMongoDocs() []mongoDoc {
	docs := make([]mongoDoc, 0, mongoTotalDocs)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= mongoTotalDocs; i++ {
		d := mongoDoc{
			ID:        i,
			Name:      fmt.Sprintf("user%02d", i),
			City:      mongoCities[i%3],
			Age:       18 + i,
			Status:    map[bool]string{true: "active", false: "inactive"}[i%2 == 1],
			Bio:       "plain user profile",
			Score:     float64(i) * 1.5,
			CreatedAt: base.AddDate(0, 0, i).Format(time.RFC3339),
		}
		switch i % 4 {
		case 0:
			d.Tags = []string{"vip", "go"}
		case 1:
			d.Tags = []string{"go"}
		}
		if i%5 == 0 {
			d.Bio = "loves mongodb and golang"
		}
		if i%3 == 0 {
			d.Email = fmt.Sprintf("user%02d@example.com", i)
		}
		docs = append(docs, d)
	}
	return docs
}

type mongoEnv struct {
	client *mongo.Client
	coll   *mongo.Collection
	data   *core.DBProxy
	dbName string
	docs   []mongoDoc
}

func setupMongo(t *testing.T) *mongoEnv {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		t.Skip("MONGO_URI not set, skipping mongodb integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Skipf("mongodb not available at %s: %v", uri, err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		t.Skipf("mongodb ping failed: %v", err)
	}

	dbName := fmt.Sprintf("qb_it_%d", time.Now().UnixNano())
	collName := "docs"
	coll := client.Database(dbName).Collection(collName)
	t.Cleanup(func() {
		_ = client.Database(dbName).Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})

	docs := makeMongoDocs()
	raw := make([]any, len(docs))
	for i := range docs {
		raw[i] = docs[i]
	}
	if _, err := coll.InsertMany(ctx, raw); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}
	// 便于按 id 排序的稳定索引
	_, _ = coll.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "id", Value: 1}}})

	return &mongoEnv{
		client: client,
		coll:   coll,
		data:   builder.NewDBProxyWithAdapters(builder.NewMongoAdapter(coll)),
		dbName: dbName,
		docs:   docs,
	}
}

func (e *mongoEnv) b() *builder.MongoBuilder[mongoDoc] {
	return builder.NewMongoBuilder[mongoDoc](e.data)
}

func (e *mongoEnv) expectIDs(pred func(mongoDoc) bool) []int {
	var ids []int
	for _, d := range e.docs {
		if pred(d) {
			ids = append(ids, d.ID)
		}
	}
	return ids
}

func (e *mongoEnv) doc(id int) mongoDoc { return e.docs[id-1] }

func mongoIDs(items []*mongoDoc) []int {
	out := make([]int, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func assertMongoIDs(t *testing.T, got []*mongoDoc, want []int) {
	t.Helper()
	g := mongoIDs(got)
	if !slices.Equal(g, want) {
		t.Fatalf("ids mismatch\n want=%v\n  got=%v", want, g)
	}
}

func assertMongoTotal(t *testing.T, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("total mismatch want=%d got=%d", want, got)
	}
}

func mongoIDRange(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func byMongoIDAsc() builder.MongoSort { return bson.D{{Key: "id", Value: 1}} }

func equalMongoDoc(a, b mongoDoc) bool {
	return a.ID == b.ID && a.Name == b.Name && a.City == b.City && a.Age == b.Age && a.Status == b.Status &&
		slices.Equal(a.Tags, b.Tags) && a.Bio == b.Bio && a.Email == b.Email && a.Score == b.Score && a.CreatedAt == b.CreatedAt
}

// cursorValAsInt 将 Mongo NextCursorValues 中的数值统一转成 int，便于断言
func cursorValAsInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case bson.D: // 不应出现
		return 0, false
	default:
		return 0, false
	}
}

func TestMongoIntegration(t *testing.T) {
	env := setupMongo(t)
	ctx := context.Background()

	// ---------------- QueryList: 过滤条件 ----------------
	listCases := []struct {
		name   string
		filter bson.D
		pred   func(mongoDoc) bool
	}{
		{"MatchAll_nil", nil, func(mongoDoc) bool { return true }},
		{"MatchAll_empty", bson.D{}, func(mongoDoc) bool { return true }},
		{"Eq", bson.D{{Key: "city", Value: "beijing"}}, func(d mongoDoc) bool { return d.City == "beijing" }},
		{"In", bson.D{{Key: "city", Value: bson.D{{Key: "$in", Value: bson.A{"shanghai", "shenzhen"}}}}},
			func(d mongoDoc) bool { return d.City != "beijing" }},
		{"Ne", bson.D{{Key: "status", Value: bson.D{{Key: "$ne", Value: "active"}}}},
			func(d mongoDoc) bool { return d.Status != "active" }},
		{"Range_int", bson.D{{Key: "age", Value: bson.D{{Key: "$gte", Value: 30}, {Key: "$lt", Value: 40}}}},
			func(d mongoDoc) bool { return d.Age >= 30 && d.Age < 40 }},
		{"Range_date", bson.D{{Key: "created_at", Value: bson.D{
			{Key: "$gte", Value: "2026-01-10T00:00:00Z"}, {Key: "$lte", Value: "2026-01-15T00:00:00Z"},
		}}}, func(d mongoDoc) bool { return d.ID >= 9 && d.ID <= 14 }},
		{"Range_double", bson.D{{Key: "score", Value: bson.D{{Key: "$gt", Value: 30.0}}}},
			func(d mongoDoc) bool { return d.Score > 30 }},
		{"And", bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "age", Value: bson.D{{Key: "$gte", Value: 25}}}},
			bson.D{{Key: "status", Value: "active"}},
			bson.D{{Key: "city", Value: bson.D{{Key: "$ne", Value: "shanghai"}}}},
		}}}, func(d mongoDoc) bool { return d.Age >= 25 && d.Status == "active" && d.City != "shanghai" }},
		{"Or", bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "tags", Value: "vip"}},
			bson.D{{Key: "city", Value: "beijing"}},
		}}}, func(d mongoDoc) bool { return slices.Contains(d.Tags, "vip") || d.City == "beijing" }},
		{"Nested_and_or", bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "$and", Value: bson.A{
				bson.D{{Key: "status", Value: "inactive"}},
				bson.D{{Key: "tags", Value: "vip"}},
			}}},
			bson.D{{Key: "age", Value: bson.D{{Key: "$lte", Value: 20}}}},
		}}}, func(d mongoDoc) bool {
			return (d.Status == "inactive" && slices.Contains(d.Tags, "vip")) || d.Age <= 20
		}},
		{"Exists_email", bson.D{{Key: "email", Value: bson.D{{Key: "$exists", Value: true}}}},
			func(d mongoDoc) bool { return d.Email != "" }},
		{"Exists_false_tags", bson.D{{Key: "tags", Value: bson.D{{Key: "$exists", Value: false}}}},
			func(d mongoDoc) bool { return len(d.Tags) == 0 }},
		{"Regex_prefix", bson.D{{Key: "name", Value: bson.D{{Key: "$regex", Value: "^user1"}}}},
			func(d mongoDoc) bool { return strings.HasPrefix(d.Name, "user1") }},
		{"ElemMatch_tags", bson.D{{Key: "tags", Value: bson.D{{Key: "$all", Value: bson.A{"vip", "go"}}}}},
			func(d mongoDoc) bool { return slices.Contains(d.Tags, "vip") && slices.Contains(d.Tags, "go") }},
		{"Nin", bson.D{{Key: "city", Value: bson.D{{Key: "$nin", Value: bson.A{"beijing", "shanghai"}}}}},
			func(d mongoDoc) bool { return d.City == "shenzhen" }},
		{"Textish_regex_bio", bson.D{{Key: "bio", Value: bson.D{{Key: "$regex", Value: "mongodb"}}}},
			func(d mongoDoc) bool { return strings.Contains(d.Bio, "mongodb") }},
	}
	for _, tc := range listCases {
		t.Run("List/"+tc.name, func(t *testing.T) {
			b := env.b()
			if tc.filter != nil {
				b.SetFilter(tc.filter)
			}
			b.SetSort(byMongoIDAsc()).SetLimit(100)
			res, err := b.QueryList(ctx)
			if err != nil {
				t.Fatalf("QueryList: %v", err)
			}
			want := env.expectIDs(tc.pred)
			assertMongoIDs(t, res.Items, want)
			assertMongoTotal(t, res.Total, int64(len(want)))
			for _, it := range res.Items {
				if exp := env.doc(it.ID); !equalMongoDoc(*it, exp) {
					t.Fatalf("doc %d mismatch want=%+v got=%+v", it.ID, exp, *it)
				}
			}
		})
	}

	// ---------------- 排序 ----------------
	t.Run("Sort/id_desc", func(t *testing.T) {
		res, err := env.b().SetSort(bson.D{{Key: "id", Value: -1}}).SetLimit(5).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, []int{25, 24, 23, 22, 21})
		assertMongoTotal(t, res.Total, 25)
	})
	t.Run("Sort/multi_city_asc_age_desc", func(t *testing.T) {
		res, err := env.b().SetSort(bson.D{{Key: "city", Value: 1}, {Key: "age", Value: -1}}).SetLimit(100).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exp := slices.Clone(env.docs)
		sort.SliceStable(exp, func(i, j int) bool {
			if exp[i].City != exp[j].City {
				return exp[i].City < exp[j].City
			}
			return exp[i].Age > exp[j].Age
		})
		var want []int
		for _, d := range exp {
			want = append(want, d.ID)
		}
		assertMongoIDs(t, res.Items, want)
	})
	t.Run("Sort/date_desc", func(t *testing.T) {
		res, err := env.b().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(3).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, []int{25, 24, 23})
	})

	// ---------------- 分页 / 总数 ----------------
	t.Run("Page/default_limit_10", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, mongoIDRange(1, 10))
		assertMongoTotal(t, res.Total, 25)
	})
	t.Run("Page/from10_size5", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetStart(10).SetLimit(5).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, mongoIDRange(11, 15))
		assertMongoTotal(t, res.Total, 25)
	})
	t.Run("Page/last_partial_page", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetStart(20).SetLimit(10).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, mongoIDRange(21, 25))
	})
	t.Run("Page/start_beyond_total", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetStart(100).SetLimit(10).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, nil)
		assertMongoTotal(t, res.Total, 25)
	})
	t.Run("Page/limit0_uses_default", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetLimit(0).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, mongoIDRange(1, 10))
	})
	t.Run("Page/NeedPagination_false_returns_all", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetNeedPagination(false).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoTotal(t, res.Total, 25)
		assertMongoIDs(t, res.Items, mongoIDRange(1, 25))
	})
	t.Run("Total/NeedTotal_false", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetLimit(5).SetNeedTotal(false).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, mongoIDRange(1, 5))
		assertMongoTotal(t, res.Total, 0)
	})
	t.Run("Total/TotalLimit_capped", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetLimit(3).SetTotalLimit(7).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, mongoIDRange(1, 3))
		assertMongoTotal(t, res.Total, 7)
	})
	t.Run("Total/TotalLimit_above_actual", func(t *testing.T) {
		res, err := env.b().SetFilter(bson.D{{Key: "city", Value: "beijing"}}).SetTotalLimit(1000).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoTotal(t, res.Total, 8)
	})
	t.Run("Empty/no_match", func(t *testing.T) {
		res, err := env.b().SetFilter(bson.D{{Key: "city", Value: "nowhere"}}).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, nil)
		assertMongoTotal(t, res.Total, 0)
	})

	// ---------------- 字段投影 ----------------
	t.Run("Fields/id_name_only", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetLimit(3).SetFields("id", "name").QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range res.Items {
			want := mongoDoc{ID: it.ID, Name: env.doc(it.ID).Name}
			if !equalMongoDoc(*it, want) {
				t.Fatalf("projection leaked fields: %+v", *it)
			}
		}
		assertMongoIDs(t, res.Items, []int{1, 2, 3})
	})
	t.Run("Fields/dedupe_and_empty", func(t *testing.T) {
		res, err := env.b().SetSort(byMongoIDAsc()).SetLimit(1).SetFields("city", "", "city", "id").QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := *res.Items[0]; got.ID != 1 || got.City != env.doc(1).City || got.Name != "" {
			t.Fatalf("unexpected projection %+v", got)
		}
	})
	t.Run("Fields/invalid_name_rejected", func(t *testing.T) {
		_, err := env.b().SetFields("name; drop").QueryList(ctx)
		if !errors.Is(err, builder.ErrInvalidField) {
			t.Fatalf("want ErrInvalidField got %v", err)
		}
	})

	// ---------------- 错误与边界 ----------------
	t.Run("Err/limit_exceeded", func(t *testing.T) {
		_, err := env.b().SetLimit(5001).QueryList(ctx)
		if !errors.Is(err, builder.ErrLimitExceeded) {
			t.Fatalf("want ErrLimitExceeded got %v", err)
		}
	})
	t.Run("Err/missing_collection_config", func(t *testing.T) {
		_, err := builder.NewMongoBuilder[mongoDoc](builder.NewDBProxyWithAdapters()).QueryList(ctx)
		if !errors.Is(err, builder.ErrDataNotConfigured) {
			t.Fatalf("want ErrDataNotConfigured got %v", err)
		}
		_, err = builder.NewMongoBuilder[mongoDoc](builder.NewDBProxy(nil, nil, nil)).QueryList(ctx)
		if !errors.Is(err, builder.ErrDataNotConfigured) {
			t.Fatalf("want ErrDataNotConfigured got %v", err)
		}
	})
	t.Run("Err/bad_filter_unknown_operator", func(t *testing.T) {
		_, err := env.b().SetFilter(bson.D{{Key: "age", Value: bson.D{{Key: "$notARealOp", Value: 1}}}}).QueryList(ctx)
		if err == nil {
			t.Fatal("expected mongo filter error")
		}
	})
	t.Run("Err/context_canceled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := env.b().QueryList(cctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled got %v", err)
		}
	})
	t.Run("Err/cursor_value_count_mismatch", func(t *testing.T) {
		b := env.b().SetSort(bson.D{{Key: "age", Value: 1}})
		b.SetCursorField("id").SetCursorValue(1, 2, 3)
		_, err := b.QueryPage(ctx)
		if !errors.Is(err, builder.ErrCursorMismatch) {
			t.Fatalf("want ErrCursorMismatch got %v", err)
		}
	})

	// ---------------- List / Scope / Option API ----------------
	t.Run("ListAPI/Scope_and_options", func(t *testing.T) {
		l := builder.NewList[mongoDoc]().SetDataSource(builder.MongoDB).
			SetScope(builder.NewMongoScope[mongoDoc](
				bson.D{{Key: "status", Value: "active"}},
				bson.D{{Key: "id", Value: -1}},
			))
		res, err := l.Query(ctx,
			builder.WithData(env.data),
			builder.WithStart(2), builder.WithLimit(3), builder.WithFields("id", "status"))
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, []int{21, 19, 17})
		assertMongoTotal(t, res.Total, 13)
		if res.Items[0].Name != "" || res.Items[0].Status != "active" {
			t.Fatalf("projection via WithFields not applied: %+v", *res.Items[0])
		}
		meta := l.GetQueryMeta()
		if meta.DataSource != builder.MongoDB || meta.Start != 2 || meta.Limit != 3 {
			t.Fatalf("unexpected meta %+v", meta)
		}
	})
	t.Run("ListAPI/NewListWithData_missing_collection", func(t *testing.T) {
		l := builder.NewListWithData[mongoDoc](builder.MongoDB, builder.NewDBProxyWithAdapters())
		_, err := l.Query(ctx)
		if !errors.Is(err, builder.ErrDataNotConfigured) {
			t.Fatalf("want ErrDataNotConfigured got %v", err)
		}
	})
	t.Run("Hooks_and_Middleware", func(t *testing.T) {
		var before, after, mw int
		var hookTotal int64
		b := env.b().SetFilter(bson.D{{Key: "city", Value: "shenzhen"}}).SetSort(byMongoIDAsc())
		b.SetBeforeQueryHook(func(c context.Context) context.Context { before++; return c })
		b.SetAfterQueryHook(func(c context.Context, r core.Result[mongoDoc], err error) { after++; hookTotal = r.GetTotal() })
		b.Use(func(c context.Context, q builder.Querier[mongoDoc], next func(context.Context) (core.Result[mongoDoc], error)) (core.Result[mongoDoc], error) {
			mw++
			if q.GetQueryMeta().DataSource != builder.MongoDB {
				t.Errorf("bad meta in middleware")
			}
			return next(c)
		})
		res, err := b.QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := env.expectIDs(func(d mongoDoc) bool { return d.City == "shenzhen" })
		assertMongoIDs(t, res.Items, want)
		if before != 1 || after != 1 || mw != 1 || hookTotal != int64(len(want)) {
			t.Fatalf("hooks before=%d after=%d mw=%d hookTotal=%d", before, after, mw, hookTotal)
		}
	})
	t.Run("Clone_concurrent", func(t *testing.T) {
		base := env.b()
		base.SetSort(byMongoIDAsc()).SetLimit(100)
		var wg sync.WaitGroup
		errs := make([]error, len(mongoCities))
		for i, city := range mongoCities {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := base.Clone().SetFilter(bson.D{{Key: "city", Value: city}})
				res, err := c.QueryList(ctx)
				if err != nil {
					errs[i] = err
					return
				}
				want := env.expectIDs(func(d mongoDoc) bool { return d.City == city })
				if !slices.Equal(mongoIDs(res.Items), want) || res.Total != int64(len(want)) {
					errs[i] = fmt.Errorf("city %s want %v got %v total %d", city, want, mongoIDs(res.Items), res.Total)
				}
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Explain_DSL", func(t *testing.T) {
		dsl, err := env.b().SetFilter(bson.D{{Key: "city", Value: "beijing"}}).
			SetSort(byMongoIDAsc()).SetStart(5).SetLimit(2).Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{`"city"`, `"beijing"`, `"skip"`, `"limit"`, `"sort"`} {
			if !strings.Contains(dsl, s) {
				t.Fatalf("explain missing %s:\n%s", s, dsl)
			}
		}
		if !strings.Contains(dsl, "5") || !strings.Contains(dsl, "2") {
			t.Fatalf("explain missing skip/limit values:\n%s", dsl)
		}
	})
	t.Run("Explain_cursor_mode", func(t *testing.T) {
		dsl, err := env.b().SetCursorField("-created_at", "id").SetLimit(3).Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsl, `"mode": "cursor"`) || !strings.Contains(dsl, "cursor_fields") {
			t.Fatalf("unexpected cursor explain: %s", dsl)
		}
	})

	// ---------------- 游标 QueryCursor ----------------
	t.Run("Cursor/single_batch_by_id", func(t *testing.T) {
		var got []*mongoDoc
		for d, err := range env.b().SetCursorField("id").SetLimit(4).QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertMongoIDs(t, got, mongoIDRange(1, 4))
	})
	t.Run("Cursor/single_batch_with_cursor_value", func(t *testing.T) {
		var got []*mongoDoc
		for d, err := range env.b().SetCursorField("id").SetCursorValue(5).SetLimit(3).QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertMongoIDs(t, got, []int{6, 7, 8})
	})
	t.Run("Cursor/full_traversal_by_id", func(t *testing.T) {
		var got []*mongoDoc
		b := env.b().SetFilter(bson.D{{Key: "age", Value: bson.D{{Key: "$gt", Value: 20}}}})
		b.SetCursorField("id").SetLimit(4).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertMongoIDs(t, got, env.expectIDs(func(d mongoDoc) bool { return d.Age > 20 }))
	})
	t.Run("Cursor/full_traversal_desc", func(t *testing.T) {
		var got []*mongoDoc
		b := env.b()
		b.SetCursorField("-id").SetLimit(6).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		want := mongoIDRange(1, 25)
		slices.Reverse(want)
		assertMongoIDs(t, got, want)
	})
	t.Run("Cursor/full_traversal_default_oid", func(t *testing.T) {
		// 未设 cursor field 时 Mongo 默认 _id；只校验拿到全部 25 条且 id 唯一
		var got []*mongoDoc
		b := env.b()
		b.SetLimit(7).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		g := mongoIDs(got)
		sort.Ints(g)
		if !slices.Equal(g, mongoIDRange(1, 25)) {
			t.Fatalf("want all 25 unique docs, got %v", mongoIDs(got))
		}
	})
	t.Run("Cursor/early_break", func(t *testing.T) {
		n := 0
		b := env.b()
		b.SetCursorField("id").SetLimit(5).SetNeedPagination(false)
		for _, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			n++
			if n == 7 {
				break
			}
		}
		if n != 7 {
			t.Fatalf("n=%d", n)
		}
	})
	t.Run("Cursor/ListAPI_full_traversal", func(t *testing.T) {
		l := builder.NewList[mongoDoc]().SetDataSource(builder.MongoDB).
			SetScope(builder.NewMongoScope[mongoDoc](bson.D{{Key: "city", Value: "shanghai"}}, nil))
		var got []*mongoDoc
		for d, err := range l.QueryCursor(ctx, builder.WithData(env.data),
			builder.WithCursorField("id"), builder.WithLimit(3), builder.WithNeedPagination(false), builder.WithNeedTotal(false)) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertMongoIDs(t, got, env.expectIDs(func(d mongoDoc) bool { return d.City == "shanghai" }))
	})
	t.Run("Cursor/multi_field_compound", func(t *testing.T) {
		var got []*mongoDoc
		b := env.b()
		b.SetCursorField("city", "id").SetLimit(5).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		exp := slices.Clone(env.docs)
		sort.SliceStable(exp, func(i, j int) bool {
			if exp[i].City != exp[j].City {
				return exp[i].City < exp[j].City
			}
			return exp[i].ID < exp[j].ID
		})
		var want []int
		for _, d := range exp {
			want = append(want, d.ID)
		}
		assertMongoIDs(t, got, want)
	})

	// ---------------- QueryPage 往返 ----------------
	t.Run("QueryPage/same_builder_loop", func(t *testing.T) {
		b := env.b()
		b.SetCursorField("id").SetLimit(10)
		var all []*mongoDoc
		for page := 0; page < 10; page++ {
			res, err := b.QueryPage(ctx)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			t.Logf("page %d ids=%v hasMore=%v next=%v total=%d", page, mongoIDs(res.Items), res.HasMore, res.NextCursorValues, res.Total)
			if page == 0 {
				assertMongoTotal(t, res.Total, 25)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				if res.NextCursorValues != nil {
					t.Fatalf("NextCursorValues should be nil when !HasMore")
				}
				break
			}
			b.SetCursorValue(res.NextCursorValues...)
		}
		assertMongoIDs(t, all, mongoIDRange(1, 25))
	})
	t.Run("QueryPage/ListAPI_cross_request", func(t *testing.T) {
		l := builder.NewList[mongoDoc]().SetDataSource(builder.MongoDB)
		var all []*mongoDoc
		var cursor []any
		for page := 0; page < 10; page++ {
			opts := []builder.QueryOption{builder.WithData(env.data),
				builder.WithCursorField("id"), builder.WithLimit(10)}
			if cursor != nil {
				opts = append(opts, builder.WithCursorValue(cursor...))
			}
			res, err := l.QueryPage(ctx, opts...)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			t.Logf("page %d ids=%v hasMore=%v next=%v", page, mongoIDs(res.Items), res.HasMore, res.NextCursorValues)
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			cursor = res.NextCursorValues
		}
		assertMongoIDs(t, all, mongoIDRange(1, 25))
	})
	t.Run("QueryPage/start_as_cursor", func(t *testing.T) {
		b := env.b()
		b.SetCursorField("id").SetStart(20).SetLimit(3)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, []int{21, 22, 23})
		if !res.HasMore {
			t.Fatal("expected HasMore")
		}
	})
	t.Run("QueryPage/exact_limit_no_false_hasMore", func(t *testing.T) {
		b := env.b().SetFilter(bson.D{{Key: "city", Value: "beijing"}})
		b.SetCursorField("id").SetLimit(8)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, env.expectIDs(func(d mongoDoc) bool { return d.City == "beijing" }))
		if res.HasMore || res.NextCursorValues != nil {
			t.Fatalf("HasMore should be false with nil cursor, got %v %v", res.HasMore, res.NextCursorValues)
		}
	})
	t.Run("QueryPage/desc_roundtrip", func(t *testing.T) {
		b := env.b()
		b.SetCursorField("-id").SetLimit(7)
		var all []*mongoDoc
		for page := 0; page < 10; page++ {
			res, err := b.QueryPage(ctx)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			b.SetCursorValue(res.NextCursorValues...)
		}
		want := mongoIDRange(1, 25)
		slices.Reverse(want)
		assertMongoIDs(t, all, want)
	})

	orderBy := func(less func(a, b mongoDoc) bool) []int {
		exp := slices.Clone(env.docs)
		sort.SliceStable(exp, func(i, j int) bool { return less(exp[i], exp[j]) })
		out := make([]int, 0, len(exp))
		for _, d := range exp {
			out = append(out, d.ID)
		}
		return out
	}

	// 非唯一游标字段应把唯一字段一并放进 SetCursorField（Mongo 正确用法）
	t.Run("QueryPage/nonunique_as_multi_cursor_field", func(t *testing.T) {
		b := env.b()
		b.SetCursorField("status", "id").SetLimit(6)
		var all []*mongoDoc
		for page := 0; page < 10; page++ {
			res, err := b.QueryPage(ctx)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			if len(res.NextCursorValues) != 2 {
				t.Fatalf("expected 2 cursor values, got %v", res.NextCursorValues)
			}
			b.SetCursorValue(res.NextCursorValues...)
		}
		assertMongoIDs(t, all, orderBy(func(a, b mongoDoc) bool {
			if a.Status != b.Status {
				return a.Status < b.Status
			}
			return a.ID < b.ID
		}))
	})

	t.Run("AuxSort/unique_cursor_with_aux_sort_roundtrip", func(t *testing.T) {
		// 游标字段唯一时，辅助排序不影响续查正确性；NextCursorValues 仍只有 1 个
		var all []*mongoDoc
		var cursor []any
		for page := 0; page < 10; page++ {
			b := env.b().SetSort(bson.D{{Key: "name", Value: 1}})
			b.SetCursorField("-age").SetLimit(10)
			if cursor != nil {
				b.SetCursorValue(cursor...)
			}
			res, err := b.QueryPage(ctx)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			if res.HasMore && len(res.NextCursorValues) != 1 {
				t.Fatalf("expected 1 cursor value, got %v", res.NextCursorValues)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			cursor = res.NextCursorValues
		}
		want := mongoIDRange(1, 25)
		slices.Reverse(want) // age desc == id desc for this fixture
		assertMongoIDs(t, all, want)
	})

	t.Run("AuxSort/cursor_field_only_value_with_aux", func(t *testing.T) {
		b := env.b().SetSort(bson.D{{Key: "age", Value: -1}})
		b.SetCursorField("id").SetCursorValue(20).SetLimit(10)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res.Items, mongoIDRange(21, 25))

		b2 := env.b().SetSort(bson.D{{Key: "age", Value: -1}})
		b2.SetCursorField("id").SetStart(22).SetLimit(10)
		res2, err := b2.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMongoIDs(t, res2.Items, mongoIDRange(23, 25))
	})

	t.Run("AuxSort/multi_field_mixed_direction_resume", func(t *testing.T) {
		b := env.b().SetSort(bson.D{{Key: "age", Value: 1}})
		b.SetCursorField("city", "-id").SetCursorValue("shanghai", 13).SetLimit(100)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := orderBy(func(a, b mongoDoc) bool {
			if a.City != b.City {
				return a.City < b.City
			}
			return a.ID > b.ID
		})
		idx := slices.Index(want, 13)
		assertMongoIDs(t, res.Items, want[idx+1:])
	})

	t.Run("AuxSort/QueryCursor_full_from_cursor_value", func(t *testing.T) {
		var got []*mongoDoc
		b := env.b().SetSort(bson.D{{Key: "name", Value: 1}})
		b.SetCursorField("id").SetCursorValue(10).SetLimit(4).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertMongoIDs(t, got, mongoIDRange(11, 25))
	})

	t.Run("AuxSort/passing_aux_values_rejected_by_validate", func(t *testing.T) {
		// PR #11：Mongo 不实现 cursorSortKeyCounter，传入游标字段+辅助排序数量应被拒绝
		b := env.b().SetSort(bson.D{{Key: "age", Value: 1}})
		b.SetCursorField("id").SetCursorValue(10, 28) // 2 values while cursorFields=1
		_, err := b.QueryPage(ctx)
		if !errors.Is(err, builder.ErrCursorMismatch) {
			t.Fatalf("want ErrCursorMismatch (PR#11 Mongo strict), got %v", err)
		}
	})

	t.Run("QueryPage/next_cursor_numeric_type", func(t *testing.T) {
		b := env.b()
		b.SetCursorField("id").SetLimit(3)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.NextCursorValues) != 1 {
			t.Fatalf("next=%v", res.NextCursorValues)
		}
		n, ok := cursorValAsInt(res.NextCursorValues[0])
		if !ok || n != 3 {
			t.Fatalf("want cursor int 3, got %T %v", res.NextCursorValues[0], res.NextCursorValues[0])
		}
	})
}
