package builder

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	qbtest "github.com/fantasticbin/QueryBuilder/v2/test"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

const gormTestDriverName = "querybuilder_gorm_test"

var (
	gormTestDriverOnce sync.Once
	gormTestDSNSeq     atomic.Uint64
	gormTestStates     sync.Map
)

type GormTestEntity struct {
	ID        uint32 `gorm:"column:id"`
	Name      string `gorm:"column:name"`
	CreatedAt int64  `gorm:"column:created_at"`
	Status    string `gorm:"column:status"`
}

func (GormTestEntity) TableName() string {
	return "gorm_test_entities"
}

type gormRecordedQuery struct {
	SQL  string
	Args []any
}

type gormTestState struct {
	mu      sync.Mutex
	rows    []*GormTestEntity
	total   int64
	queries []gormRecordedQuery
}

func (s *gormTestState) record(query string, args []driver.NamedValue) {
	s.mu.Lock()
	defer s.mu.Unlock()

	recordedArgs := make([]any, len(args))
	for i, arg := range args {
		recordedArgs[i] = arg.Value
	}
	s.queries = append(s.queries, gormRecordedQuery{
		SQL:  normalizeSQL(query),
		Args: recordedArgs,
	})
}

func (s *gormTestState) snapshotQueries() []gormRecordedQuery {
	s.mu.Lock()
	defer s.mu.Unlock()

	queries := make([]gormRecordedQuery, len(s.queries))
	copy(queries, s.queries)
	return queries
}

func (s *gormTestState) rowsFor(query string) driver.Rows {
	if strings.Contains(strings.ToLower(query), "count(") {
		return &gormTestRows{
			columns: []string{"count"},
			values:  [][]driver.Value{{s.total}},
		}
	}

	values := make([][]driver.Value, 0, len(s.rows))
	for _, row := range s.rows {
		values = append(values, []driver.Value{int64(row.ID), row.Name, row.CreatedAt, row.Status})
	}
	return &gormTestRows{
		columns: []string{"id", "name", "created_at", "status"},
		values:  values,
	}
}

type gormTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *gormTestRows) Columns() []string {
	return r.columns
}

func (r *gormTestRows) Close() error {
	return nil
}

func (r *gormTestRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

type gormTestDriver struct{}

func (gormTestDriver) Open(name string) (driver.Conn, error) {
	state, ok := gormTestStates.Load(name)
	if !ok {
		return nil, fmt.Errorf("gorm test state %q not found", name)
	}
	return &gormTestConn{state: state.(*gormTestState)}, nil
}

type gormTestConn struct {
	state *gormTestState
}

func (c *gormTestConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported in gorm tests")
}

func (c *gormTestConn) Close() error {
	return nil
}

func (c *gormTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported in gorm tests")
}

func (c *gormTestConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.state.record(query, args)
	return c.state.rowsFor(query), nil
}

func (c *gormTestConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.state.record(query, args)
	return driver.RowsAffected(0), nil
}

type gormTestDialector struct {
	conn gorm.ConnPool
}

func (d gormTestDialector) Name() string {
	return "querybuilder-gorm-test"
}

func (d gormTestDialector) Initialize(db *gorm.DB) error {
	db.ConnPool = d.conn
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{})
	return nil
}

func (d gormTestDialector) Migrator(db *gorm.DB) gorm.Migrator {
	return nil
}

func (d gormTestDialector) DataTypeOf(field *schema.Field) string {
	return ""
}

