//go:build integration

// GORM 后端专属 agg 集成测试（MySQL + PostgreSQL）。未设置 GORM_MYSQL_DSN / GORM_PG_DSN 时对应方言跳过。
// 运行:
//
//	GORM_MYSQL_DSN='qb:qbpass@tcp(127.0.0.1:3306)/qbtest?charset=utf8mb4&parseTime=True&loc=UTC' \
//	GORM_PG_DSN='host=127.0.0.1 user=box dbname=qbtest port=5432 sslmode=disable TimeZone=UTC' \
//	go test -tags integration -race -v -run 'TestGormAgg' ./agg
package gormintegration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// gaDoc 为 agg 测试种子表模型
type gaDoc struct {
	ID         int       `gorm:"column:id;primaryKey;autoIncrement:false"`
	Region     *string   `gorm:"column:region;size:16"`
	Channel    string    `gorm:"column:channel;size:16"`
	Status     string    `gorm:"column:status;size:16"`
	CustomerID string    `gorm:"column:customer_id;size:16"`
	Amount     *float64  `gorm:"column:amount"`
	Quantity   int       `gorm:"column:quantity"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	Bio        string    `gorm:"column:bio;size:64"`
	SKU        string    `gorm:"column:sku;size:16"`
}

func (gaDoc) TableName() string { return "qb_agg_orders" }

type gaCustomer struct {
	CustomerID string `gorm:"column:customer_id;primaryKey;size:16"`
	Tier       string `gorm:"column:tier;size:16"`
}

func (gaCustomer) TableName() string { return "qb_agg_customers" }

type gaNull struct {
	ID int      `gorm:"column:id;primaryKey;autoIncrement:false"`
	G  string   `gorm:"column:g;size:8"`
	V  *float64 `gorm:"column:v"`
}

func (gaNull) TableName() string { return "qb_agg_nulls" }

type gaMissing struct{ ID int }

func (gaMissing) TableName() string { return "qb_agg_does_not_exist" }

type gormAggEnv struct {
	dialect string
	db      *gorm.DB
	data    *core.DBProxy
	docs    []esAggDoc
}

func gaTier(c string) string {
	if c <= "c03" {
		return "gold"
	}
	return "basic"
}

func openGormAggDB(t *testing.T, dialect, dsn string) *gorm.DB {
	t.Helper()
	var d gorm.Dialector
	if dialect == "mysql" {
		d = mysql.Open(dsn)
	} else {
		d = postgres.Open(dsn)
	}
	db, err := gorm.Open(d, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("%s open: %v", dialect, err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(10) // 测试环境 mysqld max_connections=30
	}
	return db
}

func setupGormAgg(t *testing.T, dialect, dsn string) *gormAggEnv {
	t.Helper()
	db := openGormAggDB(t, dialect, dsn)
	m := db.Migrator()
	_ = m.DropTable(&gaDoc{}, &gaCustomer{}, &gaNull{})
	if err := db.AutoMigrate(&gaDoc{}, &gaCustomer{}, &gaNull{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Migrator().DropTable(&gaDoc{}, &gaCustomer{}, &gaNull{})
		if s, err := db.DB(); err == nil {
			_ = s.Close()
		}
	})
	docs := makeAggDocs()
	rows := make([]gaDoc, 0, len(docs))
	custs := map[string]bool{}
	for _, d := range docs {
		ts, _ := time.Parse(time.RFC3339, d.CreatedAt)
		rows = append(rows, gaDoc{ID: d.ID, Region: d.Region, Channel: d.Channel, Status: d.Status, CustomerID: d.CustomerID,
			Amount: d.Amount, Quantity: d.Quantity, CreatedAt: ts.UTC(), Bio: d.Bio, SKU: d.SKU})
		custs[d.CustomerID] = true
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	for c := range custs {
		if err := db.Create(&gaCustomer{CustomerID: c, Tier: gaTier(c)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	f := func(v float64) *float64 { return &v }
	nulls := []gaNull{{1, "a", f(1)}, {2, "a", f(1.0)}, {3, "a", nil}, {4, "a", nil}, {5, "b", nil}, {6, "b", nil}}
	if err := db.Create(&nulls).Error; err != nil {
		t.Fatal(err)
	}
	return &gormAggEnv{dialect: dialect, db: db, docs: docs, data: core.NewDBProxyWithAdapters(core.NewGormAdapter(db))}
}

func forEachGormDialect(t *testing.T, fn func(t *testing.T, env *gormAggEnv)) {
	my, pg := os.Getenv("GORM_MYSQL_DSN"), os.Getenv("GORM_PG_DSN")
	if my == "" && pg == "" {
		t.Skip("GORM_MYSQL_DSN / GORM_PG_DSN unset")
	}
	for _, c := range []struct{ d, dsn string }{{"mysql", my}, {"postgres", pg}} {
		c := c
		t.Run(c.d, func(t *testing.T) {
			if c.dsn == "" {
				t.Skipf("%s DSN unset", c.d)
			}
			fn(t, setupGormAgg(t, c.d, c.dsn))
		})
	}
}

type gRegionRow struct {
	Region    string   `gorm:"column:region"`
	Tier      string   `gorm:"column:tier"`
	Total     int64    `gorm:"column:total"`
	Cust      int64    `gorm:"column:cust"`
	QtyD      *float64 `gorm:"column:qty_d"`
	Paid      *float64 `gorm:"column:paid_sum"`
	PaidD     *float64 `gorm:"column:paid_sd"`
	PaidCnt   int64    `gorm:"column:paid_cnt"`
	PaidCustD int64    `gorm:"column:paid_cd"`
	AvgIf     *float64 `gorm:"column:avg_if"`
	None      *float64 `gorm:"column:none_sum"`
	NoneD     *float64 `gorm:"column:none_sd"`
	NoneAvg   *float64 `gorm:"column:none_avg"`
	NoneCnt   int64    `gorm:"column:none_cnt"`
}

type gDayRow struct {
	Day   string `gorm:"column:day"`
	Total int64  `gorm:"column:total"`
}

type gDayTimeRow struct {
	Day   time.Time `gorm:"column:day"`
	Total int64     `gorm:"column:total"`
}

func gNormDay(v any) string {
	switch x := v.(type) {
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	case []byte:
		return gNormDay(string(x))
	case string:
		if tt, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return tt.Format("2006-01-02 15:04:05")
		}
		if len(x) == 10 {
			return x + " 00:00:00"
		}
		return x
	}
	return fmt.Sprintf("%T:%v", v, v)
}

func gBucket(ts time.Time, loc *time.Location, iv agg.TimeInterval) string {
	w := ts.In(loc)
	y, m, d := w.Date()
	var b time.Time
	switch iv {
	case agg.TimeIntervalMinute:
		b = time.Date(y, m, d, w.Hour(), w.Minute(), 0, 0, time.UTC)
	case agg.TimeIntervalHour:
		b = time.Date(y, m, d, w.Hour(), 0, 0, 0, time.UTC)
	case agg.TimeIntervalDay:
		b = time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	case agg.TimeIntervalWeek:
		b = time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -((int(w.Weekday()) + 6) % 7))
	case agg.TimeIntervalMonth:
		b = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	case agg.TimeIntervalQuarter:
		b = time.Date(y, time.Month((int(m)-1)/3*3+1), 1, 0, 0, 0, 0, time.UTC)
	case agg.TimeIntervalYear:
		b = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return b.Format("2006-01-02 15:04:05")
}

func TestGormAggIntegration(t *testing.T) {
	forEachGormDialect(t, runGormAggSpecific)
}

func runGormAggSpecific(t *testing.T, env *gormAggEnv) {
	ctx := context.Background()
	all := env.docs
	q := "`"
	if env.dialect == "postgres" {
		q = `"`
	}
	nb := func() agg.Builder[gRegionRow] { return agg.NewGormBuilder[gaDoc, gRegionRow](env.data) }
	byRegion := mgGroupDocsCompat(all)

	t.Run("Explain/SQL_quoting_case_distinct_notnull", func(t *testing.T) {
		s, err := nb().GroupBy("region", "region").Count("total").CountDistinct("customer_id", "cust").
			SumIf("amount", "paid_sum", "status = ?", "paid").SumDistinctIf("amount", "paid_sd", "status = ?", "paid").
			Having("total > ?", 1).OrderByDesc("total").SetStart(1).SetLimit(2).Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s explain: %s", env.dialect, s)
		for _, frag := range []string{
			"COUNT(DISTINCT " + q + "customer_id" + q + ")",
			"CASE WHEN " + q + "status" + q + " = ", " THEN " + q + "amount" + q + " ELSE 0 END",
			"SUM(DISTINCT CASE WHEN", q + "region" + q + " IS NOT NULL", "GROUP BY " + q + "region" + q,
			"HAVING COUNT(*) > ", "ORDER BY " + q + "total" + q + " DESC", "LIMIT ", "OFFSET ",
			`plan: {"flags":`, "args: [paid, paid, 1, 2, 1]",
		} {
			if !strings.Contains(s, frag) {
				t.Errorf("explain missing %q", frag)
			}
		}
	})

	t.Run("Explain/scalar_no_group_no_order_limit", func(t *testing.T) {
		s, err := nb().Count("total").SetLimit(5).SetStart(2).Explain(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(s, "LIMIT") || strings.Contains(s, "GROUP BY") || strings.Contains(s, "ORDER BY") {
			t.Fatalf("scalar should not page/group: %s", s)
		}
	})

	t.Run("SetFilter/where_scopes", func(t *testing.T) {
		paidScope := func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "paid") }
		res, err := agg.NewGormBuilder[gaDoc, gRegionRow](env.data).SetFilter(func(db *gorm.DB) *gorm.DB {
			return db.Scopes(paidScope).Where("quantity >= ?", 3)
		}).GroupBy("region", "region").Count("total").SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		exp := map[string]int64{}
		for _, d := range all {
			if d.Status == "paid" && d.Quantity >= 3 && d.Region != nil {
				exp[*d.Region]++
			}
		}
		if len(res.Rows) != len(exp) || res.Total != int64(len(exp)) {
			t.Fatalf("rows=%d total=%d want %d", len(res.Rows), res.Total, len(exp))
		}
		for _, r := range res.Rows {
			if r.Total != exp[r.Region] {
				t.Fatalf("%s %d/%d", r.Region, r.Total, exp[r.Region])
			}
		}
	})

	t.Run("SetFilter_join_group_by_joined_column", func(t *testing.T) {
		res, err := agg.NewGormBuilder[gaDoc, gRegionRow](env.data).SetFilter(func(db *gorm.DB) *gorm.DB {
			return db.Joins("JOIN qb_agg_customers c ON c.customer_id = qb_agg_orders.customer_id").Where("qb_agg_orders.status = ?", "paid")
		}).GroupBy("c.tier", "tier").Count("total").SumIf("qb_agg_orders.amount", "paid_sum", "c.tier = ?", "gold").
			CountDistinct("qb_agg_orders.customer_id", "cust").SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		type e struct {
			n, cust int64
			s       float64
		}
		exp := map[string]*e{}
		cs := map[string]map[string]bool{}
		for _, d := range all {
			if d.Status != "paid" {
				continue
			}
			tr := gaTier(d.CustomerID)
			if exp[tr] == nil {
				exp[tr], cs[tr] = &e{}, map[string]bool{}
			}
			exp[tr].n++
			cs[tr][d.CustomerID] = true
			if tr == "gold" && d.Amount != nil {
				exp[tr].s += *d.Amount
			}
		}
		if len(res.Rows) != len(exp) || res.Total != int64(len(exp)) {
			t.Fatalf("rows=%+v", res.Rows)
		}
		for _, r := range res.Rows {
			x := exp[r.Tier]
			if x == nil || r.Total != x.n || r.Cust != int64(len(cs[r.Tier])) {
				t.Fatalf("tier %s %+v", r.Tier, r)
			}
			ptrAlmostEqual(t, r.Paid, x.s, 1e-6, r.Tier+".sumIf")
		}
	})

	t.Run("Distinct/exact_count_and_sum", func(t *testing.T) {
		res, err := nb().GroupBy("region", "region").CountDistinct("customer_id", "cust").SumDistinct("quantity", "qty_d").
			CountDistinctIf("customer_id", "paid_cd", "status = ?", "paid").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 3 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
		for _, r := range res.Rows {
			docs := byRegion[r.Region]
			qs := map[int]bool{}
			pc := map[string]bool{}
			for _, d := range docs {
				qs[d.Quantity] = true
				if d.Status == "paid" {
					pc[d.CustomerID] = true
				}
			}
			var qd float64
			for v := range qs {
				qd += float64(v)
			}
			if r.Cust != int64(uniqueCustomers(docs)) || r.PaidCustD != int64(len(pc)) {
				t.Fatalf("%s cust=%d/%d paidcd=%d/%d", r.Region, r.Cust, uniqueCustomers(docs), r.PaidCustD, len(pc))
			}
			ptrAlmostEqual(t, r.QtyD, qd, 0, r.Region+".qty_distinct")
		}
	})

	t.Run("Conditional/CASE_WHEN_ELSE0_and_empty_semantics", func(t *testing.T) {
		res, err := nb().GroupBy("region", "region").
			SumIf("amount", "paid_sum", "status = ?", "paid").
			SumDistinctIf("amount", "paid_sd", "status = ?", "paid").
			CountIf("paid_cnt", "status = ?", "paid").
			AvgIf("amount", "avg_if", "quantity >= ?", 3).
			SumIf("amount", "none_sum", "status = ?", "nope").
			SumDistinctIf("amount", "none_sd", "status = ?", "nope").
			AvgIf("amount", "none_avg", "status = ?", "nope").
			CountIf("none_cnt", "status = ?", "nope").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Rows {
			docs := byRegion[r.Region]
			paid := filterDocs(docs, func(d esAggDoc) bool { return d.Status == "paid" })
			q3 := filterDocs(docs, func(d esAggDoc) bool { return d.Quantity >= 3 })
			ptrAlmostEqual(t, r.Paid, sumF(amounts(paid)), 1e-6, r.Region+".sumIf")
			ptrAlmostEqual(t, r.PaidD, uniqueAmounts(paid), 1e-6, r.Region+".sumDistinctIf")
			ptrAlmostEqual(t, r.AvgIf, sumF(amounts(q3))/float64(len(amounts(q3))), 1e-6, r.Region+".avgIf")
			if r.PaidCnt != int64(len(paid)) {
				t.Fatalf("countIf %d", r.PaidCnt)
			}
			// PR #14: SumIf 有行但无匹配 → 0 (ELSE 0)；SumDistinctIf → nil；AvgIf → nil；CountIf → 0
			if r.None == nil || *r.None != 0 || r.NoneD != nil || r.NoneAvg != nil || r.NoneCnt != 0 {
				t.Fatalf("%s none-match: sum=%v sd=%v avg=%v cnt=%d", r.Region, r.None, r.NoneD, r.NoneAvg, r.NoneCnt)
			}
		}
	})

	// 日期分组：期望值在 Go 端按时区换算
	dateCases := []struct {
		name string
		iv   agg.TimeInterval
		tz   string
	}{
		{"day_UTC", agg.TimeIntervalDay, ""},
		{"day_Kiritimati", agg.TimeIntervalDay, "Pacific/Kiritimati"},
		{"day_LA", agg.TimeIntervalDay, "America/Los_Angeles"},
		{"hour_Kolkata", agg.TimeIntervalHour, "Asia/Kolkata"},
		{"week_UTC", agg.TimeIntervalWeek, ""},
		{"week_Shanghai", agg.TimeIntervalWeek, "Asia/Shanghai"},
		{"month_UTC", agg.TimeIntervalMonth, ""},
		{"quarter_UTC", agg.TimeIntervalQuarter, ""},
		{"quarter_LA", agg.TimeIntervalQuarter, "America/Los_Angeles"},
		{"year_Tokyo", agg.TimeIntervalYear, "Asia/Tokyo"},
	}
	for _, dc := range dateCases {
		dc := dc
		t.Run("DateGroup/"+dc.name, func(t *testing.T) {
			b := agg.NewGormBuilder[gaDoc, gDayRow](env.data)
			var bb agg.Builder[gDayRow]
			if dc.tz == "" {
				bb = b.GroupByDate("created_at", "day", dc.iv)
			} else {
				bb = b.GroupByDateWithTimeZone("created_at", "day", dc.iv, dc.tz)
			}
			res, err := bb.Count("total").SetNeedTotal(true).Query(ctx)
			if err != nil {
				t.Fatal(err)
			}
			loc := time.UTC
			if dc.tz != "" {
				loc, _ = time.LoadLocation(dc.tz)
			}
			exp := map[string]int64{}
			for _, d := range all {
				ts, _ := time.Parse(time.RFC3339, d.CreatedAt)
				exp[gBucket(ts, loc, dc.iv)]++
			}
			got := map[string]int64{}
			var types []string
			for _, r := range res.Rows {
				got[gNormDay(r.Day)] += r.Total
				types = append(types, fmt.Sprintf("%T", r.Day))
			}
			keys := func(m map[string]int64) string {
				out := []string{}
				for k, v := range m {
					out = append(out, fmt.Sprintf("%s=%d", k, v))
				}
				sort.Strings(out)
				return strings.Join(out, ",")
			}
			if keys(got) != keys(exp) || res.Total != int64(len(exp)) {
				t.Fatalf("%s buckets\n got=%s (total=%d types=%v)\nwant=%s", env.dialect, keys(got), res.Total, types[:1], keys(exp))
			}
			// 默认按分组键升序
			for i := 1; i < len(res.Rows); i++ {
				if gNormDay(res.Rows[i-1].Day) > gNormDay(res.Rows[i].Day) {
					t.Fatalf("not ascending")
				}
			}
		})
	}

	t.Run("DateGroup/decode_into_time.Time_all_intervals", func(t *testing.T) {
		loc, _ := time.LoadLocation("Asia/Shanghai")
		for _, iv := range []agg.TimeInterval{agg.TimeIntervalMinute, agg.TimeIntervalHour, agg.TimeIntervalDay, agg.TimeIntervalWeek, agg.TimeIntervalMonth, agg.TimeIntervalQuarter, agg.TimeIntervalYear} {
			res, err := agg.NewGormBuilder[gaDoc, gDayTimeRow](env.data).
				GroupByDateWithTimeZone("created_at", "day", iv, "Asia/Shanghai").Count("total").SetLimit(1000).Query(ctx)
			if err != nil {
				t.Errorf("%s interval %s → time.Time decode failed: %v", env.dialect, iv, err)
				continue
			}
			want := map[string]int64{}
			for _, d := range env.docs {
				ts, _ := time.Parse(time.RFC3339, d.CreatedAt)
				want[gBucket(ts, loc, iv)]++
			}
			got := map[string]int64{}
			for _, r := range res.Rows {
				got[r.Day.Format("2006-01-02 15:04:05")] = r.Total
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s interval %s buckets\n got=%v\nwant=%v", env.dialect, iv, got, want)
			}
		}
	})

	t.Run("DateGroup/iso_week_year_boundary", func(t *testing.T) {
		// 与 PostgreSQL DATE_TRUNC('week') 对齐：ISO 周一开始，跨年周归属上一年的周一
		cases := map[string]string{
			"2025-12-28 12:00:00": "2025-12-22 00:00:00", // 周日
			"2025-12-29 00:00:00": "2025-12-29 00:00:00", // 周一
			"2026-01-01 08:00:00": "2025-12-29 00:00:00", // 周四，ISO 2026-W01
			"2026-01-04 23:59:59": "2025-12-29 00:00:00", // 周日
			"2026-01-05 00:00:00": "2026-01-05 00:00:00", // 周一
			"2020-12-31 10:00:00": "2020-12-28 00:00:00", // ISO 2020-W53
			"2021-01-03 10:00:00": "2020-12-28 00:00:00",
		}
		type gaWeek struct {
			ID        int       `gorm:"column:id;primaryKey"`
			CreatedAt time.Time `gorm:"column:created_at"`
		}
		if err := env.db.Migrator().DropTable("qb_agg_weeks"); err != nil {
			t.Fatal(err)
		}
		if err := env.db.Table("qb_agg_weeks").AutoMigrate(&gaWeek{}); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = env.db.Migrator().DropTable("qb_agg_weeks") }()
		want := map[string]int64{}
		id := 0
		for in, bucket := range cases {
			id++
			ts, _ := time.Parse("2006-01-02 15:04:05", in)
			if err := env.db.Table("qb_agg_weeks").Create(&gaWeek{ID: id, CreatedAt: ts}).Error; err != nil {
				t.Fatal(err)
			}
			want[bucket]++
		}
		res, err := agg.NewGormBuilder[gaDoc, gDayTimeRow](env.data).
			SetFilter(func(db *gorm.DB) *gorm.DB { return db.Table("qb_agg_weeks") }).
			GroupByDate("created_at", "day", agg.TimeIntervalWeek).Count("total").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, r := range res.Rows {
			got[r.Day.Format("2006-01-02 15:04:05")] = r.Total
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s ISO week buckets\n got=%v\nwant=%v", env.dialect, got, want)
		}
	})

	t.Run("DateGroup/session_time_zone_independence", func(t *testing.T) {
		// 同一数据，用不同会话时区（MySQL time_zone / PG TimeZone）跑 day@Asia/Shanghai，结果应一致
		var dsn string
		if env.dialect == "mysql" {
			dsn = os.Getenv("GORM_MYSQL_DSN") + "&time_zone=%27America%2FNew_York%27"
		} else {
			dsn = strings.Replace(os.Getenv("GORM_PG_DSN"), "TimeZone=UTC", "TimeZone=America/New_York", 1)
		}
		db2 := openGormAggDB(t, env.dialect, dsn)
		defer func() { s, _ := db2.DB(); _ = s.Close() }()
		run := func(p *core.DBProxy) string {
			res, err := agg.NewGormBuilder[gaDoc, gDayRow](p).GroupByDateWithTimeZone("created_at", "day", agg.TimeIntervalHour, "Asia/Shanghai").Count("total").Query(ctx)
			if err != nil {
				return "ERR " + err.Error()
			}
			out := []string{}
			for _, r := range res.Rows {
				out = append(out, gNormDay(r.Day))
			}
			return strings.Join(out, ",")
		}
		a, b := run(env.data), run(core.NewDBProxyWithAdapters(core.NewGormAdapter(db2)))
		if a != b {
			t.Errorf("%s session tz changes buckets:\n UTC session=%s\n NY  session=%s", env.dialect, a, b)
		}
	})

	t.Run("Null/vs_empty_sums_PR14", func(t *testing.T) {
		type nr struct {
			G      string   `gorm:"column:g"`
			N      int64    `gorm:"column:n"`
			S      *float64 `gorm:"column:s"`
			A      *float64 `gorm:"column:a"`
			Mx     *float64 `gorm:"column:mx"`
			SD     *float64 `gorm:"column:sd"`
			CD     int64    `gorm:"column:cd"`
			Nulls  int64    `gorm:"column:nulls"`
			Exists int64    `gorm:"column:exists"`
			SI     *float64 `gorm:"column:si"`
		}
		nbn := func() agg.Builder[nr] { return agg.NewGormBuilder[gaNull, nr](env.data) }
		res, err := nbn().GroupBy("g", "g").Count("n").Sum("v", "s").Avg("v", "a").Max("v", "mx").SumDistinct("v", "sd").
			CountDistinct("v", "cd").CountIf("nulls", "v IS NULL", nil).CountIf("exists", "v EXISTS", nil).
			SumIf("v", "si", "g = ?", "zzz").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 2 {
			t.Fatalf("rows=%d", len(res.Rows))
		}
		a, b := res.Rows[0], res.Rows[1]
		f := func(p *float64) string {
			if p == nil {
				return "nil"
			}
			return fmt.Sprint(*p)
		}
		t.Logf("%s a: n=%d s=%s a=%s mx=%s sd=%s cd=%d nulls=%d exists=%d si=%s", env.dialect, a.N, f(a.S), f(a.A), f(a.Mx), f(a.SD), a.CD, a.Nulls, a.Exists, f(a.SI))
		t.Logf("%s b: n=%d s=%s a=%s mx=%s sd=%s cd=%d nulls=%d exists=%d si=%s", env.dialect, b.N, f(b.S), f(b.A), f(b.Mx), f(b.SD), b.CD, b.Nulls, b.Exists, f(b.SI))
		if a.N != 4 || f(a.S) != "2" || f(a.A) != "1" || f(a.SD) != "1" || a.CD != 1 || a.Nulls != 2 || a.Exists != 2 || f(a.SI) != "0" {
			t.Fatalf("group a wrong")
		}
		// PR #14: GORM SUM 全 NULL → nil；SumDistinct → nil；SumIf 有行无匹配 → 0
		if b.N != 2 || b.S != nil || b.A != nil || b.Mx != nil || b.SD != nil || b.CD != 0 || b.Nulls != 2 || b.Exists != 0 || f(b.SI) != "0" {
			t.Fatalf("group b (all NULL) wrong")
		}
		// 无分组 + 无匹配行 → SUM nil、SumIf nil（无行，ELSE 0 不生效）
		res2, err := agg.NewGormBuilder[gaNull, nr](env.data).SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("g = ?", "none") }).
			Count("n").Sum("v", "s").SumDistinct("v", "sd").SumIf("v", "si", "g = ?", "a").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r := res2.Rows[0]
		t.Logf("%s no-match scalar: n=%d s=%s sd=%s si=%s Total=%d", env.dialect, r.N, f(r.S), f(r.SD), f(r.SI), res2.Total)
		if r.N != 0 || r.S != nil || r.SD != nil || r.SI != nil || res2.Total != 1 {
			t.Fatalf("no-match scalar wrong")
		}
		// 有行但全 NULL 的标量
		res3, err := agg.NewGormBuilder[gaNull, nr](env.data).SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("g = ?", "b") }).
			Sum("v", "s").SumIf("v", "si", "g = ?", "b").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s all-null scalar: s=%s si(match, all NULL, ELSE 0 not hit)=%s", env.dialect, f(res3.Rows[0].S), f(res3.Rows[0].SI))
		if res3.Rows[0].S != nil {
			t.Fatalf("all-null scalar sum should be nil")
		}
	})

	t.Run("Scalar_no_data_map", func(t *testing.T) {
		res, err := agg.NewGormBuilder[gaDoc, map[string]any](env.data).
			SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "nope") }).
			Count("total").Sum("amount", "s").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		m := *res.Rows[0]
		t.Logf("%s no-match scalar map=%#v nil=%v", env.dialect, m, m == nil)
		if m == nil {
			t.Fatalf("scalar no-data map must be initialized")
		}
		m["writable"] = true
		if fmt.Sprint(m["total"]) != "0" || m["s"] != nil {
			t.Fatalf("unexpected map %v", m)
		}
		// 分组+无数据：Rows 为空切片
		res2, err := agg.NewGormBuilder[gaDoc, map[string]any](env.data).
			SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "nope") }).
			GroupBy("region", "region").Count("total").Query(ctx)
		if err != nil || len(res2.Rows) != 0 || res2.Total != 0 {
			t.Fatalf("grouped empty: %v %d %d", err, len(res2.Rows), res2.Total)
		}
	})

	t.Run("Alias/reserved_words_and_case", func(t *testing.T) {
		type rr struct {
			Group  string   `gorm:"column:group"`
			Order  int64    `gorm:"column:order"`
			Select *float64 `gorm:"column:select"`
			Mixed  int64    `gorm:"column:MixedCase"`
		}
		res, err := agg.NewGormBuilder[gaDoc, rr](env.data).GroupBy("region", "group").Count("order").
			Sum("amount", "select").CountIf("MixedCase", "status = ?", "paid").
			Having("order > ?", 10).OrderByDesc("select").SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantN := 0
		for _, d := range byRegion {
			if ggCompute(d).total > 10 {
				wantN++
			}
		}
		if len(res.Rows) != wantN || res.Total != int64(wantN) {
			t.Fatalf("rows=%d total=%d want %d", len(res.Rows), res.Total, wantN)
		}
		for i, r := range res.Rows {
			e := ggCompute(byRegion[r.Group])
			if r.Order != e.total || r.Mixed != e.paidCnt {
				t.Fatalf("%+v", r)
			}
			ptrAlmostEqual(t, r.Select, e.sum, 1e-6, "select")
			if i > 0 && *res.Rows[i-1].Select < *r.Select {
				t.Fatal("order desc by reserved alias")
			}
		}
	})

	t.Run("TotalLimit_caps_total", func(t *testing.T) {
		res, err := nb().GroupBy("region", "region").GroupBy("channel", "channel").Count("total").
			SetLimit(2).SetTotalLimit(4).SetNeedTotal(true).Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Rows) != 2 || res.Total != 4 {
			t.Fatalf("rows=%d total=%d", len(res.Rows), res.Total)
		}
	})

	t.Run("Err/data_not_configured", func(t *testing.T) {
		_, err := agg.NewGormBuilder[gaDoc, gRegionRow](core.NewDBProxyWithAdapters()).Count("total").Query(ctx)
		if !errors.Is(err, core.ErrDataNotConfigured) {
			t.Fatalf("got %v", err)
		}
		_, err = agg.NewGormBuilder[gaDoc, gRegionRow](core.NewDBProxyWithAdapters()).Count("total").Explain(ctx)
		if !errors.Is(err, core.ErrDataNotConfigured) {
			t.Fatalf("explain got %v", err)
		}
	})

	t.Run("Err/table_missing_bad_sql", func(t *testing.T) {
		_, err := agg.NewGormBuilder[gaMissing, gRegionRow](env.data).Count("total").Query(ctx)
		if err == nil || !strings.Contains(err.Error(), "executing gorm aggregate query") {
			t.Fatalf("missing table: %v", err)
		}
		t.Logf("%s missing table: %v", env.dialect, err)
		_, err = nb().GroupBy("region", "region").Count("total").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = agg.NewGormBuilder[gaDoc, gRegionRow](env.data).SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where("nonsense (((") }).
			GroupBy("region", "region").Count("total").SetNeedTotal(true).Query(ctx)
		if err == nil || !(strings.Contains(err.Error(), "executing gorm aggregate query") || strings.Contains(err.Error(), "counting gorm aggregate groups")) {
			t.Fatalf("bad sql: %v", err)
		}
		t.Logf("%s bad sql: %v", env.dialect, err)
		var unmapped struct{}
		_ = unmapped
	})

	t.Run("Err/context_canceled_and_deadline", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := nb().GroupBy("region", "region").Count("total").Query(cctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled: %v", err)
		}
		// 只让 1 行 sleep：MySQL 客户端取消后服务端语句仍会逐行执行完（go-sql-driver 不发 KILL QUERY）
		sleep := "id = 1 AND SLEEP(2) = 0"
		if env.dialect == "postgres" {
			sleep = "id = 1 AND pg_sleep(2) IS NOT NULL"
		}
		dctx, cancel2 := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel2()
		start := time.Now()
		_, err = agg.NewGormBuilder[gaDoc, gRegionRow](env.data).SetFilter(func(db *gorm.DB) *gorm.DB { return db.Where(sleep) }).
			Count("total").Query(dctx)
		t.Logf("%s deadline after %v: %v", env.dialect, time.Since(start).Round(time.Millisecond), err)
		if err == nil || time.Since(start) > 1500*time.Millisecond {
			t.Fatalf("deadline not honored: %v", err)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Logf("note: driver error does not wrap context.DeadlineExceeded")
		}
	})

	t.Run("Order_by_distinct_and_cond_metric", func(t *testing.T) {
		res, err := nb().GroupBy("region", "region").CountDistinct("customer_id", "cust").SumIf("amount", "paid_sum", "status = ?", "paid").
			OrderByDesc("paid_sum").Query(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(res.Rows); i++ {
			if *res.Rows[i-1].Paid < *res.Rows[i].Paid {
				t.Fatal("not desc")
			}
		}
	})
}

func mgGroupDocsCompat(docs []esAggDoc) map[string][]esAggDoc { return ggGroupDocs(docs, "region") }
