package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/fantasticbin/QueryBuilder/core"
)

type recordingLogger struct {
	events []QueryEvent
}

func (r *recordingLogger) LogQuery(_ context.Context, event QueryEvent) {
	r.events = append(r.events, event)
}

type recordingMetrics struct {
	events []QueryEvent
}

func (r *recordingMetrics) RecordQuery(_ context.Context, event QueryEvent) {
	r.events = append(r.events, event)
}

type recordingTracer struct {
	starts []QuerySpanStart
	span   *recordingSpan
	key    any
	value  any
}

func (r *recordingTracer) StartQuery(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan) {
	r.starts = append(r.starts, start)
	if r.span == nil {
		r.span = &recordingSpan{}
	}
	if r.key != nil {
		ctx = context.WithValue(ctx, r.key, r.value)
	}
	return ctx, r.span
}

type recordingSpan struct {
	events []QueryEvent
}

func (r *recordingSpan) EndQuery(_ context.Context, event QueryEvent) {
	r.events = append(r.events, event)
}

type panicSpan struct{}

func (panicSpan) EndQuery(_ context.Context, _ QueryEvent) {
	panic("span down")
}

type orderedSpan struct {
	order *[]string
}

func (s orderedSpan) EndQuery(_ context.Context, _ QueryEvent) {
	*s.order = append(*s.order, "trace")
}

type panicMetaQuerier struct {
	*mockQuerier[testUser]
}

func (p *panicMetaQuerier) GetQueryMeta() core.QueryMeta {
	panic("meta should not be read")
}

func TestObservabilityMiddlewareNoop(t *testing.T) {
	mq := &panicMetaQuerier{mockQuerier: &mockQuerier[testUser]{meta: baseMeta()}}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{})

	calls := 0
	result, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		calls++
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected next called once, got %d", calls)
	}
	if result.GetTotal() != 1 {
		t.Fatalf("expected result preserved, got total %d", result.GetTotal())
	}
}

func TestObservabilityMiddlewareDefaultSignalOrder(t *testing.T) {
	var order []string
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger: QueryLoggerFunc(func(ctx context.Context, event QueryEvent) {
			order = append(order, "logger")
		}),
		Metrics: QueryMetricsFunc(func(ctx context.Context, event QueryEvent) {
			order = append(order, "metrics")
		}),
		Tracer: QueryTracerFunc(func(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan) {
			return ctx, orderedSpan{order: &order}
		}),
	})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSignalOrder(t, order, []string{"trace", "metrics", "logger"})
}

func TestObservabilityMiddlewareCustomSignalOrder(t *testing.T) {
	var order []string
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger: QueryLoggerFunc(func(ctx context.Context, event QueryEvent) {
			order = append(order, "logger")
		}),
		Metrics: QueryMetricsFunc(func(ctx context.Context, event QueryEvent) {
			order = append(order, "metrics")
		}),
		Tracer: QueryTracerFunc(func(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan) {
			return ctx, orderedSpan{order: &order}
		}),
		SignalOrder: []ObservabilitySignal{
			ObservabilitySignalLogger,
			ObservabilitySignalMetrics,
			ObservabilitySignalTrace,
		},
	})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSignalOrder(t, order, []string{"logger", "metrics", "trace"})
}

func TestObservabilityMiddlewarePartialSignalOrderAppendsDefaults(t *testing.T) {
	var order []string
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger: QueryLoggerFunc(func(ctx context.Context, event QueryEvent) {
			order = append(order, "logger")
		}),
		Metrics: QueryMetricsFunc(func(ctx context.Context, event QueryEvent) {
			order = append(order, "metrics")
		}),
		Tracer: QueryTracerFunc(func(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan) {
			return ctx, orderedSpan{order: &order}
		}),
		SignalOrder: []ObservabilitySignal{
			ObservabilitySignalLogger,
			ObservabilitySignalLogger,
			ObservabilitySignal(99),
		},
	})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSignalOrder(t, order, []string{"logger", "trace", "metrics"})
}

