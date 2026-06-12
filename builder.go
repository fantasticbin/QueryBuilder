package builder

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"
)

// DataSource 数据源类型枚举（定义于 core 包，此处为类型别名）
type DataSource = core.DataSource

const (
	// Gorm 数据源
	Gorm = core.Gorm
	// MongoDB 数据源
	MongoDB = core.MongoDB
	// ElasticSearch 数据源
	ElasticSearch = core.ElasticSearch
)

var (
	// ErrDataNotConfigured 数据源未正确配置的统一错误
	ErrDataNotConfigured = errors.New("data source not configured: DBProxy or its required field is nil")
	// ErrDataSourceInvalid 数据源无效
	ErrDataSourceInvalid = errors.New("data source invalid")
	// ErrLimitExceeded limit 超出允许的最大值
	ErrLimitExceeded = errors.New("limit exceeds maximum allowed value (5000)")
	// ErrCursorMismatch cursorValues 与 cursorFields 长度不匹配
	ErrCursorMismatch = errors.New("cursorValues length does not match cursorFields length")
	// ErrPITCursorWithoutPITID ElasticSearch 单批次分页查询模式下未提供 PIT ID 的错误
	ErrPITCursorWithoutPITID = errors.New("PIT ID is required when cursor values are provided")
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
	case Gorm:
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

// QueryMeta 查询元信息结构体（定义于 core 包，此处为类型别名）
// 中间件可通过 builder.GetQueryMeta() 获取当前查询的元数据快照
type QueryMeta = core.QueryMeta

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
	// QuerierCursor 嵌入游标查询执行能力
	QuerierCursor[R]
}

// QuerierList 列表查询执行能力接口
// 泛型参数:
//
//	R: 查询结果的实体类型
type QuerierList[R any] interface {
	// QueryList 执行查询列表操作
	QueryList(ctx context.Context) (*core.ListResult[R], error)
}

// QuerierCursor 游标查询执行能力接口
// 泛型参数:
//
//	R: 查询结果的实体类型
type QuerierCursor[R any] interface {
	// QueryCursor 执行游标分页查询，返回 iter.Seq2 迭代器
	QueryCursor(ctx context.Context) iter.Seq2[*R, error]
	// QueryPage 执行单批次游标分页查询，返回结构化的分页结果
	// 包含当前页数据、是否有下一页、下一页游标值等信息
	QueryPage(ctx context.Context) (*core.CursorPageResult[R], error)
}

// QuerierExplain 查询预览能力接口
type QuerierExplain interface {
	// Explain 返回构建器最终生成的查询语句（Dry Run 模式）
	// 用于调试场景，不会实际执行查询
	Explain(ctx context.Context) (string, error)
}

