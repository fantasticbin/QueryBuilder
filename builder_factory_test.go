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
