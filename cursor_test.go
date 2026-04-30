package builder

import (
	"context"
	"errors"
	"iter"
	"testing"

	"go.uber.org/mock/gomock"
)

// CursorTestEntity 游标查询测试实体
type CursorTestEntity struct {
	ID        uint32 `json:"id" gorm:"column:id"`
	Name      string `json:"name" gorm:"column:name"`
	CreatedAt int64  `json:"created_at" bson:"created_at" gorm:"column:created_at"`
}

// TestBuildCursorIterator_NormalIteration 测试正常分批遍历
func TestBuildCursorIterator_NormalIteration(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"ID"}
	batchSize := 2

	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		callCount++
		switch callCount {
		case 1:
			// 首次查询，cursorValues 应为 nil
			if cursorValues != nil {
				t.Errorf("首次查询 cursorValues 应为 nil, got %v", cursorValues)
			}
			return []*CursorTestEntity{
				{ID: 1, Name: "Alice", CreatedAt: 100},
				{ID: 2, Name: "Bob", CreatedAt: 200},
			}, nil, nil
		case 2:
			// 第二次查询，cursorValues 应为 [2]（上一批最后一条的 ID）
			if len(cursorValues) != 1 || cursorValues[0] != uint32(2) {
				t.Errorf("第二次查询 cursorValues 应为 [2], got %v", cursorValues)
			}
			return []*CursorTestEntity{
				{ID: 3, Name: "Charlie", CreatedAt: 300},
			}, nil, nil // 返回 1 条 < batchSize，终止
		default:
			t.Error("不应有第三次调用")
			return nil, nil, nil
		}
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	if callCount != 2 {
		t.Errorf("expected 2 batch calls, got %d", callCount)
	}
}

// TestBuildCursorIterator_EmptyFirstBatch 测试首批返回空数据
func TestBuildCursorIterator_EmptyFirstBatch(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"ID"}
	batchSize := 10

	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		return []*CursorTestEntity{}, nil, nil
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	count := 0
	for _, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
	}

	if count != 0 {
		t.Errorf("expected 0 results, got %d", count)
	}
}

// TestBuildCursorIterator_ErrorHandling 测试查询错误处理
func TestBuildCursorIterator_ErrorHandling(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"ID"}
	batchSize := 10

	expectedErr := errors.New("database connection failed")
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		return nil, nil, expectedErr
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	var gotErr error
	for _, err := range seq {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Error("expected error, got nil")
	}
	if !errors.Is(gotErr, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, gotErr)
	}
}

// TestBuildCursorIterator_BreakEarlyTermination 测试 break 提前终止
func TestBuildCursorIterator_BreakEarlyTermination(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"ID"}
	batchSize := 10

	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		callCount++
		// 返回满批数据
		items := make([]*CursorTestEntity, batchSize)
		for i := range items {
			items[i] = &CursorTestEntity{ID: uint32(callCount*batchSize + i + 1), Name: "test"}
		}
		return items, nil, nil
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	count := 0
	for _, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
		if count >= 3 {
			break // 只取 3 条就终止
		}
	}

	if count != 3 {
		t.Errorf("expected 3 results before break, got %d", count)
	}
	// fetchBatch 应该只被调用 1 次（第一批 10 条，取了 3 条就 break）
	if callCount != 1 {
		t.Errorf("expected 1 batch call, got %d", callCount)
	}
}

// TestBuildCursorIterator_ContextCancellation 测试 context 取消
func TestBuildCursorIterator_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cursorFields := []string{"ID"}
	batchSize := 2

	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		callCount++
		return []*CursorTestEntity{
			{ID: uint32(callCount*2 - 1), Name: "test1"},
			{ID: uint32(callCount * 2), Name: "test2"},
		}, nil, nil
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	count := 0
	var gotErr error
	for _, err := range seq {
		if err != nil {
			gotErr = err
			break
		}
		count++
		if count == 2 {
			cancel() // 第一批遍历完后取消 context
		}
	}

	if gotErr == nil {
		t.Error("expected context cancellation error, got nil")
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", gotErr)
	}
}

