package builder

import (
	"context"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"sync"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/fantasticbin/QueryBuilder/v2/util"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// GormScope GORM 查询作用域类型
type GormScope = func(*gorm.DB) *gorm.DB

// GormBuilder GORM 兼容数据库专属查询构建器
// 泛型参数:
//
//	R: 查询结果的实体类型
type GormBuilder[R any] struct {
	builder[*GormBuilder[R], R]
	filter GormScope // GORM 专属过滤条件
	sort   GormScope // GORM 专属排序条件
}

// self 返回自身引用，实现 builderInterface 接口
func (g *GormBuilder[R]) self() *GormBuilder[R] {
	return g
}

// NewGormBuilder 创建 GORM 专属查询构建器实例
func NewGormBuilder[R any](data *DBProxy) *GormBuilder[R] {
	g := &GormBuilder[R]{}
	g.builder.data = data
	g.builder.dataSource = Gorm
	g.builder.limit = defaultLimit
	g.builder.setSelf(g, g)
	return g
}

// Clone 复制当前 GormBuilder 的查询配置，返回一个独立的新实例
// 新实例与原实例状态隔离，修改互不影响，适用于并发分叉查询场景
// 注意：原 GormBuilder 非并发安全，请勿在多 goroutine 中共享同一实例进行写操作
func (g *GormBuilder[R]) Clone() *GormBuilder[R] {
	cloned := &GormBuilder[R]{
		filter: g.filter,
		sort:   g.sort,
	}
	g.builder.cloneBase(&cloned.builder)
	cloned.builder.setSelf(cloned, cloned)
	return cloned
}

// SetFilter 设置 GORM 过滤条件
func (g *GormBuilder[R]) SetFilter(filter GormScope) *GormBuilder[R] {
	g.filter = filter
	return g
}

// SetSort 设置 GORM 排序条件
func (g *GormBuilder[R]) SetSort(sort GormScope) *GormBuilder[R] {
	g.sort = sort
	return g
}

// Use 添加中间件（实现 Querier 接口）
func (g *GormBuilder[R]) Use(middleware Middleware[R]) Querier[R] {
	g.builder.Use(middleware)
	return g
}

// SetStart 设置分页起始位置（实现 Querier 接口）
func (g *GormBuilder[R]) SetStart(start uint32) Querier[R] {
	g.builder.SetStart(start)
	return g
}

// SetLimit 设置每页数据条数（实现 Querier 接口）
func (g *GormBuilder[R]) SetLimit(limit uint32) Querier[R] {
	g.builder.SetLimit(limit)
	return g
}

// SetNeedTotal 设置是否需要查询总数（实现 Querier 接口）
func (g *GormBuilder[R]) SetNeedTotal(needTotal bool) Querier[R] {
	g.builder.SetNeedTotal(needTotal)
	return g
}

// SetNeedPagination 设置是否需要分页（实现 Querier 接口）
func (g *GormBuilder[R]) SetNeedPagination(needPagination bool) Querier[R] {
	g.builder.SetNeedPagination(needPagination)
	return g
}

// SetFields 设置查询字段投影（实现 Querier 接口）
func (g *GormBuilder[R]) SetFields(fields ...string) Querier[R] {
	g.builder.SetFields(fields...)
	return g
}

// SetBeforeQueryHook 设置查询前置钩子（实现 Querier 接口）
func (g *GormBuilder[R]) SetBeforeQueryHook(hook BeforeQueryHook) Querier[R] {
	g.builder.SetBeforeQueryHook(hook)
	return g
}

// SetAfterQueryHook 设置查询后置钩子（实现 Querier 接口）
func (g *GormBuilder[R]) SetAfterQueryHook(hook AfterQueryHook[R]) Querier[R] {
	g.builder.SetAfterQueryHook(hook)
	return g
}

// SetCursorField 设置游标分页排序字段（实现 Querier 接口）
func (g *GormBuilder[R]) SetCursorField(fields ...string) Querier[R] {
	g.builder.SetCursorField(fields...)
	return g
}

// SetCursorValue 设置游标初始值（实现 Querier 接口）
func (g *GormBuilder[R]) SetCursorValue(values ...any) Querier[R] {
	g.builder.SetCursorValue(values...)
	return g
}

// GetQueryMeta 返回当前查询元信息的只读快照（实现 Querier 接口）
func (g *GormBuilder[R]) GetQueryMeta() QueryMeta {
	return g.builder.GetQueryMeta()
}

// QueryList 执行 GORM 查询列表操作
func (g *GormBuilder[R]) QueryList(ctx context.Context) (*core.ListResult[R], error) {
	g.builder.beginQueryMode(false)
	if err := g.builder.prepareAndValidate(); err != nil {
		return nil, err
	}
	result, err := executeWithMiddlewares(
		ctx,
		newMiddlewareContext[R](&g.builder),
		func(ctx context.Context) (core.Result[R], error) {
			list, total, err := g.doQuery(ctx)
			return &core.ListResult[R]{Items: list, Total: total}, err
		},
	)
	if err != nil {
		return nil, err
	}
	return listResultFromResult(result), nil
}

// QueryCursor 执行 GORM 游标分页查询，返回迭代器（实现 Querier 接口）
func (g *GormBuilder[R]) QueryCursor(ctx context.Context) iter.Seq2[*R, error] {
	return executeBuilderCursorQuery(
		ctx,
		&g.builder,
		func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*R, []any, int64, bool, error) {
			return g.doCursorQuery(ctx, cursorValues, isFirstBatch, false)
		},
	)
}

