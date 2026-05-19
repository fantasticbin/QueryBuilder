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
	batchSize := 2
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
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
			}, []any{uint32(2)}, 0, false, nil
		case 2:
			// 第二次查询，cursorValues 应为 [2]（上一批最后一条的 ID）
			if len(cursorValues) != 1 || cursorValues[0] != uint32(2) {
				t.Errorf("第二次查询 cursorValues 应为 [2], got %v", cursorValues)
			}
			return []*CursorTestEntity{
				{ID: 3, Name: "Charlie", CreatedAt: 300},
			}, []any{uint32(3)}, 0, false, nil // 返回 1 条 < batchSize，终止
		default:
			t.Error("不应有第三次调用")
			return nil, nil, 0, false, nil
		}
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

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
	batchSize := 10
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		return []*CursorTestEntity{}, nil, 0, false, nil
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

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
	batchSize := 10
	expectedErr := errors.New("database connection failed")
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		return nil, nil, 0, false, expectedErr
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

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
	batchSize := 10
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		callCount++
		// 返回满批数据
		items := make([]*CursorTestEntity, batchSize)
		for i := range items {
			items[i] = &CursorTestEntity{ID: uint32(callCount*batchSize + i + 1), Name: "test"}
		}
		lastID := items[len(items)-1].ID
		return items, []any{lastID}, 0, false, nil
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

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

	batchSize := 2
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		callCount++
		return []*CursorTestEntity{
			{ID: uint32(callCount*2 - 1), Name: "test1"},
			{ID: uint32(callCount * 2), Name: "test2"},
		}, []any{uint32(callCount * 2)}, 0, false, nil
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

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

// TestBuildCursorIterator_NoCursorFields 测试未设置游标字段时，buildCursorIterator 正常返回空结果
// 注意：cursorFields 的校验已统一在 executeCursorWithMiddlewares/executePageWithMiddlewares 入口处完成
// buildCursorIterator 不再负责校验，调用方需确保 cursorFields 非空
func TestBuildCursorIterator_NoCursorFields(t *testing.T) {
	ctx := context.Background()
	batchSize := 10
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		// cursorFields 为空时，fetchBatch 仍会被调用（返回空数据即终止）
		return nil, nil, 0, false, nil
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

	var count int
	for _, err := range seq {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			break
		}
		count++
	}

	if count != 0 {
		t.Errorf("expected 0 items, got %d", count)
	}
}

// TestBuildCursorIterator_MultiFieldCursor 测试多字段游标
func TestBuildCursorIterator_MultiFieldCursor(t *testing.T) {
	ctx := context.Background()
	batchSize := 2
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		callCount++
		switch callCount {
		case 1:
			if cursorValues != nil {
				t.Errorf("首次查询 cursorValues 应为 nil, got %v", cursorValues)
			}
			return []*CursorTestEntity{
				{ID: 1, Name: "Alice", CreatedAt: 100},
				{ID: 2, Name: "Bob", CreatedAt: 100},
			}, []any{int64(100), uint32(2)}, 0, false, nil
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
			}, []any{int64(200), uint32(3)}, 0, false, nil
		default:
			return nil, nil, 0, false, nil
		}
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

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
	batchSize := 2
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		callCount++
		switch callCount {
		case 1:
			// 返回自定义的 nextCursorValues（模拟 ES sort values）
			return []*CursorTestEntity{
				{ID: 1, Name: "Alice"},
				{ID: 2, Name: "Bob"},
			}, []any{float64(1.5), "doc_2"}, 0, false, nil
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
			}, nil, 0, false, nil
		default:
			return nil, nil, 0, false, nil
		}
	}

	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

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

// TestListQueryCursor_WithMockQuerier 测试 List.QueryCursor 使用 MockQuerier
func TestListQueryCursor_WithMockQuerier(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
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
	batchSize := 2
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
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
			}, []any{uint32(102)}, 0, false, nil
		case 2:
			// 第二次查询，cursorValues 应为上一批最后一条的 ID [102]
			if len(cursorValues) != 1 || cursorValues[0] != uint32(102) {
				t.Errorf("第二次查询 cursorValues 应为 [102], got %v", cursorValues)
			}
			return []*CursorTestEntity{
				{ID: 103, Name: "Charlie"},
			}, []any{uint32(103)}, 0, false, nil
		default:
			return nil, nil, 0, false, nil
		}
	}

	initialValues := []any{uint32(100)}
	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, initialValues, false, fetchBatch, nil)

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
	batchSize := 2
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
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
			}, nil, 0, false, nil
		default:
			return nil, nil, 0, false, nil
		}
	}

	initialValues := []any{int64(500), uint32(10)}
	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, initialValues, false, fetchBatch, nil)

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

	// 设置预期调用：SetStart、SetLimit、SetCursorField、SetCursorValue 都应被调用
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
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
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
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

