package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/redis/go-redis/v9"
)

const (
	// redisJSONReturnAlias Redis JSON 文档完整投影的默认返回别名（RETURN $ AS json）
	redisJSONReturnAlias = "json"
	// redisDialectV2 Redis Query Engine 查询方言版本
	// 固定 DIALECT 2：属性过滤、参数化查询等能力在该方言下行为稳定
	redisDialectV2 = 2
)

var (
	// ErrRedisIndexNotConfigured Redis 索引未配置
	ErrRedisIndexNotConfigured = errors.New("redis index not configured")
	// ErrRedisSortRequired 缺少排序条件
	// 无 SORTBY 的 FT.SEARCH 分页结果顺序非确定，翻页会出现重复/遗漏，因此强制要求显式排序
	ErrRedisSortRequired = errors.New("redis sort required: paginated query without SORTBY is nondeterministic")
	// ErrRedisSortFieldUnsupported 排序字段数量超限
	// RediSearch SORTBY 仅支持单属性排序
	ErrRedisSortFieldUnsupported = errors.New("redis sort supports at most one field")
	// ErrRedisSortDirectionInvalid 排序方向冲突（Asc 与 Desc 同时设置）
	ErrRedisSortDirectionInvalid = errors.New("redis sort direction invalid: Asc and Desc are mutually exclusive")
)

// RedisSort Redis 查询排序条件，复用 go-redis 的 FT.SEARCH SORTBY 定义
// 约束：FieldName 必填，Asc / Desc 二选一
type RedisSort = redis.FTSearchSortBy

// RedisReturn Redis 查询字段投影
// JSON 文档场景 Path 为 JSONPath 表达式（如 $.name），HASH 文档场景为字段名
type RedisReturn struct {
	// Path 字段路径
	Path string
	// As 返回别名，空串时自动从 Path 推导
	As string
}

// RedisFilter Redis 专属查询条件
type RedisFilter struct {
	// Query 查询表达式，空串时退化为通配符 *（匹配全部文档）
	Query string
	// NumericFilters 数值范围过滤（FT.SEARCH FILTER 子句）
	NumericFilters []redis.FTSearchFilter
	// InKeys 限定检索的文档 Key 列表（FT.SEARCH INKEYS 子句）
	InKeys []any
	// InFields 限定参与检索的字段列表（FT.SEARCH INFIELDS 子句）
	InFields []any
	// Params 参数化查询参数表（FT.SEARCH PARAMS 子句，配合查询表达式中的 $param 占位符）
	Params map[string]any
}

// clone 返回 RedisFilter 的深拷贝，隔离外部可变状态
func (f RedisFilter) clone() RedisFilter {
	return RedisFilter{
		Query:          f.Query,
		NumericFilters: cloneRedisSearchFilters(f.NumericFilters),
		InKeys:         cloneAnySlice(f.InKeys),
		InFields:       cloneAnySlice(f.InFields),
		Params:         cloneRedisParams(f.Params),
	}
}

// RedisDocumentKind Redis 文档类型
type RedisDocumentKind uint8

const (
	// RedisJSON JSON 文档类型（默认）
	// 索引建立在 RedisJSON 对象上，字段通过 JSONPath 引用
	RedisJSON RedisDocumentKind = iota + 1
	// RedisHash HASH 文档类型，索引建立在 HASH 字段上
	RedisHash
)

// RedisSearchBuilder RedisSearch（Redis Query Engine / RediSearch）专属查询构建器
// 基于 FT.SEARCH 实现列表查询，初版约束：
//   - 必须通过 SetSort 提供单字段排序（无 SORTBY 的分页结果非确定，直接拒绝）
//   - 游标分页与 Explain 暂未实现，调用返回明确错误
//
// 泛型参数:
//
//	R: 查询结果的实体类型
type RedisSearchBuilder[R any] struct {
	builder[*RedisSearchBuilder[R], R]
	index        string            // Redis 索引名
	filter       RedisFilter       // Redis 专属查询条件
	sort         []RedisSort       // Redis 排序条件（仅允许单字段）
	returns      []RedisReturn     // Redis 字段投影（JSONPath + 别名）
	documentKind RedisDocumentKind // 文档类型，默认 JSON
}

// self 返回自身引用，实现 selfBuilder 接口
func (r *RedisSearchBuilder[R]) self() *RedisSearchBuilder[R] {
	return r
}

