package agg

import (
	"context"
	"reflect"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type pipelineRow struct {
	Total int64 `bson:"total" json:"total"`
}

func TestPipelineOrderAndMetaIsolation(t *testing.T) {
	t.Parallel()

	spec := Spec{Metrics: []Metric{{Func: Count, Alias: "total"}}}
	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := NewMongoBuilder[pipelineRow](data)
	builder.SetSpec(spec)
	events := make([]string, 0, 7)
	builder.SetBeforeHook(func(ctx context.Context) context.Context {
		events = append(events, "before")
		return ctx
	})
	builder.Use(func(ctx context.Context, q Querier[pipelineRow], next Handler[pipelineRow]) (*Result[pipelineRow], error) {
		events = append(events, "mw1-before")
		result, err := next(ctx)
		events = append(events, "mw1-after")
		return result, err
	})
	builder.Use(func(ctx context.Context, q Querier[pipelineRow], next Handler[pipelineRow]) (*Result[pipelineRow], error) {
		events = append(events, "mw2-before")
		result, err := next(ctx)
		events = append(events, "mw2-after")
		return result, err
	})
	builder.Use(func(context.Context, Querier[pipelineRow], Handler[pipelineRow]) (*Result[pipelineRow], error) {
		events = append(events, "short-circuit")
		return &Result[pipelineRow]{Rows: []*pipelineRow{{Total: 3}}}, nil
	})
	builder.SetAfterHook(func(context.Context, *Result[pipelineRow], error) {
		events = append(events, "after")
	})

	result, err := builder.Query(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 || result.Rows[0].Total != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}
	wantEvents := []string{
		"before",
		"mw1-before",
		"mw2-before",
		"short-circuit",
		"mw2-after",
		"mw1-after",
		"after",
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("expected events %v, got %v", wantEvents, events)
	}

	meta := builder.Meta()
	meta.Spec.Metrics[0].Alias = "mutated"
	if builder.Meta().Spec.Metrics[0].Alias != "total" {
		t.Fatal("Meta must return a defensive spec copy")
	}
}

func TestCloneStateIsolation(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	original := NewMongoBuilder[pipelineRow](data)
	original.Count("total")
	original.SetFilter(MongoFilter{{Key: "status", Value: "active"}})
	cloned := original.Clone()
	cloned.filter[0].Value = "inactive"
	cloned.spec.Metrics[0].Alias = "other_total"

	if original.filter[0].Value != "active" {
		t.Fatal("Clone must isolate MongoDB filters")
	}
	if original.spec.Metrics[0].Alias != "total" {
		t.Fatal("Clone must isolate aggregate specs")
	}
}