// TestBuildCursorIterator_NeedTotalViaPointer 测试 buildCursorIterator 通过 totalPtr 接收首批次总数
func TestBuildCursorIterator_NeedTotalViaPointer(t *testing.T) {
	ctx := context.Background()
	batchSize := 2
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		callCount++
		switch callCount {
		case 1:
			// 首批次：isFirstBatch 应为 true，返回 total=100
			if !isFirstBatch {
				t.Error("首批次 isFirstBatch 应为 true")
			}
			return []*CursorTestEntity{
				{ID: 1, Name: "Alice"},
				{ID: 2, Name: "Bob"},
			}, []any{uint32(2)}, 100, false, nil
		case 2:
			// 第二批次：isFirstBatch 应为 false，total 返回 0（不再查 Count）
			if isFirstBatch {
				t.Error("第二批次 isFirstBatch 应为 false")
			}
			return []*CursorTestEntity{
				{ID: 3, Name: "Charlie"},
			}, []any{uint32(3)}, 0, false, nil
		default:
			return nil, nil, 0, false, nil
		}
	}

	var total int64
	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, &total)

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
	// 验证 totalPtr 接收到了首批次返回的总数
	if total != 100 {
		t.Errorf("expected total=100, got %d", total)
	}
	if callCount != 2 {
		t.Errorf("expected 2 batch calls, got %d", callCount)
	}
}

// TestBuildCursorIterator_NeedTotalNilPointer 测试 totalPtr 为 nil 时不会 panic
func TestBuildCursorIterator_NeedTotalNilPointer(t *testing.T) {
	ctx := context.Background()
	batchSize := 10
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		// 即使返回了 total，totalPtr 为 nil 也不应 panic
		return []*CursorTestEntity{
			{ID: 1, Name: "Alice"},
		}, nil, 50, false, nil
	}

	// totalPtr 传 nil，不应 panic
	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, nil)

	count := 0
	for _, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
	}

	if count != 1 {
		t.Errorf("expected 1 result, got %d", count)
	}
}

// TestBuildCursorIterator_SingleBatchMode 测试 singleBatch=true（needPagination=true）只获取单批次
func TestBuildCursorIterator_SingleBatchMode(t *testing.T) {
	ctx := context.Background()
	batchSize := 3
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		callCount++
		// 返回满批数据（3 条 == batchSize）
		return []*CursorTestEntity{
			{ID: 1, Name: "Alice"},
			{ID: 2, Name: "Bob"},
			{ID: 3, Name: "Charlie"},
		}, []any{uint32(3)}, 50, false, nil
	}

	var total int64
	// singleBatch=true，即使返回满批数据也应在第一批后终止
	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, true, fetchBatch, &total)

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
	// singleBatch 模式下只应调用一次 fetchBatch
	if callCount != 1 {
		t.Errorf("expected 1 batch call in singleBatch mode, got %d", callCount)
	}
	// 验证 total 仍然被正确收集
	if total != 50 {
		t.Errorf("expected total=50, got %d", total)
	}
}

// TestBuildCursorIterator_SingleBatchWithNeedTotal 测试 singleBatch + needTotal 组合
// 模拟 needPagination=true 场景下只取一页数据但仍需要总数
func TestBuildCursorIterator_SingleBatchWithNeedTotal(t *testing.T) {
	ctx := context.Background()
	batchSize := 2
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		if !isFirstBatch {
			t.Error("singleBatch 模式下不应有第二次调用")
		}
		return []*CursorTestEntity{
			{ID: 1, Name: "Alice", CreatedAt: 100},
			{ID: 2, Name: "Bob", CreatedAt: 200},
		}, []any{int64(200), uint32(2)}, 999, false, nil
	}

	var total int64
	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, true, fetchBatch, &total)

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
	if total != 999 {
		t.Errorf("expected total=999, got %d", total)
	}
}

