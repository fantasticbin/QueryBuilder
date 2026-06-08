package middleware

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	builder "github.com/fantasticbin/QueryBuilder/v2"
	"github.com/fantasticbin/QueryBuilder/v2/core"
)

// ObservabilitySignal 表示查询完成事件可分发到的信号类型。
type ObservabilitySignal int

const (
	// ObservabilitySignalTrace 表示链路 span 结束信号。
	ObservabilitySignalTrace ObservabilitySignal = iota
	// ObservabilitySignalMetrics 表示指标记录信号。
	ObservabilitySignalMetrics
	// ObservabilitySignalLogger 表示日志记录信号。
	ObservabilitySignalLogger
)

var defaultSignalOrder = []ObservabilitySignal{
	ObservabilitySignalTrace,
	ObservabilitySignalMetrics,
	ObservabilitySignalLogger,
}

// Attribute 是可观测事件中的通用属性，避免绑定任意日志、指标或链路 SDK。
type Attribute struct {
	// Key 是属性名称，建议使用稳定、低基数的命名。
	Key string
	// Value 是属性值，可由调用方适配到具体日志、指标或链路系统支持的类型。
	Value any
}

// QuerySpanStart 描述一次查询 span 的启动信息。
type QuerySpanStart struct {
	// Operation 是本次查询的操作名。
	Operation string
	// Meta 是查询开始时的元信息快照。
	Meta core.QueryMeta
	// StartTime 是中间件记录的查询开始时间。
	StartTime time.Time
	// Attributes 是查询开始时可用于 span 初始化的属性。
	Attributes []Attribute
}

// QueryEvent 描述一次查询完成后的可观测事件。
type QueryEvent struct {
	// Operation 是本次查询的操作名。
	Operation string
	// Meta 是查询开始时的元信息快照。
	Meta core.QueryMeta
	// StartTime 是中间件记录的查询开始时间。
	StartTime time.Time
	// Duration 是查询执行耗时，包含后续中间件和真实查询耗时。
	Duration time.Duration
	// ResultKind 是查询结果类型；当查询失败且无结果时为零值，默认属性会标记为 unknown。
	ResultKind core.ResultKind
	// ItemCount 是结果中的实体数量。
	ItemCount int
	// Total 是结果总数，语义与 core.Result.GetTotal 保持一致。
	Total int64
	// HasMore 表示游标分页结果是否还有下一页。
	HasMore bool
	// Error 是查询返回的错误；panic 场景会转换为 error 记录后继续抛出原 panic。
	Error error
	// ErrorType 是 ErrorClassifier 生成的稳定错误分类。
	ErrorType string
	// Success 表示查询是否成功完成。
	Success bool
	// Attributes 是默认属性和 AttributeProvider 补充属性的合并结果。
	Attributes []Attribute
}

// QueryLogger 接收查询完成事件，用于对接日志系统。
type QueryLogger interface {
	// LogQuery 在查询完成、失败或 panic 被观测到后调用。
	LogQuery(ctx context.Context, event QueryEvent)
}

// QueryLoggerFunc 允许使用函数快速实现 QueryLogger。
type QueryLoggerFunc func(ctx context.Context, event QueryEvent)

// LogQuery 实现 QueryLogger。
func (f QueryLoggerFunc) LogQuery(ctx context.Context, event QueryEvent) {
	f(ctx, event)
}

// QueryMetrics 接收查询完成事件，用于对接指标系统。
type QueryMetrics interface {
	// RecordQuery 在查询完成、失败或 panic 被观测到后调用。
	RecordQuery(ctx context.Context, event QueryEvent)
}

// QueryMetricsFunc 允许使用函数快速实现 QueryMetrics。
type QueryMetricsFunc func(ctx context.Context, event QueryEvent)

// RecordQuery 实现 QueryMetrics。
func (f QueryMetricsFunc) RecordQuery(ctx context.Context, event QueryEvent) {
	f(ctx, event)
}

// QueryTracer 启动一次查询 span，用于对接链路追踪系统。
type QueryTracer interface {
	// StartQuery 在调用下一个中间件或真实查询前调用。
	// 返回的 context 会继续传递给 next；若返回 nil context，中间件会回退到原始 context。
	StartQuery(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan)
}

// QueryTracerFunc 允许使用函数快速实现 QueryTracer。
type QueryTracerFunc func(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan)

// StartQuery 实现 QueryTracer。
func (f QueryTracerFunc) StartQuery(ctx context.Context, start QuerySpanStart) (context.Context, QuerySpan) {
	return f(ctx, start)
}