// NewRedisSearchBuilder 创建 RedisSearch 专属查询构建器实例
// index 为空时 QueryList 返回 ErrRedisIndexNotConfigured，可后续通过 SetIndex 补充
func NewRedisSearchBuilder[R any](data *core.DBProxy, index string) *RedisSearchBuilder[R] {
	b := &RedisSearchBuilder[R]{
		index:        index,
		documentKind: RedisJSON,
	}
	b.builder.data = data
	b.builder.dataSource = core.RedisSearch
	b.builder.applyQueryConfigDefaults()
	b.builder.setSelf(b, b)
	return b
}

// Clone 复制当前 RedisSearchBuilder 的查询配置，返回一个独立的新实例
// 新实例与原实例状态隔离，修改互不影响，适用于并发分叉查询场景
// 注意：原 RedisSearchBuilder 非并发安全，请勿在多 goroutine 中共享同一实例进行写操作
func (r *RedisSearchBuilder[R]) Clone() *RedisSearchBuilder[R] {
	cloned := &RedisSearchBuilder[R]{
		index:        r.index,
		filter:       r.filter.clone(),
		documentKind: r.documentKind,
	}
	r.builder.cloneBase(&cloned.builder)
	cloned.builder.setSelf(cloned, cloned)

	if r.sort != nil {
		cloned.sort = cloneRedisSorts(r.sort)
	}
	if r.returns != nil {
		cloned.returns = make([]RedisReturn, len(r.returns))
		copy(cloned.returns, r.returns)
	}
	return cloned
}

// SetIndex 设置 Redis 索引名（仅 RedisSearch 构建器专属方法）
// 返回 *RedisSearchBuilder[R]，支持链式调用
func (r *RedisSearchBuilder[R]) SetIndex(index string) *RedisSearchBuilder[R] {
	r.index = index
	return r
}

// SetDocumentKind 设置 Redis 文档类型（JSON / HASH），默认 JSON
func (r *RedisSearchBuilder[R]) SetDocumentKind(kind RedisDocumentKind) *RedisSearchBuilder[R] {
	r.documentKind = kind
	return r
}

// SetFilter 设置 Redis 查询条件（深拷贝入参，避免外部修改影响构建器状态）
func (r *RedisSearchBuilder[R]) SetFilter(filter RedisFilter) *RedisSearchBuilder[R] {
	r.filter = filter.clone()
	return r
}

// SetSort 设置 Redis 排序条件（拷贝入参切片）
// RediSearch 的 SORTBY 仅支持单属性排序，多字段时 QueryList 返回 ErrRedisSortFieldUnsupported
func (r *RedisSearchBuilder[R]) SetSort(sort ...RedisSort) *RedisSearchBuilder[R] {
	if sort == nil {
		r.sort = nil
		return r
	}
	r.sort = cloneRedisSorts(sort)
	return r
}

// SetReturn 显式设置 Redis 字段投影（JSONPath + 别名）
// 优先级高于 SetFields；未设置时根据文档类型使用默认投影策略
// JSON 文档需要 JSONPath 级投影（如 $.a[0].b）时必须使用本方法，SetFields 仅接受普通字段名
func (r *RedisSearchBuilder[R]) SetReturn(fields ...RedisReturn) *RedisSearchBuilder[R] {
	if fields == nil {
		r.returns = nil
		return r
	}
	returns := make([]RedisReturn, len(fields))
	copy(returns, fields)
	r.returns = returns
	return r
}

// Use 添加中间件（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) Use(middleware Middleware[R]) Querier[R] {
	r.builder.Use(middleware)
	return r
}

// SetStart 设置分页起始位置（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) SetStart(start uint32) Querier[R] {
	r.builder.SetStart(start)
	return r
}

// SetLimit 设置每页数据条数（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) SetLimit(limit uint32) Querier[R] {
	r.builder.SetLimit(limit)
	return r
}

// SetNeedTotal 设置是否需要查询总数（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) SetNeedTotal(needTotal bool) Querier[R] {
	r.builder.SetNeedTotal(needTotal)
	return r
}

// SetTotalLimit 设置总数统计上限，0 表示精确统计（实现 Querier 接口）
// RediSearch 的 total 始终为精确匹配总数，totalLimit 在该后端为 no-op，仅保留接口兼容
func (r *RedisSearchBuilder[R]) SetTotalLimit(totalLimit uint32) Querier[R] {
	r.builder.SetTotalLimit(totalLimit)
	return r
}

// SetNeedPagination 设置是否需要分页（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) SetNeedPagination(needPagination bool) Querier[R] {
	r.builder.SetNeedPagination(needPagination)
	return r
}

