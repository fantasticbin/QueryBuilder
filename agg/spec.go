// Package agg 为 QueryBuilder 支持的数据源提供类型安全的聚合查询构建器

package agg

import (
	"errors"
	"fmt"
	"reflect"
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
	// ErrAggEmptyResponse 表示 Elasticsearch 聚合响应为空
	ErrAggEmptyResponse = errors.New("decoding elasticsearch aggregate result: empty response")
	// ErrAggRootAggMissing 表示 Elasticsearch 聚合结果缺失根聚合
	ErrAggRootAggMissing = errors.New("decoding elasticsearch aggregate result: root aggregation missing")
	// ErrAggGroupAggMissing 表示 Elasticsearch 聚合结果缺失分组聚合
	ErrAggGroupAggMissing = errors.New("decoding elasticsearch aggregate result: group aggregation missing")
	// ErrAggMetricMissing 表示 Elasticsearch 聚合结果缺失指标聚合
	ErrAggMetricMissing = errors.New("decoding elasticsearch aggregate result: metric missing")

	fieldPattern                = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	aliasPattern                = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	comparisonExpressionPattern = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*(==|<>|!=|>=|<=|=|>|<)\s*\?\s*$`)
	setExpressionPattern        = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s+(IN|NOT\s+IN)\s+\?\s*$`)
	betweenExpressionPattern    = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s+BETWEEN\s+\?\s+AND\s+\?\s*$`)
	likeExpressionPattern       = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s+(LIKE|NOT\s+LIKE)\s+\?\s*$`)
	unaryExpressionPattern      = regexp.MustCompile(`(?i)^\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s+(EXISTS|NOT\s+EXISTS|IS\s+NULL|IS\s+NOT\s+NULL)\s*$`)
	timeZonePattern             = regexp.MustCompile(`^[A-Za-z0-9_+\-./:]+$`)
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
	// In 表示集合包含比较
	In
	// NotIn 表示集合不包含比较
	NotIn
	// Between 表示闭区间比较
	Between
	// Exists 表示字段存在
	Exists
	// NotExists 表示字段不存在
	NotExists
	// IsNull 表示字段为空
	IsNull
	// IsNotNull 表示字段非空
	IsNotNull
	// Like 表示模式匹配
	Like
	// NotLike 表示模式不匹配
	NotLike
)

// TimeInterval 表示时间桶分组的粒度
type TimeInterval string

const (
	// TimeIntervalMinute 表示按分钟聚合
	TimeIntervalMinute TimeInterval = "minute"
	// TimeIntervalHour 表示按小时聚合
	TimeIntervalHour TimeInterval = "hour"
	// TimeIntervalDay 表示按天聚合
	TimeIntervalDay TimeInterval = "day"
	// TimeIntervalWeek 表示按周聚合
	TimeIntervalWeek TimeInterval = "week"
	// TimeIntervalMonth 表示按月聚合
	TimeIntervalMonth TimeInterval = "month"
	// TimeIntervalQuarter 表示按季度聚合
	TimeIntervalQuarter TimeInterval = "quarter"
	// TimeIntervalYear 表示按年聚合
	TimeIntervalYear TimeInterval = "year"
)

// Range 定义 BETWEEN 条件使用的闭区间边界
type Range struct {
	Start any `json:"start"`
	End   any `json:"end"`
}

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
	case In:
		return "in"
	case NotIn:
		return "not_in"
	case Between:
		return "between"
	case Exists:
		return "exists"
	case NotExists:
		return "not_exists"
	case IsNull:
		return "is_null"
	case IsNotNull:
		return "is_not_null"
	case Like:
		return "like"
	case NotLike:
		return "not_like"
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
	Field      string       `json:"field"`
	Alias      string       `json:"alias"`
	Descending bool         `json:"descending"`
	Interval   TimeInterval `json:"interval,omitempty"`
	TimeZone   string       `json:"time_zone,omitempty"`
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
	Field     string     `json:"field,omitempty"`
	Alias     string     `json:"alias"`
	Condition *Condition `json:"condition,omitempty"`
	Func      Func       `json:"func"`
	Distinct  bool       `json:"distinct,omitempty"`
}

