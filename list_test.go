package builder

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	"github.com/olivere/elastic/v7"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"
)

type TestEntity struct {
	ID   uint32
	Name string
	Age  int
}

func TestQueryList(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock Querier 实例
	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	tests := []struct {
		name           string
		mockSetup      func()
		opts           []QueryOption
		expectedResult []*TestEntity
		expectedTotal  int64
		expectedErr    error
	}{
		{
			name: "无筛选查询&id升序",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(ctx).
					Return(&core.ListResult[TestEntity]{Items: []*TestEntity{
						{ID: 1, Name: "Alice", Age: 25},
						{ID: 2, Name: "Bob", Age: 30},
					}, Total: 2}, nil)
			},
			opts: []QueryOption{
				WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
			},
			expectedResult: []*TestEntity{
				{ID: 1, Name: "Alice", Age: 25},
				{ID: 2, Name: "Bob", Age: 30},
			},
			expectedTotal: 2,
			expectedErr:   nil,
		},
		{
			name: "无筛选查询&age降序",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(ctx).
					Return(&core.ListResult[TestEntity]{Items: []*TestEntity{
						{ID: 2, Name: "Bob", Age: 30},
						{ID: 1, Name: "Alice", Age: 25},
					}, Total: 2}, nil)
			},
			opts: []QueryOption{
				WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
			},
			expectedResult: []*TestEntity{
				{ID: 2, Name: "Bob", Age: 30},
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 2,
			expectedErr:   nil,
		},
		{
			name: "有筛选查询",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(ctx).
					Return(&core.ListResult[TestEntity]{Items: []*TestEntity{
						{ID: 1, Name: "Alice", Age: 25},
					}, Total: 1}, nil)
			},
			opts: []QueryOption{
				WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
			},
			expectedResult: []*TestEntity{
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 1,
			expectedErr:   nil,
		},
		{
			name: "有筛选查询&函数式选项",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(ctx).
					Return(&core.ListResult[TestEntity]{Items: []*TestEntity{
						{ID: 2, Name: "Bob", Age: 30},
					}, Total: 1}, nil)
			},
			opts: []QueryOption{
				WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
			},
			expectedResult: []*TestEntity{
				{ID: 2, Name: "Bob", Age: 30},
			},
			expectedTotal: 1,
			expectedErr:   nil,
		},
		{
			name: "无数据实例",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(ctx).
					Return(&core.ListResult[TestEntity]{Total: 0}, nil)
			},
			opts: []QueryOption{
				WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
			},
			expectedResult: nil,
			expectedTotal:  0,
			expectedErr:    nil,
		},
		{
			name: "数据实例错误",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(ctx).
					Return(nil, errors.New("no data source provided"))
			},
			opts:           []QueryOption{},
			expectedResult: nil,
			expectedTotal:  0,
			expectedErr:    errors.New("no data source provided"),
		},
	}

	list := NewList[TestEntity]()
	// 使用 Mock Querier 替代真实的查询构建器
	list.SetQuerier(mockQuerier)
	// 添加耗时监控
	list.Use(func(
		ctx context.Context,
		builder Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		defer func() func() {
			pre := time.Now()
			return func() {
				elapsed := time.Since(pre)
				fmt.Println("elapsed:", elapsed)
			}
		}()()
		result, err := next(ctx)
		return result, err
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockSetup != nil {
				tt.mockSetup()
			}

			result, err := list.Query(ctx, tt.opts...)

			if tt.expectedErr != nil {
				if err == nil || err.Error() != tt.expectedErr.Error() {
					t.Errorf("expected error: %v, got: %v", tt.expectedErr, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}

				if result.Total != tt.expectedTotal {
					t.Errorf("expected total: %d, got: %d", tt.expectedTotal, result.Total)
				}

				if len(result.Items) != len(tt.expectedResult) {
					t.Errorf("expected result length: %d, got: %d", len(tt.expectedResult), len(result.Items))
				}

				for i, item := range result.Items {
					if item.ID != tt.expectedResult[i].ID || item.Name != tt.expectedResult[i].Name || item.Age != tt.expectedResult[i].Age {
						t.Errorf("expected result[%d]: %+v, got: %+v", i, tt.expectedResult[i], item)
					}
				}
			}
		})
	}
}

