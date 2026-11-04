//go:build integration

// Elasticsearch 后端特有的 agg 能力集成测试（非跨源通用 Spec DSL 全覆盖）。
// ES 特有边界见各子测试注释；共享 GroupBy/Sum/Avg 等仅作到达 ES 路径的脚手架。
// 未设置 ES_URL 时跳过。
// 运行: ES_URL=http://127.0.0.1:9200 go test -tags integration -race -v -run TestESAggIntegration ./agg
package agg_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/olivere/elastic/v7"
)

type esAggDoc struct {
	ID         int      `json:"id"`
	Region     *string  `json:"region,omitempty"`
	Channel    string   `json:"channel"`
	Status     string   `json:"status"`
	CustomerID string   `json:"customer_id"`
	Amount     *float64 `json:"amount,omitempty"`
	Quantity   int      `json:"quantity"`
	CreatedAt  string   `json:"created_at"`
	Bio        string   `json:"bio"`
	SKU        string   `json:"sku"`
}

type regionRow struct {
	Region        string   `json:"region"`
	Total         int64    `json:"total"`
	AmountSum     *float64 `json:"amount_sum"`
	Buyers        int64    `json:"buyers"`
	UniqueAmount  *float64 `json:"unique_amount"`
	PaidTotal     int64    `json:"paid_total"`
	PaidAmount    *float64 `json:"paid_amount"`
	PaidBuyers    int64    `json:"paid_buyers"`
	UniquePaidAmt *float64 `json:"unique_paid_amount"`
}

type scalarRow struct {
	Total     int64    `json:"total"`
	Buyers    int64    `json:"buyers"`
	UniqueAmt *float64 `json:"unique_amount"`
}

type dayRow struct {
	Day   string   `json:"day"`
	Total int64    `json:"total"`
	Sum   *float64 `json:"amount_sum"`
}

type esAggEnv struct {
	t      *testing.T
	client *elastic.Client
	data   *core.DBProxy
	index  string
	docs   []esAggDoc
}

func ptrStr(s string) *string   { return &s }
func ptrF64(v float64) *float64 { return &v }
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func makeAggDocs() []esAggDoc {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	regions := []string{"east", "west", "north"}
	channels := []string{"web", "app"}
	docs := make([]esAggDoc, 0, 36)
	for i := 1; i <= 36; i++ {
		region := regions[(i-1)%3]
		d := esAggDoc{
			ID:         i,
			Region:     ptrStr(region),
			Channel:    channels[(i-1)%2],
			Status:     map[bool]string{true: "paid", false: "pending"}[i%2 == 1],
			CustomerID: fmt.Sprintf("c%02d", ((i-1)%8)+1),
			Amount:     ptrF64(float64(i) * 10.5),
			Quantity:   (i % 5) + 1,
			CreatedAt:  base.AddDate(0, 0, (i-1)%10).Format(time.RFC3339),
			Bio:        "loves elasticsearch aggregation queries",
			SKU:        fmt.Sprintf("sku-%d", ((i-1)%6)+1),
		}
		if i%9 == 0 {
			d.Amount = nil
		}
		if i%12 == 0 {
			d.Region = nil
		}
		docs = append(docs, d)
	}
	return docs
}

func setupESAgg(t *testing.T) *esAggEnv {
	t.Helper()
	url := os.Getenv("ES_URL")
	if url == "" {
		t.Skip("ES_URL not set, skipping elasticsearch agg integration tests")
	}
	client, err := elastic.NewClient(elastic.SetURL(url), elastic.SetSniff(false), elastic.SetHealthcheck(false))
	if err != nil {
		t.Fatalf("elastic client: %v", err)
	}
	ctx := context.Background()
	index := fmt.Sprintf("qb-agg-%d", time.Now().UnixNano())
	mapping := `{
  "settings": {"number_of_shards": 2, "number_of_replicas": 0},
  "mappings": {"properties": {
    "id": {"type": "integer"},
    "region": {"type": "keyword"},
    "channel": {"type": "keyword"},
    "status": {"type": "keyword"},
    "customer_id": {"type": "keyword"},
    "amount": {"type": "double"},
    "quantity": {"type": "integer"},
    "created_at": {"type": "date"},
    "bio": {"type": "text"},
    "sku": {"type": "keyword"}
  }}
}`
	if _, err := client.CreateIndex(index).BodyString(mapping).Do(ctx); err != nil {
		t.Fatalf("create index: %v", err)
	}
	t.Cleanup(func() { _, _ = client.DeleteIndex(index).Do(context.Background()) })

	docs := makeAggDocs()
	bulk := client.Bulk().Index(index)
	for _, d := range docs {
		bulk = bulk.Add(elastic.NewBulkIndexRequest().Id(fmt.Sprintf("%d", d.ID)).Doc(d))
	}
	if resp, err := bulk.Refresh("true").Do(ctx); err != nil {
		t.Fatalf("bulk: %v", err)
	} else if resp.Errors {
		t.Fatalf("bulk errors: %+v", resp.Items)
	}
	return &esAggEnv{
		t: t, client: client, index: index, docs: docs,
		data: core.NewDBProxyWithAdapters(core.NewElasticSearchAdapter(client)),
	}
}

