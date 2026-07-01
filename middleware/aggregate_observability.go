package middleware

import (
	"context"
	"time"

	queryagg "github.com/fantasticbin/QueryBuilder/v2/agg"
)

// AggregateEvent 描述一次已完成的聚合查询
type AggregateEvent struct {
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

// AggregateSpanStart 描述聚合查询执行前的链路信息
type AggregateSpanStart struct {
	Operation  string
	Meta       queryagg.Meta
	StartTime  time.Time
	Attributes []Attribute
}

// AggregateLogger 记录已完成的聚合查询事件
type AggregateLogger interface {
	LogAggregate(context.Context, AggregateEvent)
}

// AggregateLoggerFunc 将函数适配为 AggregateLogger
type AggregateLoggerFunc func(context.Context, AggregateEvent)

// LogAggregate 实现 AggregateLogger 接口
func (f AggregateLoggerFunc) LogAggregate(ctx context.Context, event AggregateEvent) {
	f(ctx, event)
}

// AggregateMetrics 记录聚合查询指标
type AggregateMetrics interface {
	RecordAggregate(context.Context, AggregateEvent)
}

// AggregateMetricsFunc 将函数适配为 AggregateMetrics
type AggregateMetricsFunc func(context.Context, AggregateEvent)

// RecordAggregate 实现 AggregateMetrics 接口
func (f AggregateMetricsFunc) RecordAggregate(ctx context.Context, event AggregateEvent) {
	f(ctx, event)
}

// AggregateTracer 启动聚合查询链路
type AggregateTracer interface {
	StartAggregate(context.Context, AggregateSpanStart) (context.Context, AggregateSpan)
}

// AggregateTracerFunc 将函数适配为 AggregateTracer
type AggregateTracerFunc func(context.Context, AggregateSpanStart) (context.Context, AggregateSpan)

// StartAggregate 实现 AggregateTracer 接口
func (f AggregateTracerFunc) StartAggregate(
	ctx context.Context,
	start AggregateSpanStart,
) (context.Context, AggregateSpan) {
	return f(ctx, start)
}

// AggregateSpan 用于结束聚合查询链路
type AggregateSpan interface {
	EndAggregate(context.Context, AggregateEvent)
}

// AggregateOperationNameBuilder 根据聚合元信息生成操作名称
type AggregateOperationNameBuilder func(queryagg.Meta) string

// AggregateAttributeProvider 提供额外的聚合查询属性
type AggregateAttributeProvider func(context.Context, queryagg.Meta) []Attribute

// AggregateMetaFilter 根据聚合元信息决定是否启用某个查询前信号
type AggregateMetaFilter func(context.Context, queryagg.Meta) bool

// AggregateEventFilter 根据聚合完成事件决定是否记录某个查询后信号
type AggregateEventFilter func(context.Context, AggregateEvent) bool

// aggregateFilterInput 限制 safeAggregateFilter 只处理聚合元信息和聚合完成事件。
type aggregateFilterInput interface {
	queryagg.Meta | AggregateEvent
}

// AggregateObservabilityOptions 配置聚合查询日志、指标和链路
type AggregateObservabilityOptions struct {
	Logger               AggregateLogger
	Metrics              AggregateMetrics
	Tracer               AggregateTracer
	LoggerFilter         AggregateEventFilter
	MetricsFilter        AggregateEventFilter
	TraceFilter          AggregateMetaFilter
	SignalOrder          []ObservabilitySignal
	OperationNameBuilder AggregateOperationNameBuilder
	AttributeProvider    AggregateAttributeProvider
	ErrorClassifier      ErrorClassifier
}

// AggregateObservabilityMiddleware 围绕聚合查询记录一次完整事件
func AggregateObservabilityMiddleware[A any](opts AggregateObservabilityOptions) queryagg.Middleware[A] {
	hasPostSignals := opts.Logger != nil || opts.Metrics != nil
	if opts.Tracer == nil && !hasPostSignals {
		return func(
			ctx context.Context,
			querier queryagg.Querier[A],
			next queryagg.Handler[A],
		) (*queryagg.Result[A], error) {
			return next(ctx)
		}
	}

	operationBuilder := opts.OperationNameBuilder
	if operationBuilder == nil {
		operationBuilder = DefaultAggregateOperationName
	}
	errorClassifier := opts.ErrorClassifier
	if errorClassifier == nil {
		errorClassifier = DefaultErrorClassifier
	}
	signalOrder := normalizeSignalOrder(opts.SignalOrder)

	return func(
		ctx context.Context,
		querier queryagg.Querier[A],
		next queryagg.Handler[A],
	) (result *queryagg.Result[A], err error) {
		meta := querier.Meta()
		traceEnabled := opts.Tracer != nil && safeAggregateFilter(ctx, opts.TraceFilter, meta)
		if !traceEnabled && !hasPostSignals {
			return next(ctx)
		}

		operation := safeAggregateOperationName(operationBuilder, meta)
		startTime := time.Now()
		attributes := aggregateAttributes(ctx, opts, meta)

		var span AggregateSpan
		if traceEnabled {
			ctx, span = safeStartAggregateSpan(ctx, opts.Tracer, AggregateSpanStart{
				Operation:  operation,
				Meta:       meta,
				StartTime:  startTime,
				Attributes: cloneAttributes(attributes),
			})
		}
		if span == nil && !hasPostSignals {
			return next(ctx)
		}

		record := func(recordErr error) {
			event := buildAggregateEvent(
				operation,
				meta,
				startTime,
				result,
				recordErr,
				errorClassifier,
				attributes,
			)
			recordAggregate(ctx, opts, span, event, signalOrder)
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

// DefaultAggregateOperationName 返回标准聚合查询操作名称
func DefaultAggregateOperationName(meta queryagg.Meta) string {
	return "querybuilder." + meta.DataSource.String() + ".aggregate"
}

// buildAggregateEvent 根据聚合执行结果、错误和基础属性组装完成事件
func buildAggregateEvent[A any](
	operation string,
	meta queryagg.Meta,
	startTime time.Time,
	result *queryagg.Result[A],
	err error,
	errorClassifier ErrorClassifier,
	baseAttributes []Attribute,
) AggregateEvent {
	event := AggregateEvent{
		Operation:  operation,
		Meta:       meta,
		StartTime:  startTime,
		Duration:   time.Since(startTime),
		Success:    err == nil,
		Error:      err,
		ErrorType:  safeErrorClass(errorClassifier, err),
		Attributes: cloneAttributes(baseAttributes),
	}
	if result != nil {
		event.RowCount = len(result.Rows)
	}
	if event.Duration <= 0 {
		event.Duration = time.Nanosecond
	}
	event.Attributes = append(event.Attributes, aggregateResultAttributes(event)...)
	return event
}

// aggregateAttributes 合并聚合默认属性和调用方提供的扩展属性
func aggregateAttributes(ctx context.Context, opts AggregateObservabilityOptions, meta queryagg.Meta) []Attribute {
	attributes := defaultAggregateAttributes(meta)
	if opts.AttributeProvider != nil {
		attributes = append(attributes, safeAggregateAttributes(ctx, opts.AttributeProvider, meta)...)
	}
	return attributes
}

// defaultAggregateAttributes 返回聚合查询默认记录的低基数属性
func defaultAggregateAttributes(meta queryagg.Meta) []Attribute {
	plan := meta.Plan
	return []Attribute{
		{Key: "querybuilder.datasource", Value: meta.DataSource.String()},
		{Key: "querybuilder.mode", Value: meta.QueryMode()},
		{Key: "querybuilder.aggregate.group_count", Value: len(meta.Spec.Groups)},
		{Key: "querybuilder.aggregate.metric_count", Value: len(meta.Spec.Metrics)},
		{Key: "querybuilder.aggregate.having_count", Value: len(meta.Spec.Havings)},
		{Key: "querybuilder.aggregate.order_count", Value: len(meta.Spec.Orders)},
		{Key: "querybuilder.aggregate.has_distinct_metrics", Value: plan.Has(queryagg.PlanHasDistinctMetrics)},
		{Key: "querybuilder.aggregate.has_conditional_metrics", Value: plan.Has(queryagg.PlanHasConditionalMetrics)},
		{Key: "querybuilder.aggregate.has_date_groups", Value: plan.Has(queryagg.PlanHasDateGroups)},
		{Key: "querybuilder.aggregate.uses_mongo_facet", Value: plan.Has(queryagg.PlanUsesMongoFacet)},
		{Key: "querybuilder.aggregate.uses_elastic_scripted_metric", Value: plan.Has(queryagg.PlanUsesElasticScriptedMetric)},
		{Key: "querybuilder.aggregate.needs_client_post_processing", Value: plan.Has(queryagg.PlanNeedsClientPostProcessing)},
		{Key: "querybuilder.aggregate.needs_full_client_post_processing", Value: plan.Has(queryagg.PlanNeedsFullClientPostProcessing)},
		{Key: "querybuilder.limit", Value: meta.Spec.Limit},
	}
}

// aggregateResultAttributes 返回与聚合结果和错误状态相关的属性
func aggregateResultAttributes(event AggregateEvent) []Attribute {
	attributes := []Attribute{
		{Key: "querybuilder.success", Value: event.Success},
		{Key: "querybuilder.aggregate.row_count", Value: event.RowCount},
	}
	if event.ErrorType != "" {
		attributes = append(attributes, Attribute{Key: "querybuilder.error_type", Value: event.ErrorType})
	}
	return attributes
}

// recordAggregate 按配置顺序分发聚合查询完成事件
func recordAggregate(
	ctx context.Context,
	opts AggregateObservabilityOptions,
	span AggregateSpan,
	event AggregateEvent,
	signalOrder []ObservabilitySignal,
) {
	for _, signal := range signalOrder {
		switch signal {
		case ObservabilitySignalTrace:
			recordAggregateTrace(ctx, span, event)
		case ObservabilitySignalMetrics:
			recordAggregateMetrics(ctx, opts, event)
		case ObservabilitySignalLogger:
			recordAggregateLogger(ctx, opts, event)
		}
	}
}

// recordAggregateTrace 结束聚合查询 span，并隔离追踪适配器 panic
func recordAggregateTrace(ctx context.Context, span AggregateSpan, event AggregateEvent) {
	if span != nil {
		safeObserve(func() {
			span.EndAggregate(ctx, event)
		})
	}
}

// recordAggregateMetrics 在过滤器允许时记录聚合指标事件
func recordAggregateMetrics(ctx context.Context, opts AggregateObservabilityOptions, event AggregateEvent) {
	if opts.Metrics != nil && safeAggregateFilter(ctx, opts.MetricsFilter, event) {
		safeObserve(func() {
			opts.Metrics.RecordAggregate(ctx, event)
		})
	}
}

// recordAggregateLogger 在过滤器允许时记录聚合日志事件
func recordAggregateLogger(ctx context.Context, opts AggregateObservabilityOptions, event AggregateEvent) {
	if opts.Logger != nil && safeAggregateFilter(ctx, opts.LoggerFilter, event) {
		safeObserve(func() {
			opts.Logger.LogAggregate(ctx, event)
		})
	}
}

// safeAggregateOperationName 调用自定义 operation 构建器，并在 panic 或空值时回退默认名称
func safeAggregateOperationName(
	operationBuilder AggregateOperationNameBuilder,
	meta queryagg.Meta,
) (operation string) {
	defer func() {
		if recover() != nil || operation == "" {
			operation = DefaultAggregateOperationName(meta)
		}
	}()
	return operationBuilder(meta)
}

// safeAggregateAttributes 调用自定义属性提供器，并在 panic 时忽略扩展属性
func safeAggregateAttributes(
	ctx context.Context,
	provider AggregateAttributeProvider,
	meta queryagg.Meta,
) (attributes []Attribute) {
	defer func() {
		if recover() != nil {
			attributes = nil
		}
	}()
	return provider(ctx, meta)
}

// safeAggregateFilter 调用聚合过滤器，并在未配置时默认启用对应信号。
// 泛型参数:
//
//	T: 过滤器接收的输入快照（可自动推导）
func safeAggregateFilter[T aggregateFilterInput](
	ctx context.Context,
	filter func(context.Context, T) bool,
	value T,
) (enabled bool) {
	if filter == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			enabled = false
		}
	}()
	return filter(ctx, value)
}

// safeStartAggregateSpan 启动聚合 span，并在 tracer panic 或返回 nil context 时回退原 context
func safeStartAggregateSpan(
	ctx context.Context,
	tracer AggregateTracer,
	start AggregateSpanStart,
) (spanContext context.Context, span AggregateSpan) {
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
