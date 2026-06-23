package agg

import (
	"context"
	"errors"
	"testing"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
	"github.com/fantasticbin/QueryBuilder/v2/core"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type observationRow struct {
	Total int64 `json:"total" bson:"total"`
}

type recordingSpan struct {
	event Event
}

func (s *recordingSpan) EndAggregate(_ context.Context, event Event) { s.event = event }

func TestObservabilityRecordsSignals(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[observationRow](data, queryagg.Spec{
		Groups:  []queryagg.Group{{Field: "region", Alias: "region"}},
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	var logged Event
	var measured Event
	span := &recordingSpan{}
	builder.Use(Observability[observationRow](ObservabilityOptions{
		Logger: LoggerFunc(func(_ context.Context, event Event) { logged = event }),
		Metrics: MetricsFunc(func(_ context.Context, event Event) {
			measured = event
		}),
		Tracer: TracerFunc(func(ctx context.Context, start SpanStart) (context.Context, Span) {
			if start.Operation != "querybuilder.MongoDB.aggregate" {
				t.Fatalf("unexpected operation: %s", start.Operation)
			}
			return ctx, span
		}),
	}))
	builder.Use(func(
		context.Context,
		queryagg.Querier[observationRow],
		queryagg.Handler[observationRow],
	) (*queryagg.Result[observationRow], error) {
		return &queryagg.Result[observationRow]{Rows: []*observationRow{{Total: 4}}}, nil
	})

	if _, err := builder.Query(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for name, event := range map[string]Event{
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

func TestObservabilityRecordsErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("query failed")
	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[observationRow](data, queryagg.Spec{
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	var event Event
	builder.Use(Observability[observationRow](ObservabilityOptions{
		Logger: LoggerFunc(func(_ context.Context, recorded Event) { event = recorded }),
	}))
	builder.Use(func(
		context.Context,
		queryagg.Querier[observationRow],
		queryagg.Handler[observationRow],
	) (*queryagg.Result[observationRow], error) {
		return nil, wantErr
	})

	_, err := builder.Query(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error %v, got %v", wantErr, err)
	}
	if event.Success || !errors.Is(event.Error, wantErr) || event.ErrorType == "" {
		t.Fatalf("unexpected error event: %+v", event)
	}
}

func TestObservabilityRecordsPanics(t *testing.T) {
	t.Parallel()

	data := core.NewDBProxyWithAdapters(core.NewMongoAdapter(&mongo.Collection{}))
	builder := queryagg.NewMongoBuilder[observationRow](data, queryagg.Spec{
		Metrics: []queryagg.Metric{{Func: queryagg.Count, Alias: "total"}},
	})
	var event Event
	span := &recordingSpan{}
	builder.Use(Observability[observationRow](ObservabilityOptions{
		Logger: LoggerFunc(func(_ context.Context, recorded Event) { event = recorded }),
		Tracer: TracerFunc(func(ctx context.Context, _ SpanStart) (context.Context, Span) {
			return ctx, span
		}),
	}))
	builder.Use(func(
		context.Context,
		queryagg.Querier[observationRow],
		queryagg.Handler[observationRow],
	) (*queryagg.Result[observationRow], error) {
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
