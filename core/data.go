package core

import (
	"errors"

	"github.com/olivere/elastic/v7"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"gorm.io/gorm"
)

var (
	// ErrDataNotConfigured 表示 DBProxy 未注册指定数据源，或已注册的适配器缺少必需客户端。
	ErrDataNotConfigured = errors.New("data source not configured: DBProxy or its required adapter is nil")
	// ErrDataSourceInvalid 表示请求的数据源类型未被内置支持，也未在 DBProxy 中注册自定义适配器。
	ErrDataSourceInvalid = errors.New("data source invalid")
)

// DataSource 数据源类型枚举。
type DataSource int

const (
	// Gorm 数据源。
	Gorm DataSource = iota
	// MongoDB 数据源。
	MongoDB
	// ElasticSearch 数据源。
	ElasticSearch
)

// String 返回 DataSource 枚举值的字符串表示。
func (ds DataSource) String() string {
	switch ds {
	case Gorm:
		return "Gorm"
	case MongoDB:
		return "MongoDB"
	case ElasticSearch:
		return "ElasticSearch"
	default:
		return "Unknown"
	}
}

// DataSourceAdapter 数据源适配器。
// 新的数据源只需要实现该接口并注册到 DBProxy，即可复用统一的数据源配置校验入口。
type DataSourceAdapter interface {
	// DataSource 返回该适配器所属的数据源类型。
	DataSource() DataSource
	// IsConfigured 返回该适配器是否已经具备执行查询所需的客户端。
	IsConfigured() bool
}

// gormDBProvider 暴露 GORM 客户端，供 DBProxy 在运行时取回强类型实例。
type gormDBProvider interface {
	// GormDB 返回适配器持有的 GORM 数据库实例。
	GormDB() *gorm.DB
}

// mongoCollectionProvider 暴露 MongoDB Collection，供 DBProxy 在运行时取回强类型实例。
type mongoCollectionProvider interface {
	// MongoCollection 返回适配器持有的 MongoDB Collection 实例。
	MongoCollection() *mongo.Collection
}

// elasticSearchClientProvider 暴露 ElasticSearch 客户端，供 DBProxy 在运行时取回强类型实例。
type elasticSearchClientProvider interface {
	// ElasticSearchClient 返回适配器持有的 ElasticSearch 客户端实例。
	ElasticSearchClient() *elastic.Client
}

// GormAdapter GORM 数据源适配器。
type GormAdapter struct {
	DB *gorm.DB
}

// NewGormAdapter 创建 GORM 数据源适配器。
func NewGormAdapter(db *gorm.DB) GormAdapter {
	return GormAdapter{DB: db}
}

// DataSource 返回 GORM 数据源类型。
func (a GormAdapter) DataSource() DataSource { return Gorm }

// IsConfigured 返回 GORM 数据库实例是否已配置。
func (a GormAdapter) IsConfigured() bool { return a.DB != nil }

// GormDB 返回 GORM 数据库实例。
func (a GormAdapter) GormDB() *gorm.DB { return a.DB }

// MongoAdapter MongoDB 数据源适配器。
type MongoAdapter struct {
	Collection *mongo.Collection // 需提前指定.Database("db_name").Collection("collection_name")
}

// NewMongoAdapter 创建 MongoDB 数据源适配器。
func NewMongoAdapter(collection *mongo.Collection) MongoAdapter {
	return MongoAdapter{Collection: collection}
}

// DataSource 返回 MongoDB 数据源类型。
func (a MongoAdapter) DataSource() DataSource { return MongoDB }

// IsConfigured 返回 MongoDB Collection 是否已配置。
func (a MongoAdapter) IsConfigured() bool { return a.Collection != nil }

// MongoCollection 返回 MongoDB Collection 实例。
func (a MongoAdapter) MongoCollection() *mongo.Collection { return a.Collection }

// ElasticSearchAdapter ElasticSearch 数据源适配器。
type ElasticSearchAdapter struct {
	Client *elastic.Client
}

// NewElasticSearchAdapter 创建 ElasticSearch 数据源适配器。
func NewElasticSearchAdapter(client *elastic.Client) ElasticSearchAdapter {
	return ElasticSearchAdapter{Client: client}
}

// DataSource 返回 ElasticSearch 数据源类型。
func (a ElasticSearchAdapter) DataSource() DataSource { return ElasticSearch }