func (d gormTestDialector) DefaultValueOf(field *schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

func (d gormTestDialector) BindVarTo(writer clause.Writer, stmt *gorm.Statement, v interface{}) {
	writer.WriteByte('?')
}

func (d gormTestDialector) QuoteTo(writer clause.Writer, value string) {
	writer.WriteString(value)
}

func (d gormTestDialector) Explain(sql string, vars ...interface{}) string {
	return sql
}

func newGormTestDB(t *testing.T, rows []*GormTestEntity, total int64) (*gorm.DB, *gormTestState) {
	t.Helper()

	gormTestDriverOnce.Do(func() {
		sql.Register(gormTestDriverName, gormTestDriver{})
	})

	dsn := fmt.Sprintf("%s_%d", t.Name(), gormTestDSNSeq.Add(1))
	state := &gormTestState{rows: rows, total: total}
	gormTestStates.Store(dsn, state)
	t.Cleanup(func() {
		gormTestStates.Delete(dsn)
	})

	sqlDB, err := sql.Open(gormTestDriverName, dsn)
	qbtest.AssertNoError(t, err)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	db, err := gorm.Open(gormTestDialector{conn: sqlDB}, &gorm.Config{DisableAutomaticPing: true})
	qbtest.AssertNoError(t, err)
	return db, state
}

func normalizeSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

func assertAnyRecordedSQL(t *testing.T, state *gormTestState, match func(gormRecordedQuery) bool) gormRecordedQuery {
	t.Helper()

	for _, query := range state.snapshotQueries() {
		if match(query) {
			return query
		}
	}
	t.Fatalf("no recorded SQL matched; queries: %+v", state.snapshotQueries())
	return gormRecordedQuery{}
}

func assertSQLContains(t *testing.T, query gormRecordedQuery, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(query.SQL, fragment) {
			t.Fatalf("expected SQL %q to contain %q", query.SQL, fragment)
		}
	}
}

func assertArgsEqual(t *testing.T, got []any, want ...any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected args %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected args %v, got %v", want, got)
		}
	}
}

func TestGormBuilderDefaultsAndChainableSetters(t *testing.T) {
	db, _ := newGormTestDB(t, nil, 0)
	builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))

	if builder.builder.dataSource != Gorm {
		t.Fatalf("expected Gorm data source, got %v", builder.builder.dataSource)
	}
	if builder.builder.limit != defaultLimit {
		t.Fatalf("expected default limit %d, got %d", defaultLimit, builder.builder.limit)
	}
	if !builder.builder.needPagination {
		t.Fatal("expected default needPagination=true")
	}
	if !builder.builder.needTotal {
		t.Fatal("expected default needTotal=true")
	}

	filter := func(db *gorm.DB) *gorm.DB { return db.Where("status = ?", "active") }
	sort := func(db *gorm.DB) *gorm.DB { return db.Order("created_at DESC") }
	if got := builder.SetFilter(filter); got != builder {
		t.Fatal("SetFilter should return the same GormBuilder")
	}
	if got := builder.SetSort(sort); got != builder {
		t.Fatal("SetSort should return the same GormBuilder")
	}
	if got := builder.SetStart(5); got != builder {
		t.Fatal("SetStart should return the same builder through Querier")
	}

	builder.
		SetLimit(20).
		SetNeedTotal(true).
		SetTotalLimit(100).
		SetNeedPagination(false).
		SetFields("id", "name").
		SetCursorField("-created_at", "id").
		SetCursorValue(int64(100), uint32(10))

	meta := builder.GetQueryMeta()
	if meta.DataSource != Gorm || meta.Start != 5 || meta.Limit != 20 || !meta.NeedTotal || meta.TotalLimit != 100 || meta.NeedPagination {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	meta.Fields[0] = "mutated"
	meta.CursorFields[0] = "mutated"
	meta.CursorValues[0] = int64(999)
	freshMeta := builder.GetQueryMeta()
	if freshMeta.Fields[0] != "id" || freshMeta.CursorFields[0] != "-created_at" || freshMeta.CursorValues[0] != int64(100) {
		t.Fatalf("GetQueryMeta should return defensive copies, got %+v", freshMeta)
	}
}

