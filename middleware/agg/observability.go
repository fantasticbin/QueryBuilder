package agg

import (
	"context"
	"fmt"
	"time"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
	querymiddleware "github.com/fantasticbin/QueryBuilder/v2/middleware"
)

// Attribute 表示结构化可观测属性
type Attribute = querymiddleware.Attribute

// ErrorClassifier 将错误映射为稳定的低基数标签
type ErrorClassifier = querymiddleware.ErrorClassifier

// Event 描述一次已完成的聚合查询
type Event struct {
	Operation  string
	Meta       queryagg.Meta
	StartTime  time.Time
	Duration   time.Duration
	RowCount   int
	Success    bool
	Error      error
	ErrorType  string
	Attributes []Attribute
}

// SpanStart 描述聚合查询执行前的链路信息
type SpanStart struct {
	Operation  string
	Meta       queryagg.Meta
	StartTime  time.Time
	Attributes []Attribute
}

// Logger 记录已完成的聚合查询事件
type Logger interface {
	LogAggregate(context.Context, Event)
}

// LoggerFunc 将函数适配为 Logger
type LoggerFunc func(context.Context, Event)

// LogAggregate 实现 Logger 接口
func (f LoggerFunc) LogAggregate(ctx context.Context, event Event) { f(ctx, event) }

// Metrics 记录聚合查询指标
type Metrics interface {
	RecordAggregate(context.Context, Event)
}

// MetricsFunc 将函数适配为 Metrics
type MetricsFunc func(context.Context, Event)

// RecordAggregate 实现 Metrics 接口
func (f MetricsFunc) RecordAggregate(ctx context.Context, event Event) { f(ctx, event) }

// Tracer 启动聚合查询链路
type Tracer interface {
	StartAggregate(context.Context, SpanStart) (context.Context, Span)
}

// TracerFunc 将函数适配为 Tracer
type TracerFunc func(context.Context, SpanStart) (context.Context, Span)

// StartAggregate 实现 Tracer 接口
func (f TracerFunc) StartAggregate(ctx context.Context, start SpanStart) (context.Context, Span) {
	return f(ctx, start)
}

// Span 用于结束聚合查询链路
type Span interface {
	EndAggregate(context.Context, Event)
}

// OperationNameBuilder 根据聚合元信息生成操作名称
type OperationNameBuilder func(queryagg.Meta) string

// AttributeProvider 提供额外的聚合查询属性
type AttributeProvider func(context.Context, queryagg.Meta) []Attribute

// ObservabilityOptions 配置聚合查询日志、指标和链路
type ObservabilityOptions struct {
	Logger               Logger
	Metrics              Metrics
	Tracer               Tracer
	OperationNameBuilder OperationNameBuilder
	AttributeProvider    AttributeProvider
	ErrorClassifier      ErrorClassifier
}

// Observability 围绕聚合查询记录一次完整事件
func Observability[A any](opts ObservabilityOptions) queryagg.Middleware[A] {
	return func(
		ctx context.Context,
		querier queryagg.Querier[A],
		next queryagg.Handler[A],
	) (result *queryagg.Result[A], err error) {
		meta := querier.Meta()
		startTime := time.Now()
		operation := safeOperationName(opts.OperationNameBuilder, meta)
		attributes := defaultAttributes(meta)
		attributes = append(attributes, safeAttributes(ctx, opts.AttributeProvider, meta)...)

		var span Span
		ctx, span = safeStartSpan(ctx, opts.Tracer, SpanStart{
			Operation:  operation,
			Meta:       meta,
			StartTime:  startTime,
			Attributes: cloneAttributes(attributes),
		})

		record := func(recordErr error) {
			event := Event{
				Operation:  operation,
				Meta:       meta,
				StartTime:  startTime,
				Duration:   time.Since(startTime),
				Success:    recordErr == nil,
				Error:      recordErr,
				ErrorType:  safeErrorType(opts.ErrorClassifier, recordErr),
				Attributes: cloneAttributes(attributes),
			}
			if result != nil {
				event.RowCount = len(result.Rows)
			}
			if event.Duration <= 0 {
				event.Duration = time.Nanosecond
			}
			event.Attributes = append(event.Attributes,
				Attribute{Key: "querybuilder.success", Value: event.Success},
				Attribute{Key: "querybuilder.aggregate.row_count", Value: event.RowCount},
			)
			if event.ErrorType != "" {
				event.Attributes = append(event.Attributes, Attribute{
					Key:   "querybuilder.error_type",
					Value: event.ErrorType,
				})
			}

			if span != nil {
				safeObserve(func() { span.EndAggregate(ctx, event) })
			}
			if opts.Metrics != nil {
				safeObserve(func() { opts.Metrics.RecordAggregate(ctx, event) })
			}
			if opts.Logger != nil {
				safeObserve(func() { opts.Logger.LogAggregate(ctx, event) })
			}
		}

		defer func() {
			if recovered := recover(); recovered != nil {
				record(panicAsError(recovered))
				panic(recovered)
			}
		}()

		result, err = next(ctx)
		record(err)
		return result, err
	}
}