func TestObservabilityMiddlewareSignalFilters(t *testing.T) {
	logger := &recordingLogger{}
	metrics := &recordingMetrics{}
	tracer := &recordingTracer{}
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	expectedErr := errors.New("boom")
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger:  logger,
		Metrics: metrics,
		Tracer:  tracer,
		LoggerFilter: func(ctx context.Context, event QueryEvent) bool {
			return !event.Success
		},
		MetricsFilter: func(ctx context.Context, event QueryEvent) bool {
			return false
		},
		TraceFilter: func(ctx context.Context, meta core.QueryMeta) bool {
			return false
		},
	})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tracer.starts) != 0 {
		t.Fatalf("expected trace filter to skip span starts, got %d", len(tracer.starts))
	}
	if len(metrics.events) != 0 {
		t.Fatalf("expected metrics filter to skip metrics, got %d", len(metrics.events))
	}
	if len(logger.events) != 0 {
		t.Fatalf("expected logger filter to skip successful query, got %d", len(logger.events))
	}

	_, err = mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return nil, expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected original error, got %v", err)
	}
	if len(tracer.starts) != 0 {
		t.Fatalf("trace filter should keep skipping span starts, got %d", len(tracer.starts))
	}
	if len(metrics.events) != 0 {
		t.Fatalf("metrics filter should keep skipping metrics, got %d", len(metrics.events))
	}
	if len(logger.events) != 1 {
		t.Fatalf("expected logger filter to record failed query, got %d", len(logger.events))
	}
	if logger.events[0].Success {
		t.Fatalf("expected failed query event")
	}
}

func TestObservabilityMiddlewareRecordsSuccess(t *testing.T) {
	logger := &recordingLogger{}
	metrics := &recordingMetrics{}
	tracer := &recordingTracer{}
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger:  logger,
		Metrics: metrics,
		Tracer:  tracer,
	})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.ListResult[testUser]{
			Items: []*testUser{{ID: 1}, {ID: 2}},
			Total: 8,
		}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tracer.starts) != 1 {
		t.Fatalf("expected one span start, got %d", len(tracer.starts))
	}
	if len(tracer.span.events) != 1 || len(metrics.events) != 1 || len(logger.events) != 1 {
		t.Fatalf(
			"expected one event for span/metrics/logger, got %d/%d/%d",
			len(tracer.span.events),
			len(metrics.events),
			len(logger.events),
		)
	}

	event := logger.events[0]
	if event.Operation != "querybuilder.Gorm.list" {
		t.Fatalf("unexpected operation: %s", event.Operation)
	}
	if !event.Success || event.Error != nil || event.ErrorType != "" {
		t.Fatalf("expected successful event, got success=%v err=%v type=%q", event.Success, event.Error, event.ErrorType)
	}
	if event.ResultKind != core.ResultKindList || event.ItemCount != 2 || event.Total != 8 || event.HasMore {
		t.Fatalf("unexpected event result fields: %+v", event)
	}
	if event.Duration <= 0 {
		t.Fatalf("expected positive duration, got %v", event.Duration)
	}
	if tracer.span.events[0].Operation != event.Operation || metrics.events[0].Operation != event.Operation {
		t.Fatalf("expected consistent operation across signals")
	}
	if attrValue(event.Attributes, "querybuilder.datasource") != "Gorm" {
		t.Fatalf("expected datasource attribute")
	}
	if attrValue(event.Attributes, "querybuilder.item_count") != 2 {
		t.Fatalf("expected item count attribute")
	}
}

