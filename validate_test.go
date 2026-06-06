package builder

import (
	"context"
	"errors"
	"testing"

	"github.com/fantasticbin/QueryBuilder/core"
	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"
)

// ValidateTestEntity 校验测试用实体
type ValidateTestEntity struct {
	ID   uint32
	Name string
}

// --- limit 校验测试 ---

func TestValidateData_LimitExceeded_Gorm(t *testing.T) {
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetLimit(5001)

	_, err := g.QueryList(context.Background())
	if err == nil {
		t.Fatal("expected ErrLimitExceeded, got nil")
	}
	if !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("expected ErrLimitExceeded, got: %v", err)
	}
}

func TestValidateData_LimitExceeded_Mongo(t *testing.T) {
	m := NewMongoBuilder[ValidateTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	m.SetLimit(5001)

	_, err := m.QueryList(context.Background())
	if err == nil {
		t.Fatal("expected ErrLimitExceeded, got nil")
	}
	if !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("expected ErrLimitExceeded, got: %v", err)
	}
}

func TestValidateData_LimitExceeded_ES(t *testing.T) {
	e := NewElasticSearchBuilder[ValidateTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "test_index")
	e.SetLimit(5001)

	_, err := e.QueryList(context.Background())
	if err == nil {
		t.Fatal("expected ErrLimitExceeded, got nil")
	}
	if !errors.Is(err, ErrLimitExceeded) {
		t.Errorf("expected ErrLimitExceeded, got: %v", err)
	}
}

func TestValidateData_LimitValid_Gorm(t *testing.T) {
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

	// 使用中间件短路，避免真实数据库查询
	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	// 测试 limit = 1（最小合法值）
	g.SetLimit(1)
	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Errorf("limit=1 should pass validation, got: %v", err)
	}

	// 测试 limit = defaultLimit（默认值）
	g2 := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g2.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})
	_, err = g2.QueryList(context.Background())
	if err != nil {
		t.Errorf("default limit should pass validation, got: %v", err)
	}

	// 测试 limit = 5000（最大合法值）
	g3 := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g3.SetLimit(5000)
	g3.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})
	_, err = g3.QueryList(context.Background())
	if err != nil {
		t.Errorf("limit=5000 should pass validation, got: %v", err)
	}
}

func TestValidateData_LimitValid_Mongo(t *testing.T) {
	m := NewMongoBuilder[ValidateTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	m.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	// 默认 limit 应通过校验
	_, err := m.QueryList(context.Background())
	if err != nil {
		t.Errorf("default limit should pass validation, got: %v", err)
	}

	// limit = 5000 应通过校验
	m2 := NewMongoBuilder[ValidateTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	m2.SetLimit(5000)
	m2.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})
	_, err = m2.QueryList(context.Background())
	if err != nil {
		t.Errorf("limit=5000 should pass validation, got: %v", err)
	}
}

func TestValidateData_LimitValid_ES(t *testing.T) {
	e := NewElasticSearchBuilder[ValidateTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "test_index")
	e.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	// 默认 limit 应通过校验
	_, err := e.QueryList(context.Background())
	if err != nil {
		t.Errorf("default limit should pass validation, got: %v", err)
	}

	// limit = 5000 应通过校验
	e2 := NewElasticSearchBuilder[ValidateTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "test_index")
	e2.SetLimit(5000)
	e2.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})
	_, err = e2.QueryList(context.Background())
	if err != nil {
		t.Errorf("limit=5000 should pass validation, got: %v", err)
	}
}

// --- cursorValues 与 cursorFields 长度校验测试 ---

func TestValidateData_CursorMismatch_Gorm(t *testing.T) {
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetCursorField("id", "name")
	g.SetCursorValue("only_one_value")

	seq := g.QueryCursor(context.Background())
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected ErrCursorMismatch, got nil")
		}
		if !errors.Is(err, ErrCursorMismatch) {
			t.Errorf("expected ErrCursorMismatch, got: %v", err)
		}
		break
	}
}

func TestValidateData_CursorMismatch_Mongo(t *testing.T) {
	m := NewMongoBuilder[ValidateTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	m.SetCursorField("id", "name")
	m.SetCursorValue("only_one_value")

	seq := m.QueryCursor(context.Background())
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected ErrCursorMismatch, got nil")
		}
		if !errors.Is(err, ErrCursorMismatch) {
			t.Errorf("expected ErrCursorMismatch, got: %v", err)
		}
		break
	}
}

