package agg

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type specChainRow struct{}

type minimalQuerier struct{}

func (minimalQuerier) Query(context.Context) (*Result[specChainRow], error) { return nil, nil }
func (minimalQuerier) Explain(context.Context) (string, error)              { return "", nil }
func (minimalQuerier) Meta() Meta                                           { return Meta{} }

var _ Querier[specChainRow] = minimalQuerier{}

func TestBuilderSpecChain(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[specChainRow](nil, Spec{})
	builder.GroupBy("region", "region").
		GroupByDesc("customer.tier", "tier").
		Count("order_count").
		Sum("amount", "amount_sum").
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
			{Func: Sum, Field: "amount", Alias: "amount_sum"},
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

	builder := NewMongoBuilder[specChainRow](nil, Spec{
		Metrics: []Metric{{Func: Count, Alias: "old_total"}},
	})
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

func TestBuilderSpecChainDefaultLimit(t *testing.T) {
	t.Parallel()

	builder := NewMongoBuilder[specChainRow](nil, Spec{})
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

	builder := NewMongoBuilder[specChainRow](nil, initial)
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