// SetFields 设置查询字段投影（实现 Querier 接口）
// 字段名视为 JSON 属性名自动补全 $. 前缀（JSON 文档）或直接作为 HASH 字段名
// 需要 JSONPath 级投影（如 $.a[0].b）时请使用 SetReturn
func (r *RedisSearchBuilder[R]) SetFields(fields ...string) Querier[R] {
	r.builder.SetFields(fields...)
	return r
}

// SetBeforeQueryHook 设置查询前置钩子（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) SetBeforeQueryHook(hook BeforeQueryHook) Querier[R] {
	r.builder.SetBeforeQueryHook(hook)
	return r
}

// SetAfterQueryHook 设置查询后置钩子（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) SetAfterQueryHook(hook AfterQueryHook[R]) Querier[R] {
	r.builder.SetAfterQueryHook(hook)
	return r
}

// SetCursorField 设置游标分页排序字段（实现 Querier 接口）
// 游标查询暂未实现，该配置在当前版本不会生效
func (r *RedisSearchBuilder[R]) SetCursorField(fields ...string) Querier[R] {
	r.builder.SetCursorField(fields...)
	return r
}

// SetCursorValue 设置游标初始值（实现 Querier 接口）
// 游标查询暂未实现，该配置在当前版本不会生效
func (r *RedisSearchBuilder[R]) SetCursorValue(values ...any) Querier[R] {
	r.builder.SetCursorValue(values...)
	return r
}

// GetQueryMeta 返回当前查询元信息的只读快照（实现 Querier 接口）
func (r *RedisSearchBuilder[R]) GetQueryMeta() core.QueryMeta {
	return r.builder.GetQueryMeta()
}

// doQuery 执行实际的 Redis FT.SEARCH 查询逻辑
func (r *RedisSearchBuilder[R]) doQuery(ctx context.Context) ([]*R, int64, error) {
	if r.index == "" {
		return nil, 0, ErrRedisIndexNotConfigured
	}
	if err := r.validateSort(); err != nil {
		return nil, 0, err
	}
	client, err := r.builder.data.RedisClient()
	if err != nil {
		return nil, 0, err
	}

	result, err := client.FTSearchWithArgs(ctx, r.index, r.redisQuery(), r.buildSearchOptions()).Result()
	if err != nil {
		return nil, 0, err
	}

	list, err := decodeRedisDocuments[R](result.Docs, r.normalizedReturns(), r.documentKind)
	if err != nil {
		return nil, 0, err
	}
	if !r.builder.needTotal {
		return list, 0, nil
	}
	return list, int64(result.Total), nil
}

// doCursorQuery 游标分页查询
func (r *RedisSearchBuilder[R]) doCursorQuery(
	ctx context.Context,
	cursorValues []any,
	isFirstBatch bool,
	probeHasMore bool,
) ([]*R, []any, int64, bool, error) {
	return nil, nil, 0, false, nil
}

// cleanupCursorQuery 游标查询暂未实现，无后端资源需要清理
func (r *RedisSearchBuilder[R]) cleanupCursorQuery(result *core.CursorPageResult[R], err error) {}

// Explain 返回 FT.EXPLAIN 查询表达式
func (r *RedisSearchBuilder[R]) Explain(ctx context.Context) (string, error) {
	return "", nil
}

// redisQuery 返回 FT.SEARCH 查询表达式，空条件时退化为通配符 *
func (r *RedisSearchBuilder[R]) redisQuery() string {
	if strings.TrimSpace(r.filter.Query) == "" {
		return "*"
	}
	return r.filter.Query
}

// buildSearchOptions 构建 FT.SEARCH 命令选项
func (r *RedisSearchBuilder[R]) buildSearchOptions() *redis.FTSearchOptions {
	options := &redis.FTSearchOptions{
		Filters:        cloneRedisSearchFilters(r.filter.NumericFilters),
		InKeys:         cloneAnySlice(r.filter.InKeys),
		InFields:       cloneAnySlice(r.filter.InFields),
		Params:         cloneRedisParams(r.filter.Params),
		DialectVersion: redisDialectV2,
	}

	// 字段投影：规范化后为空时不发送 RETURN 子句，由服务端默认策略返回完整文档
	if returns := r.normalizedReturns(); len(returns) > 0 {
		options.Return = make([]redis.FTSearchReturn, len(returns))
		for i, ret := range returns {
			options.Return[i] = redis.FTSearchReturn{FieldName: ret.Path, As: ret.As}
		}
	}

	// 排序；needTotal 时附加 WITHCOUNT，避免带 SORTBY 的 total 被截断为当前窗口结果数
	if sorts := cloneRedisSorts(r.sort); len(sorts) > 0 {
		options.SortBy = sorts
		options.SortByWithCount = r.builder.needTotal
	}

	// 分页；未启用分页时显式 LIMIT 0 maxLimit，避免引擎默认 10 条结果窗口截断
	if r.builder.needPagination {
		options.LimitOffset = int(r.builder.start)
		options.Limit = int(effectiveLimit(r.builder.limit))
	} else {
		options.LimitOffset = 0
		options.Limit = maxLimit
	}

	return options
}