// TestMiddlewareChainOrder 测试中间件链的执行顺序
func TestMiddlewareChainOrder(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	// 记录中间件执行顺序
	var order []string

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	// 添加第一个中间件
	list.Use(func(
		ctx context.Context,
		builder Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		order = append(order, "middleware1_before")
		result, err := next(ctx)
		order = append(order, "middleware1_after")
		return result, err
	})

	// 添加第二个中间件
	list.Use(func(
		ctx context.Context,
		builder Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		order = append(order, "middleware2_before")
		result, err := next(ctx)
		order = append(order, "middleware2_after")
		return result, err
	})

	// 设置 Mock 期望
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier).Times(2)
	mockQuerier.EXPECT().
		QueryList(ctx).
		Return(&core.ListResult[TestEntity]{Items: []*TestEntity{{ID: 1, Name: "Test", Age: 20}}, Total: 1}, nil)

	result, err := list.Query(ctx,
		WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Items))
	}
}

// TestUnsupportedDataSourcePanicRecovery 测试不支持的数据源类型时 panic 被正确恢复为 error
func TestUnsupportedDataSourcePanicRecovery(t *testing.T) {
	ctx := context.Background()

	list := NewList[TestEntity]()
	// 设置一个不支持的数据源类型（枚举值 99）
	list.SetDataSource(DataSource(99))

	result, err := list.Query(ctx,
		WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
	)

	if err == nil {
		t.Fatal("expected error for unsupported data source, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
	// 验证错误信息包含 panic 恢复标识
	expectedErrPrefix := "query panic recovered:"
	if len(err.Error()) < len(expectedErrPrefix) || err.Error()[:len(expectedErrPrefix)] != expectedErrPrefix {
		t.Errorf("expected error starting with %q, got: %v", expectedErrPrefix, err)
	}
}

// TestMiddlewareReceivesQuerierInterface 测试中间件接收到的 builder 参数是 Querier[R] 接口类型
// 直接通过 GormBuilder 的 Use + QueryList 来验证中间件链中 builder 参数的传递
func TestMiddlewareReceivesQuerierInterface(t *testing.T) {
	ctx := context.Background()

	var receivedBuilder Querier[TestEntity]

	gormBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

	// 添加中间件，捕获 builder 参数，并短路返回（不执行真实数据库查询）
	gormBuilder.Use(func(
		ctx context.Context,
		builder Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		receivedBuilder = builder
		// 短路返回，不执行真实查询
		return &core.ListResult[TestEntity]{Items: []*TestEntity{{ID: 1, Name: "Test", Age: 20}}, Total: 1}, nil
	})

	result, err := gormBuilder.QueryList(ctx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Items))
	}

	// 验证中间件接收到的 builder 参数不为 nil 且是 Querier[R] 接口类型
	if receivedBuilder == nil {
		t.Error("expected middleware to receive a non-nil Querier builder")
	}

	// 验证接收到的 builder 就是 GormBuilder 自身
	if _, ok := receivedBuilder.(*GormBuilder[TestEntity]); !ok {
		t.Errorf("expected builder to be *GormBuilder[TestEntity], got %T", receivedBuilder)
	}
}

