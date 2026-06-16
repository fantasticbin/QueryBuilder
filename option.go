package builder

import (
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

const (
	defaultStart          = 0    // 默认从第0条开始
	defaultLimit          = 10   // 默认每页10条
	defaultNeedTotal      = true // 默认需要总数
	defaultNeedPagination = true // 默认需要分页
	maxLimit              = 5000 // limit 允许的最大值
)

// effectiveLimit 返回实际执行查询时使用的 limit。
// limit 为 0 时使用默认分页大小，避免调用方显式传 0 导致无结果或后端差异行为。
func effectiveLimit(limit uint32) uint32 {
	if limit == 0 {
		return defaultLimit
	}
	return limit
}

// QueryListOptions 定义了查询列表的通用选项接口
type QueryListOptions interface {
	// GetData 返回本次查询显式传入的数据源注册表。
	GetData() *core.DBProxy
	// GetStart 返回分页起始位置。
	GetStart() uint32
	// GetLimit 返回分页大小。
	GetLimit() uint32
	// GetNeedTotal 返回是否需要统计总数。
	GetNeedTotal() bool
	// GetTotalLimit 返回总数统计上限。
	GetTotalLimit() uint32
	// GetNeedPagination 返回是否需要应用分页。
	GetNeedPagination() bool
	// GetFields 返回字段投影列表。
	GetFields() []string
	// GetCursorFields 返回游标分页排序字段。
	GetCursorFields() []string
	// GetCursorValues 返回游标分页起始值。
	GetCursorValues() []any
}

// BaseQueryListOptions 实现了QueryListOptions接口的基础结构体
// 包含查询列表所需的所有基本选项
type BaseQueryListOptions struct {
	data           *core.DBProxy // 数据实例
	start          uint32        // 分页起始位置
	limit          uint32        // 每页数据条数
	needTotal      bool          // 是否需要查询总数
	totalLimit     uint32        // 总数统计上限，0 表示精确统计
	needPagination bool          // 是否需要分页
	fields         []string      // 查询字段投影
	cursorFields   []string      // 游标分页排序字段
	cursorValues   []any         // 游标初始值（用于断点续查/App分页场景）
	esIndex        string        // Elasticsearch 索引名
	pitID          string        // Elasticsearch PIT ID（跨请求分页）
	pitKeepAlive   time.Duration // Elasticsearch Point-in-Time 保持时间
}

// GetData 返回本次查询显式传入的数据源注册表。
func (opts *BaseQueryListOptions) GetData() *core.DBProxy {
	return opts.data
}

// GetStart 返回分页起始位置。
func (opts *BaseQueryListOptions) GetStart() uint32 {
	return opts.start
}

// GetLimit 返回分页大小。
func (opts *BaseQueryListOptions) GetLimit() uint32 {
	return opts.limit
}

// GetNeedTotal 返回是否需要统计总数。
func (opts *BaseQueryListOptions) GetNeedTotal() bool {
	return opts.needTotal
}

// GetTotalLimit 返回总数统计上限。
func (opts *BaseQueryListOptions) GetTotalLimit() uint32 {
	return opts.totalLimit
}

// GetNeedPagination 返回是否需要应用分页。
func (opts *BaseQueryListOptions) GetNeedPagination() bool { return opts.needPagination }

// GetFields 返回字段投影列表。
func (opts *BaseQueryListOptions) GetFields() []string {
	return opts.fields
}

// GetCursorFields 返回游标分页排序字段。
func (opts *BaseQueryListOptions) GetCursorFields() []string {
	return opts.cursorFields
}

// GetCursorValues 返回游标分页起始值。
func (opts *BaseQueryListOptions) GetCursorValues() []any {
	return opts.cursorValues
}

// QueryOption 定义用于配置查询选项的函数类型
type QueryOption func(options *BaseQueryListOptions)

// LoadQueryOptions 加载并应用查询选项
// 参数:
//
//	opts - 可变数量的查询选项函数
//
// 返回:
//
//	配置好的BaseQueryListOptions实例
func LoadQueryOptions(opts ...QueryOption) BaseQueryListOptions {
	// 初始化默认选项
	options := BaseQueryListOptions{
		start:          defaultStart,
		limit:          defaultLimit,
		needTotal:      defaultNeedTotal,
		needPagination: defaultNeedPagination,
	}

	// 应用所有选项函数
	for _, opt := range opts {
		opt(&options)
	}

	return options
}

// WithData 设置本次查询使用的数据源注册表。
func WithData(data *core.DBProxy) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.data = data
	}
}

// WithStart 设置分页起始位置。
func WithStart(start uint32) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.start = start
	}
}

// WithLimit 设置分页大小。
func WithLimit(limit uint32) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.limit = limit
	}
}

// WithNeedTotal 设置是否需要统计总数。
func WithNeedTotal(needTotal bool) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.needTotal = needTotal
	}
}

// WithTotalLimit 设置总数统计上限，0 表示精确统计。
func WithTotalLimit(totalLimit uint32) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.totalLimit = totalLimit
	}
}

// WithNeedPagination 设置是否需要应用分页。
func WithNeedPagination(needPagination bool) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.needPagination = needPagination
	}
}

// WithFields 设置字段投影列表。
func WithFields(fields ...string) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.fields = fields
	}
}

// WithCursorField 设置游标分页排序字段。
func WithCursorField(fields ...string) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.cursorFields = fields
	}
}

// WithCursorValue 设置游标分页起始值。
func WithCursorValue(values ...any) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.cursorValues = values
	}
}

// WithESIndex 设置 Elasticsearch 查询索引名。
func WithESIndex(index string) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.esIndex = index
	}
}

// WithPITID 设置 Elasticsearch PIT ID，用于跨请求游标分页续查。
func WithPITID(pitID string) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.pitID = pitID
	}
}

// WithPitKeepAlive 设置 Elasticsearch Point-in-Time 保持时间。
func WithPitKeepAlive(keepAlive time.Duration) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.pitKeepAlive = keepAlive
	}
}
