//go:build integration

// ElasticSearch 常规查询（不含 agg）真实集成测试，需要可访问的 ES 7.x 实例
// 未设置 ES_URL 时自动跳过；每次运行创建独立索引并在结束后删除
// 运行: ES_URL=http://127.0.0.1:9200 go test -tags integration -race -v -run TestESIntegration .
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
	"github.com/olivere/elastic/v7"
)

type esDoc struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	City      string   `json:"city"`
	Age       int      `json:"age"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags,omitempty"`
	Bio       string   `json:"bio"`
	Email     string   `json:"email,omitempty"`
	Score     float64  `json:"score"`
	CreatedAt string   `json:"created_at"`
}

const esTotalDocs = 25

var esCities = []string{"beijing", "shanghai", "shenzhen"}

func makeDocs() []esDoc {
	docs := make([]esDoc, 0, esTotalDocs)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= esTotalDocs; i++ {
		d := esDoc{
			ID:        i,
			Name:      fmt.Sprintf("user%02d", i),
			City:      esCities[i%3],
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
			d.Bio = "loves elasticsearch and golang"
		}
		if i%3 == 0 {
			d.Email = fmt.Sprintf("user%02d@example.com", i)
		}
		docs = append(docs, d)
	}
	return docs
}

const esMapping = `{
  "settings": {"number_of_shards": 2, "number_of_replicas": 0},
  "mappings": {"properties": {
    "id": {"type": "long"},
    "name": {"type": "keyword"},
    "city": {"type": "keyword"},
    "age": {"type": "integer"},
    "status": {"type": "keyword"},
    "tags": {"type": "keyword"},
    "bio": {"type": "text"},
    "email": {"type": "keyword"},
    "score": {"type": "double"},
    "created_at": {"type": "date"}
  }}
}`

type esEnv struct {
	client *elastic.Client
	data   *core.DBProxy
	index  string
	docs   []esDoc
}

func setupES(t *testing.T) *esEnv {
	t.Helper()
	url := os.Getenv("ES_URL")
	if url == "" {
		t.Skip("ES_URL not set, skipping elasticsearch integration tests")
	}
	client, err := elastic.NewClient(elastic.SetURL(url), elastic.SetSniff(false), elastic.SetHealthcheck(false))
	if err != nil {
		t.Skipf("elasticsearch not available at %s: %v", url, err)
	}
	ctx := context.Background()
	if _, _, err := client.Ping(url).Do(ctx); err != nil {
		t.Skipf("elasticsearch ping failed: %v", err)
	}
	index := fmt.Sprintf("qb_it_%d", time.Now().UnixNano())
	if _, err := client.CreateIndex(index).BodyString(esMapping).Do(ctx); err != nil {
		t.Fatalf("create index: %v", err)
	}
	t.Cleanup(func() { _, _ = client.DeleteIndex(index).Do(context.Background()) })

	docs := makeDocs()
	bulk := client.Bulk().Index(index).Refresh("true")
	for _, d := range docs {
		bulk.Add(elastic.NewBulkIndexRequest().Id(fmt.Sprint(d.ID)).Doc(d))
	}
	resp, err := bulk.Do(ctx)
	if err != nil || resp.Errors {
		t.Fatalf("bulk index failed: %v %+v", err, resp)
	}
	return &esEnv{
		client: client,
		data:   builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(client)),
		index:  index,
		docs:   docs,
	}
}

func (e *esEnv) b() *builder.ElasticSearchBuilder[esDoc] {
	return builder.NewElasticSearchBuilder[esDoc](e.data, e.index)
}

// expectIDs 返回满足谓词的文档 ID（按 id 升序）
func (e *esEnv) expectIDs(pred func(esDoc) bool) []int {
	var ids []int
	for _, d := range e.docs {
		if pred(d) {
			ids = append(ids, d.ID)
		}
	}
	return ids
}

func (e *esEnv) doc(id int) esDoc { return e.docs[id-1] }