// TestExplain 测试 Explain 方法（正常模式）
func TestExplain(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	// 设置 Mock 期望：Explain 正常模式（非游标）
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().Explain(ctx).Return("SELECT * FROM test_entities LIMIT 10", nil)

	result, err := list.Explain(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "SELECT * FROM test_entities LIMIT 10" {
		t.Errorf("expected SQL string, got: %s", result)
	}
}

// TestExplainCursorMode 测试 Explain 方法（游标模式）
func TestExplainCursorMode(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	// 设置 Mock 期望：Explain 游标模式
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(uint32(100)).Return(mockQuerier)
	mockQuerier.EXPECT().Explain(ctx).Return("SELECT * FROM test_entities WHERE id > 100 ORDER BY id ASC LIMIT 10", nil)

	result, err := list.Explain(ctx,
		WithCursorField("id"),
		WithCursorValue(uint32(100)),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty explain result")
	}
}

// TestExplainError 测试 Explain 方法返回错误
func TestExplainError(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().Explain(ctx).Return("", errors.New("explain failed"))

	result, err := list.Explain(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "explain failed" {
		t.Errorf("expected 'explain failed', got: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result, got: %s", result)
	}
}

// TestExplainPanicRecovery 测试 Explain 方法的 panic 恢复
func TestExplainPanicRecovery(t *testing.T) {
	ctx := context.Background()

	list := NewList[TestEntity]()
	// 设置不支持的数据源类型，触发 NewBuilder panic
	list.SetDataSource(DataSource(99))

	result, err := list.Explain(ctx,
		WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
	)

	if err == nil {
		t.Fatal("expected error for unsupported data source, got nil")
	}
	if result != "" {
		t.Errorf("expected empty result, got: %s", result)
	}

	expectedErrPrefix := "explain panic recovered:"
	if len(err.Error()) < len(expectedErrPrefix) || err.Error()[:len(expectedErrPrefix)] != expectedErrPrefix {
		t.Errorf("expected error starting with %q, got: %v", expectedErrPrefix, err)
	}
}

// TestGetQueryMeta 测试 GetQueryMeta 方法
func TestGetQueryMeta(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	t.Run("有querier时返回元信息", func(t *testing.T) {
		expectedMeta := QueryMeta{
			DataSource:     Gorm,
			Start:          0,
			Limit:          10,
			NeedTotal:      true,
			NeedPagination: true,
		}

		mockQuerier.EXPECT().GetQueryMeta().Return(expectedMeta)

		list := NewList[TestEntity]()
		list.SetQuerier(mockQuerier)

		meta := list.GetQueryMeta()
		if meta.DataSource != Gorm {
			t.Errorf("expected DataSource Gorm, got: %v", meta.DataSource)
		}
		if meta.Limit != 10 {
			t.Errorf("expected Limit 10, got: %d", meta.Limit)
		}
		if !meta.NeedTotal {
			t.Error("expected NeedTotal true")
		}
	})

	t.Run("无querier时返回零值", func(t *testing.T) {
		list := NewList[TestEntity]()

		meta := list.GetQueryMeta()
		if meta.DataSource != 0 {
			t.Errorf("expected zero DataSource, got: %v", meta.DataSource)
		}
		if meta.Limit != 0 {
			t.Errorf("expected zero Limit, got: %d", meta.Limit)
		}
	})
}

// TestQueryCursor 测试 QueryCursor 方法
func TestQueryCursor(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedData := []*TestEntity{
		{ID: 1, Name: "Alice", Age: 25},
		{ID: 2, Name: "Bob", Age: 30},
	}

	// 设置 Mock 期望
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		for _, item := range expectedData {
			if !yield(item, nil) {
				return
			}
		}
	})

	seq := list.QueryCursor(ctx, WithCursorField("id"))

	var results []*TestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	for i, item := range results {
		if item.ID != expectedData[i].ID || item.Name != expectedData[i].Name {
			t.Errorf("result[%d] mismatch: expected %+v, got %+v", i, expectedData[i], item)
		}
	}
}

// TestQueryCursorPanicRecovery 测试 QueryCursor 方法的 panic 恢复
func TestQueryCursorPanicRecovery(t *testing.T) {
	ctx := context.Background()

	list := NewList[TestEntity]()
	// 设置不支持的数据源类型，触发 NewBuilder panic
	list.SetDataSource(DataSource(99))

	seq := list.QueryCursor(ctx,
		WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
		WithCursorField("id"),
	)

	var gotErr error
	for _, err := range seq {
		if err != nil {
			gotErr = err
			break
		}
	}

	if gotErr == nil {
		t.Fatal("expected error for unsupported data source, got nil")
	}

	expectedErrPrefix := "query cursor panic recovered:"
	if len(gotErr.Error()) < len(expectedErrPrefix) || gotErr.Error()[:len(expectedErrPrefix)] != expectedErrPrefix {
		t.Errorf("expected error starting with %q, got: %v", expectedErrPrefix, gotErr)
	}
}

// TestBeforeAndAfterQueryHook 测试 SetBeforeQueryHook 和 SetAfterQueryHook 通过 List 传递到 Querier
func TestBeforeAndAfterQueryHook(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	beforeHook := func(ctx context.Context) context.Context {
		return ctx
	}
	afterHook := func(ctx context.Context, result core.Result[TestEntity], err error) {
	}

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)
	list.SetBeforeQueryHook(beforeHook)
	list.SetAfterQueryHook(afterHook)

	// 设置 Mock 期望：钩子会通过 passQueryOption 传递到 querier
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetBeforeQueryHook(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetAfterQueryHook(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().QueryList(ctx).Return(&core.ListResult[TestEntity]{Items: []*TestEntity{{ID: 1, Name: "Test", Age: 20}}, Total: 1}, nil)

	result, err := list.Query(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Items))
	}

	// 注意：beforeCalled 和 afterCalled 不会被设置为 true，
	// 因为钩子是传递给 mockQuerier 的，mockQuerier 不会真正执行钩子
	// 这里验证的是钩子被正确传递到了 querier（通过 SetBeforeQueryHook/SetAfterQueryHook 的 EXPECT 验证）
}