func TestValidateData_CursorMismatch_ES(t *testing.T) {
	e := NewElasticSearchBuilder[ValidateTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "test_index")
	e.SetCursorField("id", "name")
	e.SetCursorValue("only_one_value")

	seq := e.QueryCursor(context.Background())
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected ErrCursorMismatch, got nil")
		}
		if !errors.Is(err, ErrCursorMismatch) {
			t.Errorf("expected ErrCursorMismatch, got: %v", err)
		}
		break
	}
}

func TestValidateData_CursorValuesEmpty_NoError(t *testing.T) {
	// cursorValues 为空时不触发校验
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetCursorField("id", "name")
	// 不设置 cursorValues

	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Errorf("cursorValues empty should not trigger validation, got: %v", err)
	}
}

func TestValidateData_CursorFieldsEmpty_NoError(t *testing.T) {
	// cursorFields 为空时不触发校验
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetCursorValue("value1", "value2")
	// 不设置 cursorFields

	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Errorf("cursorFields empty should not trigger validation, got: %v", err)
	}
}

func TestValidateData_CursorLengthMatch_NoError(t *testing.T) {
	// cursorValues 与 cursorFields 长度一致时不触发校验
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetCursorField("id", "name")
	g.SetCursorValue(uint32(1), "Alice")

	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Errorf("cursor length match should not trigger validation, got: %v", err)
	}
}

// --- fields 自动清洗测试 ---

func TestSanitizeFields_FilterEmpty_Gorm(t *testing.T) {
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetFields("id", "", "name", "", "age")

	// 使用中间件短路并检查 fields
	var capturedFields []string
	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if gb, ok := builder.(*GormBuilder[ValidateTestEntity]); ok {
			capturedFields = gb.builder.fields
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"id", "name", "age"}
	if len(capturedFields) != len(expected) {
		t.Fatalf("expected fields %v, got %v", expected, capturedFields)
	}
	for i, f := range capturedFields {
		if f != expected[i] {
			t.Errorf("expected fields[%d]=%q, got %q", i, expected[i], f)
		}
	}
}

