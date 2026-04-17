package builder

const (
	defaultStart          = 0    // 默认从第0条开始
	defaultLimit          = 10   // 默认每页10条
	defaultNeedTotal      = true // 默认需要总数
	defaultNeedPagination = true // 默认需要分页
)

// QueryListOptions 定义了查询列表的通用选项接口
type QueryListOptions interface {
	GetData() *DBProxy
	GetStart() uint32
	GetLimit() uint32
	GetNeedTotal() bool
	GetNeedPagination() bool
	GetFields() []string
}

// BaseQueryListOptions 实现了QueryListOptions接口的基础结构体
// 包含查询列表所需的所有基本选项
type BaseQueryListOptions struct {
	data           *DBProxy      // 数据实例
	start          uint32        // 分页起始位置
	limit          uint32        // 每页数据条数
	needTotal      bool          // 是否需要查询总数
	needPagination bool          // 是否需要分页
	fields         []string      // 查询字段投影
}

func (opts *BaseQueryListOptions) GetData() *DBProxy {
	return opts.data
}

func (opts *BaseQueryListOptions) GetStart() uint32 {
	return opts.start
}

func (opts *BaseQueryListOptions) GetLimit() uint32 {
	return opts.limit
}

func (opts *BaseQueryListOptions) GetNeedTotal() bool {
	return opts.needTotal
}

func (opts *BaseQueryListOptions) GetNeedPagination() bool { return opts.needPagination }

func (opts *BaseQueryListOptions) GetFields() []string {
	return opts.fields
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

func WithData(data *DBProxy) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.data = data
	}
}

func WithStart(start uint32) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.start = start
	}
}

func WithLimit(limit uint32) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.limit = limit
	}
}

func WithNeedTotal(needTotal bool) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.needTotal = needTotal
	}
}

func WithNeedPagination(needPagination bool) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.needPagination = needPagination
	}
}

func WithFields(fields ...string) QueryOption {
	return func(o *BaseQueryListOptions) {
		o.fields = fields
	}
}