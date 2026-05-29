package core

// DataSource 数据源类型枚举
type DataSource int

const (
	// Gorm 数据源
	Gorm DataSource = iota
	// MongoDB 数据源
	MongoDB
	// ElasticSearch 数据源
	ElasticSearch
)