// TestBuildCursorIterator_IsFirstBatchFlag 测试 isFirstBatch 标记在多批次中的正确传递
func TestBuildCursorIterator_IsFirstBatchFlag(t *testing.T) {
	ctx := context.Background()
	batchSize := 1
	var firstBatchFlags []bool
	callCount := 0
	fetchBatch := func(ctx context.Context, cursorValues []any, isFirstBatch bool) ([]*CursorTestEntity, []any, int64, bool, error) {
		callCount++
		firstBatchFlags = append(firstBatchFlags, isFirstBatch)
		if callCount <= 3 {
			return []*CursorTestEntity{
				{ID: uint32(callCount), Name: "test"},
			}, []any{uint32(callCount)}, 42, false, nil
		}
		return nil, nil, 0, false, nil // 第四次返回空，终止
	}

	var total int64
	seq := buildCursorIterator[CursorTestEntity](ctx, batchSize, nil, false, fetchBatch, &total)

	for _, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// 验证 isFirstBatch 标记：第一次为 true，后续为 false
	if len(firstBatchFlags) != 4 {
		t.Fatalf("expected 4 batch calls, got %d", len(firstBatchFlags))
	}
	if !firstBatchFlags[0] {
		t.Error("expected firstBatchFlags[0]=true")
	}
	for i := 1; i < len(firstBatchFlags); i++ {
		if firstBatchFlags[i] {
			t.Errorf("expected firstBatchFlags[%d]=false, got true", i)
		}
	}
	// 只有首批次的 total 被收集
	if total != 42 {
		t.Errorf("expected total=42 (from first batch), got %d", total)
	}
}

// TestListQueryCursor_WithNeedTotalTrue 测试 List.QueryCursor 传递 needTotal=true
func TestListQueryCursor_WithNeedTotalTrue(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用：验证 SetNeedTotal(true) 被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)

	expectedItems := []*CursorTestEntity{
		{ID: 1, Name: "Alice"},
	}
	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](func(yield func(*CursorTestEntity, error) bool) {
		for _, item := range expectedItems {
			if !yield(item, nil) {
				return
			}
		}
	}))

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	seq := list.QueryCursor(ctx,
		WithCursorField("ID"),
		WithNeedTotal(true),
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

// TestListQueryCursor_WithNeedTotalFalse 测试 List.QueryCursor 传递 needTotal=false
func TestListQueryCursor_WithNeedTotalFalse(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用：验证 SetNeedTotal(false) 被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(false).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)

	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](func(yield func(*CursorTestEntity, error) bool) {
		yield(&CursorTestEntity{ID: 1, Name: "Alice"}, nil)
	}))

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	seq := list.QueryCursor(ctx,
		WithCursorField("ID"),
		WithNeedTotal(false),
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

// TestListQueryCursor_WithNeedPaginationTrue 测试 List.QueryCursor 传递 needPagination=true（单批次模式）
func TestListQueryCursor_WithNeedPaginationTrue(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用：验证 SetNeedPagination(true) 被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)

	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](func(yield func(*CursorTestEntity, error) bool) {
		yield(&CursorTestEntity{ID: 1, Name: "Alice"}, nil)
		yield(&CursorTestEntity{ID: 2, Name: "Bob"}, nil)
	}))

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	seq := list.QueryCursor(ctx,
		WithCursorField("ID"),
		WithNeedPagination(true),
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

// TestListQueryCursor_WithNeedPaginationFalse 测试 List.QueryCursor 传递 needPagination=false（全量遍历模式）
func TestListQueryCursor_WithNeedPaginationFalse(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用：验证 SetNeedPagination(false) 被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(false).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)

	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](func(yield func(*CursorTestEntity, error) bool) {
		for i := uint32(1); i <= 5; i++ {
			if !yield(&CursorTestEntity{ID: i, Name: "test"}, nil) {
				return
			}
		}
	}))

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	seq := list.QueryCursor(ctx,
		WithCursorField("ID"),
		WithNeedPagination(false),
		WithLimit(10),
	)

	var results []*CursorTestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}
}

