package agg

// SpecOption 表示对 SpecBuilder 的一次配置变更
type SpecOption func(*SpecBuilder)

// SpecBuilder 提供独立于 Builder 接口的聚合规范 DSL
type SpecBuilder struct {
	spec Spec
}

// NewSpecBuilder 创建一个独立的聚合规范构建器
func NewSpecBuilder(spec Spec) *SpecBuilder {
	return newSpecBuilder(normalizeSpec(spec))
}

// newSpecBuilder 创建一个不额外规范化输入的聚合规范构建器
func newSpecBuilder(spec Spec) *SpecBuilder {
	return &SpecBuilder{spec: spec.Clone()}
}

// Spec 返回当前聚合规范的防御性副本
func (b *SpecBuilder) Spec() Spec {
	return b.spec.Clone()
}

// SetSpec 替换聚合查询规范
func (b *SpecBuilder) SetSpec(spec Spec) *SpecBuilder {
	b.spec = normalizeSpec(spec)
	return b
}

// SetGroups 替换全部分组字段
func (b *SpecBuilder) SetGroups(groups ...Group) *SpecBuilder {
	b.spec.Groups = append([]Group{}, groups...)
	b.normalizeLimitAfterGroups()
	return b
}

// AddGroup 追加一个完整分组配置
func (b *SpecBuilder) AddGroup(group Group) *SpecBuilder {
	b.spec.Groups = append(b.spec.Groups, group)
	b.normalizeLimitAfterGroups()
	return b
}

// GroupBy 追加一个升序分组字段
func (b *SpecBuilder) GroupBy(field, alias string) *SpecBuilder {
	return b.AddGroup(Group{Field: field, Alias: alias})
}

// GroupByDesc 追加一个降序分组字段
func (b *SpecBuilder) GroupByDesc(field, alias string) *SpecBuilder {
	return b.AddGroup(Group{Field: field, Alias: alias, Descending: true})
}

// GroupByDate 追加一个升序时间桶分组字段
func (b *SpecBuilder) GroupByDate(field, alias string, interval TimeInterval) *SpecBuilder {
	return b.GroupByDateWithTimeZone(field, alias, interval, "")
}

// GroupByDateWithTimeZone 追加一个带时区的升序时间桶分组字段
func (b *SpecBuilder) GroupByDateWithTimeZone(field, alias string, interval TimeInterval, timeZone string) *SpecBuilder {
	return b.AddGroup(Group{Field: field, Alias: alias, Interval: interval, TimeZone: timeZone})
}

// SetMetrics 替换全部聚合指标
func (b *SpecBuilder) SetMetrics(metrics ...Metric) *SpecBuilder {
	b.spec.Metrics = cloneMetrics(metrics)
	return b
}

// AddMetric 追加一个完整指标配置
func (b *SpecBuilder) AddMetric(metric Metric) *SpecBuilder {
	b.spec.Metrics = append(b.spec.Metrics, cloneMetric(metric))
	return b
}

// Metric 追加一个聚合指标
func (b *SpecBuilder) Metric(fn Func, field, alias string) *SpecBuilder {
	return b.AddMetric(Metric{Func: fn, Field: field, Alias: alias})
}

// MetricIf 追加一个条件聚合指标，条件使用比较表达式形式
func (b *SpecBuilder) MetricIf(fn Func, field, alias, expression string, value any) *SpecBuilder {
	condition := conditionFromExpression(expression, value)
	return b.MetricWhere(fn, field, alias, condition)
}

// MetricWhere 追加一个使用类型化条件的聚合指标
func (b *SpecBuilder) MetricWhere(fn Func, field, alias string, condition Condition) *SpecBuilder {
	return b.AddMetric(Metric{Func: fn, Field: field, Alias: alias, Condition: conditionPtr(condition)})
}

// Count 追加记录数统计指标
func (b *SpecBuilder) Count(alias string) *SpecBuilder {
	return b.Metric(Count, "", alias)
}

// CountIf 追加满足条件的记录数统计指标，条件使用 "field = ?" 形式
func (b *SpecBuilder) CountIf(alias, expression string, value any) *SpecBuilder {
	return b.MetricIf(Count, "", alias, expression, value)
}

// CountDistinct 追加字段去重计数指标
func (b *SpecBuilder) CountDistinct(field, alias string) *SpecBuilder {
	return b.AddMetric(Metric{Func: Count, Field: field, Alias: alias, Distinct: true})
}

// CountDistinctIf 追加满足条件的字段去重计数指标，条件使用 "field = ?" 形式
func (b *SpecBuilder) CountDistinctIf(field, alias, expression string, value any) *SpecBuilder {
	condition := conditionFromExpression(expression, value)
	return b.AddMetric(Metric{Func: Count, Field: field, Alias: alias, Distinct: true, Condition: conditionPtr(condition)})
}

