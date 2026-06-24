package agg

import (
	"context"
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
	builder := NewGormBuilder[gormOrder, gormSummary](data, Spec{
		Groups: []Group{{Field: "customer.region", Alias: "region", Descending: true}},
		Metrics: []Metric{
			{Func: Count, Alias: "total"},
			{Func: Count, Field: "customer.id", Alias: "buyer_count", Distinct: true},
			{Func: Sum, Field: "amount", Alias: "amount_sum"},
			{Func: Sum, Field: "amount", Alias: "unique_amount_sum", Distinct: true},
		},
		Limit: 20,
	})
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

type aggregateTestDialector struct{}

func (aggregateTestDialector) Name() string { return "aggregate-test" }

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
