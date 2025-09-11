package builder

const (
	defaultStart          = 0    // 默认从第0条开始
	defaultLimit          = 10   // 默认每页10条
	defaultNeedTotal      = true // 默认需要总数
	defaultNeedPagination = true // 默认需要分页
)

// Filter 定义过滤条件的通用接口类型
type Filter any

// Sort 定义排序条件的通用接口类型
type Sort any

// QueryListOptions 定义了查询列表的通用选项接口
// 泛型参数:
//
//	F - 过滤条件类型参数
//	S - 排序条件类型参数
type QueryListOptions[F Filter, S Sort] interface {
	GetData() *DBProxy
	GetFilter() *F
	GetSort() S
	GetStart() uint32
	GetLimit() uint32
	GetNeedTotal() bool
	GetNeedPagination() bool
}

// BaseQueryListOptions 实现了QueryListOptions接口的基础结构体
// 包含查询列表所需的所有基本选项
type BaseQueryListOptions[F Filter, S Sort] struct {
	data           *DBProxy // 数据实例
	filter         *F       // 过滤条件生成函数
	sort           S        // 排序条件生成函数
	start          uint32   // 分页起始位置
	limit          uint32   // 每页数据条数
	needTotal      bool     // 是否需要查询总数
	needPagination bool     // 是否需要分页
}

func (opts *BaseQueryListOptions[F, S]) GetData() *DBProxy {
	return opts.data
}

func (opts *BaseQueryListOptions[F, S]) GetFilter() *F {
	return opts.filter
}

func (opts *BaseQueryListOptions[F, S]) GetSort() S {
	return opts.sort
}

func (opts *BaseQueryListOptions[F, S]) GetStart() uint32 {
	return opts.start
}

func (opts *BaseQueryListOptions[F, S]) GetLimit() uint32 {
	return opts.limit
}

func (opts *BaseQueryListOptions[F, S]) GetNeedTotal() bool {
	return opts.needTotal
}

func (opts *BaseQueryListOptions[F, S]) GetNeedPagination() bool { return opts.needPagination }

// QueryOption 定义用于配置查询选项的函数类型
type QueryOption[F Filter, S Sort] func(options *BaseQueryListOptions[F, S])

// LoadQueryOptions 加载并应用查询选项
// 参数:
//
//	opts - 可变数量的查询选项函数
//
// 返回:
//
//	配置好的BaseQueryListOptions实例
func LoadQueryOptions[F Filter, S Sort](opts ...QueryOption[F, S]) BaseQueryListOptions[F, S] {
	// 初始化默认选项
	options := BaseQueryListOptions[F, S]{
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

func WithData[F Filter, S Sort](data *DBProxy) QueryOption[F, S] {
	return func(o *BaseQueryListOptions[F, S]) {
		o.data = data
	}
}

func WithFilter[F Filter, S Sort](filter *F) QueryOption[F, S] {
	return func(o *BaseQueryListOptions[F, S]) {
		o.filter = filter
	}
}

func WithSort[F Filter, S Sort](sort S) QueryOption[F, S] {
	return func(o *BaseQueryListOptions[F, S]) {
		o.sort = sort
	}
}

func WithStart[F Filter, S Sort](start uint32) QueryOption[F, S] {
	return func(o *BaseQueryListOptions[F, S]) {
		o.start = start
	}
}

func WithLimit[F Filter, S Sort](limit uint32) QueryOption[F, S] {
	return func(o *BaseQueryListOptions[F, S]) {
		o.limit = limit
	}
}

func WithNeedTotal[F Filter, S Sort](needTotal bool) QueryOption[F, S] {
	return func(o *BaseQueryListOptions[F, S]) {
		o.needTotal = needTotal
	}
}

func WithNeedPagination[F Filter, S Sort](needPagination bool) QueryOption[F, S] {
	return func(o *BaseQueryListOptions[F, S]) {
		o.needPagination = needPagination
	}
}

// OptionBuilder 选项构建器，用于类型推断
type OptionBuilder[F any, S any] struct {
	options []QueryOption[F, S]
}

// NewOptionBuilder 创建选项构建器，用于类型推断
func NewOptionBuilder[F any, S any]() *OptionBuilder[F, S] {
	return &OptionBuilder[F, S]{}
}

// NewOptionBuilderWithFilterAndSort 创建选项构建器，包含筛选器和排序
func NewOptionBuilderWithFilterAndSort[F any, S any](filter *F, sort S) *OptionBuilder[F, S] {
	builder := NewOptionBuilder[F, S]()
	return builder.WithFilter(filter).WithSort(sort)
}

func (b *OptionBuilder[F, S]) WithData(data *DBProxy) *OptionBuilder[F, S] {
	b.options = append(b.options, WithData[F, S](data))
	return b
}

func (b *OptionBuilder[F, S]) WithFilter(filter *F) *OptionBuilder[F, S] {
	b.options = append(b.options, WithFilter[F, S](filter))
	return b
}

func (b *OptionBuilder[F, S]) WithSort(sort S) *OptionBuilder[F, S] {
	b.options = append(b.options, WithSort[F, S](sort))
	return b
}

func (b *OptionBuilder[F, S]) WithStart(start uint32) *OptionBuilder[F, S] {
	b.options = append(b.options, WithStart[F, S](start))
	return b
}

func (b *OptionBuilder[F, S]) WithLimit(limit uint32) *OptionBuilder[F, S] {
	b.options = append(b.options, WithLimit[F, S](limit))
	return b
}

func (b *OptionBuilder[F, S]) WithNeedTotal(needTotal bool) *OptionBuilder[F, S] {
	b.options = append(b.options, WithNeedTotal[F, S](needTotal))
	return b
}

func (b *OptionBuilder[F, S]) WithNeedPagination(needPagination bool) *OptionBuilder[F, S] {
	b.options = append(b.options, WithNeedPagination[F, S](needPagination))
	return b
}

func (b *OptionBuilder[F, S]) LoadOptions() []QueryOption[F, S] {
	return b.options
}