// Sum 追加求和指标
func (b *SpecBuilder) Sum(field, alias string) *SpecBuilder {
	return b.Metric(Sum, field, alias)
}

// SumIf 追加满足条件的求和指标，条件使用 "field = ?" 形式
func (b *SpecBuilder) SumIf(field, alias, expression string, value any) *SpecBuilder {
	return b.MetricIf(Sum, field, alias, expression, value)
}

// SumDistinct 追加字段去重求和指标
func (b *SpecBuilder) SumDistinct(field, alias string) *SpecBuilder {
	return b.AddMetric(Metric{Func: Sum, Field: field, Alias: alias, Distinct: true})
}

// SumDistinctIf 追加满足条件的字段去重求和指标，条件使用 "field = ?" 形式
func (b *SpecBuilder) SumDistinctIf(field, alias, expression string, value any) *SpecBuilder {
	condition := conditionFromExpression(expression, value)
	return b.AddMetric(Metric{Func: Sum, Field: field, Alias: alias, Distinct: true, Condition: conditionPtr(condition)})
}

// Avg 追加平均值指标
func (b *SpecBuilder) Avg(field, alias string) *SpecBuilder {
	return b.Metric(Avg, field, alias)
}

// AvgIf 追加满足条件的平均值指标，条件使用 "field = ?" 形式
func (b *SpecBuilder) AvgIf(field, alias, expression string, value any) *SpecBuilder {
	return b.MetricIf(Avg, field, alias, expression, value)
}

// Min 追加最小值指标
func (b *SpecBuilder) Min(field, alias string) *SpecBuilder {
	return b.Metric(Min, field, alias)
}

// MinIf 追加满足条件的最小值指标，条件使用 "field = ?" 形式
func (b *SpecBuilder) MinIf(field, alias, expression string, value any) *SpecBuilder {
	return b.MetricIf(Min, field, alias, expression, value)
}

// Max 追加最大值指标
func (b *SpecBuilder) Max(field, alias string) *SpecBuilder {
	return b.Metric(Max, field, alias)
}

// MaxIf 追加满足条件的最大值指标，条件使用 "field = ?" 形式
func (b *SpecBuilder) MaxIf(field, alias, expression string, value any) *SpecBuilder {
	return b.MetricIf(Max, field, alias, expression, value)
}

// SetOrders 替换全部聚合结果排序规则
func (b *SpecBuilder) SetOrders(orders ...Order) *SpecBuilder {
	b.spec.Orders = append([]Order{}, orders...)
	return b
}

// AddOrder 追加一个聚合结果排序规则
func (b *SpecBuilder) AddOrder(order Order) *SpecBuilder {
	b.spec.Orders = append(b.spec.Orders, order)
	return b
}

// OrderBy 追加一个升序聚合结果排序规则
func (b *SpecBuilder) OrderBy(alias string) *SpecBuilder {
	return b.AddOrder(Order{Alias: alias})
}

// OrderByDesc 追加一个降序聚合结果排序规则
func (b *SpecBuilder) OrderByDesc(alias string) *SpecBuilder {
	return b.AddOrder(Order{Alias: alias, Descending: true})
}

// SetHavings 替换全部聚合后过滤条件
func (b *SpecBuilder) SetHavings(havings ...Having) *SpecBuilder {
	b.spec.Havings = append([]Having{}, havings...)
	return b
}

// AddHaving 追加一个聚合后过滤条件
func (b *SpecBuilder) AddHaving(having Having) *SpecBuilder {
	b.spec.Havings = append(b.spec.Havings, having)
	return b
}

// Having 追加一个聚合后指标过滤条件，条件使用 "alias >= ?" 形式
func (b *SpecBuilder) Having(expression string, value any) *SpecBuilder {
	return b.AddHaving(havingFromExpression(expression, value))
}

// SetStart 设置分组结果分页起始偏移
func (b *SpecBuilder) SetStart(start uint32) *SpecBuilder {
	b.spec.Start = start
	return b
}

// SetLimit 设置分组结果数量上限
func (b *SpecBuilder) SetLimit(limit uint32) *SpecBuilder {
	b.spec.Limit = limit
	return b
}

// normalizeLimitAfterGroups 在分组变更后同步默认 limit 语义
func (b *SpecBuilder) normalizeLimitAfterGroups() {
	if len(b.spec.Groups) == 0 {
		b.spec.Start = 0
		b.spec.Limit = 0
		return
	}
	if b.spec.Limit == 0 {
		b.spec.Limit = defaultLimit
	}
}
