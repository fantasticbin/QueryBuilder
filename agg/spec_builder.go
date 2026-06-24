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

// SetMetrics 替换全部聚合指标
func (b *SpecBuilder) SetMetrics(metrics ...Metric) *SpecBuilder {
	b.spec.Metrics = append([]Metric{}, metrics...)
	return b
}

// AddMetric 追加一个完整指标配置
func (b *SpecBuilder) AddMetric(metric Metric) *SpecBuilder {
	b.spec.Metrics = append(b.spec.Metrics, metric)
	return b
}

// Metric 追加一个聚合指标
func (b *SpecBuilder) Metric(fn Func, field, alias string) *SpecBuilder {
	return b.AddMetric(Metric{Func: fn, Field: field, Alias: alias})
}

// Count 追加记录数统计指标
func (b *SpecBuilder) Count(alias string) *SpecBuilder {
	return b.Metric(Count, "", alias)
}

// CountDistinct 追加字段去重计数指标
func (b *SpecBuilder) CountDistinct(field, alias string) *SpecBuilder {
	return b.AddMetric(Metric{Func: Count, Field: field, Alias: alias, Distinct: true})
}

// Sum 追加求和指标
func (b *SpecBuilder) Sum(field, alias string) *SpecBuilder {
	return b.Metric(Sum, field, alias)
}

// SumDistinct 追加字段去重求和指标
func (b *SpecBuilder) SumDistinct(field, alias string) *SpecBuilder {
	return b.AddMetric(Metric{Func: Sum, Field: field, Alias: alias, Distinct: true})
}

// Avg 追加平均值指标
func (b *SpecBuilder) Avg(field, alias string) *SpecBuilder {
	return b.Metric(Avg, field, alias)
}

// Min 追加最小值指标
func (b *SpecBuilder) Min(field, alias string) *SpecBuilder {
	return b.Metric(Min, field, alias)
}

// Max 追加最大值指标
func (b *SpecBuilder) Max(field, alias string) *SpecBuilder {
	return b.Metric(Max, field, alias)
}

// SetLimit 设置分组结果数量上限
func (b *SpecBuilder) SetLimit(limit uint32) *SpecBuilder {
	b.spec.Limit = limit
	return b
}

func (b *SpecBuilder) normalizeLimitAfterGroups() {
	if len(b.spec.Groups) == 0 {
		b.spec.Limit = 0
		return
	}
	if b.spec.Limit == 0 {
		b.spec.Limit = defaultLimit
	}
}
