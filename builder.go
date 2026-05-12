package builder

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"
)

// DataSource 数据源类型枚举
type DataSource int

const (
	// MySQL 数据源
	MySQL DataSource = iota
	// MongoDB 数据源
	MongoDB
	// ElasticSearch 数据源
	ElasticSearch
)

var (
	// ErrDataNotConfigured 数据源未正确配置的统一错误
	ErrDataNotConfigured = errors.New("data source not configured: DBProxy or its required field is nil")
	// ErrDataSourceInvalid 数据源无效
	ErrDataSourceInvalid = errors.New("data source invalid")
	// ErrLimitZero limit 不能为 0
	ErrLimitZero = errors.New("limit must be greater than 0")
	// ErrLimitExceeded limit 超出允许的最大值
	ErrLimitExceeded = errors.New("limit exceeds maximum allowed value (5000)")
	// ErrCursorMismatch cursorValues 与 cursorFields 长度不匹配
	ErrCursorMismatch = errors.New("cursorValues length does not match cursorFields length")
)

// DBProxy 数据实例结构
type DBProxy struct {
	DB            *gorm.DB
	Mongodb       *mongo.Collection // 需提前指定.Database("db_name").Collection("collection_name")
	ElasticSearch *elastic.Client
	// redis...
}

// NewDBProxy 创建数据实例
func NewDBProxy(db *gorm.DB, mongodb *mongo.Collection, elasticsearch *elastic.Client) *DBProxy {
	return &DBProxy{
		DB:            db,
		Mongodb:       mongodb,
		ElasticSearch: elasticsearch,
	}
}

// CheckConfigured 检查指定数据源是否已正确配置
func (p *DBProxy) CheckConfigured(ds DataSource) error {
	switch ds {
	case MySQL:
		if p.DB == nil {
			return ErrDataNotConfigured
		}
	case MongoDB:
		if p.Mongodb == nil {
			return ErrDataNotConfigured
		}
	case ElasticSearch:
		if p.ElasticSearch == nil {
			return ErrDataNotConfigured
		}
	default:
		return ErrDataSourceInvalid
	}

	return nil
}

// QueryMeta 查询元信息结构体
// 中间件可通过 builder.GetQueryMeta() 获取当前查询的元数据快照
type QueryMeta struct {
	DataSource     DataSource // 数据源类型
	Start          uint32     // 分页起始位置
	Limit          uint32     // 每页数据条数
	NeedTotal      bool       // 是否需要查询总数
	NeedPagination bool       // 是否需要分页
	Fields         []string   // 查询字段投影
	IsCursorQuery  bool       // 是否为游标查询模式
	CursorFields   []string   // 游标分页排序字段列表
	StartTime      time.Time  // 查询开始时间
}

// queryBuilder 构建器接口约束，利用 Go 1.26 自引用泛型约束特性
// 泛型参数:
//
//	B: 具体构建器类型（自引用）
//	R: 查询结果的实体类型
type queryBuilder[B any, R any] interface {
	// self 返回具体构建器自身引用，用于链式调用返回具体子类型
	self() B
	// QuerierList 嵌入列表查询执行能力，由各专属构建器各自实现
	QuerierList[R]
	// QuerierCursor 嵌入游标查询执行能力，返回迭代器
	QuerierCursor[R]
}

// QuerierList 列表查询执行能力接口
// 泛型参数:
//
//	R: 查询结果的实体类型
type QuerierList[R any] interface {
	// QueryList 执行查询列表操作
	QueryList(ctx context.Context) ([]*R, int64, error)
}

// QuerierCursor 游标查询执行能力接口
// 泛型参数:
//
//	R: 查询结果的实体类型
type QuerierCursor[R any] interface {
	// QueryCursor 执行游标分页查询，返回 iter.Seq2 迭代器
	QueryCursor(ctx context.Context) iter.Seq2[*R, error]
}

// QuerierExplain 查询预览能力接口
type QuerierExplain interface {
	// Explain 返回构建器最终生成的查询语句（Dry Run 模式）
	// 用于调试场景，不会实际执行查询
	Explain(ctx context.Context) (string, error)
}

// QuerierMeta 查询元信息能力接口
type QuerierMeta interface {
	// GetQueryMeta 返回当前查询元信息的只读快照
	// 中间件可通过 builder 参数直接调用此方法获取元数据，无需通过 ctx 传递
	GetQueryMeta() QueryMeta
}