// TestListQueryCursor_WithNeedPaginationAndNeedTotal 测试 List.QueryCursor 同时传递 needPagination=true 和 needTotal=true
func TestListQueryCursor_WithNeedPaginationAndNeedTotal(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	// 设置预期调用：验证两个选项都被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("created_at", "id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(int64(100), uint32(5)).Return(mockQuerier)

	mockQuerier.EXPECT().QueryCursor(ctx).Return(iter.Seq2[*CursorTestEntity, error](func(yield func(*CursorTestEntity, error) bool) {
		yield(&CursorTestEntity{ID: 6, Name: "Alice", CreatedAt: 200}, nil)
		yield(&CursorTestEntity{ID: 7, Name: "Bob", CreatedAt: 300}, nil)
	}))

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	seq := list.QueryCursor(ctx,
		WithCursorField("created_at", "id"),
		WithCursorValue(int64(100), uint32(5)),
		WithNeedPagination(true),
		WithNeedTotal(true),
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

// ==================== QueryPage 测试 ====================

// TestListQueryPage_Basic 测试 List.QueryPage 基本功能
func TestListQueryPage_Basic(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	expectedResult := &CursorPageResult[CursorTestEntity]{
		Items: []*CursorTestEntity{
			{ID: 1, Name: "Alice", CreatedAt: 100},
			{ID: 2, Name: "Bob", CreatedAt: 200},
		},
		Total:            50,
		HasMore:          true,
		NextCursorValues: []any{uint32(2)},
	}

	// 设置 Mock 期望
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(10)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	result, err := list.QueryPage(ctx,
		WithCursorField("ID"),
		WithLimit(10),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(result.Items))
	}
	if result.Total != 50 {
		t.Errorf("expected total=50, got %d", result.Total)
	}
	if !result.HasMore {
		t.Error("expected HasMore=true")
	}
	if len(result.NextCursorValues) != 1 || result.NextCursorValues[0] != uint32(2) {
		t.Errorf("expected NextCursorValues=[2], got %v", result.NextCursorValues)
	}
}

// TestListQueryPage_NoMore 测试 List.QueryPage 无更多数据的场景
func TestListQueryPage_NoMore(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	expectedResult := &CursorPageResult[CursorTestEntity]{
		Items:            []*CursorTestEntity{{ID: 1, Name: "Alice"}},
		Total:            1,
		HasMore:          false,
		NextCursorValues: nil,
	}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	result, err := list.QueryPage(ctx, WithCursorField("ID"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasMore {
		t.Error("expected HasMore=false")
	}
	if result.NextCursorValues != nil {
		t.Errorf("expected NextCursorValues=nil, got %v", result.NextCursorValues)
	}
}

// TestListQueryPage_WithCursorValue 测试 List.QueryPage 传递游标初始值
func TestListQueryPage_WithCursorValue(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	expectedResult := &CursorPageResult[CursorTestEntity]{
		Items:            []*CursorTestEntity{{ID: 11, Name: "Alice", CreatedAt: 600}},
		Total:            0,
		HasMore:          false,
		NextCursorValues: nil,
	}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(10)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("created_at", "id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(int64(500), uint32(10)).Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	result, err := list.QueryPage(ctx,
		WithCursorField("created_at", "id"),
		WithCursorValue(int64(500), uint32(10)),
		WithLimit(10),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(result.Items))
	}
}

// TestListQueryPage_Error 测试 List.QueryPage 错误处理
func TestListQueryPage_Error(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(nil, ErrCursorFieldNotSet)

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)

	result, err := list.QueryPage(ctx, WithCursorField("ID"))

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}

// TestListQueryPage_WithMiddleware 测试 List.QueryPage 中间件传递
func TestListQueryPage_WithMiddleware(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	expectedResult := &CursorPageResult[CursorTestEntity]{
		Items:   []*CursorTestEntity{{ID: 1, Name: "Alice"}},
		HasMore: false,
	}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)
	mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	logMiddleware := func(ctx context.Context, builder Querier[CursorTestEntity], next func(context.Context) ([]*CursorTestEntity, int64, error)) ([]*CursorTestEntity, int64, error) {
		return next(ctx)
	}

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)
	list.Use(logMiddleware)

	result, err := list.QueryPage(ctx, WithCursorField("ID"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(result.Items))
	}
}

// TestListQueryPage_WithHooks 测试 List.QueryPage 钩子传递
func TestListQueryPage_WithHooks(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[CursorTestEntity](ctrl)

	expectedResult := &CursorPageResult[CursorTestEntity]{
		Items:   []*CursorTestEntity{{ID: 1, Name: "Alice"}},
		HasMore: false,
	}

	beforeHook := func(ctx context.Context) context.Context { return ctx }
	afterHook := func(ctx context.Context, list []*CursorTestEntity, total int64, err error) {}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("ID").Return(mockQuerier)
	mockQuerier.EXPECT().SetBeforeQueryHook(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetAfterQueryHook(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	list := NewList[CursorTestEntity]()
	list.SetQuerier(mockQuerier)
	list.SetBeforeQueryHook(beforeHook)
	list.SetAfterQueryHook(afterHook)

	result, err := list.QueryPage(ctx, WithCursorField("ID"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(result.Items))
	}
}

// TestListQueryPage_PanicRecovery 测试 List.QueryPage panic 恢复
func TestListQueryPage_PanicRecovery(t *testing.T) {
	ctx := context.Background()

	list := NewList[CursorTestEntity]()
	list.SetDataSource(DataSource(99)) // 无效数据源，触发 panic

	result, err := list.QueryPage(ctx, WithCursorField("ID"), WithData(&DBProxy{}))

	if err == nil {
		t.Fatal("expected error from panic recovery, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}