// QuerierMeta 查询元信息能力接口（定义于 core 包，此处为类型别名）
type QuerierMeta = core.QuerierMeta

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
	// SetTotalLimit 设置总数统计上限，0 表示精确统计。
	SetTotalLimit(totalLimit uint32) Querier[R]
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
	totalLimit     uint32   // 总数统计上限，0 表示精确统计
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
	cursorFields       []string          // 游标分页排序字段列表
	parsedCursorFields []cursorSortField // 解析后的游标字段与方向缓存
	cursorValues       []any             // 游标初始值（外部传入，用于断点续查/App分页场景）
	isCursorQuery      bool              // 是否为游标查询模式
	isPITQuery         bool              // 是否为 Elasticsearch PIT + search_after 查询模式
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
	if c.parsedCursorFields != nil {
		parsed := make([]cursorSortField, len(c.parsedCursorFields))
		copy(parsed, c.parsedCursorFields)
		c.parsedCursorFields = parsed
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

// 以下方法实现 middlewareProvider[R] 接口，供 newMiddlewareContext 通过接口约束获取数据
func (b *builder[B, R]) getMiddlewares() []Middleware[R] { return b.middlewares }
func (b *builder[B, R]) getQuerierRef() Querier[R]       { return b.querierRef }
func (b *builder[B, R]) getBeforeHook() BeforeQueryHook  { return b.beforeHook }
func (b *builder[B, R]) getAfterHook() AfterQueryHook[R] { return b.afterHook }
func (b *builder[B, R]) setStartTime(t time.Time)        { b.startTime = t }

// GetQueryMeta 返回当前查询元信息的只读快照
// 中间件可通过 builder 参数直接调用此方法获取元数据
// 切片字段返回副本，防止外部意外修改内部状态
func (b *builder[B, R]) GetQueryMeta() QueryMeta {
	meta := QueryMeta{
		DataSource:     b.dataSource,
		Start:          b.start,
		Limit:          b.limit,
		NeedTotal:      b.needTotal,
		TotalLimit:     b.totalLimit,
		NeedPagination: b.needPagination,
		IsCursorQuery:  b.isCursorQuery,
		IsPITQuery:     b.isPITQuery,
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
	if b.cursorValues != nil {
		meta.CursorValues = make([]any, len(b.cursorValues))
		copy(meta.CursorValues, b.cursorValues)
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
	if b.limit > maxLimit {
		return ErrLimitExceeded
	}

	// fields 自动清洗
	b.sanitizeFields()
	if b.isCursorQuery {
		if err := b.ensureDefaultCursorField(); err != nil {
			return err
		}
	}

	// cursorValues 与 cursorFields 长度一致性校验
	if len(b.cursorValues) > 0 && len(b.cursorFields) > 0 && len(b.cursorValues) != len(b.cursorFields) {
		return ErrCursorMismatch
	}

	return nil
}

// getParsedCursorFields 返回解析后的游标字段缓存。
// 若缓存为空且 cursorFields 已设置，则延迟解析一次并写回缓存。
func (b *builder[B, R]) getParsedCursorFields() []cursorSortField {
	if len(b.parsedCursorFields) == 0 && len(b.cursorFields) > 0 {
		b.parsedCursorFields = parseCursorSortFields(b.cursorFields)
	}
	return b.parsedCursorFields
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

// SetTotalLimit 设置总数统计上限，0 表示精确统计。
func (b *builder[B, R]) SetTotalLimit(totalLimit uint32) B {
	b.totalLimit = totalLimit
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
	b.parsedCursorFields = parseCursorSortFields(fields)
	return b.selfRef
}

// SetCursorValue 设置游标初始值（支持多字段，与 cursorFields 一一对应）
// 用于断点续查或 App 分页场景，指定游标查询的起始位置
// 如果同时设置了 start > 0 且未设置 cursorValues，start 将作为单字段数值游标的初始值
func (b *builder[B, R]) SetCursorValue(values ...any) B {
	b.cursorValues = values
	return b.selfRef
}

// beginQueryMode 标记当前执行入口是否为游标查询。
func (b *builder[B, R]) beginQueryMode(isCursorQuery bool) {
	b.isCursorQuery = isCursorQuery
}

// finishCursorQuery 结束游标查询模式，避免复用 builder 时污染后续普通查询。
func (b *builder[B, R]) finishCursorQuery() {
	b.isCursorQuery = false
}

// ensureDefaultCursorField 在游标查询模式下为未显式设置 cursorFields 的场景自动追加唯一 tie-breaker。
func (b *builder[B, R]) ensureDefaultCursorField() error {
	if len(b.cursorFields) > 0 {
		return nil
	}
	switch b.dataSource {
	case Gorm:
		b.cursorFields = []string{"id"}
	case MongoDB:
		b.cursorFields = []string{"_id"}
	case ElasticSearch:
		b.cursorFields = []string{"_shard_doc"}
	}
	b.parsedCursorFields = parseCursorSortFields(b.cursorFields)
	return nil
}

// NewBuilder 通用工厂函数，根据 DataSource 枚举值创建对应的专属查询构建器
// 返回 Querier[R] 通用查询接口
func NewBuilder[R any](ds DataSource, data *DBProxy) Querier[R] {
	switch ds {
	case Gorm:
		return NewGormBuilder[R](data)
	case MongoDB:
		return NewMongoBuilder[R](data)
	case ElasticSearch:
		return NewElasticSearchBuilder[R](data, "")
	default:
		panic(fmt.Sprintf("unsupported data source: %d", ds))
	}
}
