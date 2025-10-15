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

type ElasticTestService struct {
	filter ElasticTestFilter
	sort   ElasticTestSort
}

func (s *ElasticTestService) GetFilter(_ context.Context) (any, error) {
	query := elastic.NewBoolQuery()
	if s.filter.Name != "" {
		query = query.Must(elastic.NewTermQuery("name", s.filter.Name))
	}
	if s.filter.Age > 0 {
		query = query.Must(elastic.NewRangeQuery("age").Gte(s.filter.Age))
	}
	return query, nil
}

func (s *ElasticTestService) GetSort() any {
	if s.sort.Field == "" {
		return nil
	}
	ascending := s.sort.Direction == "asc"
	return elastic.NewFieldSort(s.sort.Field).Order(ascending)
}

func TestElasticsearchQueryList(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Mock 策略实例
	mockStrategy := NewMockQueryListStrategy[ElasticTestEntity](ctrl)

	tests := []struct {
		name           string
		mockSetup      func()
		opts           []QueryOption[ElasticTestFilter, ElasticTestSort]
		expectedResult []*ElasticTestEntity
		expectedTotal  int64
		expectedErr    error
	}{
		{
			name: "Elasticsearch无筛选查询&id升序",
			mockSetup: func() {
				mockStrategy.EXPECT().
					QueryList(ctx, gomock.Any()).
					Return([]*ElasticTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
						{ID: 2, Name: "Bob", Age: 30},
					}, int64(2), nil)
			},
			opts: []QueryOption[ElasticTestFilter, ElasticTestSort]{
				WithData[ElasticTestFilter, ElasticTestSort](NewDBProxy(nil, nil, &elastic.Client{})),
				WithFilter[ElasticTestFilter, ElasticTestSort](&ElasticTestFilter{}),
				WithSort[ElasticTestFilter, ElasticTestSort](ElasticTestSort{Field: "id", Direction: "asc"}),
			},
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
				mockStrategy.EXPECT().
					QueryList(ctx, gomock.Any()).
					Return([]*ElasticTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
					}, int64(1), nil)
			},
			opts: []QueryOption[ElasticTestFilter, ElasticTestSort]{
				WithData[ElasticTestFilter, ElasticTestSort](NewDBProxy(nil, nil, &elastic.Client{})),
				WithFilter[ElasticTestFilter, ElasticTestSort](&ElasticTestFilter{Name: "Alice"}),
				WithSort[ElasticTestFilter, ElasticTestSort](ElasticTestSort{Field: "id", Direction: "desc"}),
			},
			expectedResult: []*ElasticTestEntity{
				{ID: 1, Name: "Alice", Age: 25},
			},
			expectedTotal: 1,
			expectedErr:   nil,
		},
	}

	list := NewList[ElasticTestEntity, ElasticTestFilter, ElasticTestSort](&ElasticTestService{})
	// 使用 Mock 策略替代真实的策略
	list.SetStrategy(mockStrategy)

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