func TestGormBuilderQueryListBuildsExpectedSQL(t *testing.T) {
	ctx := context.Background()
	rows := []*GormTestEntity{
		{ID: 1, Name: "Alice", CreatedAt: 100, Status: "active"},
		{ID: 2, Name: "Bob", CreatedAt: 200, Status: "active"},
	}
	db, state := newGormTestDB(t, rows, 7)
	builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))

	var middlewareCalled bool
	builder.
		SetFilter(func(db *gorm.DB) *gorm.DB {
			return db.Where("status = ?", "active")
		}).
		SetSort(func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at DESC")
		})
	builder.
		SetStart(5).
		SetLimit(2).
		SetNeedTotal(true).
		SetNeedPagination(true).
		SetFields("id", "name").
		Use(func(ctx context.Context, querier Querier[GormTestEntity], next func(context.Context) (core.Result[GormTestEntity], error)) (core.Result[GormTestEntity], error) {
			middlewareCalled = true
			meta := querier.GetQueryMeta()
			if meta.DataSource != Gorm || meta.Start != 5 || meta.Limit != 2 || !meta.NeedTotal {
				t.Fatalf("unexpected middleware meta: %+v", meta)
			}
			return next(ctx)
		})

	result, err := builder.QueryList(ctx)
	qbtest.AssertNoError(t, err)
	qbtest.AssertListResult(t, result, rows, 7)
	if !middlewareCalled {
		t.Fatal("expected middleware to be called")
	}

	listQuery := assertAnyRecordedSQL(t, state, func(query gormRecordedQuery) bool {
		return strings.Contains(query.SQL, "FROM gorm_test_entities") && !strings.Contains(strings.ToLower(query.SQL), "count(")
	})
	assertSQLContains(t, listQuery, "SELECT id,name", "WHERE status = ?", "ORDER BY created_at DESC", "LIMIT ? OFFSET ?")
	assertArgsEqual(t, listQuery.Args, "active", int64(2), int64(5))

	countQuery := assertAnyRecordedSQL(t, state, func(query gormRecordedQuery) bool {
		return strings.Contains(strings.ToLower(query.SQL), "count(")
	})
	assertSQLContains(t, countQuery, "SELECT count(*) FROM gorm_test_entities", "WHERE status = ?")
	assertArgsEqual(t, countQuery.Args, "active")
}

func TestGormBuilderQueryListSkipsTotalWhenDisabled(t *testing.T) {
	rows := []*GormTestEntity{{ID: 1, Name: "Alice", CreatedAt: 100, Status: "active"}}
	db, state := newGormTestDB(t, rows, 99)
	builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))
	builder.SetNeedTotal(false)

	result, err := builder.QueryList(context.Background())
	qbtest.AssertNoError(t, err)
	qbtest.AssertListResult(t, result, rows, 0)

	for _, query := range state.snapshotQueries() {
		if strings.Contains(strings.ToLower(query.SQL), "count(") {
			t.Fatalf("did not expect count query when NeedTotal=false, got %+v", query)
		}
	}
}

func TestGormBuilderExplainListAndCursor(t *testing.T) {
	db, _ := newGormTestDB(t, nil, 0)
	builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))
	builder.
		SetFilter(func(db *gorm.DB) *gorm.DB {
			return db.Where("status = ?", "active")
		}).
		SetSort(func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at DESC")
		})
	builder.SetFields("id", "name").SetStart(10).SetLimit(5).SetNeedPagination(true)

	sql, err := builder.Explain(context.Background())
	qbtest.AssertNoError(t, err)
	normalized := normalizeSQL(sql)
	for _, fragment := range []string{
		"SELECT id,name",
		"FROM gorm_test_entities",
		"WHERE status = ?",
		"ORDER BY created_at DESC",
		"LIMIT ? OFFSET ?",
		"args: [active, 5, 10]",
	} {
		if !strings.Contains(normalized, fragment) {
			t.Fatalf("expected Explain SQL %q to contain %q", normalized, fragment)
		}
	}

	builder.SetCursorField("-created_at", "id").SetLimit(25)
	cursorSQL, err := builder.Explain(context.Background())
	qbtest.AssertNoError(t, err)
	normalizedCursorSQL := normalizeSQL(cursorSQL)
	for _, fragment := range []string{
		"[CursorQuery] SELECT id,name",
		"ORDER BY created_at DESC,id ASC,created_at DESC",
		"LIMIT ?",
		"cursor_fields: [-created_at, id]",
	} {
		if !strings.Contains(normalizedCursorSQL, fragment) {
			t.Fatalf("expected cursor Explain SQL %q to contain %q", normalizedCursorSQL, fragment)
		}
	}
}