// Spec 定义跨数据源通用的聚合查询规范
type Spec struct {
	Groups  []Group  `json:"groups"`
	Metrics []Metric `json:"metrics"`
	Orders  []Order  `json:"orders,omitempty"`
	Havings []Having `json:"havings,omitempty"`
	Start   uint32   `json:"start,omitempty"`
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
	Rows  []*A  `json:"rows"`
	Total int64 `json:"total"`
}

// Meta 表示聚合查询元信息的只读快照
type Meta struct {
	Spec       Spec            `json:"spec"`
	StartTime  time.Time       `json:"start_time"`
	Plan       Plan            `json:"plan"`
	Start      uint32          `json:"start"`
	TotalLimit uint32          `json:"total_limit"`
	DataSource core.DataSource `json:"data_source"`
	NeedTotal  bool            `json:"need_total"`
}

// QueryMode 返回供中间件使用的稳定查询模式名称
func (m Meta) QueryMode() string { return "aggregate" }

// PlanFlags 以位掩码描述聚合查询的执行特征
type PlanFlags uint64

const (
	PlanHasDistinctMetrics PlanFlags = 1 << iota
	PlanHasConditionalMetrics
	PlanHasDateGroups
	PlanUsesMongoFacet
	PlanUsesElasticScriptedMetric
	PlanNeedsClientPostProcessing
	PlanNeedsFullClientPostProcessing
)

// Has 判断当前掩码是否包含指定执行特征
func (f PlanFlags) Has(flag PlanFlags) bool {
	return flag != 0 && f&flag == flag
}

// Plan 描述一次聚合查询在通用层面的执行特征
type Plan struct {
	Flags PlanFlags `json:"flags" bson:"flags"`
}

// Has 判断聚合计划是否包含指定执行特征
func (p Plan) Has(flag PlanFlags) bool {
	return p.Flags.Has(flag)
}

// AnalyzeSpec 返回聚合规范在指定数据源上的执行特征
func AnalyzeSpec(dataSource core.DataSource, spec Spec) Plan {
	spec = normalizeSpec(spec)
	plan := Plan{}
	for _, group := range spec.Groups {
		if group.Interval != "" {
			plan.Flags |= PlanHasDateGroups
			break
		}
	}
	for _, metric := range spec.Metrics {
		if metric.Distinct {
			plan.Flags |= PlanHasDistinctMetrics
		}
		if metric.Condition != nil {
			plan.Flags |= PlanHasConditionalMetrics
		}
		if dataSource == core.MongoDB && requiresMetricFacet(metric) {
			plan.Flags |= PlanUsesMongoFacet
		}
		if dataSource == core.ElasticSearch && isDistinctSum(metric) {
			plan.Flags |= PlanUsesElasticScriptedMetric
		}
	}
	if dataSource == core.ElasticSearch && len(spec.Groups) > 0 {
		if elasticOrdersNeedClientPostProcessingSpec(spec) {
			plan.Flags |= PlanNeedsFullClientPostProcessing
		}
		if len(spec.Havings) > 0 || plan.Has(PlanNeedsFullClientPostProcessing) {
			plan.Flags |= PlanNeedsClientPostProcessing
		}
	}
	return plan
}

// normalizeSpec 复制并规范化聚合查询配置，为分组查询补充默认 limit
func normalizeSpec(spec Spec) Spec {
	normalized := spec.Clone()
	if len(normalized.Groups) == 0 {
		normalized.Start = 0
		normalized.Limit = 0
	} else if normalized.Limit == 0 {
		normalized.Limit = defaultLimit
	}
	return normalized
}

