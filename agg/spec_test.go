package agg

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

type specChainRow struct{}

type minimalQuerier struct{}

func (minimalQuerier) Query(context.Context) (*Result[specChainRow], error) { return nil, nil }
func (minimalQuerier) Explain(context.Context) (string, error)              { return "", nil }
func (minimalQuerier) Meta() Meta                                           { return Meta{} }

var (
	_ Querier[specChainRow]             = minimalQuerier{}
	_ SpecConfigurer[specChainRow]      = (*MongoBuilder[specChainRow])(nil)
	_ SpecChainConfigurer[specChainRow] = (*MongoBuilder[specChainRow])(nil)
)

func TestBuilderSpecChain(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[specChainRow](nil)
	builder.GroupBy("region", "region").
		GroupByDesc("customer.tier", "tier").
		Count("order_count").
		CountDistinct("customer.id", "customer_count").
		Sum("amount", "amount_sum").
		SumDistinct("amount", "unique_amount_sum").
		Avg("amount", "amount_avg").
		Min("amount", "amount_min").
		Max("amount", "amount_max").
		SetLimit(25)

	expected := Spec{
		Groups: []Group{
			{Field: "region", Alias: "region"},
			{Field: "customer.tier", Alias: "tier", Descending: true},
		},
		Metrics: []Metric{
			{Func: Count, Alias: "order_count"},
			{Func: Count, Field: "customer.id", Alias: "customer_count", Distinct: true},
			{Func: Sum, Field: "amount", Alias: "amount_sum"},
			{Func: Sum, Field: "amount", Alias: "unique_amount_sum", Distinct: true},
			{Func: Avg, Field: "amount", Alias: "amount_avg"},
			{Func: Min, Field: "amount", Alias: "amount_min"},
			{Func: Max, Field: "amount", Alias: "amount_max"},
		},
		Limit: 25,
	}
	got := builder.Meta().Spec
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("unexpected spec: %#v", got)
	}
	if err := validateSpec(normalizeSpec(got)); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestBuilderSpecSetters(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[specChainRow](nil)
	builder.Count("old_total")
	builder.SetSpec(Spec{}).
		SetGroups(Group{Field: "region", Alias: "region", Descending: true}).
		SetMetrics(Metric{Func: Count, Alias: "total"}).
		SetLimit(10)

	expected := Spec{
		Groups:  []Group{{Field: "region", Alias: "region", Descending: true}},
		Metrics: []Metric{{Func: Count, Alias: "total"}},
		Limit:   10,
	}
	got := builder.Meta().Spec
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("unexpected spec: %#v", got)
	}
}

func TestBuilderConfigureSpec(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[specChainRow](nil)
	builder.ConfigureSpec(func(spec *SpecBuilder) {
		spec.GroupBy("region", "region").
			Count("total").
			SetLimit(30)
	}, nil)

	expected := Spec{
		Groups:  []Group{{Field: "region", Alias: "region"}},
		Metrics: []Metric{{Func: Count, Alias: "total"}},
		Limit:   30,
	}
	got := builder.Meta().Spec
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("unexpected spec: %#v", got)
	}
}

func TestBuilderSpecChainDefaultLimit(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[specChainRow](nil)
	builder.SetLimit(25).
		GroupBy("region", "region").
		Count("total")

	got := builder.Meta().Spec
	if got.Limit != 25 {
		t.Fatalf("expected configured limit 25, got %d", got.Limit)
	}

	builder.SetGroups()
	if got := builder.Meta().Spec; got.Limit != 0 {
		t.Fatalf("expected scalar limit 0, got %d", got.Limit)
	}

	builder.GroupBy("region", "region")
	if got := builder.Meta().Spec; got.Limit != defaultLimit {
		t.Fatalf("expected default limit %d, got %d", defaultLimit, got.Limit)
	}
}

