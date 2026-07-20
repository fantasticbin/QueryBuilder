package agg

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type gormOrder struct {
	ID     uint64
	Region string
	Amount float64
}

func (gormOrder) TableName() string { return "orders" }

type gormSummary struct {
	Region string  `gorm:"column:region"`
	Total  int64   `gorm:"column:total"`
	Amount float64 `gorm:"column:amount_sum"`
}

func TestGormBuilderExplain(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	data := core.NewDBProxyWithAdapters(core.NewGormAdapter(db))
	builder := NewGormBuilder[gormOrder, gormSummary](data)
	builder.GroupByDesc("customer.region", "region").
		Count("total").
		CountDistinct("customer.id", "buyer_count").
		Sum("amount", "amount_sum").
		SumDistinct("amount", "unique_amount_sum").
		SetLimit(20)
	builder.SetFilter(func(db *gorm.DB) *gorm.DB {
		return db.Where("status = ?", "paid")
	})

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	normalized := strings.Join(strings.Fields(explanation), " ")
	for _, fragment := range []string{
		`SELECT "customer"."region" AS "region", COUNT(*) AS "total", COUNT(DISTINCT "customer"."id") AS "buyer_count", SUM("amount") AS "amount_sum", SUM(DISTINCT "amount") AS "unique_amount_sum"`,
		`FROM "orders"`,
		`WHERE "customer"."region" IS NOT NULL AND status = ?`,
		`GROUP BY "customer"."region"`,
		`ORDER BY "region" DESC`,
		`LIMIT ?`,
		`args: [paid, 20]`,
	} {
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("expected explanation %q to contain %q", normalized, fragment)
		}
	}
}

type aggregateTestDialector struct {
	name string
}

func (d aggregateTestDialector) Name() string {
	if d.name != "" {
		return d.name
	}
	return "aggregate-test"
}

func (aggregateTestDialector) Initialize(db *gorm.DB) error {
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{})
	return nil
}

func (aggregateTestDialector) Migrator(*gorm.DB) gorm.Migrator { return nil }

func (aggregateTestDialector) DataTypeOf(*schema.Field) string { return "" }

func (aggregateTestDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

func (aggregateTestDialector) BindVarTo(writer clause.Writer, _ *gorm.Statement, _ any) {
	_ = writer.WriteByte('?')
}

func (aggregateTestDialector) QuoteTo(writer clause.Writer, value string) {
	_, _ = writer.WriteString(`"` + strings.ReplaceAll(value, `"`, `""`) + `"`)
}

func (aggregateTestDialector) Explain(sql string, _ ...any) string { return sql }

func TestGormBuilderExplainAdvancedSpec(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	data := core.NewDBProxyWithAdapters(core.NewGormAdapter(db))
	builder := NewGormBuilder[gormOrder, gormSummary](data)
	builder.GroupBy("region", "region").
		Count("total").
		CountIf("paid_total", "status = ?", "paid").
		SumIf("amount", "paid_amount", "status = ?", "paid").
		SumDistinctIf("amount", "unique_paid_amount", "status = ?", "paid").
		Having("paid_amount >= ?", 100).
		OrderByDesc("paid_amount").
		SetLimit(5)

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	normalized := strings.Join(strings.Fields(explanation), " ")
	for _, fragment := range []string{
		`COUNT(CASE WHEN "status" = ? THEN 1 END) AS "paid_total"`,
		`SUM(CASE WHEN "status" = ? THEN "amount" ELSE 0 END) AS "paid_amount"`,
		`SUM(DISTINCT CASE WHEN "status" = ? THEN "amount" ELSE 0 END) AS "unique_paid_amount"`,
		`HAVING SUM(CASE WHEN "status" = ? THEN "amount" ELSE 0 END) >= ?`,
		`ORDER BY "paid_amount" DESC`,
		`args: [paid, paid, paid, paid, 100, 5]`,
	} {
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("expected explanation %q to contain %q", normalized, fragment)
		}
	}
}

func TestGormBuilderExplainRichConditionsAndDateGroup(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	data := core.NewDBProxyWithAdapters(core.NewGormAdapter(db))
	builder := NewGormBuilder[gormOrder, gormSummary](data)
	builder.GroupByDateWithTimeZone("created_at", "created_day", TimeIntervalDay, "UTC").
		CountIf("paid_total", "status IN ?", []string{"paid", "settled"}).
		SumIf("amount", "mid_amount", "amount BETWEEN ? AND ?", Range{Start: 10, End: 20}).
		CountIf("named_total", "customer.name LIKE ?", "A%").
		SetLimit(5)

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	normalized := strings.Join(strings.Fields(explanation), " ")
	for _, fragment := range []string{
		`DATE_TRUNC('day', "created_at" AT TIME ZONE 'UTC') AS "created_day"`,
		`COUNT(CASE WHEN "status" IN (?,?) THEN 1 END) AS "paid_total"`,
		`SUM(CASE WHEN "amount" BETWEEN ? AND ? THEN "amount" ELSE 0 END) AS "mid_amount"`,
		`COUNT(CASE WHEN "customer"."name" LIKE ? THEN 1 END) AS "named_total"`,
		`GROUP BY DATE_TRUNC('day', "created_at" AT TIME ZONE 'UTC')`,
		`"flags":6`,
	} {
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("expected explanation %q to contain %q", normalized, fragment)
		}
	}
}

