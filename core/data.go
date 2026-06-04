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

// String 返回 DataSource 枚举值的字符串表示
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
