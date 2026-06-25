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

	fieldPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	aliasPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
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

// Group 定义分组字段、输出别名及排序方向
type Group struct {
	Field      string `json:"field"`
	Alias      string `json:"alias"`
	Descending bool   `json:"descending"`
}

// Metric 定义聚合计算、去重选项及其输出别名
type Metric struct {
	Func     Func   `json:"func"`
	Field    string `json:"field,omitempty"`
	Alias    string `json:"alias"`
	Distinct bool   `json:"distinct,omitempty"`
}

// Spec 定义跨数据源通用的聚合查询规范
type Spec struct {
	Groups  []Group  `json:"groups"`
	Metrics []Metric `json:"metrics"`
	Limit   uint32   `json:"limit"`
}

// Clone 返回聚合查询规范的防御性副本
func (s Spec) Clone() Spec {
	cloned := s
	cloned.Groups = append([]Group{}, s.Groups...)
	cloned.Metrics = append([]Metric{}, s.Metrics...)
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
