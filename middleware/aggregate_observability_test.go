package middleware

import (
	"context"
	"errors"
	"testing"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type aggregateObservationRow struct {
	Total int64 `json:"total" bson:"total"`
}

type recordingAggregateSpan struct {
	event AggregateEvent
}

func (s *recordingAggregateSpan) EndAggregate(_ context.Context, event AggregateEvent) {
	s.event = event
}

func TestAggregateObservabilityMiddlewareRecordsSignals(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[aggregateObservationRow](data)
	builder.SetSpec(queryagg.Spec{
		Groups:  []queryagg.Group{{Field: "region", Alias: "region"}},
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	var logged AggregateEvent
	var measured AggregateEvent
	span := &recordingAggregateSpan{}
	builder.Use(AggregateObservabilityMiddleware[aggregateObservationRow](AggregateObservabilityOptions{
		Logger: AggregateLoggerFunc(func(_ context.Context, event AggregateEvent) { logged = event }),
		Metrics: AggregateMetricsFunc(func(_ context.Context, event AggregateEvent) {
			measured = event
		}),
		Tracer: AggregateTracerFunc(func(ctx context.Context, start AggregateSpanStart) (context.Context, AggregateSpan) {
			if start.Operation != "querybuilder.MongoDB.aggregate" {
				t.Fatalf("unexpected operation: %s", start.Operation)
			}
			return ctx, span
		}),
	}))
	builder.Use(func(
		context.Context,
		queryagg.Querier[aggregateObservationRow],
		queryagg.Handler[aggregateObservationRow],
	) (*queryagg.Result[aggregateObservationRow], error) {
		return &queryagg.Result[aggregateObservationRow]{Rows: []*aggregateObservationRow{{Total: 4}}}, nil
	})

	if _, err := builder.Query(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for name, event := range map[string]AggregateEvent{
		"logger":  logged,
		"metrics": measured,
		"tracer":  span.event,
	} {
		if !event.Success || event.RowCount != 1 || event.Duration <= 0 {
			t.Fatalf("unexpected %s event: %+v", name, event)
		}
		if event.Meta.QueryMode() != "aggregate" {
			t.Fatalf("unexpected %s mode: %s", name, event.Meta.QueryMode())
		}
	}
}

func TestAggregateObservabilityMiddlewareSignalFilters(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[aggregateObservationRow](data)
	builder.SetSpec(queryagg.Spec{
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	var logged []AggregateEvent
	var measured []AggregateEvent
	traceStarts := 0
	builder.Use(AggregateObservabilityMiddleware[aggregateObservationRow](AggregateObservabilityOptions{
		Logger: AggregateLoggerFunc(func(_ context.Context, event AggregateEvent) {
			logged = append(logged, event)
		}),
		Metrics: AggregateMetricsFunc(func(_ context.Context, event AggregateEvent) {
			measured = append(measured, event)
		}),
		Tracer: AggregateTracerFunc(func(ctx context.Context, _ AggregateSpanStart) (context.Context, AggregateSpan) {
			traceStarts++
			return ctx, &recordingAggregateSpan{}
		}),
		LoggerFilter: func(_ context.Context, event AggregateEvent) bool {
			return !event.Success
		},
		MetricsFilter: func(context.Context, AggregateEvent) bool {
			return false
		},
		TraceFilter: func(context.Context, queryagg.Meta) bool {
			return false
		},
	}))
	wantErr := errors.New("query failed")
	builder.Use(func(
		context.Context,
		queryagg.Querier[aggregateObservationRow],
		queryagg.Handler[aggregateObservationRow],
	) (*queryagg.Result[aggregateObservationRow], error) {
		return nil, wantErr
	})

	_, err := builder.Query(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
	if traceStarts != 0 {
		t.Fatalf("expected trace filter to skip span starts, got %d", traceStarts)
	}
	if len(measured) != 0 {
		t.Fatalf("expected metrics filter to skip metrics, got %d", len(measured))
	}
	if len(logged) != 1 || logged[0].Success {
		t.Fatalf("expected logger to record one failed query, got %+v", logged)
	}
}

func TestAggregateObservabilityMiddlewareRecordsPanics(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[aggregateObservationRow](data)
	builder.SetSpec(queryagg.Spec{
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	var event AggregateEvent
	span := &recordingAggregateSpan{}
	builder.Use(AggregateObservabilityMiddleware[aggregateObservationRow](AggregateObservabilityOptions{
		Logger: AggregateLoggerFunc(func(_ context.Context, recorded AggregateEvent) { event = recorded }),
		Tracer: AggregateTracerFunc(func(ctx context.Context, _ AggregateSpanStart) (context.Context, AggregateSpan) {
			return ctx, span
		}),
	}))
	builder.Use(func(
		context.Context,
		queryagg.Querier[aggregateObservationRow],
		queryagg.Handler[aggregateObservationRow],
	) (*queryagg.Result[aggregateObservationRow], error) {
		panic("aggregate exploded")
	})

	defer func() {
		recovered := recover()
		if recovered != "aggregate exploded" {
			t.Fatalf("expected original panic, got %v", recovered)
		}
		if event.Success || event.Error == nil || event.ErrorType == "" || event.Duration <= 0 {
			t.Fatalf("unexpected panic event: %+v", event)
		}
		if span.event.Error == nil || span.event.Success {
			t.Fatalf("span did not receive panic event: %+v", span.event)
		}
	}()

	_, _ = builder.Query(context.Background())
	t.Fatal("expected panic")
}
