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
// 泛型参数:
//
//	M: GORM 源模型类型，用于 db.Model(new(M)) 推导源表和字段映射
//	A: 聚合结果行 DTO 类型，用于 Scan 解码聚合输出
type GormBuilder[M any, A any] struct {
	base[A]
	filter GormFilter
}

// NewGormBuilder 创建 GORM 聚合查询构建器
// 泛型参数:
//
//	M: GORM 源模型类型，用于推导聚合查询的数据来源
//	A: 聚合结果行 DTO 类型
func NewGormBuilder[M any, A any](data *core.DBProxy) *GormBuilder[M, A] {
	b := &GormBuilder[M, A]{}
	b.data = data
	b.dataSource = core.Gorm
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
	selectArgs := make([]any, 0)
	for _, group := range b.spec.Groups {
		field := quoteGormIdentifier(db, group.Field)
		alias := quoteGormIdentifier(db, group.Alias)
		selects = append(selects, field+" AS "+alias)
		query = query.Where(field + " IS NOT NULL")
	}
	for _, metric := range b.spec.Metrics {
		selection, args := gormMetricSelection(db, metric)
		selects = append(selects, selection)
		selectArgs = append(selectArgs, args...)
	}

	query = query.Select(strings.Join(selects, ", "), selectArgs...)
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
	for _, having := range b.spec.Havings {
		expression, args := gormHavingExpression(db, b.spec, having)
		query = query.Having(expression, args...)
	}
	for _, order := range effectiveOrders(b.spec) {
		query = query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: order.Alias},
			Desc:   order.Descending,
		})
	}
	return query.Limit(int(b.spec.Limit))
}

// gormMetricSelection 构建单个指标的 SELECT 表达式和绑定参数
func gormMetricSelection(db *gorm.DB, metric Metric) (string, []any) {
	alias := quoteGormIdentifier(db, metric.Alias)
	expression, args := gormMetricExpression(db, metric)
	return expression + " AS " + alias, args
}

// gormMetricExpression 构建单个指标的聚合表达式和绑定参数
func gormMetricExpression(db *gorm.DB, metric Metric) (string, []any) {
	if metric.Condition != nil {
		return gormConditionalMetricExpression(db, metric)
	}
	if metric.Func == Count {
		if isDistinctCount(metric) {
			field := quoteGormIdentifier(db, metric.Field)
			return "COUNT(DISTINCT " + field + ")", nil
		}
		return "COUNT(*)", nil
	}
	field := quoteGormIdentifier(db, metric.Field)
	fn := strings.ToUpper(metric.Func.String())
	if isDistinctSum(metric) {
		return fn + "(DISTINCT " + field + ")", nil
	}
	return fn + "(" + field + ")", nil
}

// gormConditionalMetricExpression 构建带条件指标的聚合表达式和绑定参数
func gormConditionalMetricExpression(db *gorm.DB, metric Metric) (string, []any) {
	condition, args := gormConditionExpression(db, *metric.Condition)
	if metric.Func == Count {
		if isDistinctCount(metric) {
			field := quoteGormIdentifier(db, metric.Field)
			return "COUNT(DISTINCT CASE WHEN " + condition + " THEN " + field + " END)", args
		}
		return "COUNT(CASE WHEN " + condition + " THEN 1 END)", args
	}

	field := quoteGormIdentifier(db, metric.Field)
	valueExpression := "CASE WHEN " + condition + " THEN " + field
	if metric.Func == Sum {
		valueExpression += " ELSE 0"
	}
	valueExpression += " END"

	fn := strings.ToUpper(metric.Func.String())
	if isDistinctSum(metric) {
		return fn + "(DISTINCT " + valueExpression + ")", args
	}
	return fn + "(" + valueExpression + ")", args
}

// gormConditionExpression 构建字段级条件的 SQL 片段和绑定参数
func gormConditionExpression(db *gorm.DB, condition Condition) (string, []any) {
	field := quoteGormIdentifier(db, condition.Field)
	return field + " " + gormOperator(condition.Op) + " ?", []any{condition.Value}
}

// gormHavingExpression 构建 HAVING 条件的 SQL 片段和绑定参数
func gormHavingExpression(db *gorm.DB, spec Spec, having Having) (string, []any) {
	metric, ok := metricByAlias(spec.Metrics, having.Alias)
	if !ok {
		alias := quoteGormIdentifier(db, having.Alias)
		return alias + " " + gormOperator(having.Op) + " ?", []any{having.Value}
	}
	expression, args := gormMetricExpression(db, metric)
	args = append(args, having.Value)
	return expression + " " + gormOperator(having.Op) + " ?", args
}

// gormOperator 将通用比较操作符转换为 SQL 操作符
func gormOperator(op Operator) string {
	switch op {
	case Eq:
		return "="
	case Ne:
		return "<>"
	case Gt:
		return ">"
	case Gte:
		return ">="
	case Lt:
		return "<"
	case Lte:
		return "<="
	default:
		return "="
	}
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