func (e *esAggEnv) regionB() *agg.ElasticSearchBuilder[regionRow] {
	return agg.NewElasticSearchBuilder[regionRow](e.data, e.index)
}
func (e *esAggEnv) scalarB() *agg.ElasticSearchBuilder[scalarRow] {
	return agg.NewElasticSearchBuilder[scalarRow](e.data, e.index)
}

func docsWithRegion(docs []esAggDoc) []esAggDoc {
	out := make([]esAggDoc, 0, len(docs))
	for _, d := range docs {
		if d.Region != nil {
			out = append(out, d)
		}
	}
	return out
}
func filterDocs(docs []esAggDoc, pred func(esAggDoc) bool) []esAggDoc {
	out := make([]esAggDoc, 0)
	for _, d := range docs {
		if pred(d) {
			out = append(out, d)
		}
	}
	return out
}
func amounts(docs []esAggDoc) []float64 {
	var out []float64
	for _, d := range docs {
		if d.Amount != nil {
			out = append(out, *d.Amount)
		}
	}
	return out
}
func sumF(vals []float64) float64 {
	var s float64
	for _, v := range vals {
		s += v
	}
	return s
}
func uniqueCustomers(docs []esAggDoc) int {
	set := map[string]struct{}{}
	for _, d := range docs {
		set[d.CustomerID] = struct{}{}
	}
	return len(set)
}
func uniqueAmounts(docs []esAggDoc) float64 {
	set := map[float64]struct{}{}
	var s float64
	for _, d := range docs {
		if d.Amount == nil {
			continue
		}
		if _, ok := set[*d.Amount]; ok {
			continue
		}
		set[*d.Amount] = struct{}{}
		s += *d.Amount
	}
	return s
}
func almostEqual(t *testing.T, got, want, tol float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got=%v want=%v", label, got, want)
	}
}
func ptrAlmostEqual(t *testing.T, got *float64, want, tol float64, label string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: nil want %v", label, want)
	}
	almostEqual(t, *got, want, tol, label)
}