func TestObservabilityMiddlewareObserverPanicIsIsolated(t *testing.T) {
	logger := &recordingLogger{}
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger: logger,
		Metrics: QueryMetricsFunc(func(ctx context.Context, event QueryEvent) {
			panic("metrics down")
		}),
		Tracer: QueryTracerFunc(func(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan) {
			return nil, panicSpan{}
		}),
		OperationNameBuilder: func(meta core.QueryMeta) string {
			panic("operation down")
		},
		AttributeProvider: func(ctx context.Context, meta core.QueryMeta) []Attribute {
			panic("attributes down")
		},
		ErrorClassifier: func(err error) string {
			panic("classifier down")
		},
	})

	result, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		if ctx == nil {
			t.Fatalf("expected original context when tracer returns nil context")
		}
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GetTotal() != 1 {
		t.Fatalf("expected query result to be preserved, got total %d", result.GetTotal())
	}
	if len(logger.events) != 1 {
		t.Fatalf("expected logger to still receive one event, got %d", len(logger.events))
	}
	event := logger.events[0]
	if event.Operation != "querybuilder.Gorm.list" {
		t.Fatalf("expected operation fallback, got %s", event.Operation)
	}
	if !event.Success || event.ErrorType != "" {
		t.Fatalf("expected successful event with default error classification, got %+v", event)
	}
}

func TestObservabilityMiddlewareRecordsErrorAndPreservesError(t *testing.T) {
	logger := &recordingLogger{}
	expectedErr := errors.New("boom")
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{Logger: logger})

	result, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return nil, expectedErr
	})

	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected original error, got %v", err)
	}
	if len(logger.events) != 1 {
		t.Fatalf("expected one log event, got %d", len(logger.events))
	}
	event := logger.events[0]
	if event.Success || !errors.Is(event.Error, expectedErr) || event.ErrorType != "errorString" {
		t.Fatalf("unexpected error event: success=%v err=%v type=%q", event.Success, event.Error, event.ErrorType)
	}
	if attrValue(event.Attributes, "querybuilder.result_kind") != "unknown" {
		t.Fatalf("expected unknown result kind for nil error result")
	}
}

func TestDefaultErrorClassifierContextErrors(t *testing.T) {
	if got := DefaultErrorClassifier(context.Canceled); got != "context_canceled" {
		t.Fatalf("expected context_canceled, got %q", got)
	}
	if got := DefaultErrorClassifier(context.DeadlineExceeded); got != "context_deadline_exceeded" {
		t.Fatalf("expected context_deadline_exceeded, got %q", got)
	}
}

func TestObservabilityMiddlewareTracerContextPassedToNext(t *testing.T) {
	type ctxKey struct{}
	key := ctxKey{}
	tracer := &recordingTracer{key: key, value: "trace-context"}
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{Tracer: tracer})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		if ctx.Value(key) != "trace-context" {
			t.Fatalf("expected tracer context to be passed to next")
		}
		return &core.ListResult[testUser]{Items: []*testUser{{ID: 1}}, Total: 1}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestObservabilityMiddlewareCustomizationHooks(t *testing.T) {
	logger := &recordingLogger{}
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	expectedErr := errors.New("custom")
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger: logger,
		OperationNameBuilder: func(meta core.QueryMeta) string {
			return "custom.users.list"
		},
		AttributeProvider: func(ctx context.Context, meta core.QueryMeta) []Attribute {
			return []Attribute{{Key: "tenant_id", Value: "tenant-a"}}
		},
		ErrorClassifier: func(err error) string {
			if err == nil {
				return ""
			}
			return "custom_error"
		},
	})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return nil, expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected original error, got %v", err)
	}
	if len(logger.events) != 1 {
		t.Fatalf("expected one log event, got %d", len(logger.events))
	}
	event := logger.events[0]
	if event.Operation != "custom.users.list" {
		t.Fatalf("unexpected operation: %s", event.Operation)
	}
	if attrValue(event.Attributes, "tenant_id") != "tenant-a" {
		t.Fatalf("expected custom attribute")
	}
	if event.ErrorType != "custom_error" {
		t.Fatalf("expected custom error type, got %q", event.ErrorType)
	}
}

