package demo

import "time"

// User 对应 README Quick Start / 游标示例中的实体
type User struct {
	ID        uint32 `gorm:"primaryKey"`
	Name      string
	Status    int
	CreatedAt int64
}

// Order 对应 README 聚合示例中的源模型
type Order struct {
	ID         uint64 `gorm:"primaryKey"`
	Region     string
	CustomerID uint64
	Amount     float64
	Status     string
	CreatedAt  time.Time
}

// SalesSummary 对应 README 聚合结果 DTO
type SalesSummary struct {
	Region          string  `gorm:"column:region" bson:"region" json:"region"`
	Count           int64   `gorm:"column:order_count" bson:"order_count" json:"order_count"`
	BuyerCount      int64   `gorm:"column:buyer_count" bson:"buyer_count" json:"buyer_count"`
	UniqueAmountSum float64 `gorm:"column:unique_amount_sum" bson:"unique_amount_sum" json:"unique_amount_sum"`
	Amount          float64 `gorm:"column:amount_sum" bson:"amount_sum" json:"amount_sum"`
}

// RegionPaidSummary 对应 README 条件指标 + HAVING 的结果 DTO
type RegionPaidSummary struct {
	Region     string  `gorm:"column:region"`
	Total      int64   `gorm:"column:total"`
	PaidTotal  int64   `gorm:"column:paid_total"`
	PaidAmount float64 `gorm:"column:paid_amount"`
}

// DaySummary 对应 GroupByDate 的结果 DTO（SQLite 时间桶输出字符串）
type DaySummary struct {
	Day   string `gorm:"column:created_day"`
	Total int64  `gorm:"column:total"`
}
