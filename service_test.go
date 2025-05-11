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

type TestFilter struct {
	Name string
	Age  uint8
}

type TestSort struct {
	Field     string
	Direction string
}

type TestService struct {
	filter TestFilter
	sort   TestSort
}

func (s *TestService) GetFilter(_ context.Context) (any, error) {
	return func(db *gorm.DB) *gorm.DB {
		if s.filter.Name != "" {
			db.Where("name = ?", s.filter.Name)
		}

		if s.filter.Age > 0 {
			db.Where("age >= ?", s.filter.Age)
		}

		return db
	}, nil
}

func (s *TestService) GetSort() any {
	return func(db *gorm.DB) *gorm.DB {
		// 实际项目中的排序需要根据pb文件生成的枚举值来处理
		return db.Order(s.sort.Field + " " + s.sort.Direction)
	}
}

func TestQueryList(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock 策略实例
	mockStrategy := NewMockQueryListStrategy[TestEntity](ctrl)

	tests := []struct {
		name           string
		service        Service
		mockSetup      func()
		opts           []QueryOption[TestFilter, TestSort]
		expectedResult []*TestEntity
		expectedTotal  int64
		expectedErr    error
	}{
		{
			name:    "无筛选查询&id升序",
			service: &TestService{},
			mockSetup: func() {
				mockStrategy.EXPECT().
					QueryList(ctx, gomock.Any()).
					Return([]*TestEntity{
						{ID: 1, Name: "Alice", Age: 25},
						{ID: 2, Name: "Bob", Age: 30},
					}, int64(2), nil)
			},
			opts: []QueryOption[TestFilter, TestSort]{
				WithData[TestFilter, TestSort](NewDBProxy(&gorm.DB{}, nil)),
				WithFilter[TestFilter, TestSort](&TestFilter{}),
				WithSort[TestFilter, TestSort](TestSort{Field: "id", Direction: "asc"}),
			},
			expectedResult: []*TestEntity{
				{ID: 1, Name: "Alice", Age: 25},
				{ID: 2, Name: "Bob", Age: 30},
			},
			expectedTotal: 2,
			expectedErr:   nil,
		},
		{
			name:    "无筛选查询&age降序",
			service: &TestService{},
			mockSetup: func() {
				mockStrategy.EXPECT().
					QueryList(ctx, gomock.Any()).
					Return([]*TestEntity{
						{ID: 2, Name: "Bob", Age: 30},
						{ID: 1, Name: "Alice", Age: 25},
					}, int64(2), nil)
			},
			opts: []QueryOption[TestFilter, TestSort]{
				WithData[TestFilter, TestSort](NewDBProxy(&gorm.DB{}, nil)),
				WithFilter[TestFilter, TestSort](&TestFilter{}),
				WithSort[TestFilter, TestSort](TestSort{Field: "age", Direction: "desc"}),
			},
			expectedResult: []*TestEntity{
				{ID: 2, Name: "Bob", Age: 30},
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 2,
			expectedErr:   nil,
		},
		{
			name:    "有筛选查询",
			service: &TestService{},
			mockSetup: func() {
				mockStrategy.EXPECT().
					QueryList(ctx, gomock.Any()).
					Return([]*TestEntity{
						{ID: 1, Name: "Alice", Age: 25},
					}, int64(1), nil)
			},
			opts: []QueryOption[TestFilter, TestSort]{
				WithData[TestFilter, TestSort](NewDBProxy(&gorm.DB{}, nil)),
				WithFilter[TestFilter, TestSort](&TestFilter{Name: "Alice"}),
				WithSort[TestFilter, TestSort](TestSort{Field: "id", Direction: "desc"}),
			},
			expectedResult: []*TestEntity{
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 1,
			expectedErr:   nil,
		},
		{
			name:    "无数据实例",
			service: &TestService{},
			mockSetup: func() {
				mockStrategy.EXPECT().
					QueryList(ctx, gomock.Any()).
					Return(nil, int64(0), nil)
			},
			opts: []QueryOption[TestFilter, TestSort]{
				WithData[TestFilter, TestSort](NewDBProxy(&gorm.DB{}, nil)),
				WithFilter[TestFilter, TestSort](&TestFilter{Name: "test"}),
				WithSort[TestFilter, TestSort](TestSort{Field: "id", Direction: "asc"}),
			},
			expectedResult: nil,
			expectedTotal:  0,
			expectedErr:    nil,
		},
		{
			name:    "数据实例错误",
			service: &TestService{},
			mockSetup: func() {
				mockStrategy.EXPECT().
					QueryList(ctx, gomock.Any()).
					Return(nil, int64(0), errors.New("no data source provided"))
			},
			opts: []QueryOption[TestFilter, TestSort]{
				WithFilter[TestFilter, TestSort](&TestFilter{Name: "test"}),
				WithSort[TestFilter, TestSort](TestSort{Field: "id", Direction: "asc"}),
			},
			expectedResult: nil,
			expectedTotal:  0,
			expectedErr:    errors.New("no data source provided"),
		},
	}

	list := &List[TestEntity, TestFilter, TestSort]{}
	// 这里使用 Mock 策略替代真实的策略
	// 实际使用时会根据数据源自动选择
	list.SetStrategy(mockStrategy)
	// 添加耗时监控
	list.Use(func(
		ctx context.Context,
		builder *builder[TestEntity],
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

			result, total, err := list.Query(ctx, tt.service, tt.opts...)

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