// validateSort 校验排序条件满足 RediSearch 约束
//   - 无 SORTBY 的分页查询结果顺序非确定，直接拒绝
//   - SORTBY 仅支持单属性
//   - Asc 与 Desc 互斥，FieldName 必填
func (r *RedisSearchBuilder[R]) validateSort() error {
	switch len(r.sort) {
	case 0:
		return ErrRedisSortRequired
	case 1:
		if r.sort[0].FieldName == "" {
			return ErrRedisSortRequired
		}
		if r.sort[0].Asc && r.sort[0].Desc {
			return ErrRedisSortDirectionInvalid
		}
		return nil
	default:
		return ErrRedisSortFieldUnsupported
	}
}

// normalizedReturns 返回规范化后的投影列表，nil 表示无投影
// JSON 文档自动补全 JSONPath 前缀与默认别名，HASH 文档仅补全别名；空 Path 条目被过滤
func (r *RedisSearchBuilder[R]) normalizedReturns() []RedisReturn {
	returns := r.decodingReturns()
	if len(returns) == 0 {
		return nil
	}
	normalized := make([]RedisReturn, 0, len(returns))
	for _, ret := range returns {
		if ret.Path == "" {
			continue
		}
		normalized = append(normalized, normalizeRedisReturn(ret, r.documentKind))
	}
	return normalized
}

// decodingReturns 解析生效的投影列表，来源优先级：SetReturn > SetFields > 文档类型默认策略
// JSON 文档默认投影完整文档（$ AS json）；HASH 文档默认无投影（返回全部字段）
func (r *RedisSearchBuilder[R]) decodingReturns() []RedisReturn {
	if len(r.returns) > 0 {
		return r.returns
	}
	if len(r.builder.fields) > 0 {
		returns := make([]RedisReturn, len(r.builder.fields))
		for i, field := range r.builder.fields {
			returns[i] = redisReturnFromField(field, r.documentKind)
		}
		return returns
	}
	if r.documentKind == RedisJSON {
		return []RedisReturn{{Path: "$", As: redisJSONReturnAlias}}
	}
	return nil
}

// normalizeRedisReturn 规范化单条投影：JSON 文档补全 JSONPath 前缀，别名缺省时自动推导
func normalizeRedisReturn(ret RedisReturn, kind RedisDocumentKind) RedisReturn {
	normalized := ret
	if normalized.Path == "" {
		return normalized
	}
	if kind == RedisJSON {
		normalized.Path = ensureRedisJSONPath(normalized.Path)
	}
	if normalized.As == "" {
		normalized.As = redisJSONPathAlias(normalized.Path)
	}
	return normalized
}

// redisReturnFromField 将通用字段名转换为 Redis 投影
// JSON 文档字段名视为 JSON 属性名，自动补全 $. 前缀；HASH 文档直接使用字段名
func redisReturnFromField(field string, kind RedisDocumentKind) RedisReturn {
	if kind == RedisJSON {
		path := ensureRedisJSONPath(field)
		return RedisReturn{Path: path, As: redisJSONPathAlias(path)}
	}
	return RedisReturn{Path: field, As: field}
}

// ensureRedisJSONPath 确保 JSONPath 以 $. 开头
// "name" → "$.name"；"$"、"$.*"、"$[0]" 等已带前缀的原样返回
func ensureRedisJSONPath(path string) string {
	if path == "" || path == "$" {
		return path
	}
	if strings.HasPrefix(path, "$.") || strings.HasPrefix(path, "$[") {
		return path
	}
	return "$." + path
}

// redisJSONPathAlias 从 JSONPath 推导投影别名，取最后一段路径并清理特殊字符
// "$.a.b" → "b"；"$.a[0]" → "0"；"$" → "json"（完整文档默认别名）
func redisJSONPathAlias(path string) string {
	if path == "" || path == "$" {
		return redisJSONReturnAlias
	}
	segment := path
	if idx := strings.LastIndexAny(segment, ".["); idx >= 0 {
		segment = segment[idx+1:]
	}
	segment = strings.Trim(segment, "`\"'*]")
	if segment == "" {
		return redisJSONReturnAlias
	}
	return segment
}