// Querier 通用查询接口，作为工厂函数的返回类型
// 包含所有配置方法（Setter）和执行能力接口
// 泛型参数:
//
//	R: 查询结果的实体类型
type Querier[R any] interface {
	// Use 添加中间件
	Use(middleware Middleware[R]) Querier[R]
	// SetStart 设置分页起始位置
	SetStart(start uint32) Querier[R]
	// SetLimit 设置每页数据条数
	SetLimit(limit uint32) Querier[R]
	// SetNeedTotal 设置是否需要查询总数
	SetNeedTotal(needTotal bool) Querier[R]
	// SetNeedPagination 设置是否需要分页
	SetNeedPagination(needPagination bool) Querier[R]
	// SetFields 设置查询字段投影，指定只返回部分字段
	SetFields(fields ...string) Querier[R]
	// SetBeforeQueryHook 设置查询前置钩子
	SetBeforeQueryHook(hook BeforeQueryHook) Querier[R]
	// SetAfterQueryHook 设置查询后置钩子
	SetAfterQueryHook(hook AfterQueryHook[R]) Querier[R]
	// SetCursorField 设置游标分页排序字段（支持多字段）
	SetCursorField(fields ...string) Querier[R]
	// SetCursorValue 设置游标初始值（支持多字段，与 cursorFields 一一对应）
	// 用于断点续查或 App 分页场景，指定游标查询的起始位置
	SetCursorValue(values ...any) Querier[R]

	// 嵌入纯执行能力接口
	QuerierList[R]
	QuerierCursor[R]
	QuerierExplain
	QuerierMeta
}

// queryConfig 分页配置
type queryConfig struct {
	start          uint32   // 分页起始位置
	limit          uint32   // 每页数据条数
	needTotal      bool     // 是否需要查询总数
	needPagination bool     // 是否需要分页
	fields         []string // 查询字段投影
}

// clone 返回 queryConfig 的深拷贝
func (c queryConfig) clone() queryConfig {
	if c.fields != nil {
		fields := make([]string, len(c.fields))
		copy(fields, c.fields)
		c.fields = fields
	}
	return c
}

// cursorConfig 游标配置
type cursorConfig struct {
	cursorFields  []string // 游标分页排序字段列表
	cursorValues  []any    // 游标初始值（外部传入，用于断点续查/App分页场景）
	isCursorQuery bool     // 是否为游标查询模式
}

// clone 返回 cursorConfig 的深拷贝
func (c cursorConfig) clone() cursorConfig {
	if c.cursorFields != nil {
		cursorFields := make([]string, len(c.cursorFields))
		copy(cursorFields, c.cursorFields)
		c.cursorFields = cursorFields
	}
	if c.cursorValues != nil {
		cursorValues := make([]any, len(c.cursorValues))
		copy(cursorValues, c.cursorValues)
		c.cursorValues = cursorValues
	}
	return c
}

// hookChain 钩子与中间件链
type hookChain[R any] struct {
	beforeHook  BeforeQueryHook   // 查询前置钩子
	afterHook   AfterQueryHook[R] // 查询后置钩子
	middlewares []Middleware[R]   // 中间件链
}

// clone 返回 hookChain 的深拷贝
func (c hookChain[R]) clone() hookChain[R] {
	if c.middlewares != nil {
		middlewares := make([]Middleware[R], len(c.middlewares))
		copy(middlewares, c.middlewares)
		c.middlewares = middlewares
	}
	return c
}

// builder 查询构建器公共模板基类，使用自引用泛型约束
// 泛型参数:
//
//	B: 具体构建器类型（自引用，满足 queryBuilder 约束）
//	R: 查询结果的实体类型
type builder[B queryBuilder[B, R], R any] struct {
	data       *DBProxy
	dataSource DataSource // 数据源类型，用于查询元信息
	startTime  time.Time  // 查询开始时间

	queryConfig  // 嵌入分页配置
	cursorConfig // 嵌入游标配置
	hookChain[R] // 嵌入钩子与中间件链

	selfRef    B          // 存储具体子类型引用，用于链式调用返回具体子类型
	querierRef Querier[R] // 存储 Querier 接口引用，避免中间件执行时的类型断言
}

// setSelf 设置具体子类型引用，供子类型构造时调用
// querier 参数同时保存 Querier[R] 接口引用，避免中间件执行时需要类型断言
func (b *builder[B, R]) setSelf(self B, querier Querier[R]) {
	b.selfRef = self
	b.querierRef = querier
}

// GetQueryMeta 返回当前查询元信息的只读快照
// 中间件可通过 builder 参数直接调用此方法获取元数据
// 切片字段返回副本，防止外部意外修改内部状态
func (b *builder[B, R]) GetQueryMeta() QueryMeta {
	meta := QueryMeta{
		DataSource:     b.dataSource,
		Start:          b.start,
		Limit:          b.limit,
		NeedTotal:      b.needTotal,
		NeedPagination: b.needPagination,
		IsCursorQuery:  b.isCursorQuery,
		StartTime:      b.startTime,
	}
	if b.fields != nil {
		meta.Fields = make([]string, len(b.fields))
		copy(meta.Fields, b.fields)
	}
	if b.cursorFields != nil {
		meta.CursorFields = make([]string, len(b.cursorFields))
		copy(meta.CursorFields, b.cursorFields)
	}
	return meta
}

