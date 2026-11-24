//go:build integration

// 通用(跨后端) agg 功能在 GORM(MySQL + PostgreSQL) 上的集成测试。种子数据复用 makeAggDocs。
// 运行: GORM_MYSQL_DSN=... GORM_PG_DSN=... go test -tags integration -race -v -run TestGormAggGeneric ./agg
package gormintegration_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"gorm.io/gorm"
)

type ggRow struct {
	Region  string   `json:"region" gorm:"column:region"`
	Channel string   `json:"channel" gorm:"column:channel"`
	Total   int64    `json:"total" gorm:"column:total"`
	Sum     *float64 `json:"amount_sum" gorm:"column:amount_sum"`
	Avg     *float64 `json:"amount_avg" gorm:"column:amount_avg"`
	Min     *float64 `json:"amount_min" gorm:"column:amount_min"`
	Max     *float64 `json:"amount_max" gorm:"column:amount_max"`
	QtySum  *float64 `json:"qty_sum" gorm:"column:qty_sum"`
	QtyMin  *float64 `json:"qty_min" gorm:"column:qty_min"`
	PaidCnt int64    `json:"paid_cnt" gorm:"column:paid_cnt"`
	PaidSum *float64 `json:"paid_sum" gorm:"column:paid_sum"`
}

type ggScalar struct {
	Total  int64      `json:"total" gorm:"column:total"`
	Sum    *float64   `json:"amount_sum" gorm:"column:amount_sum"`
	Avg    *float64   `json:"amount_avg" gorm:"column:amount_avg"`
	Min    *float64   `json:"amount_min" gorm:"column:amount_min"`
	Max    *float64   `json:"amount_max" gorm:"column:amount_max"`
	QtySum *float64   `json:"qty_sum" gorm:"column:qty_sum"`
	QtyAvg *float64   `json:"qty_avg" gorm:"column:qty_avg"`
	DMin   *time.Time `json:"date_min" gorm:"column:date_min"`
	DMax   *time.Time `json:"date_max" gorm:"column:date_max"`
}

type ggExp struct {
	total          int64
	sum            float64
	n              int
	min, max       float64
	qtySum, qtyMin float64
	paidCnt        int64
	paidSum        float64
}

