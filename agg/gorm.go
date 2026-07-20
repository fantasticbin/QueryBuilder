package agg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/fantasticbin/QueryBuilder/v2/util"
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
	b.needTotal = true
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
		if err := validateGormDateGroups(db, b.spec); err != nil {
			return nil, err
		}

		if len(b.spec.Groups) == 0 {
			rows := make([]*A, 0)
			query := b.buildQuery(db.WithContext(ctx))
			if err := query.Scan(&rows).Error; err != nil {
				return nil, fmt.Errorf("executing gorm aggregate query: %w", err)
			}
			if len(rows) == 0 {
				rows = append(rows, new(A))
			}
			return &Result[A]{Rows: rows, Total: 1}, nil
		}

		rows := make([]*A, 0)
		var total int64
		if err := util.WaitAndGoWithContext(ctx, func(ctx context.Context) error {
			query := b.buildQueryWithOptions(db.WithContext(ctx), true, true)
			if err := query.Scan(&rows).Error; err != nil {
				return fmt.Errorf("executing gorm aggregate query: %w", err)
			}
			return nil
		}, func(ctx context.Context) error {
			if !b.needTotal {
				return nil
			}
			count, err := b.countTotal(ctx, db)
			if err != nil {
				return err
			}
			total = count
			return nil
		}); err != nil {
			return nil, err
		}
		return &Result[A]{Rows: rows, Total: total}, nil
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
	if err := validateGormDateGroups(db, b.spec); err != nil {
		return "", err
	}

	query := b.buildQuery(db.WithContext(ctx).Session(&gorm.Session{DryRun: true}))
	stmt := query.Find(new([]A)).Statement
	if stmt.Error != nil {
		return "", fmt.Errorf("building gorm aggregate query: %w", stmt.Error)
	}

	explanation := stmt.SQL.String()
	if len(stmt.Vars) > 0 {
		args := make([]string, 0, len(stmt.Vars))
		for _, value := range stmt.Vars {
			args = append(args, fmt.Sprintf("%v", value))
		}
		explanation += " | args: [" + strings.Join(args, ", ") + "]"
	}
	return explanation + " | plan: " + gormPlanJSON(b.Meta().Plan), nil
}

// buildQuery 根据聚合规范构建 GORM 查询对象
func (b *GormBuilder[M, A]) buildQuery(db *gorm.DB) *gorm.DB {
	return b.buildQueryWithOptions(db, true, true)
}

// buildQueryWithOptions 根据聚合规范构建 GORM 查询对象，可选择跳过排序和分页
func (b *GormBuilder[M, A]) buildQueryWithOptions(db *gorm.DB, includeOrder, includePagination bool) *gorm.DB {
	query := db.Model(new(M))
	if b.filter != nil {
		query = query.Scopes(b.filter)
	}

	selects := make([]string, 0, len(b.spec.Groups)+len(b.spec.Metrics))
	selectArgs := make([]any, 0)
	for _, group := range b.spec.Groups {
		field := quoteGormIdentifier(db, group.Field)
		selection := gormGroupExpression(db, group)
		alias := quoteGormIdentifier(db, group.Alias)
		selects = append(selects, selection+" AS "+alias)
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
			Name: gormGroupExpression(db, group),
			Raw:  true,
		})
	}
	query = query.Clauses(clause.GroupBy{Columns: groupColumns})
	for _, having := range b.spec.Havings {
		expression, args := gormHavingExpression(db, b.spec, having)
		query = query.Having(expression, args...)
	}
	if includeOrder {
		for _, order := range effectiveOrders(b.spec) {
			query = query.Order(clause.OrderByColumn{
				Column: clause.Column{Name: order.Alias},
				Desc:   order.Descending,
			})
		}
	}
	if includePagination {
		if b.spec.Start > 0 {
			query = query.Offset(int(b.spec.Start))
		}
		query = query.Limit(int(b.spec.Limit))
	}
	return query
}