// prepareAndValidate 执行查询前的参数校验与数据准备
// 包括：数据源配置校验、limit 上下限校验、cursorValues/cursorFields 长度一致性校验、fields 自动清洗
func (b *builder[B, R]) prepareAndValidate() error {
	if b.data == nil {
		return ErrDataNotConfigured
	}

	// 数据源校验
	if err := b.data.CheckConfigured(b.dataSource); err != nil {
		return err
	}

	// limit 校验
	if b.limit == 0 {
		return ErrLimitZero
	}
	if b.limit > maxLimit {
		return ErrLimitExceeded
	}

	// cursorValues 与 cursorFields 长度一致性校验
	if len(b.cursorValues) > 0 && len(b.cursorFields) > 0 && len(b.cursorValues) != len(b.cursorFields) {
		return ErrCursorMismatch
	}

	// fields 自动清洗
	b.sanitizeFields()

	return nil
}

// sanitizeFields 对 fields 切片进行自动清洗：过滤空字符串、去重
// 若清洗后为空切片，则将 fields 置为 nil（表示查询所有字段）
func (b *builder[B, R]) sanitizeFields() {
	if len(b.fields) == 0 {
		return
	}

	seen := make(map[string]struct{}, len(b.fields))
	cleaned := make([]string, 0, len(b.fields))

	for _, field := range b.fields {
		// 过滤空字符串
		if field == "" {
			continue
		}
		// 去重
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		cleaned = append(cleaned, field)
	}

	// 清洗后为空则视为未设置
	if len(cleaned) == 0 {
		b.fields = nil
	} else {
		b.fields = cleaned
	}
}

// cloneBase 复制 builder 基类的查询配置到目标 builder
// 用于各专属构建器的 Clone() 方法，实现状态隔离的并发分叉
// 注意：selfRef 和 querierRef 不复制，由子类通过 setSelf 重新设置
func (b *builder[B, R]) cloneBase(dst *builder[B, R]) {
	dst.data = b.data
	dst.dataSource = b.dataSource

	// 通过子结构体的 clone() 方法进行深拷贝，确保切片引用独立
	dst.queryConfig = b.queryConfig.clone()
	dst.cursorConfig = b.cursorConfig.clone()
	dst.hookChain = b.hookChain.clone()
}

// Use 添加中间件
// 返回具体子类型，支持类型安全的链式调用
func (b *builder[B, R]) Use(middleware Middleware[R]) B {
	b.middlewares = append(b.middlewares, middleware)
	return b.selfRef
}

// SetStart 设置分页起始位置
func (b *builder[B, R]) SetStart(start uint32) B {
	b.start = start
	return b.selfRef
}

// SetLimit 设置每页数据条数
func (b *builder[B, R]) SetLimit(limit uint32) B {
	b.limit = limit
	return b.selfRef
}

// SetNeedTotal 设置是否需要查询总数
func (b *builder[B, R]) SetNeedTotal(needTotal bool) B {
	b.needTotal = needTotal
	return b.selfRef
}

// SetNeedPagination 设置是否需要分页
func (b *builder[B, R]) SetNeedPagination(needPagination bool) B {
	b.needPagination = needPagination
	return b.selfRef
}

// SetFields 设置查询字段投影，指定只返回部分字段
func (b *builder[B, R]) SetFields(fields ...string) B {
	b.fields = fields
	return b.selfRef
}

// SetBeforeQueryHook 设置查询前置钩子
func (b *builder[B, R]) SetBeforeQueryHook(hook BeforeQueryHook) B {
	b.beforeHook = hook
	return b.selfRef
}

// SetAfterQueryHook 设置查询后置钩子
func (b *builder[B, R]) SetAfterQueryHook(hook AfterQueryHook[R]) B {
	b.afterHook = hook
	return b.selfRef
}

// SetCursorField 设置游标分页排序字段（支持多字段）
func (b *builder[B, R]) SetCursorField(fields ...string) B {
	b.cursorFields = fields
	b.isCursorQuery = true
	return b.selfRef
}

// SetCursorValue 设置游标初始值（支持多字段，与 cursorFields 一一对应）
// 用于断点续查或 App 分页场景，指定游标查询的起始位置
// 如果同时设置了 start > 0 且未设置 cursorValues，start 将作为单字段数值游标的初始值
func (b *builder[B, R]) SetCursorValue(values ...any) B {
	b.cursorValues = values
	b.isCursorQuery = true
	return b.selfRef
}

