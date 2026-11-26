package builder

import (
	"context"
	"errors"
	"testing"

	"github.com/fantasticbin/QueryBuilder/v2/core"
)

const testCustomDataSource core.DataSource = 42

func TestNewBuilderUnknownDataSourceReturnsInvalidError(t *testing.T) {
	querier := NewBuilder[TestEntity](core.DataSource(99), nil)
	_, err := querier.QueryList(context.Background())
	if !errors.Is(err, ErrDataSourceInvalid) {
		t.Fatalf("expected ErrDataSourceInvalid, got %v", err)
	}
}

func TestRegisterBuilderNewBuilderUsesFactory(t *testing.T) {
	t.Cleanup(func() {
		RegisterBuilder[TestEntity](testCustomDataSource, nil)
	})

	RegisterBuilder[TestEntity](testCustomDataSource, func(data *core.DBProxy) Querier[TestEntity] {
		return NewGormBuilder[TestEntity](data)
	})

	querier := NewBuilder[TestEntity](testCustomDataSource, NewDBProxyWithAdapters())
	if _, ok := querier.(*GormBuilder[TestEntity]); !ok {
		t.Fatalf("expected custom factory to return GormBuilder, got %T", querier)
	}
}

func TestRegisterBuilderBuiltInPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when overriding a built-in data source")
		}
	}()
	RegisterBuilder[TestEntity](Gorm, func(data *core.DBProxy) Querier[TestEntity] {
		return NewGormBuilder[TestEntity](data)
	})
}

func TestRegisterBuilderSupportsMultipleEntityTypesForSameDataSource(t *testing.T) {
	type otherCustomEntity struct {
		Code string
	}
	t.Cleanup(func() {
		RegisterBuilder[TestEntity](testCustomDataSource, nil)
		RegisterBuilder[otherCustomEntity](testCustomDataSource, nil)
	})

	RegisterBuilder[TestEntity](testCustomDataSource, func(data *core.DBProxy) Querier[TestEntity] {
		return NewGormBuilder[TestEntity](data)
	})
	RegisterBuilder[otherCustomEntity](testCustomDataSource, func(data *core.DBProxy) Querier[otherCustomEntity] {
		return NewMongoBuilder[otherCustomEntity](data)
	})

	q1 := NewBuilder[TestEntity](testCustomDataSource, NewDBProxyWithAdapters())
	if _, ok := q1.(*GormBuilder[TestEntity]); !ok {
		t.Fatalf("expected GormBuilder for TestEntity, got %T", q1)
	}
	q2 := NewBuilder[otherCustomEntity](testCustomDataSource, NewDBProxyWithAdapters())
	if _, ok := q2.(*MongoBuilder[otherCustomEntity]); !ok {
		t.Fatalf("expected MongoBuilder for otherCustomEntity, got %T", q2)
	}

	// 删除一个实体类型的注册不应影响同一数据源下的其他实体类型
	RegisterBuilder[TestEntity](testCustomDataSource, nil)
	if _, err := NewBuilder[TestEntity](testCustomDataSource, NewDBProxyWithAdapters()).QueryList(context.Background()); !errors.Is(err, ErrDataSourceInvalid) {
		t.Fatalf("expected ErrDataSourceInvalid after unregistering TestEntity, got %v", err)
	}
	q2After := NewBuilder[otherCustomEntity](testCustomDataSource, NewDBProxyWithAdapters())
	if _, ok := q2After.(*MongoBuilder[otherCustomEntity]); !ok {
		t.Fatalf("expected otherCustomEntity factory to remain registered, got %T", q2After)
	}
}
