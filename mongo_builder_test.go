package builder

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"
)

type MongoTestEntity struct {
	ID   uint32 `bson:"id"`
	Name string `bson:"name"`
	Age  int    `bson:"age"`
}

func TestMongoDBQueryList(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock Querier 实例
	mockQuerier := NewMockQuerier[MongoTestEntity](ctrl)

	tests := []struct {
		name           string
		mockSetup      func()
		expectedResult []*MongoTestEntity
		expectedTotal  int64
		expectedErr    error
	}{
		{
			name: "MongoDB无筛选查询",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(gomock.Any()).
					Return([]*MongoTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
						{ID: 2, Name: "Bob", Age: 30},
					}, int64(2), nil)
			},
			expectedResult: []*MongoTestEntity{
				{ID: 1, Name: "Alice", Age: 25},
				{ID: 2, Name: "Bob", Age: 30},
			},
			expectedTotal: 2,
			expectedErr:   nil,
		},
		{
			name: "MongoDB有筛选查询",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(gomock.Any()).
					Return([]*MongoTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
					}, int64(1), nil)
			},
			expectedResult: []*MongoTestEntity{
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 1,
			expectedErr:   nil,
		},
		{
			name: "MongoDB空结果查询",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().Use(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(gomock.Any()).
					Return(nil, int64(0), nil)
			},
			expectedResult: nil,
			expectedTotal:  0,
			expectedErr:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockSetup != nil {
				tt.mockSetup()
			}

			// 创建 List 实例并设置 Mock Querier
			list := NewList[MongoTestEntity]()
			list.SetQuerier(mockQuerier)

			// 添加耗时监控中间件
			list.Use(func(
				ctx context.Context,
				builder Querier[MongoTestEntity],
				next func(context.Context) ([]*MongoTestEntity, int64, error),
			) ([]*MongoTestEntity, int64, error) {
				return next(ctx)
			})

			// 执行查询
			opts := []QueryOption{
				WithData(NewDBProxy(nil, nil, nil)),
			}

			result, total, err := list.Query(ctx, opts...)

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

// TestMongoBuilderFilterNilDefault 测试 MongoDB Builder 在 filter 为 nil 时使用默认空文档
func TestMongoBuilderFilterNilDefault(t *testing.T) {
	// 验证 NewMongoBuilder 创建后 filter 为 nil
	mongoBuilder := NewMongoBuilder[MongoTestEntity](NewDBProxy(nil, nil, nil))

	if mongoBuilder.filter != nil {
		t.Error("expected filter to be nil after creation")
	}

	// 验证设置 filter 后正常工作
	mongoBuilder.SetFilter(MongoFilter{})
	if mongoBuilder.filter == nil {
		t.Error("expected filter to be non-nil after SetFilter")
	}
}