// QuerySpan 表示一次查询链路 span。
type QuerySpan interface {
	// EndQuery 在查询完成、失败或 panic 被观测到后调用。
	EndQuery(ctx context.Context, event QueryEvent)
}

// OperationNameBuilder 根据查询元信息构建 operation 名称。
type OperationNameBuilder func(meta core.QueryMeta) string

// AttributeProvider 为默认可观测属性补充业务维度。
type AttributeProvider func(ctx context.Context, meta core.QueryMeta) []Attribute

// ErrorClassifier 将错误映射为稳定的错误分类名称。
type ErrorClassifier func(err error) string

// QueryMetaFilter 根据查询元信息决定是否启用某个查询前信号。
type QueryMetaFilter func(ctx context.Context, meta core.QueryMeta) bool

// QueryEventFilter 根据查询完成事件决定是否记录某个查询后信号。
type QueryEventFilter func(ctx context.Context, event QueryEvent) bool

// ObservabilityOptions 配置官方可观测中间件。
type ObservabilityOptions struct {
	// Logger 接收查询完成事件，用于写日志；为 nil 时跳过日志记录。
	Logger QueryLogger
	// Metrics 接收查询完成事件，用于记录指标；为 nil 时跳过指标记录。
	Metrics QueryMetrics
	// Tracer 在查询开始时创建 span，并在查询结束时关闭 span；为 nil 时跳过链路记录。
	Tracer QueryTracer
	// LoggerFilter 控制单次查询是否写日志；为 nil 时只要 Logger 非 nil 就写日志。
	LoggerFilter QueryEventFilter
	// MetricsFilter 控制单次查询是否记录指标；为 nil 时只要 Metrics 非 nil 就记录指标。
	MetricsFilter QueryEventFilter
	// TraceFilter 控制单次查询是否创建链路 span；为 nil 时只要 Tracer 非 nil 就创建 span。
	TraceFilter QueryMetaFilter
	// SignalOrder 控制查询完成后的信号分发顺序；为空时使用 trace -> metrics -> logger。
	SignalOrder []ObservabilitySignal
	// OperationNameBuilder 自定义 operation 名称；为 nil 或返回空字符串时使用 DefaultOperationName。
	OperationNameBuilder OperationNameBuilder
	// AttributeProvider 为默认属性补充业务维度；为 nil 时只使用默认属性。
	AttributeProvider AttributeProvider
	// ErrorClassifier 将错误映射为稳定分类；为 nil 时使用 DefaultErrorClassifier。
	ErrorClassifier ErrorClassifier
}

// ObservabilityMiddleware 创建无厂商依赖的可观测中间件。
func ObservabilityMiddleware[R any](opts ObservabilityOptions) builder.Middleware[R] {
	hasPostSignals := opts.Logger != nil || opts.Metrics != nil
	if opts.Tracer == nil && !hasPostSignals {
		return func(
			ctx context.Context,
			b builder.Querier[R],
			next func(context.Context) (core.Result[R], error),
		) (core.Result[R], error) {
			return next(ctx)
		}
	}

	operationBuilder := opts.OperationNameBuilder
	if operationBuilder == nil {
		operationBuilder = DefaultOperationName
	}
	errorClassifier := opts.ErrorClassifier
	if errorClassifier == nil {
		errorClassifier = DefaultErrorClassifier
	}
	signalOrder := normalizeSignalOrder(opts.SignalOrder)

	return func(
		ctx context.Context,
		b builder.Querier[R],
		next func(context.Context) (core.Result[R], error),
	) (result core.Result[R], err error) {
		meta := b.GetQueryMeta()
		traceEnabled := opts.Tracer != nil && safeMetaFilter(ctx, opts.TraceFilter, meta)
		if !traceEnabled && !hasPostSignals {
			return next(ctx)
		}

		operation := safeOperationName(operationBuilder, meta)

		startTime := time.Now()
		attrs := defaultQueryAttributes(meta)
		if opts.AttributeProvider != nil {
			attrs = append(attrs, safeAttributes(ctx, opts.AttributeProvider, meta)...)
		}

		var span QuerySpan
		if traceEnabled {
			ctx, span = safeStartQuery(ctx, opts.Tracer, QuerySpanStart{
				Operation:  operation,
				Meta:       meta,
				StartTime:  startTime,
				Attributes: cloneAttributes(attrs),
			})
		}
		if span == nil && !hasPostSignals {
			return next(ctx)
		}

		defer func() {
			if recovered := recover(); recovered != nil {
				event := buildQueryEvent[R](
					operation,
					meta,
					startTime,
					time.Since(startTime),
					result,
					err,
					panicAsError(recovered),
					errorClassifier,
					attrs,
				)
				recordQuery(ctx, opts, span, event, signalOrder)
				panic(recovered)
			}
		}()
		result, err = next(ctx)
		event := buildQueryEvent[R](
			operation,
			meta,
			startTime,
			time.Since(startTime),
			result,
			err,
			nil,
			errorClassifier,
			attrs,
		)
		recordQuery(ctx, opts, span, event, signalOrder)
		return result, err
	}
}