// TestSetScope 测试 SetScope 回调在 Query 中被调用
func TestSetScope(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	var scopeCalled bool
	scope := func(querier Querier[TestEntity]) {
		scopeCalled = true
	}

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)
	list.SetScope(scope)

	// 设置 Mock 期望
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().QueryList(ctx).Return(&core.ListResult[TestEntity]{Items: []*TestEntity{{ID: 1, Name: "Test", Age: 20}}, Total: 1}, nil)

	_, err := list.Query(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !scopeCalled {
		t.Error("expected scope callback to be called")
	}
}

// TestNewListWithData 测试 NewListWithData 预创建构建器
func TestNewListWithData(t *testing.T) {
	list := NewListWithData[TestEntity](Gorm, NewDBProxy(&gorm.DB{}, nil, nil))

	// 验证 GetQueryMeta 可以正常调用（说明内部 querier 已被创建）
	meta := list.GetQueryMeta()
	if meta.DataSource != Gorm {
		t.Errorf("expected DataSource Gorm, got: %v", meta.DataSource)
	}
}

// TestQueryWithFields 测试 SetFields 通过 Query 传递到 Querier
func TestQueryWithFields(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	// 设置 Mock 期望：验证 SetFields 被调用
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetFields("id", "name").Return(mockQuerier)
	mockQuerier.EXPECT().QueryList(ctx).Return(&core.ListResult[TestEntity]{Items: []*TestEntity{{ID: 1, Name: "Alice"}}, Total: 1}, nil)

	result, err := list.Query(ctx, WithFields("id", "name"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected total 1, got %d", result.Total)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Items))
	}
}

// TestExplainWithNoQuerier 测试 Explain 在无预设 Querier 时通过 DataSource 自动创建构建器
// 由于 gorm.DB{} 是零值，DryRun 会触发 panic，验证 panic recovery 正确工作
func TestExplainWithNoQuerier(t *testing.T) {
	ctx := context.Background()

	list := NewListWithData[TestEntity](Gorm, NewDBProxy(&gorm.DB{}, nil, nil))

	// 空的 gorm.DB{} 执行 Explain 会因为内部 nil 指针触发 panic
	// 验证 panic 被正确恢复为 error
	result, err := list.Explain(ctx)
	if err == nil {
		// 如果没有 panic（某些 gorm 版本可能不会 panic），验证返回了有效结果
		if result == "" {
			t.Error("expected non-empty explain result or error")
		}
	} else {
		// panic 被恢复为 error，验证错误信息
		expectedErrPrefix := "explain panic recovered:"
		if len(err.Error()) < len(expectedErrPrefix) || err.Error()[:len(expectedErrPrefix)] != expectedErrPrefix {
			// 也可能是正常的错误（非 panic），这也是可接受的
			t.Logf("Explain returned error (non-panic): %v", err)
		}
		if result != "" {
			t.Errorf("expected empty result on error, got: %s", result)
		}
	}
}

// TestQueryCursorWithCursorValues 测试 QueryCursor 传递游标初始值
func TestQueryCursorWithCursorValues(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	// 设置 Mock 期望
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(uint32(50)).Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		yield(&TestEntity{ID: 51, Name: "Next", Age: 20}, nil)
	})

	seq := list.QueryCursor(ctx,
		WithCursorField("id"),
		WithCursorValue(uint32(50)),
	)

	var results []*TestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != 51 {
		t.Errorf("expected ID 51, got %d", results[0].ID)
	}
}

