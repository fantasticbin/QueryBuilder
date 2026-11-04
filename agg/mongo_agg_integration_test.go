//go:build integration

// MongoDB 后端专属的 agg 集成测试。未设置 MONGO_URI 时跳过。
// 种子数据复用 es_agg_integration_test.go 的 makeAggDocs（同一 build tag）。
// 运行: MONGO_URI=mongodb://127.0.0.1:27017 go test -tags integration -race -v -run 'TestMongoAgg' ./agg
package agg_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type mongoAggEnv struct {
	client *mongo.Client
	db     *mongo.Database
	coll   *mongo.Collection
	data   *core.DBProxy
	docs   []esAggDoc
}

func setupMongoAgg(t *testing.T) *mongoAggEnv {
	t.Helper()
	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		t.Skip("MONGO_URI not set, skipping mongodb agg integration tests")
	}
	ctx := context.Background()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	db := client.Database(fmt.Sprintf("qb_agg_%d", time.Now().UnixNano()))
	t.Cleanup(func() { _ = db.Drop(context.Background()) })
	coll := db.Collection(fmt.Sprintf("orders_%d", time.Now().UnixNano()%100000))

	docs := makeAggDocs()
	payload := make([]any, 0, len(docs))
	for _, d := range docs {
		created, err := time.Parse(time.RFC3339, d.CreatedAt)
		if err != nil {
			t.Fatal(err)
		}
		m := bson.D{{Key: "id", Value: d.ID}}
		if d.Region != nil { // 缺失而非 null
			m = append(m, bson.E{Key: "region", Value: *d.Region})
		}
		m = append(m,
			bson.E{Key: "channel", Value: d.Channel}, bson.E{Key: "status", Value: d.Status},
			bson.E{Key: "customer_id", Value: d.CustomerID})
		if d.Amount != nil {
			m = append(m, bson.E{Key: "amount", Value: *d.Amount})
		}
		m = append(m,
			bson.E{Key: "quantity", Value: d.Quantity}, bson.E{Key: "created_at", Value: created},
			bson.E{Key: "bio", Value: d.Bio}, bson.E{Key: "sku", Value: d.SKU})
		payload = append(payload, m)
	}
	if _, err := coll.InsertMany(ctx, payload); err != nil {
		t.Fatalf("insert: %v", err)
	}
	return &mongoAggEnv{client: client, db: db, coll: coll, docs: docs,
		data: core.NewDBProxyWithAdapters(core.NewMongoAdapter(coll))}
}

type mRegionRow struct {
	Region     string   `bson:"region"`
	Total      int64    `bson:"total"`
	Buyers     int64    `bson:"buyers"`
	UniqueAmt  *float64 `bson:"unique_amount"`
	PaidCnt    int64    `bson:"paid_cnt"`
	PaidSum    *float64 `bson:"paid_sum"`
	PaidBuyers int64    `bson:"paid_buyers"`
	UniquePaid *float64 `bson:"unique_paid_amount"`
}

type mDayRow struct {
	Day   time.Time `bson:"day"`
	Total int64     `bson:"total"`
	Sum   *float64  `bson:"amount_sum"`
}

func stageNames(t *testing.T, raw any) []string {
	t.Helper()
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("pipeline not array: %T", raw)
	}
	out := make([]string, 0, len(arr))
	for _, st := range arr {
		for k := range st.(map[string]any) {
			out = append(out, k)
		}
	}
	return out
}

func explainMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("explain not json: %v\n%s", err, s)
	}
	return m
}