// QueryPage 执行 GORM 单批次游标分页查询，返回结构化的分页结果（实现 Querier 接口）
func (g *GormBuilder[R]) QueryPage(ctx context.Context) (*core.CursorPageResult[R], error) {
	g.builder.beginQueryMode(true)
	defer g.builder.finishCursorQuery()
	if err := g.builder.prepareAndValidate(); err != nil {
		return nil, err
	}
	return executePageWithMiddlewares(
		ctx,
		newMiddlewareContext[R](&g.builder),
		func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*R, []any, int64, bool, error) {
			return g.doCursorQuery(ctx, cursorValues, isFirstBatch, true)
		},
	)
}

// buildQuery 构建公共的 GORM 查询对象（私有方法）
// 将字段投影、过滤条件、排序条件、分页等公共逻辑统一抽取
func (g *GormBuilder[R]) buildQuery(db *gorm.DB) *gorm.DB {
	query := db.Model(new(R))

	// 应用字段投影
	if len(g.builder.fields) > 0 {
		query = query.Select(g.builder.fields)
	}

	if g.filter != nil {
		query = query.Scopes(g.filter)
	}
	if g.sort != nil {
		query = query.Scopes(g.sort)
	}

	if g.builder.needPagination {
		if g.builder.limit == 0 {
			g.builder.limit = defaultLimit
		}
		query = query.Offset(int(g.builder.start)).Limit(int(g.builder.limit))
	}

	return query
}

// doQuery 执行实际的 GORM 查询逻辑
func (g *GormBuilder[R]) doQuery(ctx context.Context) (list []*R, total int64, err error) {
	// 使用 WaitAndGo 并行执行数据查询和总数统计操作
	if err = util.WaitAndGo(func() error {
		query := g.buildQuery(g.builder.data.DB.WithContext(ctx))
		return query.Find(&list).Error
	}, func() error {
		if !g.builder.needTotal {
			return nil
		}

		query := g.builder.data.DB.WithContext(ctx).
			Model(new(R))

		if g.filter != nil {
			query = query.Scopes(g.filter)
		}

		return query.Count(&total).Error
	}); err != nil {
		return nil, 0, err
	}

	return list, total, nil
}