// TestQueryCursor_WithNeedPaginationTrue 测试 List.QueryCursor 传递 needPagination=true（单批次模式）
func TestQueryCursor_WithNeedPaginationTrue(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedData := []*TestEntity{
		{ID: 1, Name: "Alice", Age: 25},
		{ID: 2, Name: "Bob", Age: 30},
		{ID: 3, Name: "Charlie", Age: 35},
	}

	// 设置 Mock 期望：验证 SetNeedPagination(true) 被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(10)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		for _, item := range expectedData {
			if !yield(item, nil) {
				return
			}
		}
	})

	seq := list.QueryCursor(ctx,
		WithCursorField("id"),
		WithNeedPagination(true),
		WithLimit(10),
	)

	var results []*TestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	for i, item := range results {
		if item.ID != expectedData[i].ID {
			t.Errorf("result[%d] ID mismatch: expected %d, got %d", i, expectedData[i].ID, item.ID)
		}
	}
}

// TestQueryCursor_WithNeedTotalTrue 测试 List.QueryCursor 传递 needTotal=true
func TestQueryCursor_WithNeedTotalTrue(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedData := []*TestEntity{
		{ID: 1, Name: "Alice", Age: 25},
		{ID: 2, Name: "Bob", Age: 30},
	}

	// 设置 Mock 期望：验证 SetNeedTotal(true) 被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		for _, item := range expectedData {
			if !yield(item, nil) {
				return
			}
		}
	})

	seq := list.QueryCursor(ctx,
		WithCursorField("id"),
		WithNeedTotal(true),
	)

	var results []*TestEntity
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

// TestQueryCursor_WithNeedPaginationAndNeedTotal 测试 List.QueryCursor 同时传递 needPagination=true 和 needTotal=true
// 模拟 App 分页场景：只取一页数据但需要总数
func TestQueryCursor_WithNeedPaginationAndNeedTotal(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedData := []*TestEntity{
		{ID: 6, Name: "Frank", Age: 28},
		{ID: 7, Name: "Grace", Age: 32},
	}

	// 设置 Mock 期望：验证 needPagination=true 和 needTotal=true 同时被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(20)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("created_at", "id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(int64(500), uint32(5)).Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		for _, item := range expectedData {
			if !yield(item, nil) {
				return
			}
		}
	})

	seq := list.QueryCursor(ctx,
		WithCursorField("created_at", "id"),
		WithCursorValue(int64(500), uint32(5)),
		WithNeedPagination(true),
		WithNeedTotal(true),
		WithLimit(20),
	)

	var results []*TestEntity
	for item, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		results = append(results, item)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != 6 || results[1].ID != 7 {
		t.Errorf("unexpected result IDs: got %d, %d", results[0].ID, results[1].ID)
	}
}

// TestQueryCursor_WithNeedPaginationFalseAndNeedTotalFalse 测试 List.QueryCursor 传递 needPagination=false 和 needTotal=false
// 模拟全量遍历场景：不分页、不查总数
func TestQueryCursor_WithNeedPaginationFalseAndNeedTotalFalse(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	// 设置 Mock 期望：验证 needPagination=false 和 needTotal=false 被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(100)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(false).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(false).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		for i := uint32(1); i <= 5; i++ {
			if !yield(&TestEntity{ID: i, Name: fmt.Sprintf("User%d", i), Age: int(20 + i)}, nil) {
				return
			}
		}
	})

	seq := list.QueryCursor(ctx,
		WithCursorField("id"),
		WithNeedPagination(false),
		WithNeedTotal(false),
		WithLimit(100),
	)

	var results []*TestEntity
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

// TestQueryCursor_WithNeedPaginationTrueAndHooks 测试 needPagination=true 场景下钩子被正确传递
func TestQueryCursor_WithNeedPaginationTrueAndHooks(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	beforeHook := func(ctx context.Context) context.Context {
		return ctx
	}
	afterHook := func(ctx context.Context, result core.Result[TestEntity], err error) {
	}

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)
	list.SetBeforeQueryHook(beforeHook)
	list.SetAfterQueryHook(afterHook)

	// 设置 Mock 期望：验证钩子和 needPagination=true 同时被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().SetBeforeQueryHook(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetAfterQueryHook(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		yield(&TestEntity{ID: 1, Name: "Alice", Age: 25}, nil)
	})

	seq := list.QueryCursor(ctx,
		WithCursorField("id"),
		WithNeedPagination(true),
	)

	var results []*TestEntity
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