// validateSpec 校验聚合函数、字段、别名和结果数量上限
// 采用 errors.Join 汇总所有校验错误，一次性返回全部问题，避免调用方逐个试错
func validateSpec(spec Spec) error {
	var errs []error

	if len(spec.Metrics) == 0 {
		errs = append(errs, fmt.Errorf("%w: at least one metric is required", ErrInvalidSpec))
	}
	if spec.Limit > maxLimit {
		errs = append(errs, ErrLimitExceeded)
	}

	aliases := make(map[string]struct{}, len(spec.Groups)+len(spec.Metrics))
	metricAliases := make(map[string]struct{}, len(spec.Metrics))
	for i, group := range spec.Groups {
		if err := validateField(group.Field); err != nil {
			errs = append(errs, fmt.Errorf("%w: group %d field: %v", ErrInvalidSpec, i, err))
		}
		if err := registerAlias(aliases, group.Alias); err != nil {
			errs = append(errs, fmt.Errorf("%w: group %d alias: %v", ErrInvalidSpec, i, err))
		}
		if err := validateGroupTimeBucket(group); err != nil {
			errs = append(errs, fmt.Errorf("%w: group %d: %v", ErrInvalidSpec, i, err))
		}
	}

	for i, metric := range spec.Metrics {
		if err := validateFunc(metric.Func); err != nil {
			errs = append(errs, fmt.Errorf("%w: metric %d: %v", ErrInvalidSpec, i, err))
		}
		if metric.Distinct && !supportsDistinct(metric.Func) {
			errs = append(errs, fmt.Errorf("%w: metric %d distinct is only supported by count and sum", ErrInvalidSpec, i))
		}
		if metric.Func == Count {
			if metric.Distinct {
				if err := validateField(metric.Field); err != nil {
					errs = append(errs, fmt.Errorf("%w: metric %d field: %v", ErrInvalidSpec, i, err))
				}
			} else if metric.Field != "" {
				errs = append(errs, fmt.Errorf("%w: metric %d count field must be empty", ErrInvalidSpec, i))
			}
		} else if err := validateField(metric.Field); err != nil {
			errs = append(errs, fmt.Errorf("%w: metric %d field: %v", ErrInvalidSpec, i, err))
		}
		if err := registerAlias(aliases, metric.Alias); err != nil {
			errs = append(errs, fmt.Errorf("%w: metric %d alias: %v", ErrInvalidSpec, i, err))
		}
		if metric.Condition != nil {
			if err := validateCondition(*metric.Condition); err != nil {
				errs = append(errs, fmt.Errorf("%w: metric %d condition: %v", ErrInvalidSpec, i, err))
			}
		}
		metricAliases[strings.ToLower(metric.Alias)] = struct{}{}
	}

	orderAliases := make(map[string]struct{}, len(spec.Orders))
	for i, order := range spec.Orders {
		if err := validateAliasReference(aliases, order.Alias); err != nil {
			errs = append(errs, fmt.Errorf("%w: order %d alias: %v", ErrInvalidSpec, i, err))
		}
		key := strings.ToLower(order.Alias)
		if _, exists := orderAliases[key]; exists {
			errs = append(errs, fmt.Errorf("%w: order %d alias: duplicate alias %q", ErrInvalidSpec, i, order.Alias))
		} else {
			orderAliases[key] = struct{}{}
		}
	}

	if len(spec.Havings) > 0 && len(spec.Groups) == 0 {
		errs = append(errs, fmt.Errorf("%w: having requires at least one group", ErrInvalidSpec))
	}
	for i, having := range spec.Havings {
		if err := validateAliasReference(metricAliases, having.Alias); err != nil {
			errs = append(errs, fmt.Errorf("%w: having %d alias: %v", ErrInvalidSpec, i, err))
		}
		if err := validateComparisonOperator(having.Op); err != nil {
			errs = append(errs, fmt.Errorf("%w: having %d: %v", ErrInvalidSpec, i, err))
		}
		if !isNumericValue(having.Value) {
			errs = append(errs, fmt.Errorf("%w: having %d value must be numeric", ErrInvalidSpec, i))
		}
	}

	return errors.Join(errs...)
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
	case Eq, Ne, Gt, Gte, Lt, Lte, In, NotIn, Between, Exists, NotExists, IsNull, IsNotNull, Like, NotLike:
		return nil
	default:
		return fmt.Errorf("unsupported operator %q", op.String())
	}
}

// validateComparisonOperator 校验聚合后过滤仅使用数值比较操作符
func validateComparisonOperator(op Operator) error {
	switch op {
	case Eq, Ne, Gt, Gte, Lt, Lte:
		return nil
	default:
		return fmt.Errorf("unsupported comparison operator %q", op.String())
	}
}

// validateTimeInterval 校验时间桶粒度
func validateTimeInterval(interval TimeInterval) error {
	switch interval {
	case TimeIntervalMinute, TimeIntervalHour, TimeIntervalDay, TimeIntervalWeek,
		TimeIntervalMonth, TimeIntervalQuarter, TimeIntervalYear:
		return nil
	default:
		return fmt.Errorf("unsupported time interval %q", interval)
	}
}

