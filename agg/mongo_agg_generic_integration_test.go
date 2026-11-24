//go:build integration

// 通用(跨后端) agg 功能在 MongoDB 后端上的集成测试。种子数据复用 es_agg_integration_test.go 的 makeAggDocs。
// 运行: MONGO_URI=mongodb://127.0.0.1:27017 go test -tags integration -race -v -run TestMongoAggGeneric ./agg
package agg_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type mgRow struct {
	Region  string   `json:"region" bson:"region"`
	Channel string   `json:"channel" bson:"channel"`
	Total   int64    `json:"total" bson:"total"`
	Sum     *float64 `json:"amount_sum" bson:"amount_sum"`
	Avg     *float64 `json:"amount_avg" bson:"amount_avg"`
	Min     *float64 `json:"amount_min" bson:"amount_min"`
	Max     *float64 `json:"amount_max" bson:"amount_max"`
	QtySum  *float64 `json:"qty_sum" bson:"qty_sum"`
	QtyMin  *float64 `json:"qty_min" bson:"qty_min"`
	PaidCnt int64    `json:"paid_cnt" bson:"paid_cnt"`
	PaidSum *float64 `json:"paid_sum" bson:"paid_sum"`
}

type mgScalar struct {
	Total  int64      `json:"total" bson:"total"`
	Sum    *float64   `json:"amount_sum" bson:"amount_sum"`
	Avg    *float64   `json:"amount_avg" bson:"amount_avg"`
	Min    *float64   `json:"amount_min" bson:"amount_min"`
	Max    *float64   `json:"amount_max" bson:"amount_max"`
	QtySum *float64   `json:"qty_sum" bson:"qty_sum"`
	QtyAvg *float64   `json:"qty_avg" bson:"qty_avg"`
	DMin   *time.Time `json:"date_min" bson:"date_min"`
	DMax   *time.Time `json:"date_max" bson:"date_max"`
}

type mgExp struct {
	total          int64
	sum            float64
	n              int
	min, max       float64
	qtySum, qtyMin float64
	paidCnt        int64
	paidSum        float64
}

func mgCompute(docs []esAggDoc) mgExp {
	e := mgExp{min: math.Inf(1), max: math.Inf(-1), qtyMin: math.Inf(1)}
	for _, d := range docs {
		e.total++
		e.qtySum += float64(d.Quantity)
		e.qtyMin = math.Min(e.qtyMin, float64(d.Quantity))
		if d.Status == "paid" {
			e.paidCnt++
		}
		if d.Amount != nil {
			a := *d.Amount
			e.sum += a
			e.n++
			e.min = math.Min(e.min, a)
			e.max = math.Max(e.max, a)
			if d.Status == "paid" {
				e.paidSum += a
			}
		}
	}
	return e
}

func mgGroupKey(d esAggDoc, fields ...string) (string, bool) {
	parts := []string{}
	for _, f := range fields {
		switch f {
		case "region":
			if d.Region == nil {
				return "", false
			}
			parts = append(parts, *d.Region)
		case "channel":
			parts = append(parts, d.Channel)
		}
	}
	return strings.Join(parts, "|"), true
}

func mgGroupDocs(docs []esAggDoc, fields ...string) map[string][]esAggDoc {
	out := map[string][]esAggDoc{}
	for _, d := range docs {
		if k, ok := mgGroupKey(d, fields...); ok {
			out[k] = append(out[k], d)
		}
	}
	return out
}

func mgFullMetrics(b agg.Builder[mgRow]) agg.Builder[mgRow] {
	return b.Count("total").Sum("amount", "amount_sum").Avg("amount", "amount_avg").
		Min("amount", "amount_min").Max("amount", "amount_max").
		Sum("quantity", "qty_sum").Min("quantity", "qty_min").
		CountIf("paid_cnt", "status = ?", "paid").SumIf("amount", "paid_sum", "status = ?", "paid")
}

func mgCheckRow(t *testing.T, label string, r *mgRow, e mgExp) {
	t.Helper()
	if r.Total != e.total || r.PaidCnt != e.paidCnt {
		t.Fatalf("%s total=%d/%d paid=%d/%d", label, r.Total, e.total, r.PaidCnt, e.paidCnt)
	}
	ptrAlmostEqual(t, r.Sum, e.sum, 1e-6, label+".sum")
	ptrAlmostEqual(t, r.Avg, e.sum/float64(e.n), 1e-6, label+".avg")
	ptrAlmostEqual(t, r.Min, e.min, 1e-9, label+".min")
	ptrAlmostEqual(t, r.Max, e.max, 1e-9, label+".max")
	ptrAlmostEqual(t, r.QtySum, e.qtySum, 1e-9, label+".qty_sum")
	ptrAlmostEqual(t, r.QtyMin, e.qtyMin, 1e-9, label+".qty_min")
	ptrAlmostEqual(t, r.PaidSum, e.paidSum, 1e-6, label+".paid_sum")
}

