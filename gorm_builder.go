package builder

import (
	"context"
	"fmt"
	"strings"

	"github.com/fantasticbin/QueryBuilder/util"
	"gorm.io/gorm"
)

// GormScope GORM 查询作用域类型
type GormScope = func(*gorm.DB) *gorm.DB

// GormBuilder MySQL（GORM）专属查询构建器
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
	g.builder.dataSource = MySQL
	g.builder.setSelf(g, g)
	return g
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

// QueryList 执行 GORM 查询列表操作
func (g *GormBuilder[R]) QueryList(ctx context.Context) ([]*R, int64, error) {
	return g.builder.executeWithMiddlewares(ctx, func(ctx context.Context) ([]*R, int64, error) {
		return g.doQuery(ctx)
	})
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
		if g.builder.limit < 1 {
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
func (g *GormBuilder[R]) Explain(ctx context.Context) (string, error) {
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