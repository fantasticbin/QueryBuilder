package builder

import (
	"context"
	"strings"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
	qbtest "github.com/fantasticbin/QueryBuilder/v2/test"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
					Return(&core.ListResult[MongoTestEntity]{Items: []*MongoTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
						{ID: 2, Name: "Bob", Age: 30},
					}, Total: 2}, nil)
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
					Return(&core.ListResult[MongoTestEntity]{Items: []*MongoTestEntity{
						{ID: 1, Name: "Alice", Age: 25},
					}, Total: 1}, nil)
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
					Return(&core.ListResult[MongoTestEntity]{Total: 0}, nil)
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

			list.Use(qbtest.PassthroughMiddleware[MongoTestEntity, Querier[MongoTestEntity]]())

			// 执行查询
			opts := []QueryOption{
				WithData(NewDBProxy(nil, nil, nil)),
			}

			result, err := list.Query(ctx, opts...)

			if tt.expectedErr != nil {
				if err == nil || err.Error() != tt.expectedErr.Error() {
					t.Errorf("expected error: %v, got: %v", tt.expectedErr, err)
				}
			} else {
				qbtest.AssertNoError(t, err)
				qbtest.AssertListResult(t, result, tt.expectedResult, tt.expectedTotal)
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

func TestMongoBuilderDefaults(t *testing.T) {
	b := NewMongoBuilder[MongoTestEntity](NewDBProxy(nil, nil, nil))
	if b.builder.dataSource != MongoDB {
		t.Fatalf("expected MongoDB data source, got %v", b.builder.dataSource)
	}
	if b.builder.limit != defaultLimit {
		t.Fatalf("expected default limit %d, got %d", defaultLimit, b.builder.limit)
	}
	if !b.builder.needPagination {
		t.Fatal("expected default needPagination=true")
	}
	if !b.builder.needTotal {
		t.Fatal("expected default needTotal=true")
	}
}

func resolveMongoFindOptions(b *options.FindOptionsBuilder) *options.FindOptions {
	opts := &options.FindOptions{}
	for _, fn := range b.List() {
		if err := fn(opts); err != nil {
			panic(err)
		}
	}
	return opts
}

// TestMongoBuilderListFindOptionsOmitsNilSort ensures doQuery does not call SetSort
// when sort is unset. Passing a nil bson.D to the driver fails with WriteNull.
func TestMongoBuilderListFindOptionsOmitsNilSort(t *testing.T) {
	b := NewMongoBuilder[MongoTestEntity](NewDBProxy(nil, nil, nil))
	opts := resolveMongoFindOptions(b.buildListFindOptions())
	if opts.Sort != nil {
		t.Fatalf("expected Sort unset when m.sort is nil, got %#v", opts.Sort)
	}

	// Explain should likewise omit sort when unset
	dsl, err := b.Explain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dsl, `"sort"`) {
		t.Fatalf("Explain should omit sort when unset, got %s", dsl)
	}
}

func TestMongoBuilderListFindOptionsSetsSortWhenConfigured(t *testing.T) {
	sort := MongoSort{{Key: "id", Value: 1}, {Key: "age", Value: -1}}
	b := NewMongoBuilder[MongoTestEntity](NewDBProxy(nil, nil, nil)).SetSort(sort)
	opts := resolveMongoFindOptions(b.buildListFindOptions())
	got, ok := opts.Sort.(MongoSort)
	if !ok {
		t.Fatalf("expected MongoSort, got %T", opts.Sort)
	}
	if len(got) != 2 || got[0].Key != "id" || got[0].Value != 1 || got[1].Key != "age" || got[1].Value != -1 {
		t.Fatalf("unexpected sort %#v", got)
	}

	dsl, err := b.Explain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsl, `"sort"`) || !strings.Contains(dsl, `"id"`) {
		t.Fatalf("Explain should include configured sort, got %s", dsl)
	}
}

func TestMongoBuilderListFindOptionsProjectionAndPagination(t *testing.T) {
	b := NewMongoBuilder[MongoTestEntity](NewDBProxy(nil, nil, nil))
	b.SetFields("id", "name")
	b.SetStart(5)
	b.SetLimit(10)
	opts := resolveMongoFindOptions(b.buildListFindOptions())
	if opts.Skip == nil || *opts.Skip != 5 {
		t.Fatalf("expected skip=5, got %v", opts.Skip)
	}
	if opts.Limit == nil || *opts.Limit != 10 {
		t.Fatalf("expected limit=10, got %v", opts.Limit)
	}
	proj, ok := opts.Projection.(bson.D)
	if !ok || len(proj) != 2 {
		t.Fatalf("expected projection bson.D len 2, got %#v", opts.Projection)
	}

	b.SetNeedPagination(false)
	opts = resolveMongoFindOptions(b.buildListFindOptions())
	if opts.Skip != nil || opts.Limit != nil {
		t.Fatalf("expected no skip/limit when pagination disabled, got skip=%v limit=%v", opts.Skip, opts.Limit)
	}
}