// IsConfigured 返回 ElasticSearch 客户端是否已配置。
func (a ElasticSearchAdapter) IsConfigured() bool { return a.Client != nil }

// ElasticSearchClient 返回 ElasticSearch 客户端实例。
func (a ElasticSearchAdapter) ElasticSearchClient() *elastic.Client { return a.Client }

// DBProxy 数据源适配器注册表。
type DBProxy struct {
	adapters map[DataSource]DataSourceAdapter
}

// NewDBProxy 创建兼容旧调用方式的数据源注册表。
func NewDBProxy(db *gorm.DB, mongodb *mongo.Collection, elasticsearch *elastic.Client) *DBProxy {
	return NewDBProxyWithAdapters(
		NewGormAdapter(db),
		NewMongoAdapter(mongodb),
		NewElasticSearchAdapter(elasticsearch),
	)
}

// NewDBProxyWithAdapters 通过适配器创建数据源注册表。
func NewDBProxyWithAdapters(adapters ...DataSourceAdapter) *DBProxy {
	p := &DBProxy{}
	for _, adapter := range adapters {
		p.RegisterAdapter(adapter)
	}
	return p
}

// RegisterAdapter 按数据源注册适配器。
func (p *DBProxy) RegisterAdapter(adapter DataSourceAdapter) *DBProxy {
	if p == nil {
		p = &DBProxy{}
	}
	if adapter == nil {
		return p
	}
	if p.adapters == nil {
		p.adapters = make(map[DataSource]DataSourceAdapter)
	}
	p.adapters[adapter.DataSource()] = adapter
	return p
}

// Adapter 返回指定数据源注册的适配器。
func (p *DBProxy) Adapter(ds DataSource) (DataSourceAdapter, bool) {
	if p == nil {
		return nil, false
	}
	adapter, ok := p.adapters[ds]
	return adapter, ok
}

// CheckConfigured 检查指定数据源是否已正确配置。
func (p *DBProxy) CheckConfigured(ds DataSource) error {
	_, err := p.adapterFor(ds)
	return err
}

// adapterFor 返回指定数据源的已配置适配器。
func (p *DBProxy) adapterFor(ds DataSource) (DataSourceAdapter, error) {
	adapter, ok := p.Adapter(ds)
	if !ok {
		if isBuiltInDataSource(ds) {
			return nil, ErrDataNotConfigured
		}
		return nil, ErrDataSourceInvalid
	}
	if !adapter.IsConfigured() {
		return nil, ErrDataNotConfigured
	}
	return adapter, nil
}

// isBuiltInDataSource 返回数据源类型是否属于库内置实现。
func isBuiltInDataSource(ds DataSource) bool {
	switch ds {
	case Gorm, MongoDB, ElasticSearch:
		return true
	default:
		return false
	}
}

// GormDB 返回 GORM 数据源实例。
func (p *DBProxy) GormDB() (*gorm.DB, error) {
	adapter, err := p.adapterFor(Gorm)
	if err != nil {
		return nil, err
	}
	provider, ok := adapter.(gormDBProvider)
	if !ok {
		return nil, ErrDataNotConfigured
	}
	db := provider.GormDB()
	if db == nil {
		return nil, ErrDataNotConfigured
	}
	return db, nil
}

// MongoCollection 返回 MongoDB Collection 实例。
func (p *DBProxy) MongoCollection() (*mongo.Collection, error) {
	adapter, err := p.adapterFor(MongoDB)
	if err != nil {
		return nil, err
	}
	provider, ok := adapter.(mongoCollectionProvider)
	if !ok {
		return nil, ErrDataNotConfigured
	}
	collection := provider.MongoCollection()
	if collection == nil {
		return nil, ErrDataNotConfigured
	}
	return collection, nil
}

// ElasticSearchClient 返回 ElasticSearch Client 实例。
func (p *DBProxy) ElasticSearchClient() (*elastic.Client, error) {
	adapter, err := p.adapterFor(ElasticSearch)
	if err != nil {
		return nil, err
	}
	provider, ok := adapter.(elasticSearchClientProvider)
	if !ok {
		return nil, ErrDataNotConfigured
	}
	client := provider.ElasticSearchClient()
	if client == nil {
		return nil, ErrDataNotConfigured
	}
	return client, nil
}