// TestBuildCursorIterator_NoCursorFields 测试未设置游标字段
func TestBuildCursorIterator_NoCursorFields(t *testing.T) {
	ctx := context.Background()
	var cursorFields []string // 空
	batchSize := 10

	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		t.Error("fetchBatch should not be called")
		return nil, nil, nil
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	var gotErr error
	for _, err := range seq {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Error("expected ErrCursorFieldNotSet, got nil")
	}
	if !errors.Is(gotErr, ErrCursorFieldNotSet) {
		t.Errorf("expected ErrCursorFieldNotSet, got %v", gotErr)
	}
}

// TestBuildCursorIterator_MultiFieldCursor 测试多字段游标
func TestBuildCursorIterator_MultiFieldCursor(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"CreatedAt", "ID"}
	batchSize := 2

	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		callCount++
		switch callCount {
		case 1:
			if cursorValues != nil {
				t.Errorf("首次查询 cursorValues 应为 nil, got %v", cursorValues)
			}
			return []*CursorTestEntity{
				{ID: 1, Name: "Alice", CreatedAt: 100},
				{ID: 2, Name: "Bob", CreatedAt: 100},
			}, nil, nil
		case 2:
			// 应该提取到 [100, 2]（CreatedAt=100, ID=2）
			if len(cursorValues) != 2 {
				t.Errorf("expected 2 cursor values, got %d", len(cursorValues))
			} else {
				if cursorValues[0] != int64(100) {
					t.Errorf("expected cursor[0]=100, got %v", cursorValues[0])
				}
				if cursorValues[1] != uint32(2) {
					t.Errorf("expected cursor[1]=2, got %v", cursorValues[1])
				}
			}
			return []*CursorTestEntity{
				{ID: 3, Name: "Charlie", CreatedAt: 200},
			}, nil, nil
		default:
			return nil, nil, nil
		}
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

// TestBuildCursorIterator_CustomNextCursorValues 测试自定义 nextCursorValues（ES 场景）
func TestBuildCursorIterator_CustomNextCursorValues(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"_doc"}
	batchSize := 2

	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		callCount++
		switch callCount {
		case 1:
			// 返回自定义的 nextCursorValues（模拟 ES sort values）
			return []*CursorTestEntity{
				{ID: 1, Name: "Alice"},
				{ID: 2, Name: "Bob"},
			}, []any{float64(1.5), "doc_2"}, nil
		case 2:
			// 验证传入的是上一批返回的自定义 cursorValues
			if len(cursorValues) != 2 {
				t.Errorf("expected 2 cursor values, got %d", len(cursorValues))
			} else {
				if cursorValues[0] != float64(1.5) {
					t.Errorf("expected cursor[0]=1.5, got %v", cursorValues[0])
				}
				if cursorValues[1] != "doc_2" {
					t.Errorf("expected cursor[1]='doc_2', got %v", cursorValues[1])
				}
			}
			return []*CursorTestEntity{
				{ID: 3, Name: "Charlie"},
			}, nil, nil
		default:
			return nil, nil, nil
		}
	}

seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, nil, fetchBatch)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