func ggCompute(docs []esAggDoc) ggExp {
	e := ggExp{min: math.Inf(1), max: math.Inf(-1), qtyMin: math.Inf(1)}
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

func ggGroupKey(d esAggDoc, fields ...string) (string, bool) {
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

func ggGroupDocs(docs []esAggDoc, fields ...string) map[string][]esAggDoc {
	out := map[string][]esAggDoc{}
	for _, d := range docs {
		if k, ok := ggGroupKey(d, fields...); ok {
			out[k] = append(out[k], d)
		}
	}
	return out
}

func ggFullMetrics(b agg.Builder[ggRow]) agg.Builder[ggRow] {
	return b.Count("total").Sum("amount", "amount_sum").Avg("amount", "amount_avg").
		Min("amount", "amount_min").Max("amount", "amount_max").
		Sum("quantity", "qty_sum").Min("quantity", "qty_min").
		CountIf("paid_cnt", "status = ?", "paid").SumIf("amount", "paid_sum", "status = ?", "paid")
}

func ggCheckRow(t *testing.T, label string, r *ggRow, e ggExp) {
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

func ggb(env *gormAggEnv) agg.Builder[ggRow] {
	return agg.NewGormBuilder[gaDoc, ggRow](env.data)
}

// 按 metric 排序并分页后的期望区域顺序
func ggSortedKeys(groups map[string][]esAggDoc, less func(a, b string) bool) []string {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool { return less(keys[i], keys[j]) })
	return keys
}

func TestGormAggGeneric(t *testing.T) {
	forEachGormDialect(t, runGormAggGeneric)
}

func runGormAggGeneric(t *testing.T, env *gormAggEnv) {
	ctx := context.Background()
	all := env.docs

	t.Run("Metrics/Scalar_all", func(t *testing.T) {
		res, err := agg.NewGormBuilder[gaDoc, ggScalar](env.data).
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
		r, e := res.Rows[0], ggCompute(all)
		if r.Total != e.total {
			t.Fatalf("total %d/%d", r.Total, e.total)
		}
		ptrAlmostEqual(t, r.Sum, e.sum, 1e-6, "sum")
		ptrAlmostEqual(t, r.Avg, e.sum/float64(e.n), 1e-6, "avg(null skipped)")
		ptrAlmostEqual(t, r.Min, e.min, 0, "min")
		ptrAlmostEqual(t, r.Max, e.max, 0, "max")
		ptrAlmostEqual(t, r.QtySum, e.qtySum, 0, "qty_sum(int)")
		qtol := 1e-9
		if env.dialect == "mysql" {
			qtol = 1e-4 // MySQL AVG(INT) 返回 DECIMAL，小数位受 div_precision_increment=4 限制
			if r.QtyAvg != nil {
				t.Logf("mysql AVG(int) precision: got=%v exact=%v", *r.QtyAvg, e.qtySum/float64(e.total))
			}
		}
		ptrAlmostEqual(t, r.QtyAvg, e.qtySum/float64(e.total), qtol, "qty_avg(int)")
		base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		if r.DMin == nil || !r.DMin.Equal(base) || r.DMax == nil || !r.DMax.Equal(base.AddDate(0, 0, 9)) {
			t.Fatalf("date min/max = %v / %v", r.DMin, r.DMax)
		}
	})

	t.Run("GroupBy/Single_full_metrics", func(t *testing.T) {
		res, err := ggFullMetrics(ggb(env).GroupBy("region", "region")).SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exp := ggGroupDocs(all, "region")
		if len(res.Rows) != len(exp) || res.Total != int64(len(exp)) {
			t.Fatalf("rows=%d total=%d want %d", len(res.Rows), res.Total, len(exp))
		}
		for _, r := range res.Rows {
			ggCheckRow(t, r.Region, r, ggCompute(exp[r.Region]))
		}
		// 默认按分组键升序
		if !sort.SliceIsSorted(res.Rows, func(i, j int) bool { return res.Rows[i].Region < res.Rows[j].Region }) {
			t.Fatal("default group order not asc")
		}
	})

	t.Run("GroupBy/Multi_field", func(t *testing.T) {
		res, err := ggFullMetrics(ggb(env).GroupBy("region", "region").GroupByDesc("channel", "channel")).
			SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exp := ggGroupDocs(all, "region", "channel")
		if len(res.Rows) != len(exp) || res.Total != int64(len(exp)) {
			t.Fatalf("rows=%d total=%d want %d", len(res.Rows), res.Total, len(exp))
		}
		for i, r := range res.Rows {
			ggCheckRow(t, r.Region+"|"+r.Channel, r, ggCompute(exp[r.Region+"|"+r.Channel]))
			if i > 0 {
				p := res.Rows[i-1]
				if p.Region > r.Region || (p.Region == r.Region && p.Channel < r.Channel) {
					t.Fatalf("order region asc/channel desc violated at %d", i)
				}
			}
		}
	})

	exp2 := ggGroupDocs(all, "region", "channel")
	key := func(r *ggRow) string { return r.Region + "|" + r.Channel }
	keysOf := func(rows []*ggRow) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, key(r))
		}
		return out
	}
	orderCases := []struct {
		name  string
		apply func(agg.Builder[ggRow]) agg.Builder[ggRow]
		less  func(a, b string) bool
		// proj 只保留显式排序涉及的键：GORM 不会追加分组字段打破并列，并列行顺序不确定
		proj func(k string) string
	}{
		{"metric_desc", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.OrderByDesc("amount_sum") },
			func(a, b string) bool { return ggCompute(exp2[a]).sum > ggCompute(exp2[b]).sum }, func(k string) string { return k }},
		{"metric_asc_then_group_desc", func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.OrderBy("total").OrderByDesc("region")
		}, func(a, b string) bool {
			ea, eb := ggCompute(exp2[a]), ggCompute(exp2[b])
			if ea.total != eb.total {
				return ea.total < eb.total
			}
			ra, rb := strings.Split(a, "|")[0], strings.Split(b, "|")[0]
			if ra != rb {
				return ra > rb
			}
			return strings.Split(a, "|")[1] > strings.Split(b, "|")[1]
		}, func(k string) string { return fmt.Sprint(ggCompute(exp2[k]).total) + "|" + strings.Split(k, "|")[0] }},
		{"group_nonprefix_channel_asc", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.OrderBy("channel") },
			func(a, b string) bool {
				ca, cb := strings.Split(a, "|")[1], strings.Split(b, "|")[1]
				if ca != cb {
					return ca < cb
				}
				return strings.Split(a, "|")[0] < strings.Split(b, "|")[0]
			}, func(k string) string { return strings.Split(k, "|")[1] }},
		{"group_desc_then_metric", func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.OrderByDesc("region").OrderByDesc("amount_max")
		}, func(a, b string) bool {
			ra, rb := strings.Split(a, "|")[0], strings.Split(b, "|")[0]
			if ra != rb {
				return ra > rb
			}
			return ggCompute(exp2[a]).max > ggCompute(exp2[b]).max
		}, func(k string) string { return k }},
	}
	for _, oc := range orderCases {
		oc := oc
		t.Run("Order/"+oc.name, func(t *testing.T) {
			b := ggFullMetrics(ggb(env).GroupBy("region", "region").GroupByDesc("channel", "channel"))
			res, err := oc.apply(b).SetLimit(100).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := ggSortedKeys(exp2, oc.less)
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

	expR := ggGroupDocs(all, "region")
	havingCases := []struct {
		name string
		h    []agg.Having
		expr func(b agg.Builder[ggRow]) agg.Builder[ggRow]
		pred func(e ggExp) bool
	}{
		{"gt", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.Having("total > ?", 10) }, func(e ggExp) bool { return e.total > 10 }},
		{"gte_float", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.Having("amount_sum >= ?", 2000.0) }, func(e ggExp) bool { return e.sum >= 2000 }},
		{"lt", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.Having("amount_max < ?", 370) }, func(e ggExp) bool { return e.max < 370 }},
		{"lte", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.Having("amount_min <= ?", 10.5) }, func(e ggExp) bool { return e.min <= 10.5 }},
		{"eq", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.Having("total = ?", 12) }, func(e ggExp) bool { return e.total == 12 }},
		{"ne", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.Having("total != ?", 12) }, func(e ggExp) bool { return e.total != 12 }},
		{"and_combo", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.Having("total >= ?", 11).Having("paid_sum > ?", 900)
		}, func(e ggExp) bool { return e.total >= 11 && e.paidSum > 900 }},
		{"AddHaving_struct_int", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.AddHaving(agg.Having{Alias: "qty_sum", Op: agg.Gt, Value: int64(35)})
		}, func(e ggExp) bool { return e.qtySum > 35 }},
		{"none_match", nil, func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.Having("total > ?", 1000) }, func(e ggExp) bool { return false }},
	}
	for _, hc := range havingCases {
		hc := hc
		t.Run("Having/"+hc.name, func(t *testing.T) {
			res, err := hc.expr(ggFullMetrics(ggb(env).GroupBy("region", "region"))).SetNeedTotal(true).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{}
			for k, docs := range expR {
				if hc.pred(ggCompute(docs)) {
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
			b := ggFullMetrics(ggb(env).GroupBy("region", "region").GroupBy("channel", "channel"))
			less := func(a, b string) bool { return a < b }
			if pc.order {
				b = b.OrderByDesc("amount_sum")
				less = func(a, c string) bool { return ggCompute(exp2[a]).sum > ggCompute(exp2[c]).sum }
			}
			res, err := b.SetStart(pc.start).SetLimit(pc.limit).SetNeedTotal(true).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			full := ggSortedKeys(exp2, less)
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

	// 显式 Orders 之后按升序追加未出现的分组键作为决胜键，保证并列时分页稳定
	t.Run("Order/SQL_appends_group_key_tiebreak", func(t *testing.T) {
		s, err := ggFullMetrics(ggb(env).GroupBy("region", "region").GroupBy("channel", "channel")).OrderBy("channel").Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		i := strings.Index(s, "ORDER BY")
		ob := s[i : i+strings.Index(s[i:], "LIMIT")]
		t.Logf("%s: %s", env.dialect, ob)
		q := func(s string) string {
			if env.dialect == "mysql" {
				return "`" + s + "`"
			}
			return `"` + s + `"`
		}
		if want := "ORDER BY " + q("channel") + "," + q("region"); !strings.HasPrefix(strings.TrimSpace(ob), want) || strings.Count(ob, q("channel")) != 1 {
			t.Errorf("ORDER BY must be %q (explicit order then remaining group keys): %s", want, ob)
		}
	})
	for _, ord := range []struct {
		name string
		mk   func(agg.Builder[ggRow]) agg.Builder[ggRow]
	}{
		{"channel_asc", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.OrderBy("channel") }},
		{"paid_cnt_desc", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.OrderByDesc("paid_cnt") }},
		{"qty_min_asc", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.OrderBy("qty_min") }},
	} {
		ord := ord
		t.Run("Paging/tied_order_is_stable_"+ord.name, func(t *testing.T) {
			seen := map[string]int{}
			for start := uint32(0); start < 6; start++ {
				res, err := ord.mk(ggFullMetrics(ggb(env).GroupBy("region", "region").GroupBy("channel", "channel"))).
					SetStart(start).SetLimit(1).Query(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range res.Rows {
					seen[key(r)]++
				}
			}
			t.Logf("%s paged(limit=1): distinct=%d of 6 seen=%v", env.dialect, len(seen), seen)
			if len(seen) != 6 {
				t.Errorf("tied ORDER BY + OFFSET paging duplicated/skipped groups: distinct=%d/6 %v", len(seen), seen)
			}
			for k, n := range seen {
				if n != 1 {
					t.Errorf("group %s returned %d times", k, n)
				}
			}
		})
	}

	t.Run("Page/NeedTotal_false", func(t *testing.T) {
		res, err := ggb(env).GroupBy("region", "region").Count("total").SetLimit(2).SetNeedTotal(false).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("NeedTotal=false rows=%d total=%d", len(res.Rows), res.Total)
		if len(res.Rows) != 2 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
	})

	t.Run("Page/Limit_default_and_max", func(t *testing.T) {
		b := ggb(env).GroupBy("region", "region").Count("total")
		if _, err := b.Query(ctx); err != nil {
			t.Fatal(err)
		}
		if got := b.Meta().Spec.Limit; got != 100 {
			t.Fatalf("default limit=%d want 100", got)
		}
		_, err := ggb(env).GroupBy("region", "region").Count("total").SetLimit(5001).Query(ctx)
		if !errors.Is(err, agg.ErrLimitExceeded) {
			t.Fatalf("want ErrLimitExceeded got %v", err)
		}
		if _, err := ggb(env).GroupBy("region", "region").Count("total").SetLimit(5000).Query(ctx); err != nil {
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
		res, err := ggb(env).SetSpec(spec).SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for k, d := range expR {
			if ggCompute(d).total >= 11 {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return ggCompute(expR[keys[i]]).sum > ggCompute(expR[keys[j]]).sum })
		if len(res.Rows) != 1 || res.Rows[0].Region != keys[1] || res.Total != int64(len(keys)) {
			t.Fatalf("got rows=%v total=%d want %s of %v", res.Rows, res.Total, keys[1], keys)
		}
		spec.Groups[0].Field = "MUTATED" // SetSpec 必须深拷贝
		if res2, err := ggb(env).SetSpec(agg.Spec{Metrics: spec.Metrics[:1]}).Query(ctx); err != nil || res2.Rows[0].Total != 36 {
			t.Fatalf("scalar via SetSpec: %v %v", err, res2)
		}
	})

	t.Run("Spec/ConfigureSpec_SpecBuilder", func(t *testing.T) {
		sb := agg.NewSpecBuilder(agg.Spec{}).GroupBy("region", "region").Count("total").Sum("amount", "amount_sum")
		specA := sb.Spec()
		b := ggb(env).ConfigureSpec(
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
		keys := ggSortedKeys(expR, func(a, c string) bool {
			ea, ec := ggCompute(expR[a]), ggCompute(expR[c])
			return ea.sum/float64(ea.n) > ec.sum/float64(ec.n)
		})[:2]
		for i, r := range res.Rows {
			e := ggCompute(expR[keys[i]])
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
		b := ggb(env).GroupBy("channel", "channel").Count("x").OrderBy("x").Having("x > ?", 0)
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
		mk   func(agg.Builder[ggRow]) agg.Builder[ggRow]
		pred func(d esAggDoc) bool
	}{
		{"in", func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.CountIf("paid_cnt", "customer_id IN ?", []string{"c01", "c02"})
		}, func(d esAggDoc) bool { return d.CustomerID == "c01" || d.CustomerID == "c02" }},
		{"not_in", func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.CountIf("paid_cnt", "channel NOT IN ?", []string{"web"})
		}, func(d esAggDoc) bool { return d.Channel != "web" }},
		{"between", func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.CountIf("paid_cnt", "quantity BETWEEN ? AND ?", agg.Range{Start: 2, End: 4})
		}, func(d esAggDoc) bool { return d.Quantity >= 2 && d.Quantity <= 4 }},
		{"like", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.CountIf("paid_cnt", "sku LIKE ?", "sku-_") }, func(d esAggDoc) bool { return strings.HasPrefix(d.SKU, "sku-") && len(d.SKU) == 5 }},
		{"not_like", func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.CountIf("paid_cnt", "customer_id NOT LIKE ?", "c0%")
		}, func(d esAggDoc) bool { return !strings.HasPrefix(d.CustomerID, "c0") }},
		{"is_null", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.CountIf("paid_cnt", "amount IS NULL", nil) }, func(d esAggDoc) bool { return d.Amount == nil }},
		{"exists", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.CountIf("paid_cnt", "amount EXISTS", nil) }, func(d esAggDoc) bool { return d.Amount != nil }},
		{"gt_date", func(b agg.Builder[ggRow]) agg.Builder[ggRow] {
			return b.CountIf("paid_cnt", "created_at > ?", time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC))
		}, func(d esAggDoc) bool { return d.CreatedAt > "2026-01-05T12:00:00Z" }},
		{"ne", func(b agg.Builder[ggRow]) agg.Builder[ggRow] { return b.CountIf("paid_cnt", "status <> ?", "paid") }, func(d esAggDoc) bool { return d.Status != "paid" }},
	}
	for _, cc := range condCases {
		cc := cc
		t.Run("Cond/"+cc.name, func(t *testing.T) {
			res, err := cc.mk(ggb(env).Count("total")).Query(ctx)
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
		res, err := ggb(env).GroupBy("region", "region").
			AvgIf("amount", "amount_avg", "channel = ?", "app").
			MinIf("amount", "amount_min", "channel = ?", "app").
			MaxIf("amount", "amount_max", "quantity >= ?", 3).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Rows {
			app := ggCompute(filterDocs(expR[r.Region], func(d esAggDoc) bool { return d.Channel == "app" }))
			q := ggCompute(filterDocs(expR[r.Region], func(d esAggDoc) bool { return d.Quantity >= 3 }))
			ptrAlmostEqual(t, r.Avg, app.sum/float64(app.n), 1e-6, "avgIf")
			ptrAlmostEqual(t, r.Min, app.min, 0, "minIf")
			ptrAlmostEqual(t, r.Max, q.max, 0, "maxIf")
		}
	})

	// 空/不命中语义
	t.Run("Empty/NoMatch_scalar", func(t *testing.T) {
		res, err := agg.NewGormBuilder[gaDoc, ggScalar](env.data).
			SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "nope") }).
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
		// README（PR #14）：GORM 无分组且无匹配文档 → 补零值行，指标为 nil
		if r.Total != 0 || r.Sum != nil || r.Avg != nil || r.Min != nil || r.Max != nil || res.Total != 1 || len(res.Rows) != 1 {
			t.Fatalf("unexpected no-match scalar %+v", r)
		}
	})

	t.Run("Empty/AllNull_field_in_group", func(t *testing.T) {
		// i%9==0 的文档 amount 缺失：筛 id ∈ {9,18,27,36} → 所有 amount 均缺失
		res, err := agg.NewGormBuilder[gaDoc, ggRow](env.data).SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("id IN ?", []int{9, 18, 27, 36}) }).
			GroupBy("channel", "channel").Count("total").Sum("amount", "amount_sum").
			Avg("amount", "amount_avg").Max("amount", "amount_max").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
		for _, r := range res.Rows {
			// README（PR #14）：GORM 分组内值全为 NULL → SUM 为 nil，Avg/Max 为 nil
			if r.Total == 0 || r.Avg != nil || r.Max != nil || r.Sum != nil {
				t.Fatalf("all-null group %+v", r)
			}
			t.Logf("ALL-NULL group %s: total=%d sum=nil avg=nil max=nil", r.Channel, r.Total)
		}
	})

	t.Run("Empty/NoMatch_grouped", func(t *testing.T) {
		res, err := agg.NewGormBuilder[gaDoc, ggRow](env.data).SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "nope") }).
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
		_, err := agg.NewGormBuilder[gaDoc, map[string]any](env.data).
			Count("total").Sum("ghost", "s").Avg("ghost", "a").Query(ctx)
		t.Logf("unknown column: %v", err)
		if err == nil || !strings.Contains(err.Error(), "executing gorm aggregate query") {
			t.Fatalf("want SQL error for unknown column, got %v", err)
		}
	})

	t.Run("Decode/map_and_types", func(t *testing.T) {
		res, err := agg.NewGormBuilder[gaDoc, map[string]any](env.data).
			GroupBy("region", "region").GroupByDate("created_at", "day", agg.TimeIntervalMonth).
			Count("total").Sum("quantity", "q").Avg("amount", "a").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
		row := *res.Rows[0]
		t.Logf("%s map row=%v (types: total=%T q=%T a=%T day=%T)", env.dialect, row, row["total"], row["q"], row["a"], row["day"])
		if row["total"] == nil || row["region"] == nil || row["day"] == nil {
			t.Fatalf("map missing keys %v", row)
		}
		if _, ok := row["day"].(time.Time); !ok {
			t.Fatalf("date bucket should decode as time.Time in map rows, got %T", row["day"])
		}
		var mapTotal int64
		for _, r := range res.Rows {
			n, err := strconv.ParseInt(fmt.Sprint((*r)["total"]), 10, 64)
			if err != nil {
				t.Fatalf("total not integer: %v", (*r)["total"])
			}
			mapTotal += n
		}
		if want := int64(len(docsWithRegion(env.docs))); mapTotal != want {
			t.Fatalf("map rows total=%d want %d", mapTotal, want)
		}
		type intRow struct {
			Region string `gorm:"column:region"`
			Total  int    `gorm:"column:total"`
			Q      int64  `gorm:"column:q"`
		}
		res2, err := agg.NewGormBuilder[gaDoc, intRow](env.data).
			GroupBy("region", "region").Count("total").Sum("quantity", "q").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res2.Rows {
			if r.Q != int64(ggCompute(expR[r.Region]).qtySum) || int64(r.Total) != ggCompute(expR[r.Region]).total {
				t.Fatalf("int decode %+v", r)
			}
		}
		type badRow struct {
			Total time.Time `gorm:"column:total"`
		}
		_, err = agg.NewGormBuilder[gaDoc, badRow](env.data).Count("total").Query(ctx)
		t.Logf("count->time.Time: %v", err)
		if err == nil || !strings.Contains(err.Error(), "executing gorm aggregate query") {
			t.Fatalf("want scan error, got %v", err)
		}
	})

	t.Run("Decode/int_field_float_avg_into_int", func(t *testing.T) {
		type r struct {
			A int `json:"a" gorm:"column:a"`
		}
		_, err := agg.NewGormBuilder[gaDoc, r](env.data).Avg("quantity", "a").Query(ctx)
		t.Logf("%s avg(non-integer)->int field: err=%v", env.dialect, err)
		if err == nil {
			t.Fatal("expected scan error for non-integer avg into int")
		}
	})

	// 校验错误
	valCases := []struct {
		name string
		mk   func() agg.Builder[ggRow]
		frag string
	}{
		{"empty_spec", func() agg.Builder[ggRow] { return ggb(env) }, "at least one metric"},
		{"unknown_func", func() agg.Builder[ggRow] { return ggb(env).Metric(agg.Func(99), "amount", "x") }, "metric 0"},
		{"distinct_avg", func() agg.Builder[ggRow] {
			return ggb(env).AddMetric(agg.Metric{Func: agg.Avg, Field: "amount", Alias: "x", Distinct: true})
		}, "distinct is only supported"},
		{"count_with_field", func() agg.Builder[ggRow] { return ggb(env).Metric(agg.Count, "amount", "x") }, "count field must be empty"},
		{"bad_field", func() agg.Builder[ggRow] { return ggb(env).Sum("amount;drop", "x") }, "field"},
		{"dup_alias", func() agg.Builder[ggRow] { return ggb(env).GroupBy("region", "x").Count("X") }, "alias"},
		{"order_unknown_alias", func() agg.Builder[ggRow] { return ggb(env).GroupBy("region", "region").Count("t").OrderBy("nope") }, "order 0"},
		{"dup_order", func() agg.Builder[ggRow] {
			return ggb(env).GroupBy("region", "region").Count("t").OrderBy("t").OrderByDesc("t")
		}, "duplicate"},
		{"having_no_group", func() agg.Builder[ggRow] { return ggb(env).Count("t").Having("t > ?", 1) }, "having requires"},
		{"having_group_alias", func() agg.Builder[ggRow] {
			return ggb(env).GroupBy("region", "region").Count("t").Having("region > ?", 1)
		}, "having 0 alias"},
		{"having_non_numeric", func() agg.Builder[ggRow] { return ggb(env).GroupBy("region", "region").Count("t").Having("t > ?", "x") }, "numeric"},
		{"having_bad_expr", func() agg.Builder[ggRow] {
			return ggb(env).GroupBy("region", "region").Count("t").Having("t LIKE ?", 1)
		}, "having 0"},
		{"cond_in_empty", func() agg.Builder[ggRow] { return ggb(env).CountIf("t", "status IN ?", []string{}) }, "non-empty"},
		{"cond_bad_expr", func() agg.Builder[ggRow] { return ggb(env).CountIf("t", "status = 'x' OR 1=1", nil) }, "condition"},
		{"bad_interval", func() agg.Builder[ggRow] { return ggb(env).GroupByDate("created_at", "d", "fortnight").Count("t") }, "group 0"},
		{"bad_tz", func() agg.Builder[ggRow] {
			return ggb(env).GroupByDateWithTimeZone("created_at", "d", agg.TimeIntervalDay, "Asia/Shang hai").Count("t")
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
		_, err := ggb(env).Sum("bad field", "1x").OrderBy("zz").Query(ctx)
		if err == nil || strings.Count(err.Error(), "agg: invalid spec") < 3 {
			t.Fatalf("expected joined errors, got %v", err)
		}
	})

	t.Run("Hooks/Before_After_Middleware_order", func(t *testing.T) {
		type ck struct{}
		var mu sync.Mutex
		var trail []string
		add := func(s string) { mu.Lock(); trail = append(trail, s); mu.Unlock() }
		var afterRes *agg.Result[ggRow]
		b := ggb(env).GroupBy("region", "region").Count("total")
		b.SetBeforeHook(func(c context.Context) context.Context { add("before"); return context.WithValue(c, ck{}, "v") }).
			SetAfterHook(func(c context.Context, r *agg.Result[ggRow], err error) {
				add(fmt.Sprintf("after:%v:%v", c.Value(ck{}), err))
				afterRes = r
			}).
			Use(func(c context.Context, q agg.Querier[ggRow], next agg.Handler[ggRow]) (*agg.Result[ggRow], error) {
				add("m1:" + fmt.Sprint(c.Value(ck{})) + ":" + q.Meta().QueryMode())
				r, err := next(c)
				add("m1-out")
				return r, err
			}).
			Use(nil).
			Use(func(c context.Context, q agg.Querier[ggRow], next agg.Handler[ggRow]) (*agg.Result[ggRow], error) {
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
		b := ggb(env).Count("total")
		b.SetAfterHook(func(_ context.Context, _ *agg.Result[ggRow], err error) { gotErr = err }).
			Use(func(context.Context, agg.Querier[ggRow], agg.Handler[ggRow]) (*agg.Result[ggRow], error) {
				return nil, sentinel
			}).
			Use(func(c context.Context, _ agg.Querier[ggRow], n agg.Handler[ggRow]) (*agg.Result[ggRow], error) {
				calls++
				return n(c)
			})
		if _, err := b.Query(ctx); !errors.Is(err, sentinel) || !errors.Is(gotErr, sentinel) || calls != 0 {
			t.Fatalf("err=%v after=%v calls=%d", err, gotErr, calls)
		}
		// 校验错误在 hook 之前还是之后？记录行为
		var hooked bool
		_, err := ggb(env).SetBeforeHook(func(c context.Context) context.Context { hooked = true; return c }).Query(ctx)
		t.Logf("invalid spec: err=%v beforeHookCalled=%v", err, hooked)
	})

	t.Run("Clone/independent_and_race", func(t *testing.T) {
		var mwCalls sync.Map
		base := ggb(env).GroupBy("region", "region").Count("total").Sum("amount", "amount_sum")
		base.Use(func(c context.Context, q agg.Querier[ggRow], n agg.Handler[ggRow]) (*agg.Result[ggRow], error) {
			mwCalls.Store(fmt.Sprint(len(q.Meta().Spec.Metrics)), true)
			return n(c)
		})
		esb := base.(*agg.GormBuilder[gaDoc, ggRow])
		var wg sync.WaitGroup
		errs := make(chan error, 32)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c := esb.Clone()
				var b agg.Builder[ggRow] = c
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
					if res.Rows[0].Max == nil || res.Rows[0].Max != nil && *res.Rows[0].Max != ggCompute(all).max {
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
		res, err := ggb(env).SetSpec(agg.Spec{Metrics: []agg.Metric{{Func: agg.Sum, Field: "amount", Alias: "amount_sum", Distinct: true}}}).Query(ctx)
		if err != nil {
			t.Fatalf("SumDistinct: %v", err)
		}
		if len(res.Rows) != 1 || res.Rows[0].Sum == nil || math.Abs(*res.Rows[0].Sum-6048) > 1e-6 {
			t.Fatalf("SumDistinct want 6048, got %+v", res.Rows)
		}
	})
}