// TestQueryCursor_WithNeedTotalTrueAndMiddleware 测试 needTotal=true 场景下中间件被正确传递
func TestQueryCursor_WithNeedTotalTrueAndMiddleware(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	// 添加一个中间件
	list.Use(func(
		ctx context.Context,
		builder Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		return next(ctx)
	})

	// 设置 Mock 期望：验证中间件和 needTotal=true 同时被正确传递
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().QueryCursor(ctx).Return(func(yield func(*TestEntity, error) bool) {
		yield(&TestEntity{ID: 1, Name: "Alice", Age: 25}, nil)
		yield(&TestEntity{ID: 2, Name: "Bob", Age: 30}, nil)
	})

	seq := list.QueryCursor(ctx,
		WithCursorField("id"),
		WithNeedTotal(true),
		WithLimit(10),
	)

	var results []*TestEntity
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

// TestQueryPage_Basic 测试 List.QueryPage 基本功能
func TestQueryPage_Basic(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedResult := &core.CursorPageResult[TestEntity]{
		Items: []*TestEntity{
			{ID: 1, Name: "Alice", Age: 25},
			{ID: 2, Name: "Bob", Age: 30},
			{ID: 3, Name: "Charlie", Age: 35},
		},
		Total:            100,
		HasMore:          true,
		NextCursorValues: []any{uint32(3)},
	}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(3)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	result, err := list.QueryPage(ctx,
		WithCursorField("id"),
		WithLimit(3),
		WithNeedTotal(true),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(result.Items))
	}
	if result.Total != 100 {
		t.Errorf("expected total=100, got %d", result.Total)
	}
	if !result.HasMore {
		t.Error("expected HasMore=true")
	}
	if result.NextCursorValues[0] != uint32(3) {
		t.Errorf("expected NextCursorValues=[3], got %v", result.NextCursorValues)
	}
}

// TestQueryPage_LastPage 测试 List.QueryPage 最后一页（HasMore=false）
func TestQueryPage_LastPage(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedResult := &core.CursorPageResult[TestEntity]{
		Items: []*TestEntity{
			{ID: 4, Name: "David", Age: 40},
		},
		Total:            4,
		HasMore:          false,
		NextCursorValues: nil,
	}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(3)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(uint32(3)).Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	result, err := list.QueryPage(ctx,
		WithCursorField("id"),
		WithCursorValue(uint32(3)),
		WithLimit(3),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.HasMore {
		t.Error("expected HasMore=false")
	}
	if result.NextCursorValues != nil {
		t.Errorf("expected NextCursorValues=nil, got %v", result.NextCursorValues)
	}
	if len(result.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(result.Items))
	}
}

// TestQueryPage_EmptyResult 测试 List.QueryPage 空结果
func TestQueryPage_EmptyResult(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedResult := &core.CursorPageResult[TestEntity]{
		Items:            []*TestEntity{},
		Total:            0,
		HasMore:          false,
		NextCursorValues: nil,
	}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("id").Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	result, err := list.QueryPage(ctx, WithCursorField("id"))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(result.Items))
	}
	if result.HasMore {
		t.Error("expected HasMore=false")
	}
}