func TestBuilderSpecChainIsolation(t *testing.T) {
	t.Parallel()

	initial := Spec{
		Groups:  make([]Group, 1, 2),
		Metrics: make([]Metric, 1, 2),
	}
	initial.Groups[0] = Group{Field: "region", Alias: "region"}
	initial.Metrics[0] = Metric{Func: Count, Alias: "total"}

	builder := NewMongoBuilder[specChainRow](nil)
	builder.SetSpec(initial)
	builder.GroupBy("customer.tier", "tier").
		Sum("amount", "amount_sum").
		SetLimit(20)

	if len(initial.Groups) != 1 || len(initial.Metrics) != 1 || initial.Limit != 0 {
		t.Fatalf("initial spec was mutated: %#v", initial)
	}

	clone := builder.Clone()
	clone.GroupByDesc("channel", "channel").Count("channel_total")

	originalSpec := builder.Meta().Spec
	cloneSpec := clone.Meta().Spec
	if len(originalSpec.Groups) != 2 || len(originalSpec.Metrics) != 2 {
		t.Fatalf("original builder spec changed after clone mutation: %#v", originalSpec)
	}
	if len(cloneSpec.Groups) != 3 || len(cloneSpec.Metrics) != 3 {
		t.Fatalf("clone builder spec was not updated: %#v", cloneSpec)
	}

	cloneSpec.Groups[0].Alias = "changed"
	cloneSpec.Metrics[0].Alias = "changed"
	if got := builder.Meta().Spec; got.Groups[0].Alias != "region" || got.Metrics[0].Alias != "total" {
		t.Fatalf("meta spec shares slices with builder: %#v", got)
	}
}

func TestSpecBuilderIsolation(t *testing.T) {
	t.Parallel()

	initial := Spec{
		Groups:  make([]Group, 1, 2),
		Metrics: make([]Metric, 1, 2),
	}
	initial.Groups[0] = Group{Field: "region", Alias: "region"}
	initial.Metrics[0] = Metric{Func: Count, Alias: "total"}

	builder := NewSpecBuilder(initial)
	builder.GroupBy("customer.tier", "tier").Sum("amount", "amount_sum")

	if len(initial.Groups) != 1 || len(initial.Metrics) != 1 || initial.Limit != 0 {
		t.Fatalf("initial spec was mutated: %#v", initial)
	}

	built := builder.Spec()
	if len(built.Groups) != 2 || len(built.Metrics) != 2 || built.Limit != defaultLimit {
		t.Fatalf("unexpected built spec: %#v", built)
	}

	built.Groups[0].Alias = "changed"
	built.Metrics[0].Alias = "changed"
	if got := builder.Spec(); got.Groups[0].Alias != "region" || got.Metrics[0].Alias != "total" {
		t.Fatalf("SpecBuilder shares slices with returned spec: %#v", got)
	}
}

func TestValidateSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec Spec
		err  error
	}{
		{
			name: "valid grouped metrics",
			spec: Spec{
				Groups: []Group{{Field: "customer.region", Alias: "region"}},
				Metrics: []Metric{
					{Func: Count, Alias: "order_count"},
					{Func: Sum, Field: "amount", Alias: "amount_sum"},
				},
				Limit: 50,
			},
		},
		{
			name: "valid distinct count",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Field: "customer.id", Alias: "customer_count", Distinct: true}},
			},
		},
		{
			name: "valid distinct sum",
			spec: Spec{
				Metrics: []Metric{{Func: Sum, Field: "amount", Alias: "unique_amount_sum", Distinct: true}},
			},
		},
		{
			name: "missing metrics",
			spec: Spec{},
			err:  ErrInvalidSpec,
		},
		{
			name: "unsafe group field",
			spec: Spec{
				Groups:  []Group{{Field: "region; DROP TABLE orders", Alias: "region"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "duplicate alias ignores case",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "Total"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "reserved alias",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Alias: "_id"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "count rejects field",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Field: "id", Alias: "total"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "distinct count requires field",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Alias: "customer_count", Distinct: true}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "distinct count rejects unsafe field",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Field: "customer.id; DROP", Alias: "customer_count", Distinct: true}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "distinct rejects unsupported metric",
			spec: Spec{
				Metrics: []Metric{{Func: Avg, Field: "amount", Alias: "amount_avg", Distinct: true}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "metric requires field",
			spec: Spec{
				Metrics: []Metric{{Func: Avg, Alias: "average"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "unknown function",
			spec: Spec{
				Metrics: []Metric{{Func: Func(0), Field: "amount", Alias: "value"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "limit exceeded",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "region"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
				Limit:   5001,
			},
			err: ErrLimitExceeded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateSpec(normalizeSpec(test.spec))
			if test.err == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("expected error %v, got %v", test.err, err)
			}
		})
	}
}

func TestNormalizeSpec(t *testing.T) {
	t.Parallel()

	grouped := normalizeSpec(Spec{
		Groups:  []Group{{Field: "region", Alias: "region"}},
		Metrics: []Metric{{Func: Count, Alias: "total"}},
	})
	if grouped.Limit != defaultLimit {
		t.Fatalf("expected default limit %d, got %d", defaultLimit, grouped.Limit)
	}

	scalar := normalizeSpec(Spec{
		Metrics: []Metric{{Func: Count, Alias: "total"}},
		Limit:   25,
	})
	if scalar.Limit != 0 {
		t.Fatalf("expected scalar limit 0, got %d", scalar.Limit)
	}
}

func TestBuilderAdvancedSpecChain(t *testing.T) {
	t.Parallel()

	paid := conditionFromExpression("status = ?", "paid")
	builder := NewMongoBuilder[specChainRow](nil)
	builder.GroupBy("region", "region").
		GroupByDateWithTimeZone("created_at", "created_day", TimeIntervalDay, "Asia/Shanghai").
		Count("total").
		CountIf("paid_total", "status = ?", "paid").
		CountDistinctIf("customer.id", "paid_customers", "status = ?", "paid").
		SumIf("amount", "paid_amount", "status = ?", "paid").
		SumDistinctIf("amount", "unique_paid_amount", "status = ?", "paid").
		AvgIf("amount", "paid_avg", "status = ?", "paid").
		MinIf("amount", "paid_min", "status = ?", "paid").
		MaxIf("amount", "paid_max", "status = ?", "paid").
		OrderByDesc("paid_amount").
		Having("paid_amount >= ?", 100.5).
		SetLimit(10)

	expected := Spec{
		Groups: []Group{
			{Field: "region", Alias: "region"},
			{Field: "created_at", Alias: "created_day", Interval: TimeIntervalDay, TimeZone: "Asia/Shanghai"},
		},
		Metrics: []Metric{
			{Func: Count, Alias: "total"},
			{Func: Count, Alias: "paid_total", Condition: &paid},
			{Func: Count, Field: "customer.id", Alias: "paid_customers", Distinct: true, Condition: &paid},
			{Func: Sum, Field: "amount", Alias: "paid_amount", Condition: &paid},
			{Func: Sum, Field: "amount", Alias: "unique_paid_amount", Distinct: true, Condition: &paid},
			{Func: Avg, Field: "amount", Alias: "paid_avg", Condition: &paid},
			{Func: Min, Field: "amount", Alias: "paid_min", Condition: &paid},
			{Func: Max, Field: "amount", Alias: "paid_max", Condition: &paid},
		},
		Orders:  []Order{{Alias: "paid_amount", Descending: true}},
		Havings: []Having{{Alias: "paid_amount", Op: Gte, Value: 100.5}},
		Limit:   10,
	}
	got := builder.Meta().Spec
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("unexpected spec: %#v", got)
	}
	if err := validateSpec(normalizeSpec(got)); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	paid.Value = "refunded"
	if got := builder.Meta().Spec; got.Metrics[1].Condition.Value != "paid" {
		t.Fatalf("condition was not defensively copied: %#v", got.Metrics[1].Condition)
	}
}

func TestBuilderConditionValuesAreDefensivelyCopied(t *testing.T) {
	t.Parallel()

	statuses := []string{"paid", "settled"}
	window := &Range{Start: 10, End: 20}
	builder := NewMongoBuilder[specChainRow](nil)
	builder.CountIf("paid_total", "status IN ?", statuses).
		SumIf("amount", "mid_amount", "amount BETWEEN ? AND ?", window)

	statuses[0] = "refunded"
	window.Start = 99

	spec := builder.Meta().Spec
	gotStatuses, ok := spec.Metrics[0].Condition.Value.([]string)
	if !ok {
		t.Fatalf("expected []string condition value, got %T", spec.Metrics[0].Condition.Value)
	}
	if gotStatuses[0] != "paid" {
		t.Fatalf("condition slice was not defensively copied: %#v", gotStatuses)
	}
	gotWindow, ok := spec.Metrics[1].Condition.Value.(*Range)
	if !ok {
		t.Fatalf("expected *Range condition value, got %T", spec.Metrics[1].Condition.Value)
	}
	if gotWindow.Start != 10 || gotWindow.End != 20 {
		t.Fatalf("condition range was not defensively copied: %#v", gotWindow)
	}

	gotStatuses[0] = "mutated"
	gotWindow.Start = 77
	if got := builder.Meta().Spec; got.Metrics[0].Condition.Value.([]string)[0] != "paid" {
		t.Fatalf("meta condition slice shares state with builder: %#v", got.Metrics[0].Condition.Value)
	}
	if got := builder.Meta().Spec; got.Metrics[1].Condition.Value.(*Range).Start != 10 {
		t.Fatalf("meta condition range shares state with builder: %#v", got.Metrics[1].Condition.Value)
	}
}

func TestComparisonExpressionParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		query    string
		value    any
		expected Condition
	}{

		{name: "equals", query: "status = ?", value: "paid", expected: Condition{Field: "status", Op: Eq, Value: "paid"}},
		{name: "double equals", query: "status == ?", value: "paid", expected: Condition{Field: "status", Op: Eq, Value: "paid"}},
		{name: "not equals", query: "status != ?", value: "refunded", expected: Condition{Field: "status", Op: Ne, Value: "refunded"}},
		{name: "sql not equals", query: "status <> ?", value: "refunded", expected: Condition{Field: "status", Op: Ne, Value: "refunded"}},
		{name: "greater than", query: "amount > ?", value: 10, expected: Condition{Field: "amount", Op: Gt, Value: 10}},
		{name: "greater or equal", query: "amount >= ?", value: 10, expected: Condition{Field: "amount", Op: Gte, Value: 10}},
		{name: "less than", query: "customer.score < ?", value: 100, expected: Condition{Field: "customer.score", Op: Lt, Value: 100}},
		{name: "less or equal", query: "customer.score <= ?", value: 100, expected: Condition{Field: "customer.score", Op: Lte, Value: 100}},
		{name: "in", query: "status IN ?", value: []string{"paid", "settled"}, expected: Condition{Field: "status", Op: In, Value: []string{"paid", "settled"}}},
		{name: "not in", query: "status NOT IN ?", value: []string{"refunded"}, expected: Condition{Field: "status", Op: NotIn, Value: []string{"refunded"}}},
		{name: "between", query: "amount BETWEEN ? AND ?", value: Range{Start: 10, End: 20}, expected: Condition{Field: "amount", Op: Between, Value: Range{Start: 10, End: 20}}},
		{name: "like", query: "customer.name LIKE ?", value: "A%", expected: Condition{Field: "customer.name", Op: Like, Value: "A%"}},
		{name: "not like", query: "customer.name NOT LIKE ?", value: "%test%", expected: Condition{Field: "customer.name", Op: NotLike, Value: "%test%"}},
		{name: "is null", query: "deleted_at IS NULL", expected: Condition{Field: "deleted_at", Op: IsNull}},
		{name: "exists", query: "paid_at EXISTS", expected: Condition{Field: "paid_at", Op: Exists}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := conditionFromExpression(test.query, test.value)
			if !reflect.DeepEqual(got, test.expected) {
				t.Fatalf("unexpected condition: %#v", got)
			}
		})
	}

	if got := havingFromExpression("paid_amount >= ?", 100); !reflect.DeepEqual(got, Having{Alias: "paid_amount", Op: Gte, Value: 100}) {
		t.Fatalf("unexpected having from expression: %#v", got)
	}
}

func TestValidateAdvancedSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec Spec
		err  error
	}{
		{
			name: "valid order having and condition",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "region"}},
				Metrics: []Metric{{Func: Sum, Field: "amount", Alias: "paid_amount", Condition: conditionPtr(conditionFromExpression("status = ?", "paid"))}},
				Orders:  []Order{{Alias: "paid_amount", Descending: true}},
				Havings: []Having{havingFromExpression("paid_amount > ?", 10)},
			},
		},
		{
			name: "valid rich condition and date group",
			spec: Spec{
				Groups: []Group{{Field: "created_at", Alias: "created_day", Interval: TimeIntervalDay, TimeZone: "Asia/Shanghai"}},
				Metrics: []Metric{
					{Func: Count, Alias: "paid_total", Condition: conditionPtr(conditionFromExpression("status IN ?", []string{"paid", "settled"}))},
					{Func: Sum, Field: "amount", Alias: "mid_amount", Condition: conditionPtr(conditionFromExpression("amount BETWEEN ? AND ?", Range{Start: 10, End: 20}))},
					{Func: Count, Alias: "deleted_total", Condition: conditionPtr(conditionFromExpression("deleted_at IS NULL", nil))},
				},
			},
		},
		{
			name: "order rejects unknown alias",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "region"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
				Orders:  []Order{{Alias: "missing"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "having rejects group alias",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "region"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
				Havings: []Having{{Alias: "region", Op: Eq, Value: 1}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "having requires groups",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Alias: "total"}},
				Havings: []Having{{Alias: "total", Op: Gte, Value: 1}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "having requires numeric value",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "region"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
				Havings: []Having{{Alias: "total", Op: Gte, Value: "10"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "having rejects non-comparison operator",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "region"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
				Havings: []Having{{Alias: "total", Op: In, Value: 1}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "condition rejects unsafe field",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Alias: "paid_total", Condition: conditionPtr(conditionFromExpression("status; DROP = ?", "paid"))}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "condition rejects unsupported expression",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Alias: "paid_total", Condition: conditionPtr(conditionFromExpression("status contains ?", "paid"))}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "condition rejects empty in list",
			spec: Spec{
				Metrics: []Metric{{Func: Count, Alias: "paid_total", Condition: &Condition{Field: "status", Op: In, Value: []string{}}}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "date group rejects timezone without interval",
			spec: Spec{
				Groups:  []Group{{Field: "created_at", Alias: "created_day", TimeZone: "Asia/Shanghai"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
			},
			err: ErrInvalidSpec,
		},
		{
			name: "having rejects unsupported expression",
			spec: Spec{
				Groups:  []Group{{Field: "region", Alias: "region"}},
				Metrics: []Metric{{Func: Count, Alias: "total"}},
				Havings: []Having{havingFromExpression("total like ?", 1)},
			},
			err: ErrInvalidSpec,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validateSpec(normalizeSpec(test.spec))
			if test.err == nil && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.err != nil && !errors.Is(err, test.err) {
				t.Fatalf("expected error %v, got %v", test.err, err)
			}
		})
	}
}
func TestAnalyzeSpec(t *testing.T) {
	t.Parallel()

	spec := normalizeSpec(Spec{
		Groups: []Group{{Field: "created_at", Alias: "created_day", Interval: TimeIntervalDay}},
		Metrics: []Metric{
			{Func: Count, Alias: "total", Condition: conditionPtr(conditionFromExpression("status IN ?", []string{"paid"}))},
			{Func: Sum, Field: "amount", Alias: "unique_amount", Distinct: true},
		},
		Orders:  []Order{{Alias: "unique_amount", Descending: true}},
		Havings: []Having{{Alias: "total", Op: Gte, Value: 1}},
	})

	plan := AnalyzeSpec(core.Gorm, spec)
	expectedFlags := PlanHasDateGroups | PlanHasDistinctMetrics | PlanHasConditionalMetrics
	if plan.Flags != expectedFlags || !plan.Has(PlanHasDateGroups) || !plan.Has(PlanHasDistinctMetrics) || !plan.Has(PlanHasConditionalMetrics) {
		t.Fatalf("unexpected generic plan: %+v", plan)
	}
	elasticPlan := AnalyzeSpec(core.ElasticSearch, spec)
	expectedElasticFlags := expectedFlags | PlanUsesElasticScriptedMetric | PlanNeedsClientPostProcessing | PlanNeedsFullClientPostProcessing
	if elasticPlan.Flags != expectedElasticFlags || !elasticPlan.Has(PlanNeedsClientPostProcessing) || !elasticPlan.Has(PlanNeedsFullClientPostProcessing) || !elasticPlan.Has(PlanUsesElasticScriptedMetric) {
		t.Fatalf("unexpected elastic plan: %+v", elasticPlan)
	}
}

func TestBuilderSpecChainPaginationMeta(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[specChainRow](nil)
	builder.GroupBy("region", "region").
		Count("total").
		SetStart(20).
		SetLimit(10).
		SetNeedTotal(false).
		SetTotalLimit(1000)

	meta := builder.Meta()
	if meta.Start != 20 || meta.Spec.Start != 20 || meta.Spec.Limit != 10 || meta.NeedTotal || meta.TotalLimit != 1000 {
		t.Fatalf("unexpected pagination meta: %+v", meta)
	}
	meta.Spec.Start = 99
	if got := builder.Meta(); got.Start != 20 || got.Spec.Start != 20 {
		t.Fatalf("meta spec must be defensively copied: %+v", got)
	}

	clone := builder.Clone()
	clone.SetStart(30).SetNeedTotal(true).SetTotalLimit(2000)
	if got := builder.Meta(); got.Start != 20 || got.NeedTotal || got.TotalLimit != 1000 {
		t.Fatalf("clone mutation changed original meta: %+v", got)
	}
	if got := clone.Meta(); got.Start != 30 || got.Spec.Start != 30 || !got.NeedTotal || got.TotalLimit != 2000 {
		t.Fatalf("clone pagination meta not updated: %+v", got)
	}

	builder.SetGroups()
	if got := builder.Meta(); got.Start != 0 || got.Spec.Start != 0 || got.Spec.Limit != 0 {
		t.Fatalf("scalar aggregates should reset pagination: %+v", got)
	}
}