func TestGormBuilderQueryPageDefaultCursorField(t *testing.T) {
	rows := []*GormTestEntity{
		{ID: 1, Name: "Alice", CreatedAt: 100, Status: "active"},
		{ID: 2, Name: "Bob", CreatedAt: 200, Status: "active"},
	}
	db, state := newGormTestDB(t, rows, 0)
	builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))
	builder.SetLimit(1).SetNeedTotal(false)

	result, err := builder.QueryPage(context.Background())
	qbtest.AssertNoError(t, err)
	qbtest.AssertCursorPageResult(t, result, rows[:1], 1, true, []any{uint32(1)})

	meta := builder.GetQueryMeta()
	if meta.IsCursorQuery {
		t.Fatal("QueryPage should restore IsCursorQuery after execution")
	}
	if len(meta.CursorFields) != 0 {
		t.Fatalf("auto-injected cursor fields should be cleared after QueryPage, got %+v", meta.CursorFields)
	}

	query := assertAnyRecordedSQL(t, state, func(query gormRecordedQuery) bool {
		return strings.Contains(query.SQL, "ORDER BY id ASC")
	})
	assertSQLContains(t, query, "LIMIT ?")
	assertArgsEqual(t, query.Args, int64(2))
}

func TestGormBuilderQueryPageCursorConditions(t *testing.T) {
	tests := []struct {
		name       string
		fields     []string
		values     []any
		wantSQL    string
		wantArgs   []any
		resultRows []*GormTestEntity
	}{
		{
			name:       "single desc cursor",
			fields:     []string{"-created_at"},
			values:     []any{int64(200)},
			wantSQL:    "WHERE created_at < ?",
			wantArgs:   []any{int64(200), int64(2)},
			resultRows: []*GormTestEntity{{ID: 1, Name: "Alice", CreatedAt: 100}},
		},
		{
			name:       "uniform multi field cursor uses row value comparison",
			fields:     []string{"created_at", "id"},
			values:     []any{int64(100), uint32(1)},
			wantSQL:    "WHERE (created_at, id) > (?,?)",
			wantArgs:   []any{int64(100), int64(1), int64(2)},
			resultRows: []*GormTestEntity{{ID: 2, Name: "Bob", CreatedAt: 200}},
		},
		{
			name:       "mixed direction cursor uses lexicographic or condition",
			fields:     []string{"-created_at", "id"},
			values:     []any{int64(200), uint32(1)},
			wantSQL:    "WHERE (created_at < ?) OR (created_at = ? AND id > ?)",
			wantArgs:   []any{int64(200), int64(200), int64(1), int64(2)},
			resultRows: []*GormTestEntity{{ID: 2, Name: "Bob", CreatedAt: 100}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, state := newGormTestDB(t, tt.resultRows, 0)
			builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))
			builder.SetCursorField(tt.fields...).SetCursorValue(tt.values...).SetLimit(1).SetNeedTotal(false)

			result, err := builder.QueryPage(context.Background())
			qbtest.AssertNoError(t, err)
			qbtest.AssertCursorPageResult(t, result, tt.resultRows, int64(len(tt.resultRows)), false, nil)

			query := assertAnyRecordedSQL(t, state, func(query gormRecordedQuery) bool {
				return strings.Contains(query.SQL, tt.wantSQL)
			})
			assertArgsEqual(t, query.Args, tt.wantArgs...)
		})
	}
}

func TestGormBuilderQueryPageRejectsInvalidCursorField(t *testing.T) {
	db, _ := newGormTestDB(t, nil, 0)
	builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))
	builder.SetCursorField("id; DROP TABLE users").SetLimit(1)

	_, err := builder.QueryPage(context.Background())
	if err == nil {
		t.Fatal("expected invalid cursor field error, got nil")
	}
	if !strings.Contains(err.Error(), "cursor field") {
		t.Fatalf("expected cursor field error, got %v", err)
	}
}

func TestGormBuilderQueryPageProjectionIncludesCursorFields(t *testing.T) {
	rows := []*GormTestEntity{{ID: 1, Name: "Alice", CreatedAt: 100, Status: "active"}}
	db, state := newGormTestDB(t, rows, 0)
	builder := NewGormBuilder[GormTestEntity](NewDBProxy(db, nil, nil))
	builder.SetFields("name").SetCursorField("id").SetLimit(1).SetNeedTotal(false)

	result, err := builder.QueryPage(context.Background())
	qbtest.AssertNoError(t, err)
	qbtest.AssertCursorPageResult(t, result, rows, 1, false, nil)

	query := assertAnyRecordedSQL(t, state, func(query gormRecordedQuery) bool {
		return strings.Contains(query.SQL, "FROM gorm_test_entities") && !strings.Contains(strings.ToLower(query.SQL), "count(")
	})
	assertSQLContains(t, query, "SELECT name,id")
}