// TestESAggIntegration 仅覆盖 Elasticsearch 后端特有的聚合能力与实现路径。
func TestESAggIntegration(t *testing.T) {
	env := setupESAgg(t)
	ctx := context.Background()

	// ---- ES-only: CountDistinct → cardinality（近似）----
	t.Run("ESSpecific/CountDistinct_cardinality", func(t *testing.T) {
		b := env.scalarB()
		b.Count("total").CountDistinct("customer_id", "buyers")
		plan := b.Meta().Plan
		if !plan.Has(agg.PlanHasDistinctMetrics) {
			t.Fatalf("plan missing PlanHasDistinctMetrics: %+v", plan)
		}
		// AnalyzeSpec 对 ES CountDistinct 标注近似
		analyzed := agg.AnalyzeSpec(core.ElasticSearch, b.Meta().Spec)
		t.Logf("AnalyzeSpec notes=%v flags=%v", analyzed.Notes, analyzed.Flags)

		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := int64(uniqueCustomers(env.docs))
		got := res.Rows[0].Buyers
		// 2 shard + cardinality：小基数通常精确，允许 ±1
		if abs64(got-want) > 1 {
			t.Fatalf("cardinality buyers=%d exact=%d (approx tolerance ±1)", got, want)
		}
		t.Logf("CountDistinct got=%d exact=%d shards=2 (ES cardinality approx)", got, want)

		dsl, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsl, `"cardinality"`) {
			t.Fatalf("Explain should contain cardinality:\n%s", dsl)
		}
	})

	// ---- ES-only: SumDistinct → scripted_metric（字符串键去重，跨分片合并）----
	t.Run("ESSpecific/SumDistinct_scripted_metric", func(t *testing.T) {
		b := env.scalarB()
		b.Count("total").SumDistinct("amount", "unique_amount")
		plan := b.Meta().Plan
		if !plan.Has(agg.PlanUsesElasticScriptedMetric) {
			t.Fatalf("expected PlanUsesElasticScriptedMetric: %+v", plan)
		}
		dsl, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{`"scripted_metric"`, `state.values`, `"params"`} {
			if !strings.Contains(dsl, frag) {
				t.Fatalf("Explain missing %q:\n%s", frag, dsl)
			}
		}

		res, err := b.Query(ctx)
		if err != nil {
			t.Fatalf("SumDistinct scripted_metric: %v", err)
		}
		wantSum := uniqueAmounts(env.docs)
		if wantSum != 6048 {
			t.Fatalf("seed unique amount sum changed: %v", wantSum)
		}
		ptrAlmostEqual(t, res.Rows[0].UniqueAmt, wantSum, 1e-6, "sum_distinct")
	})

	// ---- ES-only: CountDistinctIf / SumDistinctIf（filter 子聚合 + cardinality/scripted_metric）----
	t.Run("ESSpecific/DistinctIf_under_group", func(t *testing.T) {
		b := env.regionB()
		b.GroupBy("region", "region").
			Count("total").
			CountDistinctIf("customer_id", "paid_buyers", "status = ?", "paid").
			SumDistinctIf("amount", "unique_paid_amount", "status = ?", "paid").
			SetLimit(20).SetNeedTotal(false)
		dsl, _ := b.Explain(ctx)
		if !strings.Contains(dsl, `"cardinality"`) || !strings.Contains(dsl, `"scripted_metric"`) {
			t.Fatalf("DistinctIf Explain should embed cardinality+scripted_metric:\n%s", dsl)
		}
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatalf("SumDistinctIf: %v", err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 region rows, got %d", len(res.Rows))
		}
		byRegion := map[string][]esAggDoc{}
		for _, d := range docsWithRegion(env.docs) {
			byRegion[*d.Region] = append(byRegion[*d.Region], d)
		}
		for _, row := range res.Rows {
			paid := filterDocs(byRegion[row.Region], func(d esAggDoc) bool { return d.Status == "paid" })
			if abs64(row.PaidBuyers-int64(uniqueCustomers(paid))) > 1 {
				t.Fatalf("%s paid_buyers=%d want≈%d", row.Region, row.PaidBuyers, uniqueCustomers(paid))
			}
			ptrAlmostEqual(t, row.UniquePaidAmt, uniqueAmounts(paid), 1e-6, row.Region+".unique_paid")
		}
	})

	// SumDistinct 去重语义：int/float 归一化、null/缺失、负数、重复值、多分片、空桶
	t.Run("ESSpecific/SumDistinct_dedupe_semantics", func(t *testing.T) {
		longIdx := env.index + "-sd-long"
		dblIdx := env.index + "-sd-double"
		for idx, typ := range map[string]string{longIdx: "long", dblIdx: "double"} {
			body := `{"settings":{"number_of_shards":2,"number_of_replicas":0},"mappings":{"properties":{"g":{"type":"keyword"},"v":{"type":"` + typ + `"},"tag":{"type":"keyword"}}}}`
			if _, err := env.client.CreateIndex(idx).BodyString(body).Do(ctx); err != nil {
				t.Fatalf("create %s: %v", idx, err)
			}
			idx := idx
			t.Cleanup(func() { _, _ = env.client.DeleteIndex(idx).Do(context.Background()) })
		}
		type vdoc struct {
			idx string
			g   string
			v   any
			tag string
		}
		docs := []vdoc{
			{longIdx, "a", 1, "x"}, {longIdx, "a", 1, "y"}, {longIdx, "a", -5, "x"}, {longIdx, "a", nil, "x"},
			{longIdx, "b", 7, "x"}, {longIdx, "b", 1000000000000, "x"}, {longIdx, "b", 1000000000000, "y"},
			{dblIdx, "a", 1.0, "x"}, {dblIdx, "a", 2.5, "y"}, {dblIdx, "a", -5.0, "y"}, {dblIdx, "a", 2.5, "x"},
			{dblIdx, "b", 7.0, "y"}, {dblIdx, "b", nil, "y"}, {dblIdx, "c", nil, "x"},
		}
		bulk := env.client.Bulk()
		for i, d := range docs {
			src := map[string]any{"g": d.g, "tag": d.tag}
			if d.v != nil {
				src["v"] = d.v
			}
			bulk = bulk.Add(elastic.NewBulkIndexRequest().Index(d.idx).Id(fmt.Sprint(i)).Routing(fmt.Sprint(i)).Doc(src))
		}
		if resp, err := bulk.Refresh("true").Do(ctx); err != nil || resp.Errors {
			t.Fatalf("bulk: %v %+v", err, resp)
		}
		both := longIdx + "," + dblIdx
		// 期望值：按 float64 归一化后去重求和
		want := func(pred func(vdoc) bool) float64 {
			seen := map[float64]bool{}
			var s float64
			for _, d := range docs {
				if d.v == nil || !pred(d) {
					continue
				}
				var f float64
				switch x := d.v.(type) {
				case int:
					f = float64(x)
				case float64:
					f = x
				}
				if !seen[f] {
					seen[f] = true
					s += f
				}
			}
			return s
		}
		type row struct {
			G string   `json:"g"`
			U *float64 `json:"u"`
			X *float64 `json:"ux"`
		}
		// 标量：两个索引共 4 个分片，long 1 与 double 1.0 合并为同一值
		sb := agg.NewElasticSearchBuilder[row](env.data, both)
		sb.SumDistinct("v", "u").SumDistinctIf("v", "ux", "tag = ?", "x")
		res, err := sb.Query(ctx)
		if err != nil {
			t.Fatalf("scalar: %v", err)
		}
		all := want(func(vdoc) bool { return true })
		if all != 1+2.5-5+7+1e12 {
			t.Fatalf("unexpected expected value %v", all)
		}
		ptrAlmostEqual(t, res.Rows[0].U, all, 1e-6, "scalar.u")
		ptrAlmostEqual(t, res.Rows[0].X, want(func(d vdoc) bool { return d.tag == "x" }), 1e-6, "scalar.ux")

		// 分组：c 组只有 null → 空桶返回 0（保持现有 ES 行为）
		gb := agg.NewElasticSearchBuilder[row](env.data, both)
		gb.GroupBy("g", "g").SumDistinct("v", "u").SumDistinctIf("v", "ux", "tag = ?", "x").SetLimit(10).SetNeedTotal(false)
		gres, err := gb.Query(ctx)
		if err != nil {
			t.Fatalf("grouped: %v", err)
		}
		if len(gres.Rows) != 3 {
			t.Fatalf("expected 3 groups, got %d", len(gres.Rows))
		}
		for _, r := range gres.Rows {
			g := r.G
			ptrAlmostEqual(t, r.U, want(func(d vdoc) bool { return d.g == g }), 1e-6, g+".u")
			ptrAlmostEqual(t, r.X, want(func(d vdoc) bool { return d.g == g && d.tag == "x" }), 1e-6, g+".ux")
		}

		// 过滤后无文档：标量空结果不报错，返回 0
		eb := agg.NewElasticSearchBuilder[row](env.data, longIdx)
		eb.SetFilter(elastic.NewTermQuery("g", "none")).SumDistinct("v", "u")
		eres, err := eb.Query(ctx)
		if err != nil {
			t.Fatalf("empty: %v", err)
		}
		if len(eres.Rows) != 1 || eres.Rows[0].U == nil || *eres.Rows[0].U != 0 {
			t.Fatalf("empty SumDistinct should be 0, got %+v", eres.Rows)
		}
	})

	// CountDistinctIf alone (no SumDistinct) should work
	t.Run("ESSpecific/CountDistinctIf_only", func(t *testing.T) {
		b := env.regionB()
		b.GroupBy("region", "region").
			Count("total").
			CountDistinctIf("customer_id", "paid_buyers", "status = ?", "paid").
			SetLimit(20)
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		byRegion := map[string][]esAggDoc{}
		for _, d := range docsWithRegion(env.docs) {
			byRegion[*d.Region] = append(byRegion[*d.Region], d)
		}
		for _, row := range res.Rows {
			paid := filterDocs(byRegion[row.Region], func(d esAggDoc) bool { return d.Status == "paid" })
			if abs64(row.PaidBuyers-int64(uniqueCustomers(paid))) > 1 {
				t.Fatalf("%s paid_buyers=%d want≈%d", row.Region, row.PaidBuyers, uniqueCustomers(paid))
			}
		}
	})

	// ---- ES-only: composite date_histogram (+ timezone) ----
	t.Run("ESSpecific/GroupByDate_composite_histogram", func(t *testing.T) {
		b := agg.NewElasticSearchBuilder[dayRow](env.data, env.index)
		b.GroupByDate("created_at", "day", agg.TimeIntervalDay).
			Count("total").Sum("amount", "amount_sum").
			SetLimit(100).SetNeedTotal(true)
		dsl, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsl, `"date_histogram"`) && !strings.Contains(dsl, "date_histogram") {
			// composite sources embed date_histogram
			if !strings.Contains(dsl, `"composite"`) {
				t.Fatalf("expected composite/date_histogram in Explain:\n%s", dsl)
			}
		}
		if !b.Meta().Plan.Has(agg.PlanHasDateGroups) {
			t.Fatalf("PlanHasDateGroups missing: %+v", b.Meta().Plan)
		}
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exp := map[string][]esAggDoc{}
		for _, d := range env.docs {
			ts, _ := time.Parse(time.RFC3339, d.CreatedAt)
			key := ts.UTC().Truncate(24 * time.Hour).Format("2006-01-02T15:04:05.000Z")
			exp[key] = append(exp[key], d)
		}
		if int(res.Total) != len(exp) {
			t.Fatalf("day buckets total=%d want=%d", res.Total, len(exp))
		}
		for _, row := range res.Rows {
			docs, ok := exp[row.Day]
			if !ok {
				parsed, err := time.Parse(time.RFC3339Nano, row.Day)
				if err != nil {
					t.Fatalf("unexpected day key %q", row.Day)
				}
				key := parsed.UTC().Truncate(24 * time.Hour).Format("2006-01-02T15:04:05.000Z")
				docs = exp[key]
			}
			if row.Total != int64(len(docs)) {
				t.Fatalf("day %s total=%d want=%d", row.Day, row.Total, len(docs))
			}
			ptrAlmostEqual(t, row.Sum, sumF(amounts(docs)), 1e-6, "day.sum")
		}
	})

	t.Run("ESSpecific/GroupByDateWithTimeZone", func(t *testing.T) {
		b := agg.NewElasticSearchBuilder[dayRow](env.data, env.index)
		b.GroupByDateWithTimeZone("created_at", "day", agg.TimeIntervalDay, "Asia/Shanghai").
			Count("total").SetLimit(100)
		dsl, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsl, "Asia/Shanghai") && !strings.Contains(dsl, `"time_zone"`) {
			t.Fatalf("Explain should carry timezone:\n%s", dsl)
		}
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) == 0 {
			t.Fatal("expected tz buckets")
		}
		var total int64
		for _, r := range res.Rows {
			total += r.Total
			t.Logf("tz day key=%q total=%d", r.Day, r.Total)
		}
		if total != int64(len(env.docs)) {
			t.Fatalf("tz docs sum=%d want=%d", total, len(env.docs))
		}
	})

	// ---- ES-only: HAVING → bucket_selector + client_post_processing ----
	t.Run("ESSpecific/Having_bucket_selector_and_client_post", func(t *testing.T) {
		byRegion := map[string]float64{}
		for _, d := range docsWithRegion(env.docs) {
			if d.Amount != nil {
				byRegion[*d.Region] += *d.Amount
			}
		}
		var sums []float64
		for _, s := range byRegion {
			sums = append(sums, s)
		}
		sort.Float64s(sums)
		threshold := sums[len(sums)-1]

		b := env.regionB()
		b.GroupBy("region", "region").
			Count("total").
			Sum("amount", "amount_sum").
			Having("amount_sum >= ?", threshold).
			OrderByDesc("amount_sum").
			SetLimit(10).SetNeedTotal(true)
		if !b.Meta().Plan.Has(agg.PlanNeedsClientPostProcessing) {
			t.Fatalf("HAVING+metric order should need client post-processing: %+v", b.Meta().Plan)
		}
		dsl, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{`"bucket_selector"`, `"client_post_processing"`, `"full_scan"`} {
			if !strings.Contains(dsl, frag) {
				t.Fatalf("Explain missing %q:\n%s", frag, dsl)
			}
		}
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) < 1 {
			t.Fatal("expected having matches")
		}
		for _, row := range res.Rows {
			ptrAlmostEqual(t, row.AmountSum, byRegion[row.Region], 1e-6, "having")
			if *row.AmountSum+1e-6 < threshold {
				t.Fatalf("having violated %v < %v", *row.AmountSum, threshold)
			}
		}
	})

	// ---- ES-only: composite pagination / exact total scan / TotalLimit ----
	t.Run("ESSpecific/Composite_pagination_TotalLimit", func(t *testing.T) {
		type skuRow struct {
			Region string `json:"region"`
			Total  int64  `json:"total"`
		}
		b := agg.NewElasticSearchBuilder[skuRow](env.data, env.index)
		b.GroupBy("sku", "region").Count("total").
			SetNeedTotal(true).SetTotalLimit(2).SetLimit(10)
		dsl, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dsl, `"composite"`) {
			t.Fatalf("expected composite:\n%s", dsl)
		}
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 2 {
			t.Fatalf("TotalLimit=2 got total=%d rows=%d", res.Total, len(res.Rows))
		}

		b2 := env.regionB()
		b2.GroupBy("region", "region").Count("total").Sum("amount", "amount_sum").
			OrderByDesc("amount_sum").SetLimit(1).SetStart(1).SetNeedTotal(true)
		res2, err := b2.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res2.Rows) != 1 {
			t.Fatalf("offset page rows=%d", len(res2.Rows))
		}
		// full ordered list for check
		b3 := env.regionB()
		b3.GroupBy("region", "region").Count("total").Sum("amount", "amount_sum").
			OrderByDesc("amount_sum").SetLimit(10).SetNeedTotal(true)
		all, err := b3.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res2.Rows[0].Region != all.Rows[1].Region {
			t.Fatalf("start=1 want %s got %s", all.Rows[1].Region, res2.Rows[0].Region)
		}
	})

	// ---- ES-only: SetFilter(elastic.Query) + MissingBucket(false) ----
	t.Run("ESSpecific/SetFilter_elastic_query", func(t *testing.T) {
		b := env.regionB()
		b.SetFilter(elastic.NewTermQuery("status", "paid"))
		b.GroupBy("region", "region").Count("total").Sum("amount", "amount_sum").SetLimit(20)
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		paid := filterDocs(docsWithRegion(env.docs), func(d esAggDoc) bool { return d.Status == "paid" })
		byRegion := map[string]int{}
		for _, d := range paid {
			byRegion[*d.Region]++
		}
		if len(res.Rows) != len(byRegion) {
			t.Fatalf("groups=%d want=%d", len(res.Rows), len(byRegion))
		}
		for _, row := range res.Rows {
			if row.Total != int64(byRegion[row.Region]) {
				t.Fatalf("%s total=%d want=%d", row.Region, row.Total, byRegion[row.Region])
			}
		}
	})

	t.Run("ESSpecific/MissingBucket_false_excludes_null_group", func(t *testing.T) {
		// elasticGroupSource uses MissingBucket(false) — ES composite 特有行为
		missing := 0
		for _, d := range env.docs {
			if d.Region == nil {
				missing++
			}
		}
		b := env.regionB()
		b.GroupBy("region", "region").Count("total").SetLimit(20).SetNeedTotal(true)
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var summed int64
		for _, r := range res.Rows {
			summed += r.Total
		}
		want := int64(len(env.docs) - missing)
		if summed != want {
			t.Fatalf("grouped=%d want=%d (missing excluded)", summed, want)
		}
		for _, r := range res.Rows {
			if r.Region == "" {
				t.Fatal("empty/missing region bucket should not appear")
			}
		}
	})

	// ---- ES-only: Explain plan flags / ErrIndexNotConfigured / text field ----
	t.Run("ESSpecific/Explain_plan_flags", func(t *testing.T) {
		b := env.regionB()
		b.GroupBy("region", "region").
			Count("total").
			CountDistinct("customer_id", "buyers").
			SumDistinct("amount", "unique_amount").
			SumIf("amount", "paid_amount", "status = ?", "paid").
			Having("paid_amount >= ?", 1).
			OrderByDesc("paid_amount").
			SetLimit(5)
		dsl, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, frag := range []string{
			env.index, `"composite"`, `"cardinality"`, `"scripted_metric"`,
			`"client_post_processing"`, `"bucket_selector"`,
		} {
			if !strings.Contains(dsl, frag) {
				t.Fatalf("missing %q:\n%s", frag, dsl)
			}
		}
		meta := b.Meta()
		if meta.DataSource != core.ElasticSearch || meta.QueryMode() != "aggregate" {
			t.Fatalf("meta=%+v", meta)
		}
		if !meta.Plan.Has(agg.PlanUsesElasticScriptedMetric) || !meta.Plan.Has(agg.PlanNeedsClientPostProcessing) {
			t.Fatalf("plan=%+v notes=%v", meta.Plan, meta.Plan.Notes)
		}
	})

	t.Run("ESSpecific/Err_index_not_configured", func(t *testing.T) {
		b := agg.NewElasticSearchBuilder[regionRow](env.data, "")
		b.Count("total")
		_, err := b.Query(ctx)
		if !errors.Is(err, agg.ErrIndexNotConfigured) {
			t.Fatalf("want ErrIndexNotConfigured got %v", err)
		}
	})

	t.Run("ESSpecific/Err_terms_on_text_field", func(t *testing.T) {
		// composite terms on analyzed text → ES 400 / fielddata
		b := env.regionB()
		b.GroupBy("bio", "region").Count("total").SetLimit(10)
		_, err := b.Query(ctx)
		if err == nil {
			t.Fatal("expected ES error for terms on text")
		}
		t.Logf("text-field error (expected): %v", err)
	})

	t.Run("ESSpecific/Err_context_canceled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		b := env.regionB()
		b.GroupBy("region", "region").Count("total")
		_, err := b.Query(cctx)
		if err == nil {
			t.Fatal("expected canceled")
		}
	})

	// ---- ES builder Clone + filter isolation under -race ----
	t.Run("ESSpecific/Clone_filter_race", func(t *testing.T) {
		base := env.regionB()
		base.GroupBy("region", "region").Count("total").Sum("amount", "amount_sum").SetLimit(10)
		var wg sync.WaitGroup
		errCh := make(chan error, 16)
		var paidOnly atomic.Int64
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := base.Clone()
				if i%2 == 0 {
					c.SetFilter(elastic.NewTermQuery("status", "paid"))
					paidOnly.Add(1)
				}
				res, err := c.Query(ctx)
				if err != nil {
					errCh <- err
					return
				}
				if len(res.Rows) == 0 {
					errCh <- fmt.Errorf("empty clone %d", i)
				}
			}(i)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Fatal(err)
		}
		if paidOnly.Load() != 4 {
			t.Fatalf("paidOnly setups=%d", paidOnly.Load())
		}
	})

	// 对照组：仅 Cardinality 的多字段 group（composite terms）顺序 — ES composite source order
	t.Run("ESSpecific/Composite_terms_order_GroupByDesc", func(t *testing.T) {
		b := env.regionB()
		b.GroupByDesc("region", "region").Count("total").SetNeedTotal(false).SetLimit(10)
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(res.Rows))
		for _, r := range res.Rows {
			got = append(got, r.Region)
		}
		want := slices.Clone(got)
		sort.Sort(sort.Reverse(sort.StringSlice(want)))
		if !slices.Equal(got, want) {
			t.Fatalf("composite desc got=%v want=%v", got, want)
		}
	})
}