// Explain 返回 GORM 构建器最终生成的 SQL 语句（Dry Run 模式）
// 用于调试场景，不会实际执行查询
// 若已配置游标字段，将输出游标查询模式的首批查询 SQL
func (g *GormBuilder[R]) Explain(ctx context.Context) (string, error) {
	if err := g.builder.prepareAndValidate(); err != nil {
		return "", err
	}

	// 如果配置了游标字段，展示游标查询模式的首批 SQL
	if len(g.builder.cursorFields) > 0 {
		return g.explainCursor(ctx)
	}

	query := g.buildQuery(g.builder.data.DB.WithContext(ctx).
		Session(&gorm.Session{DryRun: true}))

	stmt := query.Find(new([]R)).Statement
	if stmt.Error != nil {
		return "", stmt.Error
	}

	// 构建带参数的完整 SQL
	sql := stmt.SQL.String()
	if len(stmt.Vars) > 0 {
		var args []string
		for _, v := range stmt.Vars {
			args = append(args, fmt.Sprintf("%v", v))
		}
		sql = sql + " | args: [" + strings.Join(args, ", ") + "]"
	}

	return sql, nil
}

// buildCursorBatchSize 获取游标查询的批次大小
func (g *GormBuilder[R]) buildCursorBatchSize() int {
	batchSize := int(g.builder.limit)
	if batchSize == 0 {
		batchSize = defaultLimit
	}
	return batchSize
}

// buildCursorQuery 构建游标查询的公共 GORM 查询对象（不含游标条件）
// 包含字段投影、用户 filter、游标字段排序、用户辅助排序、批次大小
func (g *GormBuilder[R]) buildCursorQuery(db *gorm.DB) *gorm.DB {
	query := db.Model(new(R))

	// 应用字段投影
	if len(g.builder.fields) > 0 {
		query = query.Select(g.builder.fields)
	}

	// 应用用户 filter 条件
	if g.filter != nil {
		query = query.Scopes(g.filter)
	}

	// 游标字段排序为主（升序）
	cursorFields := g.builder.getParsedCursorFields()
	for _, cursorField := range cursorFields {
		order := "ASC"
		if !cursorField.Asc {
			order = "DESC"
		}
		query = query.Order(fmt.Sprintf("%s %s", cursorField.Field, order))
	}

	// 用户 sort 作为辅助排序
	if g.sort != nil {
		query = query.Scopes(g.sort)
	}

	// 设置批次大小
	query = query.Limit(g.buildCursorBatchSize())

	return query
}

// explainCursor 返回游标查询模式的首批查询 SQL（Dry Run 模式）
func (g *GormBuilder[R]) explainCursor(ctx context.Context) (string, error) {
	query := g.buildCursorQuery(
		g.builder.data.DB.WithContext(ctx).Session(&gorm.Session{DryRun: true}),
	)

	stmt := query.Find(new([]R)).Statement
	if stmt.Error != nil {
		return "", stmt.Error
	}

	// 构建带参数的完整 SQL
	sql := "[CursorQuery] " + stmt.SQL.String()
	if len(stmt.Vars) > 0 {
		var args []string
		for _, v := range stmt.Vars {
			args = append(args, fmt.Sprintf("%v", v))
		}
		sql = sql + " | args: [" + strings.Join(args, ", ") + "]"
	}
	sql = sql + " | cursor_fields: [" + strings.Join(g.builder.cursorFields, ", ") + "]"

	return sql, nil
}

