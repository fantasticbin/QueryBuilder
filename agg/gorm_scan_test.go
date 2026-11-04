package agg

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"gorm.io/gorm"
)

// fakeRowsDriver 是返回固定结果集的最小 database/sql 驱动，用于验证 GORM 结果解码
type fakeRowsDriver struct {
	columns []string
	rows    [][]driver.Value
}

var fakeRowsDrivers sync.Map

func (d *fakeRowsDriver) Open(string) (driver.Conn, error) { return fakeRowsConn{d}, nil }

type fakeRowsConn struct{ d *fakeRowsDriver }

func (c fakeRowsConn) Prepare(string) (driver.Stmt, error) { return fakeRowsStmt(c), nil }
func (fakeRowsConn) Close() error                          { return nil }
func (fakeRowsConn) Begin() (driver.Tx, error)             { return nil, driver.ErrSkip }

type fakeRowsStmt struct{ d *fakeRowsDriver }

func (fakeRowsStmt) Close() error                               { return nil }
func (fakeRowsStmt) NumInput() int                              { return -1 }
func (fakeRowsStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (s fakeRowsStmt) Query([]driver.Value) (driver.Rows, error) {
	return &fakeRows{columns: s.d.columns, rows: s.d.rows}, nil
}

type fakeRows struct {
	columns []string
	rows    [][]driver.Value
	next    int
}

func (r *fakeRows) Columns() []string { return r.columns }
func (*fakeRows) Close() error        { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++
	return nil
}

func openFakeRowsGorm(t *testing.T, columns []string, rows [][]driver.Value) *core.DBProxy {
	t.Helper()
	name := "agg-fake-rows-" + strings.ReplaceAll(t.Name(), "/", "-")
	if _, loaded := fakeRowsDrivers.LoadOrStore(name, true); !loaded {
		sql.Register(name, &fakeRowsDriver{columns: columns, rows: rows})
	}
	sqlDB, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("opening fake sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(aggregateTestDialector{}, &gorm.Config{ConnPool: sqlDB, DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	return core.NewDBProxyWithAdapters(core.NewGormAdapter(db))
}

func TestGormBuilderQueryDecodesGroupedMapRows(t *testing.T) {
	data := openFakeRowsGorm(t, []string{"region", "total"}, [][]driver.Value{
		{"east", int64(3)},
		{"west", int64(1)},
	})
	builder := NewGormBuilder[gormOrder, map[string]any](data)
	builder.GroupBy("region", "region").Count("total").SetNeedTotal(false)

	result, err := builder.Query(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	want := []map[string]any{{"region": "east", "total": int64(3)}, {"region": "west", "total": int64(1)}}
	for i, row := range result.Rows {
		if !reflect.DeepEqual(*row, want[i]) {
			t.Fatalf("row %d = %#v, want %#v", i, *row, want[i])
		}
	}
}

func TestGormBuilderQueryDecodesScalarMapRow(t *testing.T) {
	type namedMap map[string]any
	data := openFakeRowsGorm(t, []string{"total", "amount_sum"}, [][]driver.Value{{int64(4), nil}})
	builder := NewGormBuilder[gormOrder, namedMap](data)
	builder.Count("total").Sum("amount", "amount_sum")

	result, err := builder.Query(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	row := *result.Rows[0]
	if row["total"] != int64(4) {
		t.Fatalf("unexpected total: %#v", row)
	}
	if value, ok := row["amount_sum"]; !ok || value != nil {
		t.Fatalf("expected nil amount_sum key, got %#v", row)
	}
}

func TestGormBuilderQueryScalarNoRowsReturnsInitializedMap(t *testing.T) {
	data := openFakeRowsGorm(t, []string{"total"}, nil)
	builder := NewGormBuilder[gormOrder, map[string]any](data)
	builder.Count("total")

	result, err := builder.Query(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Rows) != 1 || *result.Rows[0] == nil {
		t.Fatalf("expected one initialized map row, got %#v", result.Rows)
	}
	(*result.Rows[0])["extra"] = true
}

func TestGormBuilderQueryDecodesStructRowsUnchanged(t *testing.T) {
	data := openFakeRowsGorm(t, []string{"region", "total", "amount_sum"}, [][]driver.Value{{"east", int64(2), 9.5}})
	builder := NewGormBuilder[gormOrder, gormSummary](data)
	builder.GroupBy("region", "region").Count("total").Sum("amount", "amount_sum").SetNeedTotal(false)

	result, err := builder.Query(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := *result.Rows[0]; got != (gormSummary{Region: "east", Total: 2, Amount: 9.5}) {
		t.Fatalf("unexpected row: %#v", got)
	}
}

func TestGormBuilderOrderAppendsGroupKeyTieBreaker(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	builder := NewGormBuilder[gormOrder, gormSummary](core.NewDBProxyWithAdapters(core.NewGormAdapter(db)))
	builder.GroupBy("region", "region").GroupBy("channel", "channel").Count("total").OrderByDesc("total")

	explanation, err := builder.Explain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(explanation, `ORDER BY "total" DESC,"region","channel"`) {
		t.Fatalf("expected group-key tie-breaker in ORDER BY, got %s", explanation)
	}
}

func TestGormBuilderMySQLDateGroupExpressions(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(aggregateTestDialector{name: "mysql"}, &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("opening test gorm db: %v", err)
	}
	data := core.NewDBProxyWithAdapters(core.NewGormAdapter(db))
	for _, interval := range []TimeInterval{TimeIntervalMinute, TimeIntervalHour, TimeIntervalDay, TimeIntervalWeek, TimeIntervalMonth, TimeIntervalQuarter, TimeIntervalYear} {
		builder := NewGormBuilder[gormOrder, gormSummary](data)
		builder.GroupByDateWithTimeZone("created_at", "bucket", interval, "Asia/Shanghai").Count("total")
		explanation, err := builder.Explain(context.Background())
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", interval, err)
		}
		if !strings.Contains(explanation, `SELECT CAST(`) || !strings.Contains(explanation, ` AS DATETIME) AS "bucket"`) {
			t.Fatalf("%s: expected DATETIME bucket, got %s", interval, explanation)
		}
		if strings.Contains(explanation, "@@session.time_zone") || !strings.Contains(explanation, `CONVERT_TZ("created_at", '+00:00', 'Asia/Shanghai')`) {
			t.Fatalf("%s: expected fixed UTC source zone, got %s", interval, explanation)
		}
		if interval == TimeIntervalWeek && !strings.Contains(explanation, `YEARWEEK(CONVERT_TZ("created_at", '+00:00', 'Asia/Shanghai'), 3), ' Monday'), '%x%v %W')`) {
			t.Fatalf("expected ISO week expression, got %s", explanation)
		}
	}
}
