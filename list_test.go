package builder

import (
	"context"
	"errors"
	"fmt"
	"go.uber.org/mock/gomock"
	"gorm.io/gorm"
	"testing"
	"time"
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
					Return([]*TestEntity{
						{ID: 1, Name: "Alice", Age: 25},
						{ID: 2, Name: "Bob", Age: 30},
					}, int64(2), nil)
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
					Return([]*TestEntity{
						{ID: 2, Name: "Bob", Age: 30},
						{ID: 1, Name: "Alice", Age: 25},
					}, int64(2), nil)
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
					Return([]*TestEntity{
						{ID: 1, Name: "Alice", Age: 25},
					}, int64(1), nil)
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
					Return([]*TestEntity{
						{ID: 2, Name: "Bob", Age: 30},
					}, int64(1), nil)
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
					Return(nil, int64(0), nil)
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
					Return(nil, int64(0), errors.New("no data source provided"))
			},
			opts: []QueryOption{},
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
		next func(context.Context,
		) ([]*TestEntity, int64, error)) ([]*TestEntity, int64, error) {
		defer func() func() {
			pre := time.Now()
			return func() {
				elapsed := time.Since(pre)
				fmt.Println("elapsed:", elapsed)
			}
		}()()
		result, total, err := next(ctx)
		return result, total, err
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockSetup != nil {
				tt.mockSetup()
			}

			result, total, err := list.Query(ctx, tt.opts...)

			if tt.expectedErr != nil {
				if err == nil || err.Error() != tt.expectedErr.Error() {
					t.Errorf("expected error: %v, got: %v", tt.expectedErr, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}

				if total != tt.expectedTotal {
					t.Errorf("expected total: %d, got: %d", tt.expectedTotal, total)
				}

				if len(result) != len(tt.expectedResult) {
					t.Errorf("expected result length: %d, got: %d", len(tt.expectedResult), len(result))
				}

				for i, item := range result {
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
		next func(context.Context) ([]*TestEntity, int64, error),
	) ([]*TestEntity, int64, error) {
		order = append(order, "middleware1_before")
		result, total, err := next(ctx)
		order = append(order, "middleware1_after")
		return result, total, err
	})

	// 添加第二个中间件
	list.Use(func(
		ctx context.Context,
		builder Querier[TestEntity],
		next func(context.Context) ([]*TestEntity, int64, error),
	) ([]*TestEntity, int64, error) {
		order = append(order, "middleware2_before")
		result, total, err := next(ctx)
		order = append(order, "middleware2_after")
		return result, total, err
	})

	// 设置 Mock 期望
	mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
	mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier).Times(2)
	mockQuerier.EXPECT().
		QueryList(ctx).
		Return([]*TestEntity{{ID: 1, Name: "Test", Age: 20}}, int64(1), nil)

	result, total, err := list.Query(ctx,
		WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}
}

// TestUnsupportedDataSourcePanicRecovery 测试不支持的数据源类型时 panic 被正确恢复为 error
func TestUnsupportedDataSourcePanicRecovery(t *testing.T) {
	ctx := context.Background()

	list := NewList[TestEntity]()
	// 设置一个不支持的数据源类型（枚举值 99）
	list.SetDataSource(DataSource(99))

	result, total, err := list.Query(ctx,
		WithData(NewDBProxy(&gorm.DB{}, nil, nil)),
	)

	if err == nil {
		t.Fatal("expected error for unsupported data source, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
	if total != 0 {
		t.Errorf("expected total 0, got %d", total)
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
		next func(context.Context) ([]*TestEntity, int64, error),
	) ([]*TestEntity, int64, error) {
		receivedBuilder = builder
		// 短路返回，不执行真实查询
		return []*TestEntity{{ID: 1, Name: "Test", Age: 20}}, 1, nil
	})

	result, total, err := gormBuilder.QueryList(ctx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
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