func ids(items []*esDoc) []int {
	out := make([]int, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func assertIDs(t *testing.T, got []*esDoc, want []int) {
	t.Helper()
	g := ids(got)
	if !slices.Equal(g, want) {
		t.Fatalf("ids mismatch\n want=%v\n  got=%v", want, g)
	}
}

func assertTotal(t *testing.T, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("total mismatch want=%d got=%d", want, got)
	}
}

func idRange(from, to int) []int {
	var out []int
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func byIDAsc() elastic.Sorter { return elastic.NewFieldSort("id").Asc() }

func TestESIntegration(t *testing.T) {
	env := setupES(t)
	ctx := context.Background()

	// ---------------- QueryList: 过滤条件 ----------------
	listCases := []struct {
		name   string
		filter elastic.Query
		pred   func(esDoc) bool
	}{
		{"MatchAll", nil, func(esDoc) bool { return true }},
		{"Term", elastic.NewTermQuery("city", "beijing"), func(d esDoc) bool { return d.City == "beijing" }},
		{"Terms", elastic.NewTermsQuery("city", "shanghai", "shenzhen"), func(d esDoc) bool { return d.City != "beijing" }},
		{"Match_text", elastic.NewMatchQuery("bio", "elasticsearch"), func(d esDoc) bool { return strings.Contains(d.Bio, "elasticsearch") }},
		{"Range_int", elastic.NewRangeQuery("age").Gte(30).Lt(40), func(d esDoc) bool { return d.Age >= 30 && d.Age < 40 }},
		{"Range_date", elastic.NewRangeQuery("created_at").Gte("2026-01-10T00:00:00Z").Lte("2026-01-15T00:00:00Z"),
			func(d esDoc) bool { return d.ID >= 9 && d.ID <= 14 }},
		{"Range_double", elastic.NewRangeQuery("score").Gt(30.0), func(d esDoc) bool { return d.Score > 30 }},
		{"Bool_must_filter_mustnot", elastic.NewBoolQuery().
			Must(elastic.NewRangeQuery("age").Gte(25)).
			Filter(elastic.NewTermQuery("status", "active")).
			MustNot(elastic.NewTermQuery("city", "shanghai")),
			func(d esDoc) bool { return d.Age >= 25 && d.Status == "active" && d.City != "shanghai" }},
		{"Bool_should_msm1", elastic.NewBoolQuery().
			Should(elastic.NewTermQuery("tags", "vip"), elastic.NewTermQuery("city", "beijing")).
			MinimumNumberShouldMatch(1),
			func(d esDoc) bool { return slices.Contains(d.Tags, "vip") || d.City == "beijing" }},
		{"Bool_nested", elastic.NewBoolQuery().
			Filter(elastic.NewBoolQuery().Should(
				elastic.NewBoolQuery().Must(elastic.NewTermQuery("status", "inactive"), elastic.NewTermQuery("tags", "vip")),
				elastic.NewRangeQuery("age").Lte(20),
			)),
			func(d esDoc) bool { return (d.Status == "inactive" && slices.Contains(d.Tags, "vip")) || d.Age <= 20 }},
		{"Exists", elastic.NewExistsQuery("email"), func(d esDoc) bool { return d.Email != "" }},
		{"MustNot_exists", elastic.NewBoolQuery().MustNot(elastic.NewExistsQuery("tags")), func(d esDoc) bool { return len(d.Tags) == 0 }},
		{"Wildcard", elastic.NewWildcardQuery("name", "user1*"), func(d esDoc) bool { return strings.HasPrefix(d.Name, "user1") }},
		{"Prefix", elastic.NewPrefixQuery("name", "user2"), func(d esDoc) bool { return strings.HasPrefix(d.Name, "user2") }},
		{"Ids", elastic.NewIdsQuery().Ids("3", "7", "11"), func(d esDoc) bool { return d.ID == 3 || d.ID == 7 || d.ID == 11 }},
		{"QueryString", elastic.NewQueryStringQuery("city:shenzhen AND status:active"), func(d esDoc) bool { return d.City == "shenzhen" && d.Status == "active" }},
	}
	for _, tc := range listCases {
		t.Run("List/"+tc.name, func(t *testing.T) {
			b := env.b()
			if tc.filter != nil {
				b.SetFilter(tc.filter)
			}
			b.SetSort(byIDAsc()).SetLimit(100)
			res, err := b.QueryList(ctx)
			if err != nil {
				t.Fatalf("QueryList: %v", err)
			}
			want := env.expectIDs(tc.pred)
			if len(want) == 0 {
				t.Fatalf("bad fixture: empty expectation")
			}
			assertIDs(t, res.Items, want)
			assertTotal(t, res.Total, int64(len(want)))
			// 整文档内容比对
			for _, it := range res.Items {
				if exp := env.doc(it.ID); !equalDoc(*it, exp) {
					t.Fatalf("doc %d mismatch want=%+v got=%+v", it.ID, exp, *it)
				}
			}
		})
	}

	// ---------------- 排序 ----------------
	t.Run("Sort/id_desc", func(t *testing.T) {
		res, err := env.b().SetSort(elastic.NewFieldSort("id").Desc()).SetLimit(5).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, []int{25, 24, 23, 22, 21})
		assertTotal(t, res.Total, 25)
	})
	t.Run("Sort/multi_city_asc_age_desc", func(t *testing.T) {
		res, err := env.b().SetSort(elastic.NewFieldSort("city").Asc(), elastic.NewFieldSort("age").Desc()).SetLimit(100).QueryList(ctx)
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
		assertIDs(t, res.Items, want)
	})
	t.Run("Sort/date_desc", func(t *testing.T) {
		res, err := env.b().SetSort(elastic.NewFieldSort("created_at").Desc()).SetLimit(3).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, []int{25, 24, 23})
	})
	t.Run("Sort/score_relevance_match", func(t *testing.T) {
		// 不设 sort，按 _score：只校验集合
		res, err := env.b().SetFilter(elastic.NewMatchQuery("bio", "golang")).SetLimit(100).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := ids(res.Items)
		sort.Ints(got)
		if want := []int{5, 10, 15, 20, 25}; !slices.Equal(got, want) {
			t.Fatalf("want %v got %v", want, got)
		}
	})

	// ---------------- 分页 / 总数 ----------------
	t.Run("Page/default_limit_10", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, idRange(1, 10))
		assertTotal(t, res.Total, 25)
	})
	t.Run("Page/from10_size5", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetStart(10).SetLimit(5).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, idRange(11, 15))
		assertTotal(t, res.Total, 25)
	})
	t.Run("Page/last_partial_page", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetStart(20).SetLimit(10).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, idRange(21, 25))
	})
	t.Run("Page/start_beyond_total", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetStart(100).SetLimit(10).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, nil)
		assertTotal(t, res.Total, 25)
	})
	t.Run("Page/limit0_uses_default", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetLimit(0).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, idRange(1, 10))
	})
	t.Run("Page/NeedPagination_false_returns_all", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetNeedPagination(false).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertTotal(t, res.Total, 25)
		assertIDs(t, res.Items, idRange(1, 25))
	})
	t.Run("Total/NeedTotal_false", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetLimit(5).SetNeedTotal(false).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, idRange(1, 5))
		assertTotal(t, res.Total, 0) // 与 Gorm/Mongo 行为一致：不统计时 Total=0
	})
	t.Run("Total/TotalLimit_capped", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetLimit(3).SetTotalLimit(7).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, idRange(1, 3))
		assertTotal(t, res.Total, 7)
	})
	t.Run("Total/TotalLimit_above_actual", func(t *testing.T) {
		res, err := env.b().SetFilter(elastic.NewTermQuery("city", "beijing")).SetTotalLimit(1000).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertTotal(t, res.Total, 8)
	})
	t.Run("Empty/no_match", func(t *testing.T) {
		res, err := env.b().SetFilter(elastic.NewTermQuery("city", "nowhere")).QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, nil)
		assertTotal(t, res.Total, 0)
	})

	// ---------------- 字段投影 ----------------
	t.Run("Fields/id_name_only", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetLimit(3).SetFields("id", "name").QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range res.Items {
			want := esDoc{ID: it.ID, Name: env.doc(it.ID).Name}
			if !equalDoc(*it, want) {
				t.Fatalf("projection leaked fields: %+v", *it)
			}
		}
		assertIDs(t, res.Items, []int{1, 2, 3})
	})
	t.Run("Fields/dedupe_and_empty", func(t *testing.T) {
		res, err := env.b().SetSort(byIDAsc()).SetLimit(1).SetFields("city", "", "city", "id").QueryList(ctx)
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
	t.Run("Err/index_not_found", func(t *testing.T) {
		_, err := builder.NewElasticSearchBuilder[esDoc](env.data, "qb_it_not_exist_index").QueryList(ctx)
		if err == nil || !elastic.IsNotFound(err) {
			t.Fatalf("want 404 error got %v", err)
		}
	})
	t.Run("Err/bad_query_field_type", func(t *testing.T) {
		_, err := env.b().SetFilter(elastic.NewRangeQuery("age").Gte("not-a-number")).QueryList(ctx)
		if err == nil {
			t.Fatal("expected ES 400 error")
		}
	})
	t.Run("Err/context_canceled", func(t *testing.T) {
		// 使用独立 client：olivere/elastic 可能在并发请求被取消时将连接标记为不可用，
		// 此时返回 ErrNoClient 而非 context.Canceled，且会影响共享 client 的后续用例
		client, err := elastic.NewClient(elastic.SetURL(os.Getenv("ES_URL")), elastic.SetSniff(false), elastic.SetHealthcheck(false))
		if err != nil {
			t.Fatal(err)
		}
		data := builder.NewDBProxyWithAdapters(builder.NewElasticSearchAdapter(client))
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err = builder.NewElasticSearchBuilder[esDoc](data, env.index).QueryList(cctx)
		if !errors.Is(err, context.Canceled) && !errors.Is(err, elastic.ErrNoClient) {
			t.Fatalf("want context.Canceled (or ErrNoClient) got %v", err)
		}
	})

	// ---------------- List / Scope / Option API ----------------
	t.Run("ListAPI/Scope_and_options", func(t *testing.T) {
		l := builder.NewList[esDoc]().SetDataSource(builder.ElasticSearch).
			SetScope(builder.NewElasticSearchScope[esDoc](elastic.NewTermQuery("status", "active"), elastic.NewFieldSort("id").Desc()))
		res, err := l.Query(ctx,
			builder.WithData(env.data), builder.WithESIndex(env.index),
			builder.WithStart(2), builder.WithLimit(3), builder.WithFields("id", "status"))
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, []int{21, 19, 17})
		assertTotal(t, res.Total, 13)
		if res.Items[0].Name != "" || res.Items[0].Status != "active" {
			t.Fatalf("projection via WithFields not applied: %+v", *res.Items[0])
		}
		meta := l.GetQueryMeta()
		if meta.DataSource != builder.ElasticSearch || meta.Start != 2 || meta.Limit != 3 {
			t.Fatalf("unexpected meta %+v", meta)
		}
	})
	t.Run("ListAPI/NewListWithData_missing_index", func(t *testing.T) {
		l := builder.NewListWithData[esDoc](builder.ElasticSearch, env.data)
		_, err := l.Query(ctx)
		if !errors.Is(err, builder.ErrESIndexNotConfigured) {
			t.Fatalf("want ErrESIndexNotConfigured got %v", err)
		}
	})
	t.Run("Hooks_and_Middleware", func(t *testing.T) {
		var before, after, mw int
		var hookTotal int64
		b := env.b().SetFilter(elastic.NewTermQuery("city", "shenzhen")).SetSort(byIDAsc())
		b.SetBeforeQueryHook(func(c context.Context) context.Context { before++; return c })
		b.SetAfterQueryHook(func(c context.Context, r core.Result[esDoc], err error) { after++; hookTotal = r.GetTotal() })
		b.Use(func(c context.Context, q builder.Querier[esDoc], next func(context.Context) (core.Result[esDoc], error)) (core.Result[esDoc], error) {
			mw++
			if q.GetQueryMeta().DataSource != builder.ElasticSearch {
				t.Errorf("bad meta in middleware")
			}
			return next(c)
		})
		res, err := b.QueryList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := env.expectIDs(func(d esDoc) bool { return d.City == "shenzhen" })
		assertIDs(t, res.Items, want)
		if before != 1 || after != 1 || mw != 1 || hookTotal != int64(len(want)) {
			t.Fatalf("hooks before=%d after=%d mw=%d hookTotal=%d", before, after, mw, hookTotal)
		}
	})
	t.Run("Clone_concurrent", func(t *testing.T) {
		base := env.b()
		base.SetSort(byIDAsc()).SetLimit(100)
		var wg sync.WaitGroup
		errs := make([]error, len(esCities))
		for i, city := range esCities {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := base.Clone().SetFilter(elastic.NewTermQuery("city", city))
				res, err := c.QueryList(ctx)
				if err != nil {
					errs[i] = err
					return
				}
				want := env.expectIDs(func(d esDoc) bool { return d.City == city })
				if !slices.Equal(ids(res.Items), want) || res.Total != int64(len(want)) {
					errs[i] = fmt.Errorf("city %s want %v got %v total %d", city, want, ids(res.Items), res.Total)
				}
			}()
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("Explain_DSL", func(t *testing.T) {
		dsl, err := env.b().SetFilter(elastic.NewTermQuery("city", "beijing")).SetSort(byIDAsc()).SetStart(5).SetLimit(2).Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{`"term"`, `"city": "beijing"`, `"from": 5`, `"size": 2`, env.index} {
			if !strings.Contains(dsl, s) {
				t.Fatalf("explain missing %s:\n%s", s, dsl)
			}
		}
	})

	// ---------------- 游标 / search_after / PIT ----------------
	t.Run("Cursor/single_batch_by_id", func(t *testing.T) {
		var got []*esDoc
		for d, err := range env.b().SetCursorField("id").SetLimit(4).QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertIDs(t, got, idRange(1, 4))
	})
	t.Run("Cursor/single_batch_with_cursor_value", func(t *testing.T) {
		var got []*esDoc
		for d, err := range env.b().SetCursorField("id").SetCursorValue(5).SetLimit(3).QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertIDs(t, got, []int{6, 7, 8})
	})
	t.Run("Cursor/full_traversal_PIT_by_id", func(t *testing.T) {
		var got []*esDoc
		b := env.b().SetFilter(elastic.NewRangeQuery("age").Gt(20))
		b.SetCursorField("id").SetLimit(4).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertIDs(t, got, env.expectIDs(func(d esDoc) bool { return d.Age > 20 }))
	})
	t.Run("Cursor/full_traversal_PIT_desc", func(t *testing.T) {
		var got []*esDoc
		b := env.b()
		b.SetCursorField("-id").SetLimit(6).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		want := idRange(1, 25)
		slices.Reverse(want)
		assertIDs(t, got, want)
	})
	t.Run("Cursor/full_traversal_default_shard_doc", func(t *testing.T) {
		var got []*esDoc
		b := env.b()
		b.SetLimit(7).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		g := ids(got)
		sort.Ints(g)
		if !slices.Equal(g, idRange(1, 25)) {
			t.Fatalf("want all 25 unique docs, got %v", ids(got))
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
		l := builder.NewList[esDoc]().SetDataSource(builder.ElasticSearch).
			SetScope(builder.NewElasticSearchScope[esDoc](elastic.NewTermQuery("city", "shanghai")))
		var got []*esDoc
		for d, err := range l.QueryCursor(ctx, builder.WithData(env.data), builder.WithESIndex(env.index),
			builder.WithCursorField("id"), builder.WithLimit(3), builder.WithNeedPagination(false), builder.WithNeedTotal(false)) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertIDs(t, got, env.expectIDs(func(d esDoc) bool { return d.City == "shanghai" }))
	})
	t.Run("QueryPage/same_builder_loop", func(t *testing.T) {
		b := env.b()
		b.SetCursorField("id").SetLimit(10)
		var all []*esDoc
		for page := 0; page < 10; page++ {
			res, err := b.QueryPage(ctx)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			t.Logf("page %d ids=%v hasMore=%v next=%v total=%d", page, ids(res.Items), res.HasMore, res.NextCursorValues, res.Total)
			if page == 0 {
				assertTotal(t, res.Total, 25)
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
		assertIDs(t, all, idRange(1, 25))
	})
	t.Run("QueryPage/ListAPI_cross_request", func(t *testing.T) {
		l := builder.NewList[esDoc]().SetDataSource(builder.ElasticSearch)
		var all []*esDoc
		var cursor []any
		for page := 0; page < 10; page++ {
			opts := []builder.QueryOption{builder.WithData(env.data), builder.WithESIndex(env.index),
				builder.WithCursorField("id"), builder.WithLimit(10)}
			if cursor != nil {
				opts = append(opts, builder.WithCursorValue(cursor...))
			}
			res, err := l.QueryPage(ctx, opts...)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			t.Logf("page %d ids=%v hasMore=%v next=%v", page, ids(res.Items), res.HasMore, res.NextCursorValues)
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			cursor = res.NextCursorValues
		}
		assertIDs(t, all, idRange(1, 25))
	})
	t.Run("QueryPage/start_as_cursor", func(t *testing.T) {
		b := env.b()
		b.SetCursorField("id").SetStart(20).SetLimit(3)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, []int{21, 22, 23})
		if !res.HasMore {
			t.Fatal("expected HasMore")
		}
	})
	t.Run("QueryPage/exact_limit_no_false_hasMore", func(t *testing.T) {
		b := env.b().SetFilter(elastic.NewTermQuery("city", "beijing"))
		b.SetCursorField("id").SetLimit(8)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, env.expectIDs(func(d esDoc) bool { return d.City == "beijing" }))
		if res.HasMore || res.NextCursorValues != nil {
			t.Fatalf("HasMore should be false with nil cursor, got %v %v", res.HasMore, res.NextCursorValues)
		}
	})
	t.Run("PIT/QueryPageWithPIT_builder_loop", func(t *testing.T) {
		var all []*esDoc
		pitID := ""
		var cursor []any
		for page := 0; page < 10; page++ {
			b := env.b().SetFilter(elastic.NewMatchAllQuery()).SetPitKeepAlive(30 * time.Second)
			b.SetCursorField("created_at", "id").SetLimit(7)
			if pitID != "" {
				b.SetPITID(pitID).SetCursorValue(cursor...)
			}
			res, err := b.QueryPageWithPIT(ctx)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			t.Logf("page %d ids=%v hasMore=%v next=%v pit=%t total=%d", page, ids(res.Items), res.HasMore, res.NextCursorValues, res.PitID != "", res.Total)
			if page == 0 {
				assertTotal(t, res.Total, 25)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				if res.PitID != "" || res.NextCursorValues != nil {
					t.Fatalf("last page should clear PitID/cursor")
				}
				break
			}
			if res.PitID == "" {
				t.Fatal("PitID empty while HasMore")
			}
			pitID, cursor = res.PitID, res.NextCursorValues
		}
		assertIDs(t, all, idRange(1, 25))
	})
	t.Run("PIT/ListAPI_desc_with_filter", func(t *testing.T) {
		l := builder.NewList[esDoc]().SetDataSource(builder.ElasticSearch).
			SetScope(builder.NewElasticSearchScope[esDoc](elastic.NewTermQuery("status", "active")))
		var all []*esDoc
		pitID := ""
		var cursor []any
		for page := 0; page < 10; page++ {
			opts := []builder.QueryOption{builder.WithData(env.data), builder.WithESIndex(env.index),
				builder.WithCursorField("-id"), builder.WithLimit(5), builder.WithPitKeepAlive(time.Minute)}
			if pitID != "" {
				opts = append(opts, builder.WithPITID(pitID), builder.WithCursorValue(cursor...))
			}
			res, err := l.QueryPageWithPIT(ctx, opts...)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			pitID, cursor = res.PitID, res.NextCursorValues
		}
		want := env.expectIDs(func(d esDoc) bool { return d.Status == "active" })
		slices.Reverse(want)
		assertIDs(t, all, want)
	})
	t.Run("PIT/snapshot_isolation", func(t *testing.T) {
		// PIT 打开后新写入的文档不应出现在后续页
		b := env.b().SetPitKeepAlive(time.Minute)
		b.SetCursorField("id").SetLimit(10)
		p1, err := b.QueryPageWithPIT(ctx)
		if err != nil {
			t.Fatal(err)
		}
		extra := esDoc{ID: 999, Name: "user999", City: "beijing", CreatedAt: "2027-01-01T00:00:00Z"}
		if _, err := env.client.Index().Index(env.index).Id("999").BodyJson(extra).Refresh("true").Do(ctx); err != nil {
			t.Fatal(err)
		}
		defer env.client.Delete().Index(env.index).Id("999").Refresh("true").Do(context.Background())
		all := slices.Clone(p1.Items)
		pit, cur := p1.PitID, p1.NextCursorValues
		for pit != "" {
			b2 := env.b()
			b2.SetCursorField("id").SetLimit(10)
			b2.SetPITID(pit).SetCursorValue(cur...)
			p, err := b2.QueryPageWithPIT(ctx)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, p.Items...)
			pit, cur = p.PitID, p.NextCursorValues
		}
		assertIDs(t, all, idRange(1, 25))
	})
	t.Run("Cursor/full_traversal_non_unique_field", func(t *testing.T) {
		// 非唯一游标字段（city）全量遍历依赖内部保留的隐式 _shard_doc tiebreaker，不应丢失同值记录
		var got []*esDoc
		b := env.b()
		b.SetCursorField("city").SetLimit(4).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		g := ids(got)
		sort.Ints(g)
		if !slices.Equal(g, idRange(1, 25)) {
			t.Fatalf("want all 25 unique docs, got %v", ids(got))
		}
	})
	t.Run("PIT/explicit_shard_doc_cursor_field", func(t *testing.T) {
		var all []*esDoc
		pitID := ""
		var cursor []any
		for page := 0; page < 10; page++ {
			b := env.b()
			b.SetCursorField("id", "_shard_doc").SetLimit(7)
			if pitID != "" {
				b.SetPITID(pitID).SetCursorValue(cursor...)
			}
			res, err := b.QueryPageWithPIT(ctx)
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
			pitID, cursor = res.PitID, res.NextCursorValues
		}
		assertIDs(t, all, idRange(1, 25))
	})
	// ---------------- 游标字段 + 辅助排序 ----------------
	orderBy := func(less func(a, b esDoc) bool) []int {
		exp := slices.Clone(env.docs)
		sort.SliceStable(exp, func(i, j int) bool { return less(exp[i], exp[j]) })
		out := make([]int, 0, len(exp))
		for _, d := range exp {
			out = append(out, d.ID)
		}
		return out
	}
	t.Run("AuxSort/QueryPage_roundtrip_nonunique_cursor", func(t *testing.T) {
		// 游标字段 status 不唯一，辅助排序 id 使完整 sort values 唯一，可原样传回续查
		b := env.b().SetSort(elastic.NewFieldSort("id").Asc())
		b.SetCursorField("status").SetLimit(6)
		var all []*esDoc
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
				t.Fatalf("expected cursor+aux values, got %v", res.NextCursorValues)
			}
			b.SetCursorValue(res.NextCursorValues...)
		}
		assertIDs(t, all, orderBy(func(a, b esDoc) bool {
			if a.Status != b.Status {
				return a.Status < b.Status
			}
			return a.ID < b.ID
		}))
	})
	t.Run("AuxSort/QueryPageWithPIT_ListAPI_roundtrip", func(t *testing.T) {
		l := builder.NewList[esDoc]().SetDataSource(builder.ElasticSearch).
			SetScope(builder.NewElasticSearchScope[esDoc](nil, elastic.NewFieldSort("id").Desc()))
		var all []*esDoc
		pitID := ""
		var cursor []any
		for page := 0; page < 10; page++ {
			opts := []builder.QueryOption{builder.WithData(env.data), builder.WithESIndex(env.index),
				builder.WithCursorField("city"), builder.WithLimit(4)}
			if pitID != "" {
				opts = append(opts, builder.WithPITID(pitID), builder.WithCursorValue(cursor...))
			}
			res, err := l.QueryPageWithPIT(ctx, opts...)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			pitID, cursor = res.PitID, res.NextCursorValues
		}
		assertIDs(t, all, orderBy(func(a, b esDoc) bool {
			if a.City != b.City {
				return a.City < b.City
			}
			return a.ID > b.ID
		}))
	})
	t.Run("AuxSort/QueryPage_cross_request_new_builder", func(t *testing.T) {
		var all []*esDoc
		var cursor []any
		for page := 0; page < 10; page++ {
			b := env.b().SetSort(elastic.NewFieldSort("name").Asc())
			b.SetCursorField("-age").SetLimit(10)
			if cursor != nil {
				b.SetCursorValue(cursor...)
			}
			res, err := b.QueryPage(ctx)
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			all = append(all, res.Items...)
			if !res.HasMore {
				break
			}
			cursor = res.NextCursorValues
		}
		want := idRange(1, 25)
		slices.Reverse(want)
		assertIDs(t, all, want)
	})
	t.Run("AuxSort/cursor_field_only_value_uses_filter", func(t *testing.T) {
		b := env.b().SetSort(elastic.NewFieldSort("age").Desc())
		b.SetCursorField("id").SetCursorValue(20).SetLimit(10)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res.Items, idRange(21, 25))

		b2 := env.b().SetSort(elastic.NewFieldSort("age").Desc())
		b2.SetCursorField("id").SetStart(22).SetLimit(10)
		res2, err := b2.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, res2.Items, idRange(23, 25))
	})
	t.Run("AuxSort/cursor_field_only_multi_field_mixed_direction", func(t *testing.T) {
		// 从 (city=shanghai, id=13) 之后开始：city 升序、id 降序，辅助排序 age
		b := env.b().SetSort(elastic.NewFieldSort("age").Asc())
		b.SetCursorField("city", "-id").SetCursorValue("shanghai", 13).SetLimit(100)
		res, err := b.QueryPage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := orderBy(func(a, b esDoc) bool {
			if a.City != b.City {
				return a.City < b.City
			}
			return a.ID > b.ID
		})
		idx := slices.Index(want, 13)
		assertIDs(t, res.Items, want[idx+1:])
	})
	t.Run("AuxSort/QueryCursor_full_traversal_from_cursor_value", func(t *testing.T) {
		var got []*esDoc
		b := env.b().SetSort(elastic.NewFieldSort("name").Asc())
		b.SetCursorField("id").SetCursorValue(10).SetLimit(4).SetNeedPagination(false)
		for d, err := range b.QueryCursor(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, d)
		}
		assertIDs(t, got, idRange(11, 25))
	})
	t.Run("AuxSort/wrong_value_count_rejected", func(t *testing.T) {
		b := env.b().SetSort(elastic.NewFieldSort("age").Asc())
		b.SetCursorField("id").SetCursorValue(1, 2, 3)
		_, err := b.QueryPage(ctx)
		if !errors.Is(err, builder.ErrCursorMismatch) {
			t.Fatalf("want ErrCursorMismatch got %v", err)
		}
	})
	t.Run("Explain_cursor_mode", func(t *testing.T) {
		dsl, err := env.b().SetCursorField("-created_at", "id").SetLimit(3).Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsl, `"mode": "cursor"`) || !strings.Contains(dsl, "search_after") {
			t.Fatalf("unexpected cursor explain: %s", dsl)
		}
	})
}

func equalDoc(a, b esDoc) bool {
	return a.ID == b.ID && a.Name == b.Name && a.City == b.City && a.Age == b.Age && a.Status == b.Status &&
		slices.Equal(a.Tags, b.Tags) && a.Bio == b.Bio && a.Email == b.Email && a.Score == b.Score && a.CreatedAt == b.CreatedAt
}