// TestMongoAggIntegration 覆盖 MongoDB 后端特有的聚合实现路径。
func TestMongoAggIntegration(t *testing.T) {
	env := setupMongoAgg(t)
	ctx := context.Background()
	all := env.docs
	byRegion := map[string][]esAggDoc{}
	for _, d := range docsWithRegion(all) {
		byRegion[*d.Region] = append(byRegion[*d.Region], d)
	}

	t.Run("MongoSpecific/Explain_pipeline_grouped", func(t *testing.T) {
		b := agg.NewMongoBuilder[mRegionRow](env.data).SetFilter(bson.D{{Key: "status", Value: "paid"}})
		b.GroupBy("region", "region").Count("total").Having("total > ?", 1).OrderByDesc("total").SetStart(1).SetLimit(2)
		s, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m := explainMap(t, s)
		got := strings.Join(stageNames(t, m["pipeline"]), ",")
		if got != "$match,$group,$project,$match,$sort,$skip,$limit" {
			t.Fatalf("pipeline stages=%s\n%s", got, s)
		}
		if tot := strings.Join(stageNames(t, m["total_pipeline"]), ","); tot != "$match,$group,$project,$match,$count" {
			t.Fatalf("total pipeline=%s", tot)
		}
		if _, ok := m["plan"]; !ok || strings.Contains(s, "$facet") {
			t.Fatalf("plan missing or unexpected $facet:\n%s", s)
		}
		// 分组字段非空条件 + 业务过滤合并为 $and
		for _, frag := range []string{`"$and"`, `"$exists": true`, `"status": "paid"`, `"$ne": null`} {
			if !strings.Contains(s, frag) {
				t.Fatalf("explain missing %s:\n%s", frag, s)
			}
		}
	})

	t.Run("MongoSpecific/Explain_pipeline_scalar_and_TotalLimit", func(t *testing.T) {
		b := agg.NewMongoBuilder[mRegionRow](env.data)
		b.Count("total")
		s, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m := explainMap(t, s)
		if got := strings.Join(stageNames(t, m["pipeline"]), ","); got != "$group,$project" {
			t.Fatalf("scalar stages=%s", got)
		}
		if _, ok := m["total_pipeline"]; ok {
			t.Fatal("scalar explain should not include total_pipeline")
		}
		g := agg.NewMongoBuilder[mRegionRow](env.data)
		g.GroupBy("customer_id", "region").Count("total").SetTotalLimit(3).SetLimit(2)
		s2, _ := g.Explain(ctx)
		if tot := strings.Join(stageNames(t, explainMap(t, s2)["total_pipeline"]), ","); tot != "$match,$group,$project,$limit,$count" {
			t.Fatalf("total pipeline with TotalLimit=%s", tot)
		}
		res, err := g.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 2 || res.Total != 3 { // 8 个客户，总数截断为 3
			t.Fatalf("TotalLimit rows=%d total=%d", len(res.Rows), res.Total)
		}
	})

	t.Run("MongoSpecific/SetFilter_bson_and_isolation", func(t *testing.T) {
		filter := bson.D{{Key: "channel", Value: bson.D{{Key: "$in", Value: bson.A{"app"}}}}}
		b := agg.NewMongoBuilder[mRegionRow](env.data).SetFilter(filter)
		filter[0].Value.(bson.D)[0].Value.(bson.A)[0] = "web" // 调用方事后修改不影响构建器
		b.GroupBy("region", "region").Count("total").SumIf("amount", "paid_sum", "status = ?", "paid")
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 3 || res.Total != 3 {
			t.Fatalf("rows=%d total=%d", len(res.Rows), res.Total)
		}
		for _, r := range res.Rows {
			app := filterDocs(byRegion[r.Region], func(d esAggDoc) bool { return d.Channel == "app" })
			e := mgCompute(app)
			if r.Total != e.total {
				t.Fatalf("%s total=%d want %d", r.Region, r.Total, e.total)
			}
			ptrAlmostEqual(t, r.PaidSum, e.paidSum, 1e-9, r.Region+".paid_sum")
		}
	})

	t.Run("MongoSpecific/Distinct_addToSet_exact", func(t *testing.T) {
		b := agg.NewMongoBuilder[mRegionRow](env.data)
		b.GroupBy("region", "region").Count("total").
			CountDistinct("customer_id", "buyers").SumDistinct("amount", "unique_amount").
			CountDistinctIf("customer_id", "paid_buyers", "status = ?", "paid").
			SumDistinctIf("amount", "unique_paid_amount", "status = ?", "paid")
		plan := b.Meta().Plan
		if !plan.Has(agg.PlanUsesMongoDistinctSet) || plan.Has(agg.PlanUsesApproximateDistinct) {
			t.Fatalf("plan flags %+v", plan)
		}
		s, _ := b.Explain(ctx)
		for _, frag := range []string{`"$addToSet"`, `"$filter"`, `"$size"`, `"$cond"`} {
			if !strings.Contains(s, frag) {
				t.Fatalf("explain missing %s:\n%s", frag, s)
			}
		}
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
		for _, r := range res.Rows {
			docs := byRegion[r.Region]
			paid := filterDocs(docs, func(d esAggDoc) bool { return d.Status == "paid" })
			if r.Buyers != int64(uniqueCustomers(docs)) || r.PaidBuyers != int64(uniqueCustomers(paid)) {
				t.Fatalf("%s buyers=%d/%d paid=%d/%d (must be exact)", r.Region, r.Buyers, uniqueCustomers(docs), r.PaidBuyers, uniqueCustomers(paid))
			}
			ptrAlmostEqual(t, r.UniqueAmt, uniqueAmounts(docs), 1e-9, r.Region+".unique")
			ptrAlmostEqual(t, r.UniquePaid, uniqueAmounts(paid), 1e-9, r.Region+".unique_paid")
		}
		// 标量：精确值
		type sc struct {
			Buyers int64    `bson:"buyers"`
			U      *float64 `bson:"unique_amount"`
		}
		sres, err := agg.NewMongoBuilder[sc](env.data).CountDistinct("customer_id", "buyers").SumDistinct("amount", "unique_amount").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if sres.Rows[0].Buyers != int64(uniqueCustomers(all)) {
			t.Fatalf("scalar buyers=%d", sres.Rows[0].Buyers)
		}
		ptrAlmostEqual(t, sres.Rows[0].U, 6048, 1e-9, "scalar unique amount")
	})

	t.Run("MongoSpecific/Conditional_cond_metrics", func(t *testing.T) {
		b := agg.NewMongoBuilder[mRegionRow](env.data)
		b.GroupBy("region", "region").CountIf("paid_cnt", "status = ?", "paid").
			MetricWhere(agg.Sum, "amount", "paid_sum", agg.Condition{Field: "quantity", Op: agg.Gte, Value: 2})
		if !b.Meta().Plan.Has(agg.PlanHasConditionalMetrics) {
			t.Fatal("plan missing conditional flag")
		}
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Rows {
			paid := filterDocs(byRegion[r.Region], func(d esAggDoc) bool { return d.Status == "paid" })
			pq := filterDocs(byRegion[r.Region], func(d esAggDoc) bool { return d.Quantity >= 2 })
			if r.PaidCnt != int64(len(paid)) {
				t.Fatalf("%s paid_cnt=%d want %d", r.Region, r.PaidCnt, len(paid))
			}
			ptrAlmostEqual(t, r.PaidSum, sumF(amounts(pq)), 1e-9, r.Region+".sum_where_qty")
		}
	})

	// $dateTrunc 需要 MongoDB 5.0+；本环境 7.0.14
	dateCases := []struct {
		iv  agg.TimeInterval
		tz  string
		cut func(time.Time) time.Time
	}{
		{agg.TimeIntervalDay, "", func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location()) }},
		{agg.TimeIntervalDay, "Pacific/Kiritimati", func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location()) }},
		{agg.TimeIntervalDay, "America/Los_Angeles", func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location()) }},
		{agg.TimeIntervalHour, "Asia/Kolkata", func(x time.Time) time.Time { return x.Truncate(time.Hour).Add(-30 * time.Minute) }},
		{agg.TimeIntervalWeek, "Asia/Shanghai", func(x time.Time) time.Time {
			d := time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, x.Location())
			return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)) // 周一开始
		}},
		{agg.TimeIntervalMonth, "", func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), 1, 0, 0, 0, 0, x.Location()) }},
		{agg.TimeIntervalQuarter, "", func(x time.Time) time.Time {
			return time.Date(x.Year(), ((x.Month()-1)/3)*3+1, 1, 0, 0, 0, 0, x.Location())
		}},
		{agg.TimeIntervalYear, "Asia/Tokyo", func(x time.Time) time.Time { return time.Date(x.Year(), 1, 1, 0, 0, 0, 0, x.Location()) }},
	}
	for _, dc := range dateCases {
		dc := dc
		t.Run(fmt.Sprintf("MongoSpecific/GroupByDate_%s_%s", dc.iv, strings.ReplaceAll(dc.tz, "/", "_")), func(t *testing.T) {
			loc := time.UTC
			b := agg.NewMongoBuilder[mDayRow](env.data)
			if dc.tz != "" {
				var err error
				if loc, err = time.LoadLocation(dc.tz); err != nil {
					t.Fatal(err)
				}
				b.GroupByDateWithTimeZone("created_at", "day", dc.iv, dc.tz)
			} else {
				b.GroupByDate("created_at", "day", dc.iv)
			}
			b.Count("total").Sum("amount", "amount_sum").SetLimit(100)
			if !b.Meta().Plan.Has(agg.PlanHasDateGroups) {
				t.Fatal("plan missing date flag")
			}
			if s, _ := b.Explain(ctx); !strings.Contains(s, `"$dateTrunc"`) || (dc.tz != "" && !strings.Contains(s, dc.tz)) {
				t.Fatalf("explain:\n%s", s)
			}
			res, err := b.Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			type ex struct {
				n int64
				s float64
			}
			want := map[int64]*ex{}
			for _, d := range all {
				ts, _ := time.Parse(time.RFC3339, d.CreatedAt)
				k := dc.cut(ts.In(loc)).UTC().UnixMilli()
				if want[k] == nil {
					want[k] = &ex{}
				}
				want[k].n++
				if d.Amount != nil {
					want[k].s += *d.Amount
				}
			}
			if len(res.Rows) != len(want) || res.Total != int64(len(want)) {
				t.Fatalf("buckets=%d total=%d want %d", len(res.Rows), res.Total, len(want))
			}
			for i, r := range res.Rows {
				w := want[r.Day.UTC().UnixMilli()]
				if w == nil || w.n != r.Total {
					t.Fatalf("bucket %v total=%d want %+v", r.Day.UTC(), r.Total, w)
				}
				ptrAlmostEqual(t, r.Sum, w.s, 1e-9, r.Day.String())
				if i > 0 && !res.Rows[i-1].Day.Before(r.Day) {
					t.Fatal("date buckets not ascending")
				}
			}
		})
	}

	t.Run("MongoSpecific/Null_vs_missing_and_empty_sums", func(t *testing.T) {
		c := env.db.Collection("nulls")
		docs := []any{
			bson.D{{Key: "g", Value: "a"}, {Key: "v", Value: 1}},
			bson.D{{Key: "g", Value: "a"}, {Key: "v", Value: 1.0}},
			bson.D{{Key: "g", Value: "a"}, {Key: "v", Value: nil}},
			bson.D{{Key: "g", Value: "a"}},
			bson.D{{Key: "g", Value: "b"}, {Key: "v", Value: nil}},
			bson.D{{Key: "g", Value: "b"}},
			bson.D{{Key: "g", Value: nil}, {Key: "v", Value: 5}},
			bson.D{{Key: "v", Value: 7}},
		}
		if _, err := c.InsertMany(ctx, docs); err != nil {
			t.Fatal(err)
		}
		data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(c))
		type row struct {
			G      string   `bson:"g"`
			N      int64    `bson:"n"`
			S      *float64 `bson:"s"`
			A      *float64 `bson:"a"`
			Mx     *float64 `bson:"mx"`
			SD     *float64 `bson:"sd"`
			CD     int64    `bson:"cd"`
			Nulls  int64    `bson:"nulls"`
			Exists int64    `bson:"exists"`
		}
		mk := func() agg.Builder[row] {
			return agg.NewMongoBuilder[row](data).Count("n").Sum("v", "s").Avg("v", "a").Max("v", "mx").
				SumDistinct("v", "sd").CountDistinct("v", "cd").
				CountIf("nulls", "v IS NULL", nil).CountIf("exists", "v EXISTS", nil)
		}
		res, err := mk().GroupBy("g", "g").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 2 { // g 为 null 或缺失的文档不参与分组
			t.Fatalf("groups=%d %+v", len(res.Rows), res.Rows)
		}
		a, b := res.Rows[0], res.Rows[1]
		t.Logf("group a=%+v", *a)
		t.Logf("group b=%+v", *b)
		// a：1 与 1.0 在 $addToSet 中视为同一值；null/缺失被忽略
		if a.N != 4 || a.CD != 1 || a.Nulls != 2 || a.Exists != 2 || *a.S != 2 || *a.A != 1 || *a.Mx != 1 || *a.SD != 1 {
			t.Fatalf("group a unexpected %+v", *a)
		}
		// b：全部 null/缺失 → Sum 0、SumDistinct 0、Avg/Max nil、CountDistinct 0
		if b.N != 2 || b.S == nil || *b.S != 0 || b.SD == nil || *b.SD != 0 || b.A != nil || b.Mx != nil || b.CD != 0 || b.Nulls != 2 {
			t.Fatalf("group b unexpected %+v", *b)
		}
		// 标量全空（过滤到 b）：仍有匹配文档 → Sum 0 而不是 nil
		sres, err := agg.NewMongoBuilder[row](data).SetFilter(bson.D{{Key: "g", Value: "b"}}).
			Count("n").Sum("v", "s").SumDistinct("v", "sd").Avg("v", "a").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if r := sres.Rows[0]; r.N != 2 || r.S == nil || *r.S != 0 || r.SD == nil || *r.SD != 0 || r.A != nil {
			t.Fatalf("scalar all-null unexpected %+v", *r)
		}
		// 标量无匹配：补零值行，全部为 nil（mongo.go:76-77）
		eres, err := mk().(*agg.MongoBuilder[row]).SetFilter(bson.D{{Key: "g", Value: "zzz"}}).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(eres.Rows) != 1 || eres.Total != 1 {
			t.Fatalf("padding rows=%d total=%d", len(eres.Rows), eres.Total)
		}
		if r := eres.Rows[0]; r.N != 0 || r.S != nil || r.SD != nil || r.A != nil || r.Mx != nil || r.CD != 0 {
			t.Fatalf("no-match scalar unexpected %+v", *r)
		}
		// 分组无匹配：空结果
		gres, err := mk().GroupBy("g", "g").(*agg.MongoBuilder[row]).SetFilter(bson.D{{Key: "g", Value: "zzz"}}).Query(ctx)
		if err != nil || len(gres.Rows) != 0 || gres.Total != 0 {
			t.Fatalf("grouped no-match err=%v rows=%d total=%d", err, len(gres.Rows), gres.Total)
		}
	})

	t.Run("MongoSpecific/Scalar_zero_row_padding_map", func(t *testing.T) {
		res, err := agg.NewMongoBuilder[map[string]any](env.data).SetFilter(bson.D{{Key: "id", Value: -1}}).
			Count("total").Sum("amount", "s").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 1 || res.Total != 1 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
		t.Logf("padded map row=%#v", *res.Rows[0])
		if *res.Rows[0] == nil {
			t.Fatal("padded scalar map row must be initialized, not nil")
		}
		(*res.Rows[0])["writable"] = true
	})

	t.Run("MongoSpecific/Err_collection_not_configured", func(t *testing.T) {
		for name, data := range map[string]*core.DBProxy{
			"no_adapter":     core.NewDBProxyWithAdapters(),
			"nil_collection": core.NewDBProxyWithAdapters(core.NewMongoAdapter(nil)),
			"nil_proxy":      nil,
		} {
			_, err := agg.NewMongoBuilder[mRegionRow](data).Count("total").Query(ctx)
			if !errors.Is(err, core.ErrDataNotConfigured) {
				t.Fatalf("%s: want ErrDataNotConfigured got %v", name, err)
			}
		}
	})

	t.Run("MongoSpecific/Err_bad_pipeline_runtime", func(t *testing.T) {
		// $dateTrunc 作用在字符串字段上 → 服务端报错
		_, err := agg.NewMongoBuilder[mDayRow](env.data).GroupByDate("sku", "day", agg.TimeIntervalDay).Count("total").Query(ctx)
		if err == nil || !(strings.Contains(err.Error(), "executing mongodb aggregate query") || strings.Contains(err.Error(), "counting mongodb aggregate groups")) {
			t.Fatalf("want server error, got %v", err)
		}
		t.Logf("dateTrunc on string: %v", err)
		// 非法过滤操作符
		_, err = agg.NewMongoBuilder[mRegionRow](env.data).SetFilter(bson.D{{Key: "id", Value: bson.D{{Key: "$bogus", Value: 1}}}}).
			Count("total").Query(ctx)
		if err == nil {
			t.Fatal("want error for unknown operator")
		}
		t.Logf("bad filter: %v", err)
		// 分组查询下 total 管道同样出错时应返回错误
		_, err = agg.NewMongoBuilder[mRegionRow](env.data).SetFilter(bson.D{{Key: "id", Value: bson.D{{Key: "$bogus", Value: 1}}}}).
			GroupBy("region", "region").Count("total").Query(ctx)
		if err == nil {
			t.Fatal("want error for grouped bad filter")
		}
	})

	t.Run("MongoSpecific/Err_context_canceled_and_deadline", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := agg.NewMongoBuilder[mRegionRow](env.data).GroupBy("region", "region").Count("total").Query(cctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled got %v", err)
		}
		dctx, dcancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer dcancel()
		_, err = agg.NewMongoBuilder[mRegionRow](env.data).Count("total").Query(dctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want DeadlineExceeded got %v", err)
		}
	})

	t.Run("MongoSpecific/Order_by_distinct_and_cond_metric", func(t *testing.T) {
		b := agg.NewMongoBuilder[mRegionRow](env.data)
		b.GroupBy("region", "region").SumDistinct("amount", "unique_amount").CountIf("paid_cnt", "status = ?", "paid").
			OrderByDesc("unique_amount").Having("paid_cnt >= ?", 1)
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0)
		for k := range byRegion {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return uniqueAmounts(byRegion[keys[i]]) > uniqueAmounts(byRegion[keys[j]]) })
		for i, r := range res.Rows {
			if r.Region != keys[i] {
				t.Fatalf("order got %s want %v", r.Region, keys)
			}
		}
	})
}