// DefaultOperationName 构建默认 operation 名称。
func DefaultOperationName(meta core.QueryMeta) string {
	return "querybuilder." + meta.DataSource.String() + "." + meta.QueryMode()
}

// DefaultErrorClassifier 返回默认错误分类。
func DefaultErrorClassifier(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline_exceeded"
	}
	t := reflect.TypeOf(err)
	if t == nil {
		return "error"
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Name() == "" {
		return "error"
	}
	return t.Name()
}

// recordQuery 按指定顺序分发查询完成事件。
func recordQuery(
	ctx context.Context,
	opts ObservabilityOptions,
	span QuerySpan,
	event QueryEvent,
	signalOrder []ObservabilitySignal,
) {
	for _, signal := range signalOrder {
		switch signal {
		case ObservabilitySignalTrace:
			recordTrace(ctx, span, event)
		case ObservabilitySignalMetrics:
			recordMetrics(ctx, opts, event)
		case ObservabilitySignalLogger:
			recordLogger(ctx, opts, event)
		}
	}
}

// recordTrace 分发链路 span 结束信号。
func recordTrace(ctx context.Context, span QuerySpan, event QueryEvent) {
	if span != nil {
		safeObserve(func() {
			span.EndQuery(ctx, event)
		})
	}
}

// recordMetrics 分发指标记录信号。
func recordMetrics(ctx context.Context, opts ObservabilityOptions, event QueryEvent) {
	if opts.Metrics != nil {
		if safeEventFilter(ctx, opts.MetricsFilter, event) {
			safeObserve(func() {
				opts.Metrics.RecordQuery(ctx, event)
			})
		}
	}
}

// recordLogger 分发日志记录信号。
func recordLogger(ctx context.Context, opts ObservabilityOptions, event QueryEvent) {
	if opts.Logger != nil {
		if safeEventFilter(ctx, opts.LoggerFilter, event) {
			safeObserve(func() {
				opts.Logger.LogQuery(ctx, event)
			})
		}
	}
}

// buildQueryEvent 根据查询结果、错误和基础属性组装统一的可观测事件。
func buildQueryEvent[R any](
	operation string,
	meta core.QueryMeta,
	startTime time.Time,
	duration time.Duration,
	result core.Result[R],
	err error,
	panicErr error,
	errorClassifier ErrorClassifier,
	baseAttrs []Attribute,
) QueryEvent {
	eventErr := err
	if panicErr != nil {
		eventErr = panicErr
	}

	event := QueryEvent{
		Operation:  operation,
		Meta:       meta,
		StartTime:  startTime,
		Duration:   duration,
		Error:      eventErr,
		ErrorType:  safeErrorClass(errorClassifier, eventErr),
		Success:    eventErr == nil,
		Attributes: cloneAttributes(baseAttrs),
	}
	hasResult := result != nil
	if hasResult {
		event.ResultKind = result.GetResultKind()
		event.ItemCount = len(result.GetItems())
		event.Total = result.GetTotal()
		event.HasMore = result.GetHasMore()
	}
	event.Attributes = append(event.Attributes, resultAttributes(event, hasResult)...)
	return event
}

// defaultQueryAttributes 返回官方默认低敏属性集合。
func defaultQueryAttributes(meta core.QueryMeta) []Attribute {
	return []Attribute{
		{Key: "querybuilder.datasource", Value: meta.DataSource.String()},
		{Key: "querybuilder.mode", Value: meta.QueryMode()},
		{Key: "querybuilder.pit", Value: meta.IsPITQuery},
		{Key: "querybuilder.need_total", Value: meta.NeedTotal},
		{Key: "querybuilder.need_pagination", Value: meta.NeedPagination},
		{Key: "querybuilder.start", Value: meta.Start},
		{Key: "querybuilder.limit", Value: meta.Limit},
	}
}