// decodeRedisDocuments 将 FT.SEARCH 返回的文档列表反序列化为实体切片
func decodeRedisDocuments[R any](docs []redis.Document, returns []RedisReturn, kind RedisDocumentKind) ([]*R, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	list := make([]*R, 0, len(docs))
	for _, doc := range docs {
		item, err := decodeRedisDocument[R](doc, returns, kind)
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, nil
}

// decodeRedisDocument 将单个文档反序列化为实体
// 优先走完整文档路径（JSON 文档的 $ 投影），局部投影按别名组装后 best-effort 反序列化
func decodeRedisDocument[R any](doc redis.Document, returns []RedisReturn, kind RedisDocumentKind) (*R, error) {
	var item R
	if doc.Error != nil {
		return nil, fmt.Errorf("redis: document %q: %w", doc.ID, doc.Error)
	}

	if payload, ok := redisFullDocumentPayload(doc, returns, kind); ok {
		if err := json.Unmarshal([]byte(payload), &item); err != nil {
			return nil, fmt.Errorf("redis: unmarshal document %q: %w", doc.ID, err)
		}
		return &item, nil
	}

	// 局部投影（JSON）或 HASH 文档：按字段名组装为对象后反序列化
	// RediSearch 返回的字段值均为字符串：合法 JSON 直接透传，其余包裹为 JSON 字符串
	if len(doc.Fields) == 0 {
		return &item, nil
	}
	payload := make(map[string]json.RawMessage, len(doc.Fields))
	for key, value := range doc.Fields {
		payload[key] = redisBestEffortRawMessage(value)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("redis: encode partial document %q: %w", doc.ID, err)
	}
	if err := json.Unmarshal(encoded, &item); err != nil {
		return nil, fmt.Errorf("redis: unmarshal partial document %q: %w", doc.ID, err)
	}
	return &item, nil
}

// redisFullDocumentPayload 提取文档的完整 JSON 载荷
// JSON 文档场景：显式 $ 投影取别名键；无投影时 RediSearch 以 "$" 为键返回完整文档
// 第二返回值为 false 表示非完整文档路径（局部投影或 HASH 文档）
func redisFullDocumentPayload(doc redis.Document, returns []RedisReturn, kind RedisDocumentKind) (string, bool) {
	if kind != RedisJSON {
		return "", false
	}
	if full, ok := redisFullDocumentReturn(returns); ok {
		if raw, exists := doc.Fields[full.As]; exists {
			return raw, true
		}
		return "", false
	}
	if len(returns) == 0 {
		if raw, exists := doc.Fields["$"]; exists {
			return raw, true
		}
	}
	return "", false
}

// redisFullDocumentReturn 判断投影列表是否包含完整文档投影（Path 为 $）
func redisFullDocumentReturn(returns []RedisReturn) (RedisReturn, bool) {
	for _, ret := range returns {
		if ret.Path == "$" {
			return ret, true
		}
	}
	return RedisReturn{}, false
}

// redisBestEffortRawMessage 将文档字段值转换为 JSON RawMessage
// 数值 / 布尔 / 对象 / 数组等合法 JSON 直接透传，普通字符串包裹为 JSON 字符串
func redisBestEffortRawMessage(value string) json.RawMessage {
	if json.Valid([]byte(value)) {
		return json.RawMessage(value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// cloneRedisSearchFilters 深拷贝数值过滤条件切片
func cloneRedisSearchFilters(filters []redis.FTSearchFilter) []redis.FTSearchFilter {
	if filters == nil {
		return nil
	}
	cloned := make([]redis.FTSearchFilter, len(filters))
	copy(cloned, filters)
	return cloned
}

// cloneRedisSorts 深拷贝排序条件切片
func cloneRedisSorts(sorts []RedisSort) []RedisSort {
	if sorts == nil {
		return nil
	}
	cloned := make([]RedisSort, len(sorts))
	copy(cloned, sorts)
	return cloned
}

// cloneAnySlice 拷贝 any 切片，隔离切片头；元素值视为不可变
func cloneAnySlice(values []any) []any {
	if values == nil {
		return nil
	}
	cloned := make([]any, len(values))
	copy(cloned, values)
	return cloned
}

// cloneRedisParams 拷贝参数表，隔离 map 结构；参数值视为不可变
func cloneRedisParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	return cloned
}