func TestSanitizeFields_FilterEmpty_Mongo(t *testing.T) {
	m := NewMongoBuilder[ValidateTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	m.SetFields("id", "", "name", "")

	var capturedFields []string
	m.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if mb, ok := builder.(*MongoBuilder[ValidateTestEntity]); ok {
			capturedFields = mb.builder.fields
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := m.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"id", "name"}
	if len(capturedFields) != len(expected) {
		t.Fatalf("expected fields %v, got %v", expected, capturedFields)
	}
	for i, f := range capturedFields {
		if f != expected[i] {
			t.Errorf("expected fields[%d]=%q, got %q", i, expected[i], f)
		}
	}
}

func TestSanitizeFields_FilterEmpty_ES(t *testing.T) {
	e := NewElasticSearchBuilder[ValidateTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "test_index")
	e.SetFields("", "id", "", "name")

	var capturedFields []string
	e.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if eb, ok := builder.(*ElasticSearchBuilder[ValidateTestEntity]); ok {
			capturedFields = eb.builder.fields
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := e.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"id", "name"}
	if len(capturedFields) != len(expected) {
		t.Fatalf("expected fields %v, got %v", expected, capturedFields)
	}
	for i, f := range capturedFields {
		if f != expected[i] {
			t.Errorf("expected fields[%d]=%q, got %q", i, expected[i], f)
		}
	}
}

func TestSanitizeFields_Deduplicate_Gorm(t *testing.T) {
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetFields("id", "name", "id", "age", "name")

	var capturedFields []string
	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if gb, ok := builder.(*GormBuilder[ValidateTestEntity]); ok {
			capturedFields = gb.builder.fields
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"id", "name", "age"}
	if len(capturedFields) != len(expected) {
		t.Fatalf("expected fields %v, got %v", expected, capturedFields)
	}
	for i, f := range capturedFields {
		if f != expected[i] {
			t.Errorf("expected fields[%d]=%q, got %q", i, expected[i], f)
		}
	}
}

func TestSanitizeFields_Deduplicate_Mongo(t *testing.T) {
	m := NewMongoBuilder[ValidateTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	m.SetFields("name", "name", "age", "age")

	var capturedFields []string
	m.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if mb, ok := builder.(*MongoBuilder[ValidateTestEntity]); ok {
			capturedFields = mb.builder.fields
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := m.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"name", "age"}
	if len(capturedFields) != len(expected) {
		t.Fatalf("expected fields %v, got %v", expected, capturedFields)
	}
	for i, f := range capturedFields {
		if f != expected[i] {
			t.Errorf("expected fields[%d]=%q, got %q", i, expected[i], f)
		}
	}
}

func TestSanitizeFields_Deduplicate_ES(t *testing.T) {
	e := NewElasticSearchBuilder[ValidateTestEntity](NewDBProxy(nil, nil, &elastic.Client{}), "test_index")
	e.SetFields("id", "id", "id")

	var capturedFields []string
	e.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if eb, ok := builder.(*ElasticSearchBuilder[ValidateTestEntity]); ok {
			capturedFields = eb.builder.fields
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := e.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"id"}
	if len(capturedFields) != len(expected) {
		t.Fatalf("expected fields %v, got %v", expected, capturedFields)
	}
	for i, f := range capturedFields {
		if f != expected[i] {
			t.Errorf("expected fields[%d]=%q, got %q", i, expected[i], f)
		}
	}
}

func TestSanitizeFields_AllEmpty_BecomesNil(t *testing.T) {
	// 清洗后为空切片时视为未设置（fields 为 nil）
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetFields("", "", "")

	var capturedFields []string
	var fieldsIsNil bool
	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if gb, ok := builder.(*GormBuilder[ValidateTestEntity]); ok {
			capturedFields = gb.builder.fields
			fieldsIsNil = gb.builder.fields == nil
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !fieldsIsNil {
		t.Errorf("expected fields to be nil after sanitization, got %v", capturedFields)
	}
}

func TestSanitizeFields_NormalFields_Unchanged(t *testing.T) {
	// 正常 fields 不受影响
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	g.SetFields("id", "name", "age")

	var capturedFields []string
	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if gb, ok := builder.(*GormBuilder[ValidateTestEntity]); ok {
			capturedFields = gb.builder.fields
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"id", "name", "age"}
	if len(capturedFields) != len(expected) {
		t.Fatalf("expected fields %v, got %v", expected, capturedFields)
	}
	for i, f := range capturedFields {
		if f != expected[i] {
			t.Errorf("expected fields[%d]=%q, got %q", i, expected[i], f)
		}
	}
}

func TestSanitizeFields_NilFields_NoAction(t *testing.T) {
	// fields 为 nil 时不执行清洗逻辑
	g := NewGormBuilder[ValidateTestEntity](NewDBProxy(&gorm.DB{}, nil, nil))
	// 不设置 fields

	var fieldsIsNil bool
	g.Use(func(ctx context.Context, builder Querier[ValidateTestEntity], next func(context.Context) (core.Result[ValidateTestEntity], error)) (core.Result[ValidateTestEntity], error) {
		if gb, ok := builder.(*GormBuilder[ValidateTestEntity]); ok {
			fieldsIsNil = gb.builder.fields == nil
		}
		return &core.ListResult[ValidateTestEntity]{Items: []*ValidateTestEntity{}, Total: 0}, nil
	})

	_, err := g.QueryList(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !fieldsIsNil {
		t.Error("expected fields to remain nil when not set")
	}
}

// --- QueryCursor 路径的 limit 校验测试 ---

func TestValidateData_LimitExceeded_QueryCursor_Mongo(t *testing.T) {
	m := NewMongoBuilder[ValidateTestEntity](NewDBProxy(nil, &mongo.Collection{}, nil))
	m.SetLimit(6000)
	m.SetCursorField("id")

	seq := m.QueryCursor(context.Background())
	for _, err := range seq {
		if err == nil {
			t.Fatal("expected ErrLimitExceeded, got nil")
		}
		if !errors.Is(err, ErrLimitExceeded) {
			t.Errorf("expected ErrLimitExceeded, got: %v", err)
		}
		break
	}
}