// resultAttributes 返回与查询结果和错误状态相关的属性集合。
func resultAttributes(event QueryEvent, hasResult bool) []Attribute {
	resultKind := "unknown"
	if hasResult {
		resultKind = event.ResultKind.String()
	}
	attrs := []Attribute{
		{Key: "querybuilder.success", Value: event.Success},
		{Key: "querybuilder.result_kind", Value: resultKind},
		{Key: "querybuilder.item_count", Value: event.ItemCount},
		{Key: "querybuilder.total", Value: event.Total},
		{Key: "querybuilder.has_more", Value: event.HasMore},
	}
	if event.ErrorType != "" {
		attrs = append(attrs, Attribute{Key: "querybuilder.error_type", Value: event.ErrorType})
	}
	return attrs
}

// cloneAttributes 复制属性切片，防止事件分发后被调用方意外修改。
func cloneAttributes(attrs []Attribute) []Attribute {
	if attrs == nil {
		return nil
	}
	cloned := make([]Attribute, len(attrs))
	copy(cloned, attrs)
	return cloned
}

// normalizeSignalOrder 规范化信号顺序，去重并补齐未声明的默认信号。
func normalizeSignalOrder(order []ObservabilitySignal) []ObservabilitySignal {
	normalized := make([]ObservabilitySignal, 0, len(defaultSignalOrder))
	seen := make(map[ObservabilitySignal]struct{}, len(defaultSignalOrder))
	appendSignal := func(signal ObservabilitySignal) {
		if !isKnownSignal(signal) {
			return
		}
		if _, ok := seen[signal]; ok {
			return
		}
		seen[signal] = struct{}{}
		normalized = append(normalized, signal)
	}

	for _, signal := range order {
		appendSignal(signal)
	}
	for _, signal := range defaultSignalOrder {
		appendSignal(signal)
	}
	return normalized
}

// isKnownSignal 判断 signal 是否为官方支持的信号类型。
func isKnownSignal(signal ObservabilitySignal) bool {
	switch signal {
	case ObservabilitySignalTrace, ObservabilitySignalMetrics, ObservabilitySignalLogger:
		return true
	default:
		return false
	}
}

// safeOperationName 调用自定义 operation 构建器，并在 panic 或空值时回退到默认名称。
func safeOperationName(operationBuilder OperationNameBuilder, meta core.QueryMeta) (operation string) {
	defer func() {
		if recover() != nil || operation == "" {
			operation = DefaultOperationName(meta)
		}
	}()
	return operationBuilder(meta)
}

// safeAttributes 调用自定义属性提供器，并在 panic 时忽略补充属性。
func safeAttributes(ctx context.Context, provider AttributeProvider, meta core.QueryMeta) (attrs []Attribute) {
	defer func() {
		if recover() != nil {
			attrs = nil
		}
	}()
	return provider(ctx, meta)
}

// safeMetaFilter 调用查询元信息过滤器，并在未配置时默认启用信号。
func safeMetaFilter(ctx context.Context, filter QueryMetaFilter, meta core.QueryMeta) (enabled bool) {
	if filter == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			enabled = false
		}
	}()
	return filter(ctx, meta)
}

// safeStartQuery 启动链路 span，并在 tracer panic 或返回 nil context 时回退到原始 context。
func safeStartQuery(
	ctx context.Context,
	tracer QueryTracer,
	start QuerySpanStart,
) (nextCtx context.Context, span QuerySpan) {
	nextCtx = ctx
	defer func() {
		if recover() != nil {
			nextCtx = ctx
			span = nil
		}
	}()
	nextCtx, span = tracer.StartQuery(ctx, start)
	if nextCtx == nil {
		nextCtx = ctx
	}
	return nextCtx, span
}

// safeEventFilter 调用查询完成事件过滤器，并在未配置时默认启用信号。
func safeEventFilter(ctx context.Context, filter QueryEventFilter, event QueryEvent) (enabled bool) {
	if filter == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			enabled = false
		}
	}()
	return filter(ctx, event)
}

// safeErrorClass 调用错误分类器，并在 panic 时回退到默认错误分类。
func safeErrorClass(classifier ErrorClassifier, err error) (errorType string) {
	defer func() {
		if recover() != nil {
			errorType = DefaultErrorClassifier(err)
		}
	}()
	return classifier(err)
}

// safeObserve 执行观测适配器回调，并吞掉适配器自身 panic，避免影响查询结果。
func safeObserve(fn func()) {
	defer func() {
		_ = recover()
	}()
	fn()
}

// panicAsError 将 recover 得到的值转换为可记录的 error。
func panicAsError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", recovered)
}