// resolveInitialCursorValues 解析初始游标值
// 优先级：cursorValues（方案B：显式设置）> start（方案A：复用 start 作为单字段数值游标）
// 返回 nil 表示从数据集起始位置开始查询
func (b *builder[B, R]) resolveInitialCursorValues() []any {
	// 方案B：如果显式设置了 cursorValues，直接使用
	if len(b.cursorValues) > 0 {
		return b.cursorValues
	}

	// 方案A：如果 start > 0，将其作为单字段数值游标的初始值
	if b.start > 0 {
		return []any{b.start}
	}

	return nil
}

// buildMiddlewareChain 构建中间件链，将中间件按逆序包装到 queryFn 外层
func (b *builder[B, R]) buildMiddlewareChain(
	queryFn func(context.Context) ([]*R, int64, error),
) func(context.Context) ([]*R, int64, error) {
	next := queryFn
	for i := len(b.middlewares) - 1; i >= 0; i-- {
		next = func(mw Middleware[R], fn func(context.Context) ([]*R, int64, error)) func(context.Context) ([]*R, int64, error) {
			return func(ctx context.Context) ([]*R, int64, error) {
				return mw(ctx, b.querierRef, fn)
			}
		}(b.middlewares[i], next)
	}
	return next
}

// executeWithMiddlewares 执行中间件链并调用最终查询逻辑
// 由各专属构建器在 QueryList 中调用，传入最终的查询函数
// 支持超时控制和前置/后置钩子
func (b *builder[B, R]) executeWithMiddlewares(
	ctx context.Context,
	queryFn func(context.Context) ([]*R, int64, error),
) ([]*R, int64, error) {
	// 设置查询开始时间
	b.startTime = time.Now()

	// 执行前置钩子
	if b.beforeHook != nil {
		ctx = b.beforeHook(ctx)
	}

	list, total, err := b.buildMiddlewareChain(queryFn)(ctx)

	// 执行后置钩子
	if b.afterHook != nil {
		b.afterHook(ctx, list, total, err)
	}

	return list, total, err
}

// executeCursorWithMiddlewares 执行游标查询模式下的中间件链和钩子
// 封装游标查询的完整生命周期：BeforeQueryHook → 分批获取（每批执行中间件链）→ AfterQueryHook
// 参数:
//
//	ctx: 上下文
//	cursorQueryFn: 游标分批查询函数，接收 cursorValues 返回一批数据
//
// 返回:
//
//	iter.Seq2[*R, error]: 游标迭代器
func (b *builder[B, R]) executeCursorWithMiddlewares(
	ctx context.Context,
	cursorQueryFn cursorFetchBatch[R],
) iter.Seq2[*R, error] {
	// 确定批次大小
	batchSize := int(b.limit)
	if batchSize < 1 {
		batchSize = defaultLimit
	}

	// 解析初始游标值：优先使用 cursorValues（方案B），其次使用 start（方案A）
	initialCursorValues := b.resolveInitialCursorValues()

	// 设置查询开始时间
	b.startTime = time.Now()

	// 执行前置钩子
	if b.beforeHook != nil {
		ctx = b.beforeHook(ctx)
	}

	// 包装 fetchBatch，使每批次查询经过中间件链
	wrappedFetch := func(ctx context.Context, cursorValues []any) ([]*R, []any, error) {
		var nextCursorValues []any
		queryFn := func(ctx context.Context) ([]*R, int64, error) {
			batch, nextCV, err := cursorQueryFn(ctx, cursorValues)
			nextCursorValues = nextCV
			return batch, int64(len(batch)), err
		}

		list, _, err := b.buildMiddlewareChain(queryFn)(ctx)
		return list, nextCursorValues, err
	}

	// 构建迭代器，并在迭代完成后执行后置钩子
	innerIter := buildCursorIterator[R](ctx, b.cursorFields, batchSize, initialCursorValues, wrappedFetch)

	// 包装迭代器，在遍历结束后执行 AfterQueryHook
	return func(yield func(*R, error) bool) {
		var allResults []*R
		var lastErr error

		for item, err := range innerIter {
			if err != nil {
				lastErr = err
				if !yield(nil, err) {
					break
				}
				break
			}
			allResults = append(allResults, item)
			if !yield(item, nil) {
				break
			}
		}

		// 执行后置钩子
		// 大数据集场景下 allResults 为 nil，仅传递计数信息
		if b.afterHook != nil {
			b.afterHook(ctx, allResults, int64(len(allResults)), lastErr)
		}
	}
}

// NewBuilder 通用工厂函数，根据 DataSource 枚举值创建对应的专属查询构建器
// 返回 Querier[R] 通用查询接口
func NewBuilder[R any](ds DataSource, data *DBProxy) Querier[R] {
	switch ds {
	case MySQL:
		return NewGormBuilder[R](data)
	case MongoDB:
		return NewMongoBuilder[R](data)
	case ElasticSearch:
		return NewElasticSearchBuilder[R](data, "")
	default:
		panic(fmt.Sprintf("unsupported data source: %d", ds))
	}
}
