// Package agg 为 QueryBuilder 支持的数据源提供类型安全的聚合查询构建器

package agg

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

const (
	defaultLimit uint32 = 100
	maxLimit     uint32 = 5000
)

var (
	// ErrInvalidSpec 表示聚合查询规范无效
	ErrInvalidSpec = errors.New("agg: invalid spec")
	// ErrLimitExceeded 表示分组聚合的结果数量上限超过允许值
	ErrLimitExceeded = errors.New("agg: limit exceeds maximum allowed value (5000)")
	// ErrIndexNotConfigured 表示未配置 Elasticsearch 索引
	ErrIndexNotConfigured = errors.New("agg: elasticsearch index not configured")

	fieldPattern                = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	aliasPattern                = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	comparisonExpressionPattern = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*(==|<>|!=|>=|<=|=|>|<)\s*\?\s*$`)
)

// Func 表示受支持的聚合函数
type Func uint8

const (
	// Count 统计匹配的记录数
	Count Func = iota + 1
	// Sum 对字段的非空值求和
	Sum
	// Avg 计算字段非空值的平均值
	Avg
	// Min 返回字段非空值中的最小值
	Min
	// Max 返回字段非空值中的最大值
	Max
)

// Operator 表示条件表达式使用的比较操作符
type Operator uint8

const (
	// Eq 表示等于比较
	Eq Operator = iota + 1
	// Ne 表示不等于比较
	Ne
	// Gt 表示大于比较
	Gt
	// Gte 表示大于等于比较
	Gte
	// Lt 表示小于比较
	Lt
	// Lte 表示小于等于比较
	Lte
)

// String 返回比较操作符的稳定字符串名称
func (op Operator) String() string {
	switch op {
	case Eq:
		return "eq"
	case Ne:
		return "ne"
	case Gt:
		return "gt"
	case Gte:
		return "gte"
	case Lt:
		return "lt"
	case Lte:
		return "lte"
	default:
		return "unknown"
	}
}

// String 返回聚合函数的稳定字符串名称
func (f Func) String() string {
	switch f {
	case Count:
		return "count"
	case Sum:
		return "sum"
	case Avg:
		return "avg"
	case Min:
		return "min"
	case Max:
		return "max"
	default:
		return "unknown"
	}
}

// Condition 定义字段级条件，用于构建条件指标
type Condition struct {
	Field string   `json:"field"`
	Op    Operator `json:"op"`
	Value any      `json:"value"`
}

// Group 定义分组字段、输出别名及排序方向
type Group struct {
	Field      string `json:"field"`
	Alias      string `json:"alias"`
	Descending bool   `json:"descending"`
}

// Order 定义聚合结果排序规则，Alias 可引用分组或指标别名
type Order struct {
	Alias      string `json:"alias"`
	Descending bool   `json:"descending"`
}

// Having 定义聚合后的指标过滤条件，Alias 必须引用指标别名
type Having struct {
	Alias string   `json:"alias"`
	Op    Operator `json:"op"`
	Value any      `json:"value"`
}

// Metric 定义聚合计算、去重选项及其输出别名
type Metric struct {
	Func      Func       `json:"func"`
	Field     string     `json:"field,omitempty"`
	Alias     string     `json:"alias"`
	Distinct  bool       `json:"distinct,omitempty"`
	Condition *Condition `json:"condition,omitempty"`
}

// Spec 定义跨数据源通用的聚合查询规范
type Spec struct {
	Groups  []Group  `json:"groups"`
	Metrics []Metric `json:"metrics"`
	Orders  []Order  `json:"orders,omitempty"`
	Havings []Having `json:"havings,omitempty"`
	Limit   uint32   `json:"limit"`
}

// Clone 返回聚合查询规范的防御性副本
func (s Spec) Clone() Spec {
	cloned := s
	cloned.Groups = append([]Group(nil), s.Groups...)
	cloned.Metrics = cloneMetrics(s.Metrics)
	cloned.Orders = append([]Order(nil), s.Orders...)
	cloned.Havings = append([]Having(nil), s.Havings...)
	return cloned
}

// Result 保存解码为调用方 DTO 类型的聚合结果行
// 泛型参数:
//
//	A: 聚合结果行 DTO 类型
type Result[A any] struct {
	Rows []*A `json:"rows"`
}

// Meta 表示聚合查询元信息的只读快照
type Meta struct {
	DataSource core.DataSource `json:"data_source"`
	Spec       Spec            `json:"spec"`
	StartTime  time.Time       `json:"start_time"`
}

// QueryMode 返回供中间件使用的稳定查询模式名称
func (m Meta) QueryMode() string { return "aggregate" }

// normalizeSpec 复制并规范化聚合查询配置，为分组查询补充默认 limit
func normalizeSpec(spec Spec) Spec {
	normalized := spec.Clone()
	if len(normalized.Groups) == 0 {
		normalized.Limit = 0
	} else if normalized.Limit == 0 {
		normalized.Limit = defaultLimit
	}
	return normalized
}

// validateSpec 校验聚合函数、字段、别名和结果数量上限
func validateSpec(spec Spec) error {
	if len(spec.Metrics) == 0 {
		return fmt.Errorf("%w: at least one metric is required", ErrInvalidSpec)
	}
	if spec.Limit > maxLimit {
		return ErrLimitExceeded
	}

	aliases := make(map[string]struct{}, len(spec.Groups)+len(spec.Metrics))
	metricAliases := make(map[string]struct{}, len(spec.Metrics))
	for i, group := range spec.Groups {
		if err := validateField(group.Field); err != nil {
			return fmt.Errorf("%w: group %d field: %v", ErrInvalidSpec, i, err)
		}
		if err := registerAlias(aliases, group.Alias); err != nil {
			return fmt.Errorf("%w: group %d alias: %v", ErrInvalidSpec, i, err)
		}
	}

	for i, metric := range spec.Metrics {
		if err := validateFunc(metric.Func); err != nil {
			return fmt.Errorf("%w: metric %d: %v", ErrInvalidSpec, i, err)
		}
		if metric.Distinct && !supportsDistinct(metric.Func) {
			return fmt.Errorf("%w: metric %d distinct is only supported by count and sum", ErrInvalidSpec, i)
		}
		if metric.Func == Count {
			if metric.Distinct {
				if err := validateField(metric.Field); err != nil {
					return fmt.Errorf("%w: metric %d field: %v", ErrInvalidSpec, i, err)
				}
			} else if metric.Field != "" {
				return fmt.Errorf("%w: metric %d count field must be empty", ErrInvalidSpec, i)
			}
		} else if err := validateField(metric.Field); err != nil {
			return fmt.Errorf("%w: metric %d field: %v", ErrInvalidSpec, i, err)
		}
		if err := registerAlias(aliases, metric.Alias); err != nil {
			return fmt.Errorf("%w: metric %d alias: %v", ErrInvalidSpec, i, err)
		}
		if metric.Condition != nil {
			if err := validateCondition(*metric.Condition); err != nil {
				return fmt.Errorf("%w: metric %d condition: %v", ErrInvalidSpec, i, err)
			}
		}
		metricAliases[strings.ToLower(metric.Alias)] = struct{}{}
	}

	orderAliases := make(map[string]struct{}, len(spec.Orders))
	for i, order := range spec.Orders {
		if err := validateAliasReference(aliases, order.Alias); err != nil {
			return fmt.Errorf("%w: order %d alias: %v", ErrInvalidSpec, i, err)
		}
		key := strings.ToLower(order.Alias)
		if _, exists := orderAliases[key]; exists {
			return fmt.Errorf("%w: order %d alias: duplicate alias %q", ErrInvalidSpec, i, order.Alias)
		}
		orderAliases[key] = struct{}{}
	}

	if len(spec.Havings) > 0 && len(spec.Groups) == 0 {
		return fmt.Errorf("%w: having requires at least one group", ErrInvalidSpec)
	}
	for i, having := range spec.Havings {
		if err := validateAliasReference(metricAliases, having.Alias); err != nil {
			return fmt.Errorf("%w: having %d alias: %v", ErrInvalidSpec, i, err)
		}
		if err := validateOperator(having.Op); err != nil {
			return fmt.Errorf("%w: having %d: %v", ErrInvalidSpec, i, err)
		}
		if !isNumericValue(having.Value) {
			return fmt.Errorf("%w: having %d value must be numeric", ErrInvalidSpec, i)
		}
	}

	return nil
}

// isDistinctCount 判断指标是否为去重计数
func isDistinctCount(metric Metric) bool {
	return metric.Func == Count && metric.Distinct
}

// isDistinctSum 判断指标是否为去重求和
func isDistinctSum(metric Metric) bool {
	return metric.Func == Sum && metric.Distinct
}

// isDistinctMetric 判断指标是否为已支持的去重指标
func isDistinctMetric(metric Metric) bool {
	return metric.Distinct && supportsDistinct(metric.Func)
}

// supportsDistinct 判断聚合函数是否支持去重修饰
func supportsDistinct(fn Func) bool {
	switch fn {
	case Count, Sum:
		return true
	default:
		return false
	}
}

// requiresMetricFacet 判断指标是否需要在 MongoDB 中使用独立分支计算
func requiresMetricFacet(metric Metric) bool {
	return isDistinctMetric(metric) || metric.Condition != nil
}

// effectiveOrders 返回显式排序；未设置时保持按分组字段排序的兼容行为
func effectiveOrders(spec Spec) []Order {
	if len(spec.Orders) > 0 {
		return spec.Orders
	}
	orders := make([]Order, 0, len(spec.Groups))
	for _, group := range spec.Groups {
		orders = append(orders, Order{
			Alias:      group.Alias,
			Descending: group.Descending,
		})
	}
	return orders
}

// validateField 校验字段是否为安全的点分标识符
func validateField(field string) error {
	if !fieldPattern.MatchString(field) {
		return fmt.Errorf("invalid field %q", field)
	}
	return nil
}

// validateFunc 校验聚合函数是否属于当前支持集合
func validateFunc(fn Func) error {
	switch fn {
	case Count, Sum, Avg, Min, Max:
		return nil
	default:
		return fmt.Errorf("unsupported function %q", fn.String())
	}
}

// validateOperator 校验条件比较操作符是否属于当前支持集合
func validateOperator(op Operator) error {
	switch op {
	case Eq, Ne, Gt, Gte, Lt, Lte:
		return nil
	default:
		return fmt.Errorf("unsupported operator %q", op.String())
	}
}

// validateCondition 校验条件字段、操作符和值
func validateCondition(condition Condition) error {
	if err := validateField(condition.Field); err != nil {
		return err
	}
	if err := validateOperator(condition.Op); err != nil {
		return err
	}
	if !isScalarValue(condition.Value) {
		return fmt.Errorf("invalid condition value %v", condition.Value)
	}
	return nil
}

// conditionFromExpression 将 GORM-like 比较表达式转换为 Condition
func conditionFromExpression(expression string, value any) Condition {
	field, op, ok := parseComparisonExpression(expression)
	if !ok {
		return Condition{Field: expression, Value: value}
	}
	return Condition{Field: field, Op: op, Value: value}
}

// havingFromExpression 将 GORM-like 比较表达式转换为 Having
func havingFromExpression(expression string, value any) Having {
	alias, op, ok := parseComparisonExpression(expression)
	if !ok {
		return Having{Alias: expression, Value: value}
	}
	return Having{Alias: alias, Op: op, Value: value}
}

// parseComparisonExpression 解析单个标识符与一个占位符组成的比较表达式
func parseComparisonExpression(expression string) (string, Operator, bool) {
	matches := comparisonExpressionPattern.FindStringSubmatch(expression)
	if len(matches) != 3 {
		return "", 0, false
	}
	op, ok := parseComparisonOperator(matches[2])
	if !ok {
		return "", 0, false
	}
	return matches[1], op, true
}

// parseComparisonOperator 将 SQL-like 比较符号映射为通用 Operator
func parseComparisonOperator(token string) (Operator, bool) {
	switch token {
	case "=", "==":
		return Eq, true
	case "<>", "!=":
		return Ne, true
	case ">":
		return Gt, true
	case ">=":
		return Gte, true
	case "<":
		return Lt, true
	case "<=":
		return Lte, true
	default:
		return 0, false
	}
}

// registerAlias 校验并登记输出别名，确保别名不区分大小写时仍唯一
func registerAlias(aliases map[string]struct{}, alias string) error {
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("invalid alias %q", alias)
	}
	key := strings.ToLower(alias)
	if key == "_id" {
		return fmt.Errorf("reserved alias %q", alias)
	}
	if _, exists := aliases[key]; exists {
		return fmt.Errorf("duplicate alias %q", alias)
	}
	aliases[key] = struct{}{}
	return nil
}

// validateAliasReference 校验引用的输出别名是否已存在
func validateAliasReference(aliases map[string]struct{}, alias string) error {
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("invalid alias %q", alias)
	}
	if _, exists := aliases[strings.ToLower(alias)]; !exists {
		return fmt.Errorf("unknown alias %q", alias)
	}
	return nil
}

// metricByAlias 按别名查找指标配置
func metricByAlias(metrics []Metric, alias string) (Metric, bool) {
	for _, metric := range metrics {
		if strings.EqualFold(metric.Alias, alias) {
			return metric, true
		}
	}
	return Metric{}, false
}

// cloneMetrics 返回指标列表的防御性副本
func cloneMetrics(metrics []Metric) []Metric {
	cloned := append([]Metric(nil), metrics...)
	for i := range cloned {
		cloned[i] = cloneMetric(cloned[i])
	}
	return cloned
}

// cloneMetric 返回单个指标的防御性副本
func cloneMetric(metric Metric) Metric {
	if metric.Condition != nil {
		condition := *metric.Condition
		metric.Condition = &condition
	}
	return metric
}

// conditionPtr 返回条件的独立指针副本
func conditionPtr(condition Condition) *Condition {
	cloned := condition
	return &cloned
}

// isScalarValue 判断值是否可作为字段条件比较值
func isScalarValue(value any) bool {
	if value == nil {
		return false
	}
	if isNumericValue(value) {
		return true
	}
	switch value.(type) {
	case string, bool, time.Time:
		return true
	default:
		return false
	}
}

// isNumericValue 判断值是否为 HAVING 支持的数值类型
func isNumericValue(value any) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	default:
		return false
	}
}