func TestObservabilityMiddlewareRecordsAndReraisesPanic(t *testing.T) {
	logger := &recordingLogger{}
	mq := &mockQuerier[testUser]{meta: baseMeta()}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{
		Logger: logger,
		ErrorClassifier: func(err error) string {
			if err == nil {
				return ""
			}
			return "panic"
		},
	})

	defer func() {
		recovered := recover()
		if recovered != "kaboom" {
			t.Fatalf("expected original panic, got %v", recovered)
		}
		if len(logger.events) != 1 {
			t.Fatalf("expected one panic event, got %d", len(logger.events))
		}
		event := logger.events[0]
		if event.Success || event.Error == nil || event.ErrorType != "panic" {
			t.Fatalf("unexpected panic event: %+v", event)
		}
	}()

	_, _ = mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		panic("kaboom")
	})
}

func TestObservabilityMiddlewarePITCursorEvent(t *testing.T) {
	logger := &recordingLogger{}
	meta := baseMeta()
	meta.DataSource = core.ElasticSearch
	meta.IsCursorQuery = true
	meta.IsPITQuery = true
	meta.CursorFields = []string{"_shard_doc"}
	mq := &mockQuerier[testUser]{meta: meta}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{Logger: logger})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.CursorPageResult[testUser]{
			Items:            []*testUser{{ID: 1}},
			Total:            10,
			HasMore:          true,
			NextCursorValues: []any{456},
		}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logger.events) != 1 {
		t.Fatalf("expected one log event, got %d", len(logger.events))
	}
	event := logger.events[0]
	if event.Operation != "querybuilder.ElasticSearch.pit_cursor" {
		t.Fatalf("unexpected operation: %s", event.Operation)
	}
	if event.ResultKind != core.ResultKindCursorPage || !event.HasMore {
		t.Fatalf("expected cursor page result event, got %+v", event)
	}
	if attrValue(event.Attributes, "querybuilder.mode") != "pit_cursor" {
		t.Fatalf("expected pit_cursor mode attribute")
	}
	if attrValue(event.Attributes, "querybuilder.pit") != true {
		t.Fatalf("expected pit attribute")
	}
}

func TestObservabilityMiddlewareCursorPageAndSensitiveDefaults(t *testing.T) {
	logger := &recordingLogger{}
	meta := baseMeta()
	meta.IsCursorQuery = true
	meta.CursorFields = []string{"id"}
	meta.CursorValues = []any{123}
	mq := &mockQuerier[testUser]{meta: meta}
	mw := ObservabilityMiddleware[testUser](ObservabilityOptions{Logger: logger})

	_, err := mw(context.Background(), mq, func(ctx context.Context) (core.Result[testUser], error) {
		return &core.CursorPageResult[testUser]{
			Items:            []*testUser{{ID: 1}},
			Total:            3,
			HasMore:          true,
			NextCursorValues: []any{456},
		}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logger.events) != 1 {
		t.Fatalf("expected one log event, got %d", len(logger.events))
	}
	event := logger.events[0]
	if event.Operation != "querybuilder.Gorm.cursor" {
		t.Fatalf("unexpected operation: %s", event.Operation)
	}
	if event.ResultKind != core.ResultKindCursorPage || event.ItemCount != 1 || event.Total != 3 || !event.HasMore {
		t.Fatalf("unexpected cursor event fields: %+v", event)
	}
	if attrValue(event.Attributes, "querybuilder.mode") != "cursor" {
		t.Fatalf("expected cursor mode attribute")
	}
	if hasAttribute(event.Attributes, "querybuilder.cursor_values") ||
		hasAttribute(event.Attributes, "querybuilder.next_cursor_values") {
		t.Fatalf("default attributes must not expose cursor values: %+v", event.Attributes)
	}
}

func attrValue(attrs []Attribute, key string) any {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}
	return nil
}

func hasAttribute(attrs []Attribute, key string) bool {
	for _, attr := range attrs {
		if attr.Key == key {
			return true
		}
	}
	return false
}

func assertSignalOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected signal order %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected signal order %v, got %v", want, got)
		}
	}
}
