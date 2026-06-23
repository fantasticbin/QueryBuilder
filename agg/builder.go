package agg

import (
	"context"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

// Handler 表示聚合查询执行函数
type Handler[A any] func(context.Context) (*Result[A], error)

// Middleware 用于包装聚合查询执行过程
type Middleware[A any] func(context.Context, Querier[A], Handler[A]) (*Result[A], error)

// BeforeHook 在聚合中间件链执行前运行
type BeforeHook func(context.Context) context.Context

// AfterHook 在聚合中间件链执行后运行
type AfterHook[A any] func(context.Context, *Result[A], error)

// Querier 是所有聚合构建器共同实现的执行与元信息接口
type Querier[A any] interface {
	Query(context.Context) (*Result[A], error)
	Explain(context.Context) (string, error)
	Meta() Meta
}

// PipelineConfigurer 定义聚合中间件与钩子配置能力
type PipelineConfigurer[A any] interface {
	Use(Middleware[A]) Builder[A]
	SetBeforeHook(BeforeHook) Builder[A]
	SetAfterHook(AfterHook[A]) Builder[A]
}

// SpecConfigurer 定义聚合规范配置能力
type SpecConfigurer[A any] interface {
	SetSpec(Spec) Builder[A]
	SetGroups(...Group) Builder[A]
	AddGroup(Group) Builder[A]
	GroupBy(field, alias string) Builder[A]
	GroupByDesc(field, alias string) Builder[A]
	SetMetrics(...Metric) Builder[A]
	AddMetric(Metric) Builder[A]
	Metric(fn Func, field, alias string) Builder[A]
	Count(alias string) Builder[A]
	Sum(field, alias string) Builder[A]
	Avg(field, alias string) Builder[A]
	Min(field, alias string) Builder[A]
	Max(field, alias string) Builder[A]
	SetLimit(limit uint32) Builder[A]
}

// Builder 组合聚合构建器的配置、执行与元信息能力
type Builder[A any] interface {
	Querier[A]
	PipelineConfigurer[A]
	SpecConfigurer[A]
}

type base[A any] struct {
	data        *core.DBProxy
	dataSource  core.DataSource
	spec        Spec
	startTime   time.Time
	middlewares []Middleware[A]
	beforeHook  BeforeHook
	afterHook   AfterHook[A]
	self        Builder[A]
}

// setSelf 保存具体构建器的接口引用，供中间件链回传当前构建器
func (b *base[A]) setSelf(self Builder[A]) { b.self = self }

// Use 添加聚合查询中间件
func (b *base[A]) Use(middleware Middleware[A]) Builder[A] {
	if middleware != nil {
		b.middlewares = append(b.middlewares, middleware)
	}
	return b.self
}

// SetBeforeHook 设置聚合查询前置钩子
func (b *base[A]) SetBeforeHook(hook BeforeHook) Builder[A] {
	b.beforeHook = hook
	return b.self
}

// SetAfterHook 设置聚合查询后置钩子
func (b *base[A]) SetAfterHook(hook AfterHook[A]) Builder[A] {
	b.afterHook = hook
	return b.self
}

// SetSpec 替换聚合查询规范
func (b *base[A]) SetSpec(spec Spec) Builder[A] {
	b.spec = normalizeSpec(spec)
	return b.self
}

// SetGroups 替换全部分组字段
func (b *base[A]) SetGroups(groups ...Group) Builder[A] {
	b.spec.Groups = append([]Group{}, groups...)
	if len(b.spec.Groups) == 0 {
		b.spec.Limit = 0
	} else if b.spec.Limit == 0 {
		b.spec.Limit = defaultLimit
	}
	return b.self
}

// AddGroup 追加一个完整分组配置
func (b *base[A]) AddGroup(group Group) Builder[A] {
	b.spec.Groups = append(b.spec.Groups, group)
	if b.spec.Limit == 0 {
		b.spec.Limit = defaultLimit
	}
	return b.self
}

// GroupBy 追加一个升序分组字段
func (b *base[A]) GroupBy(field, alias string) Builder[A] {
	return b.AddGroup(Group{Field: field, Alias: alias})
}

// GroupByDesc 追加一个降序分组字段
func (b *base[A]) GroupByDesc(field, alias string) Builder[A] {
	return b.AddGroup(Group{Field: field, Alias: alias, Descending: true})
}

// SetMetrics 替换全部聚合指标
func (b *base[A]) SetMetrics(metrics ...Metric) Builder[A] {
	b.spec.Metrics = append([]Metric{}, metrics...)
	return b.self
}

// AddMetric 追加一个完整指标配置
func (b *base[A]) AddMetric(metric Metric) Builder[A] {
	b.spec.Metrics = append(b.spec.Metrics, metric)
	return b.self
}

// Metric 追加一个聚合指标
func (b *base[A]) Metric(fn Func, field, alias string) Builder[A] {
	return b.AddMetric(Metric{Func: fn, Field: field, Alias: alias})
}

// Count 追加记录数统计指标
func (b *base[A]) Count(alias string) Builder[A] {
	return b.Metric(Count, "", alias)
}

// Sum 追加求和指标
func (b *base[A]) Sum(field, alias string) Builder[A] {
	return b.Metric(Sum, field, alias)
}

// Avg 追加平均值指标
func (b *base[A]) Avg(field, alias string) Builder[A] {
	return b.Metric(Avg, field, alias)
}

// Min 追加最小值指标
func (b *base[A]) Min(field, alias string) Builder[A] {
	return b.Metric(Min, field, alias)
}

// Max 追加最大值指标
func (b *base[A]) Max(field, alias string) Builder[A] {
	return b.Metric(Max, field, alias)
}

// SetLimit 设置分组结果数量上限
func (b *base[A]) SetLimit(limit uint32) Builder[A] {
	b.spec.Limit = limit
	return b.self
}

// Meta 返回当前聚合查询元信息的防御性副本
func (b *base[A]) Meta() Meta {
	return Meta{
		DataSource: b.dataSource,
		Spec:       b.spec.Clone(),
		StartTime:  b.startTime,
	}
}

// prepare 校验数据源配置和聚合查询规范
func (b *base[A]) prepare() error {
	if b.data == nil {
		return core.ErrDataNotConfigured
	}
	if err := b.data.CheckConfigured(b.dataSource); err != nil {
		return err
	}
	b.spec = normalizeSpec(b.spec)
	return validateSpec(b.spec)
}

// execute 依次执行前置钩子、中间件链、查询函数和后置钩子
func (b *base[A]) execute(ctx context.Context, handler Handler[A]) (*Result[A], error) {
	b.startTime = time.Now()
	if b.beforeHook != nil {
		ctx = b.beforeHook(ctx)
	}

	next := handler
	for i := len(b.middlewares) - 1; i >= 0; i-- {
		middleware := b.middlewares[i]
		wrapped := next
		next = func(ctx context.Context) (*Result[A], error) {
			return middleware(ctx, b.self, wrapped)
		}
	}

	result, err := next(ctx)
	if b.afterHook != nil {
		b.afterHook(ctx, result, err)
	}
	return result, err
}

// cloneBase 将公共配置深拷贝到目标基础构建器
func (b *base[A]) cloneBase(dst *base[A]) {
	dst.data = b.data
	dst.dataSource = b.dataSource
	dst.spec = b.spec.Clone()
	dst.startTime = b.startTime
	dst.beforeHook = b.beforeHook
	dst.afterHook = b.afterHook
	dst.middlewares = append([]Middleware[A]{}, b.middlewares...)
}