// TestExtractCursorValues_FieldNameMatch 测试字段名匹配
func TestExtractCursorValues_FieldNameMatch(t *testing.T) {
	item := &CursorTestEntity{ID: 42, Name: "test", CreatedAt: 1000}

	// 精确字段名匹配
	values, err := extractCursorValues(item, []string{"ID"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 1 || values[0] != uint32(42) {
		t.Errorf("expected [42], got %v", values)
	}

	// 多字段
	values, err = extractCursorValues(item, []string{"CreatedAt", "ID"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 2 || values[0] != int64(1000) || values[1] != uint32(42) {
		t.Errorf("expected [1000, 42], got %v", values)
	}
}

// TestExtractCursorValues_JSONTagMatch 测试 JSON tag 匹配
func TestExtractCursorValues_JSONTagMatch(t *testing.T) {
	item := &CursorTestEntity{ID: 10, CreatedAt: 500}

	values, err := extractCursorValues(item, []string{"created_at"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 1 || values[0] != int64(500) {
		t.Errorf("expected [500], got %v", values)
	}
}

// TestExtractCursorValues_GormColumnMatch 测试 gorm column tag 匹配
func TestExtractCursorValues_GormColumnMatch(t *testing.T) {
	item := &CursorTestEntity{ID: 7, Name: "gorm_test"}

	values, err := extractCursorValues(item, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 1 || values[0] != uint32(7) {
		t.Errorf("expected [7], got %v", values)
	}
}

// TestExtractCursorValues_FieldNotFound 测试字段不存在
func TestExtractCursorValues_FieldNotFound(t *testing.T) {
	item := &CursorTestEntity{ID: 1}

	_, err := extractCursorValues(item, []string{"nonexistent_field"})
	if err == nil {
		t.Error("expected error for nonexistent field, got nil")
	}
}

// TestExtractCursorValues_NilItem 测试 nil 记录
func TestExtractCursorValues_NilItem(t *testing.T) {
	_, err := extractCursorValues[CursorTestEntity](nil, []string{"ID"})
	if err == nil {
		t.Error("expected error for nil item, got nil")
	}
}

// TestListQueryCursor_WithMockQuerier 测试 List.QueryCursor 使用 MockQuerier
func TestListQueryCursor_WithMockQuerier(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)
	mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)

	// 模拟 QueryCursor 返回一个简单的迭代器
	expectedItems := []*CursorTestEntity{
		{ID: 1, Name: "Alice"},
		{ID: 2, Name: "Bob"},
	}
	mockSeq := func(yield func(*CursorTestEntity, error) bool) {
		for _, item := range expectedItems {
			if !yield(item, nil) {
				return
			}
		}
	}
	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](mockSeq))

	// 创建一个简单的日志中间件
	logMiddleware := func(ctx context.Context, builder Querier[CursorTestEntity], next func(context.Context) ([]*CursorTestEntity, int64, error)) ([]*CursorTestEntity, int64, error) {
		return next(ctx)
	}

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)
	list.Use(logMiddleware)

	seq := list.QueryCursor(ctx, WithCursorField("ID"), WithLimit(10))

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// TestBuildCursorIterator_WithInitialCursorValues 测试传入初始游标值（方案B 底层验证）
func TestBuildCursorIterator_WithInitialCursorValues(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"ID"}
	batchSize := 2

	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		callCount++
		switch callCount {
		case 1:
			// 首次查询，cursorValues 应为传入的初始值 [100]
			if len(cursorValues) != 1 {
				t.Errorf("首次查询 cursorValues 长度应为 1, got %d", len(cursorValues))
			} else if cursorValues[0] != uint32(100) {
				t.Errorf("首次查询 cursorValues[0] 应为 100, got %v", cursorValues[0])
			}
			return []*CursorTestEntity{
				{ID: 101, Name: "Alice"},
				{ID: 102, Name: "Bob"},
			}, nil, nil
		case 2:
			// 第二次查询，cursorValues 应为上一批最后一条的 ID [102]
			if len(cursorValues) != 1 || cursorValues[0] != uint32(102) {
				t.Errorf("第二次查询 cursorValues 应为 [102], got %v", cursorValues)
			}
			return []*CursorTestEntity{
				{ID: 103, Name: "Charlie"},
			}, nil, nil
		default:
			return nil, nil, nil
		}
	}

	initialValues := []any{uint32(100)}
	seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, initialValues, fetchBatch)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	if callCount != 2 {
		t.Errorf("expected 2 batch calls, got %d", callCount)
	}
}