// validateGroupTimeBucket 校验分组时间桶配置
func validateGroupTimeBucket(group Group) error {
	if group.Interval == "" {
		if group.TimeZone != "" {
			return fmt.Errorf("time zone requires time interval")
		}
		return nil
	}
	if err := validateTimeInterval(group.Interval); err != nil {
		return err
	}
	if group.TimeZone != "" && !timeZonePattern.MatchString(group.TimeZone) {
		return fmt.Errorf("invalid time zone %q", group.TimeZone)
	}
	return nil
}

// validateCondition 校验条件字段、操作符和值
func validateCondition(condition Condition) error {
	if err := validateField(condition.Field); err != nil {
		return err
	}
	if err := validateOperator(condition.Op); err != nil {
		return err
	}
	return validateConditionValue(condition)
}

// validateConditionValue 校验不同操作符对应的条件值
func validateConditionValue(condition Condition) error {
	switch condition.Op {
	case Exists, NotExists, IsNull, IsNotNull:
		if condition.Value != nil {
			return fmt.Errorf("operator %q does not accept a value", condition.Op.String())
		}
		return nil
	case In, NotIn:
		values, ok := conditionListValues(condition.Value)
		if !ok || len(values) == 0 {
			return fmt.Errorf("operator %q requires a non-empty value list", condition.Op.String())
		}
		for _, value := range values {
			if !isScalarValue(value) {
				return fmt.Errorf("invalid condition value %v", value)
			}
		}
		return nil
	case Between:
		start, end, ok := conditionRangeValues(condition.Value)
		if !ok || !isScalarValue(start) || !isScalarValue(end) {
			return fmt.Errorf("operator %q requires two scalar range values", condition.Op.String())
		}
		return nil
	case Like, NotLike:
		if _, ok := condition.Value.(string); !ok {
			return fmt.Errorf("operator %q requires a string pattern", condition.Op.String())
		}
		return nil
	default:
		if !isScalarValue(condition.Value) {
			return fmt.Errorf("invalid condition value %v", condition.Value)
		}
		return nil
	}
}

// conditionFromExpression 将 GORM-like 比较表达式转换为 Condition
func conditionFromExpression(expression string, value any) Condition {
	if condition, ok := parseConditionExpression(expression, value); ok {
		return condition
	}
	return Condition{Field: expression, Value: value}
}

// parseConditionExpression 解析受支持的条件表达式
func parseConditionExpression(expression string, value any) (Condition, bool) {
	if field, op, ok := parseComparisonExpression(expression); ok {
		return Condition{Field: field, Op: op, Value: value}, true
	}
	if field, op, ok := parseSetExpression(expression); ok {
		return Condition{Field: field, Op: op, Value: value}, true
	}
	if field, ok := parseBetweenExpression(expression); ok {
		return Condition{Field: field, Op: Between, Value: value}, true
	}
	if field, op, ok := parseLikeExpression(expression); ok {
		return Condition{Field: field, Op: op, Value: value}, true
	}
	if field, op, ok := parseUnaryExpression(expression); ok {
		return Condition{Field: field, Op: op}, true
	}
	return Condition{}, false
}

// parseSetExpression 解析 IN / NOT IN 条件表达式
func parseSetExpression(expression string) (string, Operator, bool) {
	matches := setExpressionPattern.FindStringSubmatch(expression)
	if len(matches) != 3 {
		return "", 0, false
	}
	op, ok := parseComparisonOperator(matches[2])
	if !ok {
		return "", 0, false
	}
	return matches[1], op, true
}

// parseBetweenExpression 解析 BETWEEN 条件表达式
func parseBetweenExpression(expression string) (string, bool) {
	matches := betweenExpressionPattern.FindStringSubmatch(expression)
	if len(matches) != 2 {
		return "", false
	}
	return matches[1], true
}

// parseLikeExpression 解析 LIKE / NOT LIKE 条件表达式
func parseLikeExpression(expression string) (string, Operator, bool) {
	matches := likeExpressionPattern.FindStringSubmatch(expression)
	if len(matches) != 3 {
		return "", 0, false
	}
	op, ok := parseComparisonOperator(matches[2])
	if !ok {
		return "", 0, false
	}
	return matches[1], op, true
}

