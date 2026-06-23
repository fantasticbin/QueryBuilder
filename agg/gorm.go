package agg

import (
	"context"
	"fmt"
	"strings"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GormFilter 用于配置 GORM 聚合查询的数据过滤条件
type GormFilter = func(*gorm.DB) *gorm.DB

// GormBuilder 用于构建兼容 GORM 数据库的聚合查询
type GormBuilder[M any, A any] struct {
	base[A]
	filter GormFilter
}

// NewGormBuilder 创建 GORM 聚合查询构建器
func NewGormBuilder[M any, A any](data *core.DBProxy, spec Spec) *GormBuilder[M, A] {
	b := &GormBuilder[M, A]{}
	b.data = data
	b.dataSource = core.Gorm
	b.spec = normalizeSpec(spec)
	b.setSelf(b)
	return b
}

// SetFilter 设置 GORM 数据过滤条件
func (b *GormBuilder[M, A]) SetFilter(filter GormFilter) *GormBuilder[M, A] {
	b.filter = filter
	return b
}

// Clone 返回配置相互隔离的构建器副本
func (b *GormBuilder[M, A]) Clone() *GormBuilder[M, A] {
	cloned := &GormBuilder[M, A]{filter: b.filter}
	b.base.cloneBase(&cloned.base)
	cloned.setSelf(cloned)
	return cloned
}

// Query 执行 GORM 聚合查询
func (b *GormBuilder[M, A]) Query(ctx context.Context) (*Result[A], error) {
	if err := b.prepare(); err != nil {
		return nil, err
	}

	return b.execute(ctx, func(ctx context.Context) (*Result[A], error) {
		db, err := b.data.GormDB()
		if err != nil {
			return nil, err
		}

		rows := make([]*A, 0)
		query := b.buildQuery(db.WithContext(ctx))
		if err := query.Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("executing gorm aggregate query: %w", err)
		}
		if len(b.spec.Groups) == 0 && len(rows) == 0 {
			rows = append(rows, new(A))
		}
		return &Result[A]{Rows: rows}, nil
	})
}

// Explain 返回生成的 SQL，但不会执行查询
func (b *GormBuilder[M, A]) Explain(ctx context.Context) (string, error) {
	if err := b.prepare(); err != nil {
		return "", err
	}
	db, err := b.data.GormDB()
	if err != nil {
		return "", err
	}

	query := b.buildQuery(db.WithContext(ctx).Session(&gorm.Session{DryRun: true}))
	stmt := query.Find(new([]A)).Statement
	if stmt.Error != nil {
		return "", fmt.Errorf("building gorm aggregate query: %w", stmt.Error)
	}

	sql := stmt.SQL.String()
	if len(stmt.Vars) == 0 {
		return sql, nil
	}
	args := make([]string, 0, len(stmt.Vars))
	for _, value := range stmt.Vars {
		args = append(args, fmt.Sprintf("%v", value))
	}
	return sql + " | args: [" + strings.Join(args, ", ") + "]", nil
}

// buildQuery 根据聚合规范构建 GORM 查询对象
func (b *GormBuilder[M, A]) buildQuery(db *gorm.DB) *gorm.DB {
	query := db.Model(new(M))
	if b.filter != nil {
		query = query.Scopes(b.filter)
	}

	selects := make([]string, 0, len(b.spec.Groups)+len(b.spec.Metrics))
	for _, group := range b.spec.Groups {
		field := quoteGormIdentifier(db, group.Field)
		alias := quoteGormIdentifier(db, group.Alias)
		selects = append(selects, field+" AS "+alias)
		query = query.Where(field + " IS NOT NULL")
	}
	for _, metric := range b.spec.Metrics {
		alias := quoteGormIdentifier(db, metric.Alias)
		if metric.Func == Count {
			selects = append(selects, "COUNT(*) AS "+alias)
			continue
		}
		field := quoteGormIdentifier(db, metric.Field)
		selects = append(selects, strings.ToUpper(metric.Func.String())+"("+field+") AS "+alias)
	}

	query = query.Select(strings.Join(selects, ", "))
	if len(b.spec.Groups) == 0 {
		return query
	}
	groupColumns := make([]clause.Column, 0, len(b.spec.Groups))
	for _, group := range b.spec.Groups {
		groupColumns = append(groupColumns, clause.Column{
			Name: quoteGormIdentifier(db, group.Field),
			Raw:  true,
		})
	}
	query = query.Clauses(clause.GroupBy{Columns: groupColumns})
	for _, group := range b.spec.Groups {
		query = query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: group.Alias},
			Desc:   group.Descending,
		})
	}
	return query.Limit(int(b.spec.Limit))
}

// quoteGormIdentifier 使用当前 GORM 方言引用点分标识符
func quoteGormIdentifier(db *gorm.DB, identifier string) string {
	parts := strings.Split(identifier, ".")
	var quoted strings.Builder
	for i, part := range parts {
		if i > 0 {
			quoted.WriteByte('.')
		}
		db.Dialector.QuoteTo(&quoted, part)
	}
	return quoted.String()
}