// countTotal 统计聚合后、HAVING 后的分组行数
func (b *GormBuilder[M, A]) countTotal(ctx context.Context, db *gorm.DB) (int64, error) {
	if len(b.spec.Groups) == 0 {
		return 1, nil
	}

	subQuery := b.buildQueryWithOptions(db.WithContext(ctx), false, false)
	if b.totalLimit > 0 {
		subQuery = subQuery.Limit(int(b.totalLimit))
	}

	var total int64
	if err := db.WithContext(ctx).
		Table("(?) AS querybuilder_aggregate_total", subQuery).
		Count(&total).Error; err != nil {
		return 0, fmt.Errorf("counting gorm aggregate groups: %w", err)
	}
	return total, nil
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
	switch condition.Op {
	case In:
		return field + " IN ?", []any{condition.Value}
	case NotIn:
		return field + " NOT IN ?", []any{condition.Value}
	case Between:
		start, end, _ := conditionRangeValues(condition.Value)
		return field + " BETWEEN ? AND ?", []any{start, end}
	case Exists, IsNotNull:
		return field + " IS NOT NULL", nil
	case NotExists, IsNull:
		return field + " IS NULL", nil
	case Like:
		return field + " LIKE ?", []any{condition.Value}
	case NotLike:
		return field + " NOT LIKE ?", []any{condition.Value}
	default:
		return field + " " + gormOperator(condition.Op) + " ?", []any{condition.Value}
	}
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
	case Like:
		return "LIKE"
	case NotLike:
		return "NOT LIKE"
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

// gormPlanJSON 返回 Explain 使用的紧凑执行特征 JSON
func gormPlanJSON(plan Plan) string {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// gormGroupExpression 构建分组 SELECT / GROUP BY 表达式
func gormGroupExpression(db *gorm.DB, group Group) string {
	if group.Interval == "" {
		return quoteGormIdentifier(db, group.Field)
	}
	return gormDateGroupExpression(db, group)
}

// gormDateGroupExpression 构建时间桶分组表达式
func gormDateGroupExpression(db *gorm.DB, group Group) string {
	field := quoteGormIdentifier(db, group.Field)
	dialect := strings.ToLower(db.Dialector.Name())
	switch dialect {
	case "mysql":
		return gormMySQLDateGroupExpression(field, group.Interval)
	case "sqlite", "sqlite3":
		return gormSQLiteDateGroupExpression(field, group.Interval)
	case "sqlserver":
		return gormSQLServerDateGroupExpression(field, group.Interval)
	default:
		if group.TimeZone != "" {
			field += " AT TIME ZONE " + quoteSQLString(group.TimeZone)
		}
		return "DATE_TRUNC(" + quoteSQLString(string(group.Interval)) + ", " + field + ")"
	}
}

func gormMySQLDateGroupExpression(field string, interval TimeInterval) string {
	switch interval {
	case TimeIntervalMinute:
		return "DATE_FORMAT(" + field + ", '%Y-%m-%d %H:%i:00')"
	case TimeIntervalHour:
		return "DATE_FORMAT(" + field + ", '%Y-%m-%d %H:00:00')"
	case TimeIntervalDay:
		return "DATE_FORMAT(" + field + ", '%Y-%m-%d 00:00:00')"
	case TimeIntervalWeek:
		return "STR_TO_DATE(CONCAT(YEARWEEK(" + field + ", 3), ' Monday'), '%X%V %W')"
	case TimeIntervalMonth:
		return "DATE_FORMAT(" + field + ", '%Y-%m-01 00:00:00')"
	case TimeIntervalQuarter:
		return "MAKEDATE(YEAR(" + field + "), 1) + INTERVAL (QUARTER(" + field + ") - 1) QUARTER"
	case TimeIntervalYear:
		return "DATE_FORMAT(" + field + ", '%Y-01-01 00:00:00')"
	default:
		return field
	}
}

func gormSQLiteDateGroupExpression(field string, interval TimeInterval) string {
	switch interval {
	case TimeIntervalMinute:
		return "strftime('%Y-%m-%d %H:%M:00', " + field + ")"
	case TimeIntervalHour:
		return "strftime('%Y-%m-%d %H:00:00', " + field + ")"
	case TimeIntervalDay:
		return "strftime('%Y-%m-%d 00:00:00', " + field + ")"
	case TimeIntervalWeek:
		return "strftime('%Y-W%W', " + field + ")"
	case TimeIntervalMonth:
		return "strftime('%Y-%m-01 00:00:00', " + field + ")"
	case TimeIntervalQuarter:
		return "strftime('%Y', " + field + ") || '-Q' || ((cast(strftime('%m', " + field + ") as integer) + 2) / 3)"
	case TimeIntervalYear:
		return "strftime('%Y-01-01 00:00:00', " + field + ")"
	default:
		return field
	}
}

func gormSQLServerDateGroupExpression(field string, interval TimeInterval) string {
	part := string(interval)
	if interval == TimeIntervalQuarter {
		part = "quarter"
	}
	return "DATEADD(" + part + ", DATEDIFF(" + part + ", 0, " + field + "), 0)"
}

func quoteSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// validateGormDateGroups 校验当前方言是否支持已配置的时间桶表达能力
func validateGormDateGroups(db *gorm.DB, spec Spec) error {
	if db == nil || db.Dialector == nil {
		return nil
	}
	if gormDialectSupportsDateGroupTimeZone(db.Dialector.Name()) {
		return nil
	}
	for i, group := range spec.Groups {
		if group.Interval != "" && group.TimeZone != "" {
			return fmt.Errorf(
				"%w: group %d time zone is not supported by gorm dialect %q",
				ErrInvalidSpec,
				i,
				db.Dialector.Name(),
			)
		}
	}
	return nil
}

func gormDialectSupportsDateGroupTimeZone(dialect string) bool {
	switch strings.ToLower(dialect) {
	case "mysql", "sqlite", "sqlite3", "sqlserver":
		return false
	default:
		return true
	}
}
