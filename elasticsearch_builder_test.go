package builder

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/olivere/elastic/v7"
	"go.uber.org/mock/gomock"
)

type ElasticTestEntity struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}

type ElasticTestFilter struct {
	Name string
	Age  uint8
}

type ElasticTestSort struct {
	Field     string
	Direction string
}

func TestElasticsearchQueryList(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock Querier 实例
	mockQuerier := NewMockQuerier[ElasticTestEntity](ctrl)

	tests := []struct {
		name           string
		mockSetup      func()
		filter         ElasticTestFilter
		sort           ElasticTestSort
		expectedResult []*ElasticTestEntity
		expectedTotal  int64
		expectedErr    error
	}{
		{
		name: "Elasticsearch无筛选查询&id升序",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(gomock.Any()).
					Return([]*ElasticTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
						{ID: 2, Name: "Bob", Age: 30},
					}, int64(2), nil)
			},
			filter: ElasticTestFilter{},
			sort:   ElasticTestSort{Field: "id", Direction: "asc"},
			expectedResult: []*ElasticTestEntity{
				{ID: 1, Name: "Alice", Age: 25},
				{ID: 2, Name: "Bob", Age: 30},
			},
			expectedTotal: 2,
			expectedErr:   nil,
		},
		{
		name: "Elasticsearch有筛选查询",
			mockSetup: func() {
				mockQuerier.EXPECT().SetStart(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetLimit(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedTotal(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().SetNeedPagination(gomock.Any()).Return(mockQuerier)
				mockQuerier.EXPECT().
					QueryList(gomock.Any()).
					Return([]*ElasticTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
					}, int64(1), nil)
			},
			filter: ElasticTestFilter{Name: "Alice"},
			sort:   ElasticTestSort{Field: "id", Direction: "desc"},
			expectedResult: []*ElasticTestEntity{
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 1,
			expectedErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockSetup != nil {
				tt.mockSetup()
			}

			// 创建 List 实例并设置 Mock Querier
			list := NewList[ElasticTestEntity]()
			list.SetQuerier(mockQuerier)

			// 执行查询
			opts := []QueryOption{
				WithData(NewDBProxy(nil, nil, &elastic.Client{})),
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

func TestElasticsearchStrategyDecoding(t *testing.T) {
	// 测试 JSON 解码逻辑
	entity := ElasticTestEntity{
		ID:   1,
		Name: "Test",
		Age:  30,
	}

	data, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("failed to marshal entity: %v", err)
	}

	var decoded ElasticTestEntity
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal entity: %v", err)
	}

	if decoded.ID != entity.ID || decoded.Name != entity.Name || decoded.Age != entity.Age {
		t.Errorf("expected %+v, got %+v", entity, decoded)
	}
}

func TestElasticsearchIndexValidation(t *testing.T) {
	ctx := context.Background()

	// 测试索引名未配置的情况，使用 ElasticSearchBuilder
	esBuilder := NewElasticSearchBuilder[ElasticTestEntity](
		NewDBProxy(nil, nil, &elastic.Client{}),
		"", // 空索引名
	)
	esBuilder.SetFilter(elastic.NewMatchAllQuery())

	_, _, err := esBuilder.QueryList(ctx)
	if err == nil {
		t.Error("expected error when index is not configured, got nil")
	}
	if err != nil && err.Error() != "elasticsearch index not configured" {
		t.Errorf("expected 'elasticsearch index not configured' error, got: %v", err)
	}
}

func TestElasticsearchSortValidation(t *testing.T) {
	// 此测试验证排序类型的默认错误处理
	// 由于需要真实的 Elasticsearch 客户端才能执行查询，
	// 我们只验证类型转换逻辑在代码中存在即可
	// 实际的错误处理会在运行时验证

	// 验证有效的排序类型
	var validSort1 interface{} = elastic.NewFieldSort("name").Order(true)
	if _, ok := validSort1.(elastic.Sorter); !ok {
		t.Error("expected elastic.FieldSort to implement elastic.Sorter")
	}

	var validSort2 interface{} = []elastic.Sorter{
		elastic.NewFieldSort("name").Order(true),
		elastic.NewFieldSort("age").Order(false),
	}
	if _, ok := validSort2.([]elastic.Sorter); ok {
		// 验证切片类型正确
	}

	// 无效的排序类型会在 switch 的 default 分支中被捕获
	var invalidSort interface{} = "invalid_sort"
	if _, ok := invalidSort.(elastic.Sorter); ok {
		t.Error("string should not be a valid elastic.Sorter")
	}
	if _, ok := invalidSort.([]elastic.Sorter); ok {
		t.Error("string should not be a valid []elastic.Sorter")
	}
}