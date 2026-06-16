package core

import (
	"errors"
	"testing"

	"gorm.io/gorm"
)

// testDataSourceAdapter is a minimal adapter used to verify custom data source registration.
type testDataSourceAdapter struct {
	dataSource   DataSource
	isConfigured bool
}

// DataSource returns the custom data source configured for this test adapter.
func (a testDataSourceAdapter) DataSource() DataSource { return a.dataSource }

// IsConfigured returns whether this test adapter should be treated as configured.
func (a testDataSourceAdapter) IsConfigured() bool { return a.isConfigured }

func TestNewDBProxy_LegacyConstructorRegistersAdapters(t *testing.T) {
	db := &gorm.DB{}
	proxy := NewDBProxy(db, nil, nil)

	got, err := proxy.GormDB()
	if err != nil {
		t.Fatalf("expected Gorm adapter to be configured, got error: %v", err)
	}
	if got != db {
		t.Fatalf("expected original Gorm DB, got %p want %p", got, db)
	}

	if err := proxy.CheckConfigured(MongoDB); !errors.Is(err, ErrDataNotConfigured) {
		t.Fatalf("expected MongoDB to be unconfigured, got: %v", err)
	}
}

func TestDBProxy_RegisterAdapter(t *testing.T) {
	db := &gorm.DB{}
	proxy := NewDBProxyWithAdapters()
	proxy.RegisterAdapter(NewGormAdapter(db))

	adapter, ok := proxy.Adapter(Gorm)
	if !ok {
		t.Fatal("expected Gorm adapter to be registered")
	}
	if adapter.DataSource() != Gorm {
		t.Fatalf("expected Gorm data source, got: %v", adapter.DataSource())
	}

	got, err := proxy.GormDB()
	if err != nil {
		t.Fatalf("expected Gorm adapter to be configured, got error: %v", err)
	}
	if got != db {
		t.Fatalf("expected original Gorm DB, got %p want %p", got, db)
	}
}

func TestDBProxy_RegisterCustomDataSourceAdapter(t *testing.T) {
	const customDataSource DataSource = 99
	proxy := NewDBProxyWithAdapters(testDataSourceAdapter{
		dataSource:   customDataSource,
		isConfigured: true,
	})

	if err := proxy.CheckConfigured(customDataSource); err != nil {
		t.Fatalf("expected custom adapter to be configured, got: %v", err)
	}

	adapter, ok := proxy.Adapter(customDataSource)
	if !ok {
		t.Fatal("expected custom adapter to be registered")
	}
	if adapter.DataSource() != customDataSource {
		t.Fatalf("expected custom data source, got: %v", adapter.DataSource())
	}
}

func TestDBProxy_CheckConfiguredInvalidDataSource(t *testing.T) {
	err := (&DBProxy{}).CheckConfigured(DataSource(99))
	if !errors.Is(err, ErrDataSourceInvalid) {
		t.Fatalf("expected invalid data source error, got: %v", err)
	}
}