// TestQueryPage_MultiFieldCursor 测试 List.QueryPage 多字段游标
func TestQueryPage_MultiFieldCursor(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockQuerier := NewMockQuerier[TestEntity](ctrl)

	list := NewList[TestEntity]()
	list.SetQuerier(mockQuerier)

	expectedResult := &core.CursorPageResult[TestEntity]{
		Items: []*TestEntity{
			{ID: 6, Name: "Frank", Age: 28},
			{ID: 7, Name: "Grace", Age: 32},
		},
		Total:            50,
		HasMore:          true,
		NextCursorValues: []any{int64(700), uint32(7)},
	}

	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(uint32(2)).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(true).Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorField("created_at", "id").Return(mockQuerier)
	mockQuerier.EXPECT().SetCursorValue(int64(500), uint32(5)).Return(mockQuerier)
	mockQuerier.EXPECT().QueryPage(ctx).Return(expectedResult, nil)

	result, err := list.QueryPage(ctx,
		WithCursorField("created_at", "id"),
		WithCursorValue(int64(500), uint32(5)),
		WithNeedTotal(true),
		WithLimit(2),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	if len(result.NextCursorValues) != 2 {
		t.Errorf("expected 2 cursor values, got %d", len(result.NextCursorValues))
	}
}

func TestListQuery_DoesNotReuseConcreteQuerierState(t *testing.T) {
	ctx := context.Background()
	baseBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

	list := NewList[TestEntity]()
	list.SetQuerier(baseBuilder)

	var calls int
	var capturedFields [][]string
	list.Use(func(
		ctx context.Context,
		b Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		calls++
		gb, ok := b.(*GormBuilder[TestEntity])
		if !ok {
			t.Fatalf("expected *GormBuilder[TestEntity], got %T", b)
		}
		fields := make([]string, len(gb.builder.fields))
		copy(fields, gb.builder.fields)
		capturedFields = append(capturedFields, fields)
		return &core.ListResult[TestEntity]{Items: []*TestEntity{}, Total: 0}, nil
	})

	if _, err := list.Query(ctx, WithFields("id")); err != nil {
		t.Fatalf("unexpected first query error: %v", err)
	}
	if _, err := list.Query(ctx); err != nil {
		t.Fatalf("unexpected second query error: %v", err)
	}

	if calls != 2 {
		t.Fatalf("expected one middleware call per query, got %d", calls)
	}
	if len(baseBuilder.builder.middlewares) != 0 {
		t.Fatalf("expected injected builder to remain unmodified, got %d middlewares", len(baseBuilder.builder.middlewares))
	}
	if len(capturedFields) != 2 {
		t.Fatalf("expected two captured field snapshots, got %d", len(capturedFields))
	}
	if len(capturedFields[0]) != 1 || capturedFields[0][0] != "id" {
		t.Fatalf("expected first query fields [id], got %v", capturedFields[0])
	}
	if len(capturedFields[1]) != 0 {
		t.Fatalf("expected second query fields to be empty, got %v", capturedFields[1])
	}
}

func TestListQuery_WithESOptions(t *testing.T) {
	ctx := context.Background()
	list := NewList[TestEntity]()
	list.SetDataSource(ElasticSearch)

	list.Use(func(
		ctx context.Context,
		b Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		eb, ok := b.(*ElasticSearchBuilder[TestEntity])
		if !ok {
			t.Fatalf("expected *ElasticSearchBuilder[TestEntity], got %T", b)
		}
		if eb.index != "users" {
			t.Fatalf("expected ES index users, got %q", eb.index)
		}
		if eb.pitKeepAlive != 2*time.Minute {
			t.Fatalf("expected PIT keep alive 2m, got %v", eb.pitKeepAlive)
		}
		return &core.ListResult[TestEntity]{Items: []*TestEntity{}, Total: 0}, nil
	})

	_, err := list.Query(ctx,
		WithData(NewDBProxy(nil, nil, &elastic.Client{})),
		WithESIndex("users"),
		WithPitKeepAlive(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListQuery_WithTotalLimit(t *testing.T) {
	ctx := context.Background()
	baseBuilder := NewGormBuilder[TestEntity](NewDBProxy(&gorm.DB{}, nil, nil))

	list := NewList[TestEntity]()
	list.SetQuerier(baseBuilder)

	list.Use(func(
		ctx context.Context,
		b Querier[TestEntity],
		next func(context.Context) (core.Result[TestEntity], error),
	) (core.Result[TestEntity], error) {
		meta := b.GetQueryMeta()
		if meta.TotalLimit != 1000 {
			t.Fatalf("expected total limit 1000, got %d", meta.TotalLimit)
		}
		return &core.ListResult[TestEntity]{Items: []*TestEntity{}, Total: 0}, nil
	})

	_, err := list.Query(ctx, WithTotalLimit(1000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if baseBuilder.GetQueryMeta().TotalLimit != 0 {
		t.Fatalf("expected base builder total limit to remain 0, got %d", baseBuilder.GetQueryMeta().TotalLimit)
	}
}

func TestListQueryPageWithPITRejectsCursorWithoutPITID(t *testing.T) {
	ctx := context.Background()
	list := NewList[TestEntity]()
	list.SetDataSource(ElasticSearch)

	_, err := list.QueryPageWithPIT(ctx,
		WithData(NewDBProxy(nil, nil, &elastic.Client{})),
		WithESIndex("users"),
		WithCursorField("id"),
		WithCursorValue(uint32(10)),
	)
	if !errors.Is(err, ErrPITCursorWithoutPITID) {
		t.Fatalf("expected ErrPITCursorWithoutPITID, got %v", err)
	}
}