// DefaultOperationName 返回标准聚合查询操作名称
func DefaultOperationName(meta queryagg.Meta) string {
	return "querybuilder." + meta.DataSource.String() + ".aggregate"
}

// defaultAttributes 返回默认的低敏聚合查询属性
func defaultAttributes(meta queryagg.Meta) []Attribute {
	return []Attribute{
		{Key: "querybuilder.datasource", Value: meta.DataSource.String()},
		{Key: "querybuilder.mode", Value: meta.QueryMode()},
		{Key: "querybuilder.aggregate.group_count", Value: len(meta.Spec.Groups)},
		{Key: "querybuilder.aggregate.metric_count", Value: len(meta.Spec.Metrics)},
		{Key: "querybuilder.limit", Value: meta.Spec.Limit},
	}
}

// safeOperationName 隔离自定义操作名称构建器的 panic
func safeOperationName(builder OperationNameBuilder, meta queryagg.Meta) (operation string) {
	operation = DefaultOperationName(meta)
	if builder == nil {
		return operation
	}
	defer func() { _ = recover() }()
	if custom := builder(meta); custom != "" {
		operation = custom
	}
	return operation
}

// safeAttributes 隔离自定义属性提供者的 panic
func safeAttributes(
	ctx context.Context,
	provider AttributeProvider,
	meta queryagg.Meta,
) (attributes []Attribute) {
	if provider == nil {
		return []Attribute{}
	}
	defer func() {
		if recover() != nil {
			attributes = []Attribute{}
		}
	}()
	return provider(ctx, meta)
}

// safeStartSpan 隔离链路启动器的 panic，并保证返回有效 context
func safeStartSpan(
	ctx context.Context,
	tracer Tracer,
	start SpanStart,
) (spanContext context.Context, span Span) {
	if tracer == nil {
		return ctx, nil
	}
	spanContext = ctx
	defer func() {
		if recover() != nil {
			spanContext = ctx
			span = nil
		}
	}()
	spanContext, span = tracer.StartAggregate(ctx, start)
	if spanContext == nil {
		spanContext = ctx
	}
	return spanContext, span
}

// safeErrorType 隔离错误分类器的 panic
func safeErrorType(classifier ErrorClassifier, err error) (errorType string) {
	if classifier == nil {
		classifier = querymiddleware.DefaultErrorClassifier
	}
	defer func() {
		if recover() != nil {
			errorType = "error"
		}
	}()
	return classifier(err)
}

// cloneAttributes 返回属性切片的防御性副本
func cloneAttributes(attributes []Attribute) []Attribute {
	return append([]Attribute{}, attributes...)
}

// safeObserve 隔离日志、指标和链路观察器的 panic
func safeObserve(observe func()) {
	defer func() { _ = recover() }()
	observe()
}

func panicAsError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", recovered)
}
