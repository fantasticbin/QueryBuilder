package demo

import (
	"fmt"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open 打开进程内 SQLite，建表并写入示例数据。无需外部数据库，也不需要 CGO。
func Open() (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open("file:querybuilder-examples?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("sql db: %w", err)
	}
	// 内存库多连接会各持一份空库，限制为 1
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&User{}, &Order{}); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := seed(db); err != nil {
		return nil, err
	}
	return db, nil
}

func seed(db *gorm.DB) error {
	users := []User{
		{ID: 1, Name: "Alice", Status: 1, CreatedAt: 1_700_000_001},
		{ID: 2, Name: "Bob", Status: 1, CreatedAt: 1_700_000_002},
		{ID: 3, Name: "Carol", Status: 0, CreatedAt: 1_700_000_003},
		{ID: 4, Name: "Dave", Status: 1, CreatedAt: 1_700_000_004},
		{ID: 5, Name: "Eve", Status: 1, CreatedAt: 1_700_000_002},
		{ID: 6, Name: "Frank", Status: 1, CreatedAt: 1_700_000_004},
	}
	day1 := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 11, 12, 0, 0, 0, time.UTC)
	orders := []Order{
		{ID: 1, Region: "east", CustomerID: 1, Amount: 12.5, Status: "paid", CreatedAt: day1},
		{ID: 2, Region: "east", CustomerID: 1, Amount: 20, Status: "paid", CreatedAt: day1},
		{ID: 3, Region: "west", CustomerID: 2, Amount: 8, Status: "paid", CreatedAt: day2},
		{ID: 4, Region: "west", CustomerID: 3, Amount: 50, Status: "refunded", CreatedAt: day2},
		{ID: 5, Region: "east", CustomerID: 4, Amount: 15, Status: "paid", CreatedAt: day2},
	}
	if err := db.Create(&users).Error; err != nil {
		return fmt.Errorf("seed users: %w", err)
	}
	if err := db.Create(&orders).Error; err != nil {
		return fmt.Errorf("seed orders: %w", err)
	}
	return nil
}