// doCursorQuery 执行 GORM 游标分页的单批次查询
// 构建基于行值表达式的 SQL 游标条件
// probeHasMore 为 true 时，通过 limit+1 探测精确判断是否还有下一页
// isFirstBatch 为 true 时，若 needTotal 也为 true，则并行执行 Count 查询
func (g *GormBuilder[R]) doCursorQuery(ctx context.Context, cursorValues []any, isFirstBatch bool, probeHasMore bool) ([]*R, []any, int64, bool, error) {
	batchSize := g.buildCursorBatchSize()

	// 构建查询
	query := g.buildCursorQuery(g.builder.data.DB.WithContext(ctx))
	// probeHasMore 模式下覆盖 limit 为 batchSize+1
	if probeHasMore {
		query = query.Limit(batchSize + 1)
	}

	// 构建游标条件（仅在有游标值时添加）
	if len(cursorValues) > 0 {
		cursorFields := g.builder.getParsedCursorFields()
		if len(cursorFields) == 1 {
			op := ">"
			if !cursorFields[0].Asc {
				op = "<"
			}
			query = query.Where(fmt.Sprintf("%s %s ?", cursorFields[0].Field, op), cursorValues[0])
		} else {
			if asc, uniform := isUniformCursorDirection(cursorFields); uniform {
				// 性能优化：方向一致时使用行值比较，通常比 OR 组合条件更利于索引与执行计划。
				op := ">"
				if !asc {
					op = "<"
				}
				fieldList := make([]string, 0, len(cursorFields))
				for _, cf := range cursorFields {
					fieldList = append(fieldList, cf.Field)
				}
				placeholders := strings.TrimRight(strings.Repeat("?,", len(cursorValues)), ",")
				query = query.Where(fmt.Sprintf("(%s) %s (%s)", strings.Join(fieldList, ", "), op, placeholders), cursorValues...)
			} else {
				// 混排场景（如 created_at DESC, id ASC）无法直接使用单一行值比较，回退到词典序 OR 条件。
				var orParts []string
				args := make([]any, 0, len(cursorFields)*(len(cursorFields)+1)/2)
				for i := 0; i < len(cursorFields); i++ {
					andParts := make([]string, 0, i+1)
					for j := 0; j < i; j++ {
						andParts = append(andParts, fmt.Sprintf("%s = ?", cursorFields[j].Field))
						args = append(args, cursorValues[j])
					}
					op := ">"
					if !cursorFields[i].Asc {
						op = "<"
					}
					andParts = append(andParts, fmt.Sprintf("%s %s ?", cursorFields[i].Field, op))
					args = append(args, cursorValues[i])
					orParts = append(orParts, "("+strings.Join(andParts, " AND ")+")")
				}
				query = query.Where(strings.Join(orParts, " OR "), args...)
			}
		}
	}

	var list []*R
	var total int64
	if err := util.WaitAndGo(func() error {
		return query.Find(&list).Error
	}, func() error {
		// 首批次且需要总数时，并行执行数据查询和 Count 查询
		if !isFirstBatch || !g.builder.needTotal || g.afterHook == nil {
			return nil
		}

		countQuery := g.builder.data.DB.WithContext(ctx).Model(new(R))
		if g.filter != nil {
			countQuery = countQuery.Scopes(g.filter)
		}
		return countQuery.Count(&total).Error
	}); err != nil {
		return nil, nil, 0, false, err
	}

	if len(list) == 0 {
		return list, nil, total, false, nil
	}

	// 判断 hasMore：probeHasMore 模式下通过返回条数是否超过 batchSize 精确判断
	hasMore := probeHasMore && len(list) > batchSize
	// 如果有更多数据，截断为 batchSize 条
	if hasMore {
		list = list[:batchSize]
	}

	// 从（截断后的）最后一条提取游标值
	s, err := schema.Parse(new(R), &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		return nil, nil, 0, false, fmt.Errorf("schema parse failed: %w", err)
	}

	lastItem := list[len(list)-1]
	rv := reflect.ValueOf(lastItem).Elem()
	nextCursorValues := make([]any, 0, len(g.builder.cursorFields))
	for _, cursorField := range g.builder.getParsedCursorFields() {
		field := s.LookUpField(cursorField.Field)
		if field == nil {
			return nil, nil, 0, false, fmt.Errorf("cursor field %q not found in schema", cursorField.Field)
		}
		val := field.ReflectValueOf(ctx, rv)
		nextCursorValues = append(nextCursorValues, val.Interface())
	}

	return list, nextCursorValues, total, hasMore, nil
}