// parseUnaryExpression 解析不需要值的条件表达式
func parseUnaryExpression(expression string) (string, Operator, bool) {
	matches := unaryExpressionPattern.FindStringSubmatch(expression)
	if len(matches) != 3 {
		return "", 0, false
	}
	op, ok := parseComparisonOperator(matches[2])
	if !ok {
		return "", 0, false
	}
	return matches[1], op, true
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
	normalized := strings.ToUpper(strings.Join(strings.Fields(token), " "))
	switch normalized {
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
	case "IN":
		return In, true
	case "NOT IN":
		return NotIn, true
	case "EXISTS":
		return Exists, true
	case "NOT EXISTS":
		return NotExists, true
	case "IS NULL":
		return IsNull, true
	case "IS NOT NULL":
		return IsNotNull, true
	case "LIKE":
		return Like, true
	case "NOT LIKE":
		return NotLike, true
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
		condition := cloneCondition(*metric.Condition)
		metric.Condition = &condition
	}
	return metric
}

// cloneCondition 返回条件的防御性副本
func cloneCondition(condition Condition) Condition {
	condition.Value = cloneConditionValue(condition.Value)
	return condition
}

// conditionPtr 返回条件的独立指针副本
func conditionPtr(condition Condition) *Condition {
	cloned := cloneCondition(condition)
	return &cloned
}

// cloneConditionValue 返回条件值的防御性副本，避免调用方后续修改切片或 Range
func cloneConditionValue(value any) any {
	switch typed := value.(type) {
	case *Range:
		if typed == nil {
			return (*Range)(nil)
		}
		cloned := *typed
		return &cloned
	case Range:
		return typed
	}

	rv := reflect.ValueOf(value)
	if rv.IsValid() && rv.Kind() == reflect.Slice {
		if rv.IsNil() {
			return value
		}
		cloned := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		reflect.Copy(cloned, rv)
		return cloned.Interface()
	}
	return value
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

// conditionListValues 将任意切片或数组条件值转换为 []any
func conditionListValues(value any) ([]any, bool) {
	if value == nil {
		return nil, false
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	values := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		values = append(values, rv.Index(i).Interface())
	}
	return values, true
}

// conditionRangeValues 返回 BETWEEN 条件的起止边界
func conditionRangeValues(value any) (any, any, bool) {
	switch typed := value.(type) {
	case Range:
		return typed.Start, typed.End, true
	case *Range:
		if typed == nil {
			return nil, nil, false
		}
		return typed.Start, typed.End, true
	default:
		values, ok := conditionListValues(value)
		if !ok || len(values) != 2 {
			return nil, nil, false
		}
		return values[0], values[1], true
	}
}

// elasticOrdersNeedClientPostProcessingSpec 判断显式排序是否无法由 ES composite source 顺序表达
func elasticOrdersNeedClientPostProcessingSpec(spec Spec) bool {
	if len(spec.Orders) == 0 {
		return false
	}
	for i, order := range spec.Orders {
		if i >= len(spec.Groups) || !strings.EqualFold(order.Alias, spec.Groups[i].Alias) {
			return true
		}
	}
	return false
}

// sqlLikePatternToRegexp 将 SQL LIKE 通配符模式转换为锚定正则表达式
func sqlLikePatternToRegexp(pattern string) string {
	var builder strings.Builder
	builder.WriteByte('^')
	escaped := false
	for _, r := range pattern {
		if escaped {
			builder.WriteString(regexp.QuoteMeta(string(r)))
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '%':
			builder.WriteString(".*")
		case '_':
			builder.WriteByte('.')
		default:
			builder.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	if escaped {
		builder.WriteString(regexp.QuoteMeta("\\"))
	}
	builder.WriteByte('$')
	return builder.String()
}

// sqlLikePatternToWildcard 将 SQL LIKE 通配符模式转换为 Elasticsearch wildcard 模式
func sqlLikePatternToWildcard(pattern string) string {
	var builder strings.Builder
	escaped := false
	for _, r := range pattern {
		if escaped {
			writeElasticWildcardLiteral(&builder, r)
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '%':
			builder.WriteByte('*')
		case '_':
			builder.WriteByte('?')
		default:
			writeElasticWildcardLiteral(&builder, r)
		}
	}
	if escaped {
		writeElasticWildcardLiteral(&builder, '\\')
	}
	return builder.String()
}

func writeElasticWildcardLiteral(builder *strings.Builder, r rune) {
	switch r {
	case '*', '?', '\\':
		builder.WriteByte('\\')
	}
	builder.WriteRune(r)
}