// TestBuildCursorIterator_WithMultiFieldInitialValues 测试多字段初始游标值
func TestBuildCursorIterator_WithMultiFieldInitialValues(t *testing.T) {
	ctx := context.Background()
	cursorFields := []string{"CreatedAt", "ID"}
	batchSize := 2

	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any) ([]*CursorTestEntity, []any, error) {
		callCount++
		switch callCount {
		case 1:
			// 首次查询，cursorValues 应为传入的初始值 [500, 10]
			if len(cursorValues) != 2 {
				t.Errorf("首次查询 cursorValues 长度应为 2, got %d", len(cursorValues))
			} else {
				if cursorValues[0] != int64(500) {
					t.Errorf("首次查询 cursorValues[0] 应为 500, got %v", cursorValues[0])
				}
				if cursorValues[1] != uint32(10) {
					t.Errorf("首次查询 cursorValues[1] 应为 10, got %v", cursorValues[1])
				}
			}
			return []*CursorTestEntity{
				{ID: 11, Name: "Alice", CreatedAt: 600},
			}, nil, nil
		default:
			return nil, nil, nil
		}
	}

	initialValues := []any{int64(500), uint32(10)}
	seq := buildCursorIterator[CursorTestEntity](ctx, cursorFields, batchSize, initialValues, fetchBatch)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// TestResolveInitialCursorValues_CursorValuesPriority 测试 cursorValues 优先级高于 start
func TestResolveInitialCursorValues_CursorValuesPriority(t *testing.T) {
	g := NewGormBuilder[CursorTestEntity](nil)
	g.SetCursorField("ID")
	g.SetCursorValue(int64(999), "extra")
	g.SetStart(50)

	result := g.builder.resolveInitialCursorValues()

	if len(result) != 2 {
		t.Fatalf("expected 2 values, got %d", len(result))
	}
	if result[0] != int64(999) {
		t.Errorf("expected result[0]=999, got %v", result[0])
	}
	if result[1] != "extra" {
		t.Errorf("expected result[1]='extra', got %v", result[1])
	}
}

// TestResolveInitialCursorValues_StartFallback 测试仅 start > 0 时回退为 []any{start}（方案A）
func TestResolveInitialCursorValues_StartFallback(t *testing.T) {
	g := NewGormBuilder[CursorTestEntity](nil)
	g.SetCursorField("ID")
	g.SetStart(42)

	result := g.builder.resolveInitialCursorValues()

	if len(result) != 1 {
		t.Fatalf("expected 1 value, got %d", len(result))
	}
	if result[0] != uint32(42) {
		t.Errorf("expected result[0]=42 (uint32), got %v (%T)", result[0], result[0])
	}
}

// TestResolveInitialCursorValues_NeitherSet 测试都未设置时返回 nil
func TestResolveInitialCursorValues_NeitherSet(t *testing.T) {
	g := NewGormBuilder[CursorTestEntity](nil)
	g.SetCursorField("ID")

	result := g.builder.resolveInitialCursorValues()

	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

// TestListQueryCursor_WithCursorValueOption 测试 List.QueryCursor 通过 WithCursorValue 传递初始游标值
func TestListQueryCursor_WithCursorValueOption(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用：SetLimit、SetCursorField、SetCursorValue 都应被调用
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("created_at", "id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(int64(500), uint32(10)).Return(mockQuerier)

	// 模拟 QueryCursor 返回迭代器
	expectedItems := []*CursorTestEntity{
		{ID: 11, Name: "Alice", CreatedAt: 600},
	}
	mockSeq := func(yield func(*CursorTestEntity, error) bool) {
		for _, item := range expectedItems {
			if !yield(item, nil) {
				return
			}
		}
	}
	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](mockSeq))

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	seq := list.QueryCursor(ctx,
		WithCursorField("created_at", "id"),
		WithCursorValue(int64(500), uint32(10)),
		WithLimit(10),
	)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// TestListQueryCursor_WithStartOption 测试 List.QueryCursor 通过 WithStart 传递初始游标值（方案A）
func TestListQueryCursor_WithStartOption(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用：SetLimit、SetCursorField、SetStart 都应被调用
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)
	mockQuerier.EXPECT().SetStart(uint32(100)).Return(mockQuerier)

	// 模拟 QueryCursor 返回迭代器
	expectedItems := []*CursorTestEntity{
		{ID: 101, Name: "Alice"},
		{ID: 102, Name: "Bob"},
	}
	mockSeq := func(yield func(*CursorTestEntity, error) bool) {
		for _, item := range expectedItems {
			if !yield(item, nil) {
				return
			}
		}
	}
	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](mockSeq))

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	seq := list.QueryCursor(ctx,
		WithCursorField("ID"),
		WithStart(100),
		WithLimit(10),
	)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}