func mgb(env *mongoAggEnv) agg.Builder[mgRow] {
	return agg.NewMongoBuilder[mgRow](env.data)
}

// 按 metric 排序并分页后的期望区域顺序
func mgSortedKeys(groups map[string][]esAggDoc, less func(a, b string) bool) []string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
	return keys
}

func TestMongoAggGeneric(t *testing.T) {
	env := setupMongoAgg(t)
	ctx := context.Background()
	all := env.docs

	t.Run("Metrics/Scalar_all", func(t *testing.T) {
		res, err := agg.NewMongoBuilder[mgScalar](env.data).
			Count("total").Sum("amount", "amount_sum").Avg("amount", "amount_avg").
			Min("amount", "amount_min").Max("amount", "amount_max").
			Sum("quantity", "qty_sum").Avg("quantity", "qty_avg").
			Min("created_at", "date_min").Max("created_at", "date_max").
			SetLimit(7).SetStart(3). // 无分组时应被忽略
			Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 1 || len(res.Rows) != 1 {
			t.Fatalf("scalar total=%d rows=%d", res.Total, len(res.Rows))
		}
		r, e := res.Rows[0], mgCompute(all)
		if r.Total != e.total {
			t.Fatalf("total %d/%d", r.Total, e.total)
		}
		ptrAlmostEqual(t, r.Sum, e.sum, 1e-6, "sum")
		ptrAlmostEqual(t, r.Avg, e.sum/float64(e.n), 1e-6, "avg(null skipped)")
		ptrAlmostEqual(t, r.Min, e.min, 0, "min")
		ptrAlmostEqual(t, r.Max, e.max, 0, "max")
		ptrAlmostEqual(t, r.QtySum, e.qtySum, 0, "qty_sum(int)")
		ptrAlmostEqual(t, r.QtyAvg, e.qtySum/float64(e.total), 1e-9, "qty_avg(int)")
		base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		if r.DMin == nil || !r.DMin.Equal(base) || r.DMax == nil || !r.DMax.Equal(base.AddDate(0, 0, 9)) {
			t.Fatalf("date min/max = %v / %v", r.DMin, r.DMax)
		}
	})

	t.Run("GroupBy/Single_full_metrics", func(t *testing.T) {
		res, err := mgFullMetrics(mgb(env).GroupBy("region", "region")).SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exp := mgGroupDocs(all, "region")
		if len(res.Rows) != len(exp) || res.Total != int64(len(exp)) {
			t.Fatalf("rows=%d total=%d want %d", len(res.Rows), res.Total, len(exp))
		}
		for _, r := range res.Rows {
			mgCheckRow(t, r.Region, r, mgCompute(exp[r.Region]))
		}
		// 默认按分组键升序
		if !sort.SliceIsSorted(res.Rows, func(i, j int) bool { return res.Rows[i].Region < res.Rows[j].Region }) {
			t.Fatal("default group order not asc")
		}
	})

	t.Run("GroupBy/Multi_field", func(t *testing.T) {
		res, err := mgFullMetrics(mgb(env).GroupBy("region", "region").GroupByDesc("channel", "channel")).
			SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exp := mgGroupDocs(all, "region", "channel")
		if len(res.Rows) != len(exp) || res.Total != int64(len(exp)) {
			t.Fatalf("rows=%d total=%d want %d", len(res.Rows), res.Total, len(exp))
		}
		for i, r := range res.Rows {
			mgCheckRow(t, r.Region+"|"+r.Channel, r, mgCompute(exp[r.Region+"|"+r.Channel]))
			if i > 0 {
				p := res.Rows[i-1]
				if p.Region > r.Region || (p.Region == r.Region && p.Channel < r.Channel) {
					t.Fatalf("order region asc/channel desc violated at %d", i)
				}
			}
		}
	})

	exp2 := mgGroupDocs(all, "region", "channel")
	key := func(r *mgRow) string { return r.Region + "|" + r.Channel }
	keysOf := func(rows []*mgRow) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, key(r))
		}
		return out
	}
	orderCases := []struct {
		name  string
		apply func(agg.Builder[mgRow]) agg.Builder[mgRow]
		less  func(a, b string) bool
		// proj 只保留显式排序涉及的键：Mongo（与 GORM 相同）不会追加分组字段打破并列，并列行顺序不确定
		proj func(k string) string
	}{
		{"metric_desc", func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.OrderByDesc("amount_sum") },
			func(a, b string) bool { return mgCompute(exp2[a]).sum > mgCompute(exp2[b]).sum }, func(k string) string { return k }},
		{"metric_asc_then_group_desc", func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.OrderBy("total").OrderByDesc("region")
		}, func(a, b string) bool {
			ea, eb := mgCompute(exp2[a]), mgCompute(exp2[b])
			if ea.total != eb.total {
				return ea.total < eb.total
			}
			ra, rb := strings.Split(a, "|")[0], strings.Split(b, "|")[0]
			if ra != rb {
				return ra > rb
			}
			return strings.Split(a, "|")[1] > strings.Split(b, "|")[1]
		}, func(k string) string { return fmt.Sprint(mgCompute(exp2[k]).total) + "|" + strings.Split(k, "|")[0] }},
		{"group_nonprefix_channel_asc", func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.OrderBy("channel") },
			func(a, b string) bool {
				ca, cb := strings.Split(a, "|")[1], strings.Split(b, "|")[1]
				if ca != cb {
					return ca < cb
				}
				return strings.Split(a, "|")[0] < strings.Split(b, "|")[0]
			}, func(k string) string { return strings.Split(k, "|")[1] }},
		{"group_desc_then_metric", func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.OrderByDesc("region").OrderByDesc("amount_max")
		}, func(a, b string) bool {
			ra, rb := strings.Split(a, "|")[0], strings.Split(b, "|")[0]
			if ra != rb {
				return ra > rb
			}
			return mgCompute(exp2[a]).max > mgCompute(exp2[b]).max
		}, func(k string) string { return k }},
	}
	for _, oc := range orderCases {
		oc := oc
		t.Run("Order/"+oc.name, func(t *testing.T) {
			b := mgFullMetrics(mgb(env).GroupBy("region", "region").GroupByDesc("channel", "channel"))
			res, err := oc.apply(b).SetLimit(100).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := mgSortedKeys(exp2, oc.less)
			got := keysOf(res.Rows)
			pg, pw := make([]string, len(got)), make([]string, len(want))
			for i := range got {
				pg[i] = oc.proj(got[i])
			}
			for i := range want {
				pw[i] = oc.proj(want[i])
			}
			if len(got) != len(want) || strings.Join(pg, ",") != strings.Join(pw, ",") {
				t.Fatalf("order\n got=%v\nwant=%v", got, want)
			}
		})
	}

	expR := mgGroupDocs(all, "region")
	havingCases := []struct {
		name string
		h    []agg.Having
		expr func(b agg.Builder[mgRow]) agg.Builder[mgRow]
		pred func(e mgExp) bool
	}{
		{"gt", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.Having("total > ?", 10) }, func(e mgExp) bool { return e.total > 10 }},
		{"gte_float", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.Having("amount_sum >= ?", 2000.0) }, func(e mgExp) bool { return e.sum >= 2000 }},
		{"lt", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.Having("amount_max < ?", 370) }, func(e mgExp) bool { return e.max < 370 }},
		{"lte", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.Having("amount_min <= ?", 10.5) }, func(e mgExp) bool { return e.min <= 10.5 }},
		{"eq", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.Having("total = ?", 12) }, func(e mgExp) bool { return e.total == 12 }},
		{"ne", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.Having("total != ?", 12) }, func(e mgExp) bool { return e.total != 12 }},
		{"and_combo", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.Having("total >= ?", 11).Having("paid_sum > ?", 900)
		}, func(e mgExp) bool { return e.total >= 11 && e.paidSum > 900 }},
		{"AddHaving_struct_int", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.AddHaving(agg.Having{Alias: "qty_sum", Op: agg.Gt, Value: int64(35)})
		}, func(e mgExp) bool { return e.qtySum > 35 }},
		{"none_match", nil, func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.Having("total > ?", 1000) }, func(e mgExp) bool { return false }},
	}
	for _, hc := range havingCases {
		hc := hc
		t.Run("Having/"+hc.name, func(t *testing.T) {
			res, err := hc.expr(mgFullMetrics(mgb(env).GroupBy("region", "region"))).SetNeedTotal(true).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{}
			for k, docs := range expR {
				if hc.pred(mgCompute(docs)) {
					want = append(want, k)
				}
			}
			sort.Strings(want)
			got := []string{}
			for _, r := range res.Rows {
				got = append(got, r.Region)
			}
			if strings.Join(got, ",") != strings.Join(want, ",") || res.Total != int64(len(want)) {
				t.Fatalf("got=%v total=%d want=%v", got, res.Total, want)
			}
		})
	}

	// 分页：组合 region|channel 共 6 组
	pageCases := []struct {
		start, limit uint32
		order        bool
	}{{0, 2, false}, {2, 2, false}, {4, 2, false}, {5, 10, false}, {6, 2, false}, {100, 5, false}, {1, 3, true}, {6, 3, true}}
	for _, pc := range pageCases {
		pc := pc
		t.Run(fmt.Sprintf("Page/start%d_limit%d_metricOrder%v", pc.start, pc.limit, pc.order), func(t *testing.T) {
			b := mgFullMetrics(mgb(env).GroupBy("region", "region").GroupBy("channel", "channel"))
			less := func(a, b string) bool { return a < b }
			if pc.order {
				b = b.OrderByDesc("amount_sum")
				less = func(a, c string) bool { return mgCompute(exp2[a]).sum > mgCompute(exp2[c]).sum }
			}
			res, err := b.SetStart(pc.start).SetLimit(pc.limit).SetNeedTotal(true).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			full := mgSortedKeys(exp2, less)
			lo, hi := int(pc.start), int(pc.start+pc.limit)
			if lo > len(full) {
				lo = len(full)
			}
			if hi > len(full) {
				hi = len(full)
			}
			want := full[lo:hi]
			if got := keysOf(res.Rows); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("got=%v want=%v", got, want)
			}
			if res.Total != int64(len(full)) {
				t.Fatalf("total=%d want %d", res.Total, len(full))
			}
		})
	}

	t.Run("Order/ties_broken_by_group_keys", func(t *testing.T) {
		b := mgFullMetrics(mgb(env).GroupBy("region", "region").GroupBy("channel", "channel")).OrderBy("channel")
		s, err := b.Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		i := strings.Index(s, `"$sort"`)
		sortStage := s[i:]
		for _, next := range []string{`"$skip"`, `"$limit"`, `"$facet"`, `"$project"`} {
			if j := strings.Index(sortStage, next); j >= 0 {
				sortStage = sortStage[:j]
			}
		}
		t.Logf("$sort stage: %s", strings.Join(strings.Fields(sortStage), " "))
		if !strings.Contains(sortStage, `"region"`) {
			t.Fatalf("$sort must append group key tie-breaker: %s", sortStage)
		}
		// 用 Start/Limit=1 翻页 6 次，统计重复/遗漏（并列排序不稳定时可能出现）
		seen := map[string]int{}
		for start := uint32(0); start < 6; start++ {
			res, err := mgFullMetrics(mgb(env).GroupBy("region", "region").GroupBy("channel", "channel")).
				OrderBy("channel").SetStart(start).SetLimit(1).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range res.Rows {
				seen[key(r)]++
			}
		}
		t.Logf("paged with ties: distinct=%d of 6, seen=%v", len(seen), seen)
		if len(seen) != 6 {
			t.Fatalf("tied order paging duplicated/skipped groups: %v", seen)
		}
		for k, n := range seen {
			if n != 1 {
				t.Fatalf("group %s returned %d times", k, n)
			}
		}
	})

	t.Run("Page/NeedTotal_false", func(t *testing.T) {
		res, err := mgb(env).GroupBy("region", "region").Count("total").SetLimit(2).SetNeedTotal(false).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("NeedTotal=false rows=%d total=%d", len(res.Rows), res.Total)
		if len(res.Rows) != 2 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
	})

	t.Run("Page/Limit_default_and_max", func(t *testing.T) {
		b := mgb(env).GroupBy("region", "region").Count("total")
		if _, err := b.Query(ctx); err != nil {
			t.Fatal(err)
		}
		if got := b.Meta().Spec.Limit; got != 100 {
			t.Fatalf("default limit=%d want 100", got)
		}
		_, err := mgb(env).GroupBy("region", "region").Count("total").SetLimit(5001).Query(ctx)
		if !errors.Is(err, agg.ErrLimitExceeded) {
			t.Fatalf("want ErrLimitExceeded got %v", err)
		}
		if _, err := mgb(env).GroupBy("region", "region").Count("total").SetLimit(5000).Query(ctx); err != nil {
			t.Fatalf("limit 5000 should pass: %v", err)
		}
	})

	t.Run("Spec/SetSpec_struct", func(t *testing.T) {
		spec := agg.Spec{
			Groups:  []agg.Group{{Field: "region", Alias: "region"}},
			Metrics: []agg.Metric{{Func: agg.Count, Alias: "total"}, {Func: agg.Sum, Field: "amount", Alias: "amount_sum"}, {Func: agg.Max, Field: "amount", Alias: "amount_max"}},
			Orders:  []agg.Order{{Alias: "amount_sum", Descending: true}},
			Havings: []agg.Having{{Alias: "total", Op: agg.Gte, Value: 11}},
			Start:   1, Limit: 1,
		}
		res, err := mgb(env).SetSpec(spec).SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for k, d := range expR {
			if mgCompute(d).total >= 11 {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return mgCompute(expR[keys[i]]).sum > mgCompute(expR[keys[j]]).sum })
		if len(res.Rows) != 1 || res.Rows[0].Region != keys[1] || res.Total != int64(len(keys)) {
			t.Fatalf("got rows=%v total=%d want %s of %v", res.Rows, res.Total, keys[1], keys)
		}
		spec.Groups[0].Field = "MUTATED" // SetSpec 必须深拷贝
		if res2, err := mgb(env).SetSpec(agg.Spec{Metrics: spec.Metrics[:1]}).Query(ctx); err != nil || res2.Rows[0].Total != 36 {
			t.Fatalf("scalar via SetSpec: %v %v", err, res2)
		}
	})

	t.Run("Spec/ConfigureSpec_SpecBuilder", func(t *testing.T) {
		sb := agg.NewSpecBuilder(agg.Spec{}).GroupBy("region", "region").Count("total").Sum("amount", "amount_sum")
		specA := sb.Spec()
		b := mgb(env).ConfigureSpec(
			func(s *agg.SpecBuilder) { s.SetSpec(specA) },
			nil,
			func(s *agg.SpecBuilder) {
				s.AddMetric(agg.Metric{Func: agg.Avg, Field: "amount", Alias: "amount_avg"}).
					MetricIf(agg.Sum, "amount", "paid_sum", "status IN ?", []string{"paid"}).
					MetricWhere(agg.Count, "", "paid_cnt", agg.Condition{Field: "status", Op: agg.Eq, Value: "paid"}).
					OrderByDesc("amount_avg").SetLimit(2)
			},
		)
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		keys := mgSortedKeys(expR, func(a, c string) bool {
			ea, ec := mgCompute(expR[a]), mgCompute(expR[c])
			return ea.sum/float64(ea.n) > ec.sum/float64(ec.n)
		})[:2]
		for i, r := range res.Rows {
			e := mgCompute(expR[keys[i]])
			if r.Region != keys[i] || r.PaidCnt != e.paidCnt {
				t.Fatalf("row %d %+v want %s", i, r, keys[i])
			}
			ptrAlmostEqual(t, r.PaidSum, e.paidSum, 1e-6, "paid_sum")
			ptrAlmostEqual(t, r.Avg, e.sum/float64(e.n), 1e-6, "avg")
		}
		if len(sb.Spec().Metrics) != 2 {
			t.Fatal("ConfigureSpec mutated external SpecBuilder")
		}
	})

	t.Run("Spec/SetGroups_SetMetrics_SetOrders_SetHavings_replace", func(t *testing.T) {
		b := mgb(env).GroupBy("channel", "channel").Count("x").OrderBy("x").Having("x > ?", 0)
		b.SetGroups(agg.Group{Field: "region", Alias: "region"}).
			SetMetrics(agg.Metric{Func: agg.Count, Alias: "total"}).
			SetOrders(agg.Order{Alias: "region", Descending: true}).
			SetHavings(agg.Having{Alias: "total", Op: agg.Gt, Value: 0})
		res, err := b.Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 3 || res.Rows[0].Region != "west" || res.Rows[0].Channel != "" {
			t.Fatalf("replace semantics: %+v", res.Rows[0])
		}
	})

	// 条件指标的各操作符
	condCases := []struct {
		name string
		mk   func(agg.Builder[mgRow]) agg.Builder[mgRow]
		pred func(d esAggDoc) bool
	}{
		{"in", func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.CountIf("paid_cnt", "customer_id IN ?", []string{"c01", "c02"})
		}, func(d esAggDoc) bool { return d.CustomerID == "c01" || d.CustomerID == "c02" }},
		{"not_in", func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.CountIf("paid_cnt", "channel NOT IN ?", []string{"web"})
		}, func(d esAggDoc) bool { return d.Channel != "web" }},
		{"between", func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.CountIf("paid_cnt", "quantity BETWEEN ? AND ?", agg.Range{Start: 2, End: 4})
		}, func(d esAggDoc) bool { return d.Quantity >= 2 && d.Quantity <= 4 }},
		{"like", func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.CountIf("paid_cnt", "sku LIKE ?", "sku-_") }, func(d esAggDoc) bool { return strings.HasPrefix(d.SKU, "sku-") && len(d.SKU) == 5 }},
		{"not_like", func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.CountIf("paid_cnt", "customer_id NOT LIKE ?", "c0%")
		}, func(d esAggDoc) bool { return !strings.HasPrefix(d.CustomerID, "c0") }},
		{"is_null", func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.CountIf("paid_cnt", "amount IS NULL", nil) }, func(d esAggDoc) bool { return d.Amount == nil }},
		{"exists", func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.CountIf("paid_cnt", "amount EXISTS", nil) }, func(d esAggDoc) bool { return d.Amount != nil }},
		{"gt_date", func(b agg.Builder[mgRow]) agg.Builder[mgRow] {
			return b.CountIf("paid_cnt", "created_at > ?", time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC))
		}, func(d esAggDoc) bool { return d.CreatedAt > "2026-01-05T12:00:00Z" }},
		{"ne", func(b agg.Builder[mgRow]) agg.Builder[mgRow] { return b.CountIf("paid_cnt", "status <> ?", "paid") }, func(d esAggDoc) bool { return d.Status != "paid" }},
	}
	for _, cc := range condCases {
		cc := cc
		t.Run("Cond/"+cc.name, func(t *testing.T) {
			res, err := cc.mk(mgb(env).Count("total")).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var want int64
			for _, d := range all {
				if cc.pred(d) {
					want++
				}
			}
			if res.Rows[0].PaidCnt != want {
				t.Fatalf("got=%d want=%d", res.Rows[0].PaidCnt, want)
			}
		})
	}

	t.Run("Cond/AvgIf_MinIf_MaxIf", func(t *testing.T) {
		res, err := mgb(env).GroupBy("region", "region").
			AvgIf("amount", "amount_avg", "channel = ?", "app").
			MinIf("amount", "amount_min", "channel = ?", "app").
			MaxIf("amount", "amount_max", "quantity >= ?", 3).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Rows {
			app := mgCompute(filterDocs(expR[r.Region], func(d esAggDoc) bool { return d.Channel == "app" }))
			q := mgCompute(filterDocs(expR[r.Region], func(d esAggDoc) bool { return d.Quantity >= 3 }))
			ptrAlmostEqual(t, r.Avg, app.sum/float64(app.n), 1e-6, "avgIf")
			ptrAlmostEqual(t, r.Min, app.min, 0, "minIf")
			ptrAlmostEqual(t, r.Max, q.max, 0, "maxIf")
		}
	})

	// 空/不命中语义
	t.Run("Empty/NoMatch_scalar", func(t *testing.T) {
		res, err := agg.NewMongoBuilder[mgScalar](env.data).
			SetFilter(bson.D{{Key: "status", Value: "nope"}}).
			Count("total").Sum("amount", "amount_sum").Avg("amount", "amount_avg").
			Min("amount", "amount_min").Max("amount", "amount_max").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r := res.Rows[0]
		desc := func(p *float64) string {
			if p == nil {
				return "nil"
			}
			return fmt.Sprint(*p)
		}
		t.Logf("NO-MATCH scalar: total=%d sum=%s avg=%s min=%s max=%s Total=%d", r.Total, desc(r.Sum), desc(r.Avg), desc(r.Min), desc(r.Max), res.Total)
		// README（PR #14）：Mongo 无分组且无匹配文档 → 补零值行，指标为 nil
		if r.Total != 0 || r.Sum != nil || r.Avg != nil || r.Min != nil || r.Max != nil || res.Total != 1 || len(res.Rows) != 1 {
			t.Fatalf("unexpected no-match scalar %+v", r)
		}
	})

	t.Run("Empty/AllNull_field_in_group", func(t *testing.T) {
		// i%9==0 的文档 amount 缺失：筛 id ∈ {9,18,27,36} → 所有 amount 均缺失
		res, err := agg.NewMongoBuilder[mgRow](env.data).SetFilter(bson.D{{Key: "id", Value: bson.D{{Key: "$in", Value: bson.A{9, 18, 27, 36}}}}}).
			GroupBy("channel", "channel").Count("total").Sum("amount", "amount_sum").
			Avg("amount", "amount_avg").Max("amount", "amount_max").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
		for _, r := range res.Rows {
			// README（PR #14）：Mongo 分组内值全为空 → $sum 为 0，Avg/Max 为 nil
			if r.Total == 0 || r.Avg != nil || r.Max != nil || r.Sum == nil || *r.Sum != 0 {
				t.Fatalf("all-null group %+v", r)
			}
			t.Logf("ALL-NULL group %s: total=%d sum=%v avg=nil max=nil", r.Channel, r.Total, *r.Sum)
		}
	})

	t.Run("Empty/NoMatch_grouped", func(t *testing.T) {
		res, err := agg.NewMongoBuilder[mgRow](env.data).SetFilter(bson.D{{Key: "status", Value: "nope"}}).
			GroupBy("region", "region").Count("total").Having("total > ?", 0).OrderByDesc("total").
			SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 0 || res.Total != 0 {
			t.Fatalf("rows=%d total=%d", len(res.Rows), res.Total)
		}
	})

	t.Run("Empty/Nonexistent_field", func(t *testing.T) {
		res, err := agg.NewMongoBuilder[map[string]any](env.data).
			Count("total").Sum("ghost", "s").Avg("ghost", "a").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("unmapped field: %v", *res.Rows[0])
	})

	t.Run("Decode/map_and_types", func(t *testing.T) {
		res, err := agg.NewMongoBuilder[map[string]any](env.data).
			GroupBy("region", "region").GroupByDate("created_at", "day", agg.TimeIntervalMonth).
			Count("total").Sum("quantity", "q").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		row := *res.Rows[0]
		t.Logf("map row=%v (types: total=%T q=%T day=%T)", row, row["total"], row["q"], row["day"])
		if _, ok := row["total"].(int32); !ok {
			t.Fatalf("map total type %T", row["total"])
		}
		if _, ok := row["day"].(bson.DateTime); !ok {
			t.Fatalf("map day type %T", row["day"])
		}
		type intRow struct {
			Region string `json:"region" bson:"region"`
			Total  int    `json:"total" bson:"total"`
			Q      int64  `json:"q" bson:"q"`
			Day    string `json:"day" bson:"day"`
		}
		res2, err := agg.NewMongoBuilder[intRow](env.data).
			GroupBy("region", "region").Count("total").Sum("quantity", "q").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res2.Rows {
			if r.Q != int64(mgCompute(expR[r.Region]).qtySum) {
				t.Fatalf("int decode %+v", r)
			}
		}
		type badRow struct {
			Total string `json:"total" bson:"total"`
		}
		_, err = agg.NewMongoBuilder[badRow](env.data).Count("total").Query(ctx)
		if err == nil || !strings.Contains(err.Error(), "decoding mongodb aggregate result") {
			t.Fatalf("want decode error, got %v", err)
		}
	})

	t.Run("Decode/int_field_float_avg_into_int", func(t *testing.T) {
		type r struct {
			A int `json:"a" bson:"a"`
		}
		_, err := agg.NewMongoBuilder[r](env.data).Avg("quantity", "a").Query(ctx)
		t.Logf("avg(float)->int field: err=%v", err)
		if err == nil {
			t.Fatal("expected unmarshal error for non-integer avg into int")
		}
	})

	// 校验错误
	valCases := []struct {
		name string
		mk   func() agg.Builder[mgRow]
		frag string
	}{
		{"empty_spec", func() agg.Builder[mgRow] { return mgb(env) }, "at least one metric"},
		{"unknown_func", func() agg.Builder[mgRow] { return mgb(env).Metric(agg.Func(99), "amount", "x") }, "metric 0"},
		{"distinct_avg", func() agg.Builder[mgRow] {
			return mgb(env).AddMetric(agg.Metric{Func: agg.Avg, Field: "amount", Alias: "x", Distinct: true})
		}, "distinct is only supported"},
		{"count_with_field", func() agg.Builder[mgRow] { return mgb(env).Metric(agg.Count, "amount", "x") }, "count field must be empty"},
		{"bad_field", func() agg.Builder[mgRow] { return mgb(env).Sum("amount;drop", "x") }, "field"},
		{"dup_alias", func() agg.Builder[mgRow] { return mgb(env).GroupBy("region", "x").Count("X") }, "alias"},
		{"order_unknown_alias", func() agg.Builder[mgRow] { return mgb(env).GroupBy("region", "region").Count("t").OrderBy("nope") }, "order 0"},
		{"dup_order", func() agg.Builder[mgRow] {
			return mgb(env).GroupBy("region", "region").Count("t").OrderBy("t").OrderByDesc("t")
		}, "duplicate"},
		{"having_no_group", func() agg.Builder[mgRow] { return mgb(env).Count("t").Having("t > ?", 1) }, "having requires"},
		{"having_group_alias", func() agg.Builder[mgRow] {
			return mgb(env).GroupBy("region", "region").Count("t").Having("region > ?", 1)
		}, "having 0 alias"},
		{"having_non_numeric", func() agg.Builder[mgRow] { return mgb(env).GroupBy("region", "region").Count("t").Having("t > ?", "x") }, "numeric"},
		{"having_bad_expr", func() agg.Builder[mgRow] {
			return mgb(env).GroupBy("region", "region").Count("t").Having("t LIKE ?", 1)
		}, "having 0"},
		{"cond_in_empty", func() agg.Builder[mgRow] { return mgb(env).CountIf("t", "status IN ?", []string{}) }, "non-empty"},
		{"cond_bad_expr", func() agg.Builder[mgRow] { return mgb(env).CountIf("t", "status = 'x' OR 1=1", nil) }, "condition"},
		{"bad_interval", func() agg.Builder[mgRow] { return mgb(env).GroupByDate("created_at", "d", "fortnight").Count("t") }, "group 0"},
		{"bad_tz", func() agg.Builder[mgRow] {
			return mgb(env).GroupByDateWithTimeZone("created_at", "d", agg.TimeIntervalDay, "Asia/Shang hai").Count("t")
		}, "group 0"},
	}
	for _, vc := range valCases {
		vc := vc
		t.Run("Validate/"+vc.name, func(t *testing.T) {
			b := vc.mk()
			_, err := b.Query(ctx)
			if !errors.Is(err, agg.ErrInvalidSpec) || !strings.Contains(err.Error(), vc.frag) {
				t.Fatalf("want ErrInvalidSpec containing %q, got %v", vc.frag, err)
			}
			if _, eerr := b.Explain(ctx); eerr == nil {
				t.Fatal("Explain should also fail validation")
			}
		})
	}
	t.Run("Validate/errors_joined", func(t *testing.T) {
		_, err := mgb(env).Sum("bad field", "1x").OrderBy("zz").Query(ctx)
		if err == nil || strings.Count(err.Error(), "agg: invalid spec") < 3 {
			t.Fatalf("expected joined errors, got %v", err)
		}
	})

	t.Run("Hooks/Before_After_Middleware_order", func(t *testing.T) {
		type ck struct{}
		var mu sync.Mutex
		var trail []string
		add := func(s string) { mu.Lock(); trail = append(trail, s); mu.Unlock() }
		var afterRes *agg.Result[mgRow]
		b := mgb(env).GroupBy("region", "region").Count("total")
		b.SetBeforeHook(func(c context.Context) context.Context { add("before"); return context.WithValue(c, ck{}, "v") }).
			SetAfterHook(func(c context.Context, r *agg.Result[mgRow], err error) {
				add(fmt.Sprintf("after:%v:%v", c.Value(ck{}), err))
				afterRes = r
			}).
			Use(func(c context.Context, q agg.Querier[mgRow], next agg.Handler[mgRow]) (*agg.Result[mgRow], error) {
				add("m1:" + fmt.Sprint(c.Value(ck{})) + ":" + q.Meta().QueryMode())
				r, err := next(c)
				add("m1-out")
				return r, err
			}).
			Use(nil).
			Use(func(c context.Context, q agg.Querier[mgRow], next agg.Handler[mgRow]) (*agg.Result[mgRow], error) {
				add("m2")
				r, err := next(c)
				if err == nil {
					r.Total += 1000 // 改写结果
				}
				return r, err
			})
		res, err := b.SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := "before,m1:v:aggregate,m2,m1-out,after:v:<nil>"
		if strings.Join(trail, ",") != want {
			t.Fatalf("trail=%v", trail)
		}
		if res.Total != 1003 || afterRes != res {
			t.Fatalf("middleware rewrite total=%d", res.Total)
		}
		if b.Meta().StartTime.IsZero() {
			t.Fatal("StartTime not set")
		}
	})

	t.Run("Hooks/ShortCircuit_and_error_to_after", func(t *testing.T) {
		sentinel := errors.New("blocked")
		var gotErr error
		calls := 0
		b := mgb(env).Count("total")
		b.SetAfterHook(func(_ context.Context, _ *agg.Result[mgRow], err error) { gotErr = err }).
			Use(func(context.Context, agg.Querier[mgRow], agg.Handler[mgRow]) (*agg.Result[mgRow], error) {
				return nil, sentinel
			}).
			Use(func(c context.Context, _ agg.Querier[mgRow], n agg.Handler[mgRow]) (*agg.Result[mgRow], error) {
				calls++
				return n(c)
			})
		if _, err := b.Query(ctx); !errors.Is(err, sentinel) || !errors.Is(gotErr, sentinel) || calls != 0 {
			t.Fatalf("err=%v after=%v calls=%d", err, gotErr, calls)
		}
		// 校验错误在 hook 之前还是之后？记录行为
		var hooked bool
		_, err := mgb(env).SetBeforeHook(func(c context.Context) context.Context { hooked = true; return c }).Query(ctx)
		t.Logf("invalid spec: err=%v beforeHookCalled=%v", err, hooked)
	})

	t.Run("Clone/independent_and_race", func(t *testing.T) {
		var mwCalls sync.Map
		base := mgb(env).GroupBy("region", "region").Count("total").Sum("amount", "amount_sum")
		base.Use(func(c context.Context, q agg.Querier[mgRow], n agg.Handler[mgRow]) (*agg.Result[mgRow], error) {
			mwCalls.Store(fmt.Sprint(len(q.Meta().Spec.Metrics)), true)
			return n(c)
		})
		esb := base.(*agg.MongoBuilder[mgRow])
		var wg sync.WaitGroup
		errs := make(chan error, 32)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := esb.Clone()
				var b agg.Builder[mgRow] = c
				switch i % 4 {
				case 1:
					b = b.Max("amount", "amount_max").OrderByDesc("amount_max")
				case 2:
					b = b.Having("total > ?", 11)
				case 3:
					b = b.SetStart(1).SetLimit(1)
				}
				res, err := b.SetNeedTotal(true).Query(ctx)
				if err != nil {
					errs <- err
					return
				}
				switch i % 4 {
				case 0:
					if len(res.Rows) != 3 {
						errs <- fmt.Errorf("clone0 rows=%d", len(res.Rows))
					}
				case 1:
					if res.Rows[0].Max == nil || res.Rows[0].Max != nil && *res.Rows[0].Max != mgCompute(all).max {
						errs <- fmt.Errorf("clone1 max order wrong")
					}
				case 3:
					if len(res.Rows) != 1 || res.Total != 3 {
						errs <- fmt.Errorf("clone3 page rows=%d total=%d", len(res.Rows), res.Total)
					}
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		if m := esb.Meta().Spec; len(m.Metrics) != 2 || len(m.Havings) != 0 || len(m.Orders) != 0 {
			t.Fatalf("base mutated by clones: %+v", m)
		}
		if _, ok := mwCalls.Load("3"); !ok {
			t.Fatal("middleware not copied into clone")
		}
	})

	t.Run("Distinct/SumDistinct_via_generic_Spec", func(t *testing.T) {
		res, err := mgb(env).SetSpec(agg.Spec{Metrics: []agg.Metric{{Func: agg.Sum, Field: "amount", Alias: "amount_sum", Distinct: true}}}).Query(ctx)
		if err != nil {
			t.Fatalf("SumDistinct: %v", err)
		}
		if len(res.Rows) != 1 || res.Rows[0].Sum == nil || math.Abs(*res.Rows[0].Sum-6048) > 1e-6 {
			t.Fatalf("SumDistinct want 6048, got %+v", res.Rows)
		}
	})
}