func TestGormBuilderRejectsUnsupportedDateGroupTimeZoneDialect(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{name: "mysql"}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	data := core.NewDBProxyWithAdapters(core.NewGormAdapter(db))
	builder := NewGormBuilder[gormOrder, gormSummary](data)
	builder.GroupByDateWithTimeZone("created_at", "created_day", TimeIntervalDay, "UTC").
		Count("total")

	_, err = builder.Explain(context.Background())
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected invalid spec error, got %v", err)
	}
}

func TestGormBuilderBuildsOffsetPaginationAndTotalSource(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	data := core.NewDBProxyWithAdapters(core.NewGormAdapter(db))
	builder := NewGormBuilder[gormOrder, gormSummary](data)
	builder.GroupBy("region", "region").
		Count("total").
		Sum("amount", "amount_sum").
		Having("amount_sum >= ?", 100).
		OrderByDesc("amount_sum").
		SetStart(10).
		SetLimit(5)

	rowsStmt := builder.buildQueryWithOptions(db.Session(&gorm.Session{DryRun: true}), true, true).
		Find(new([]gormSummary)).Statement
	if rowsStmt.Error != nil {
		t.Fatalf("building rows query: %v", rowsStmt.Error)
	}
	rowsSQL := strings.Join(strings.Fields(rowsStmt.SQL.String()), " ")
	for _, fragment := range []string{
		`HAVING SUM("amount") >= ?`,
		`ORDER BY "amount_sum" DESC`,
		`LIMIT ?`,
		`OFFSET ?`,
	} {
		if !strings.Contains(rowsSQL, fragment) {
			t.Fatalf("expected rows SQL %q to contain %q", rowsSQL, fragment)
		}
	}

	totalSourceStmt := builder.buildQueryWithOptions(db.Session(&gorm.Session{DryRun: true}), false, false).
		Find(new([]gormSummary)).Statement
	if totalSourceStmt.Error != nil {
		t.Fatalf("building total source query: %v", totalSourceStmt.Error)
	}
	totalSourceSQL := strings.Join(strings.Fields(totalSourceStmt.SQL.String()), " ")
	if !strings.Contains(totalSourceSQL, `HAVING SUM("amount") >= ?`) {
		t.Fatalf("expected total source SQL to keep HAVING: %q", totalSourceSQL)
	}
	for _, fragment := range []string{`ORDER BY`, `LIMIT`, `OFFSET`} {
		if strings.Contains(totalSourceSQL, fragment) {
			t.Fatalf("expected total source SQL %q not to contain %q", totalSourceSQL, fragment)
		}
	}
}

func TestGormBuilderCapsTotalWithTotalLimit(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	data := core.NewDBProxyWithAdapters(core.NewGormAdapter(db))

	// SetTotalLimit(n) must cap the count-total subquery with LIMIT.
	capped := NewGormBuilder[gormOrder, gormSummary](data)
	capped.GroupBy("region", "region").
		Count("total").
		Having("total >= ?", 1).
		SetLimit(5).
		SetTotalLimit(10)
	cappedSub := capped.buildQueryWithOptions(db.Session(&gorm.Session{DryRun: true}), false, false)
	if capped.totalLimit > 0 {
		cappedSub = cappedSub.Limit(int(capped.totalLimit))
	}
	cappedStmt := cappedSub.Find(new([]gormSummary)).Statement
	if cappedStmt.Error != nil {
		t.Fatalf("building capped total subquery: %v", cappedStmt.Error)
	}
	cappedSQL := strings.Join(strings.Fields(cappedStmt.SQL.String()), " ")
	if !strings.Contains(cappedSQL, "LIMIT ?") {
		t.Fatalf("expected capped total subquery to contain LIMIT, got %q", cappedSQL)
	}

	// Without SetTotalLimit the count-total subquery must NOT carry LIMIT.
	uncapped := NewGormBuilder[gormOrder, gormSummary](data)
	uncapped.GroupBy("region", "region").
		Count("total").
		Having("total >= ?", 1).
		SetLimit(5)
	uncappedSub := uncapped.buildQueryWithOptions(db.Session(&gorm.Session{DryRun: true}), false, false)
	if uncapped.totalLimit > 0 {
		uncappedSub = uncappedSub.Limit(int(uncapped.totalLimit))
	}
	uncappedStmt := uncappedSub.Find(new([]gormSummary)).Statement
	if uncappedStmt.Error != nil {
		t.Fatalf("building uncapped total subquery: %v", uncappedStmt.Error)
	}
	uncappedSQL := strings.Join(strings.Fields(uncappedStmt.SQL.String()), " ")
	if strings.Contains(uncappedSQL, "LIMIT ?") {
		t.Fatalf("expected uncapped total subquery not to contain LIMIT, got %q", uncappedSQL)
	}
}
