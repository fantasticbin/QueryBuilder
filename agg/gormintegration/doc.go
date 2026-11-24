// Package gormintegration 存放 GORM 聚合构建器针对真实 MySQL / PostgreSQL 的集成测试。
//
// 该目录是独立的 Go module，MySQL / PostgreSQL 驱动只作为本模块依赖，
// 不会进入 QueryBuilder 主模块的依赖图。运行方式：
//
//	cd agg/gormintegration
//	GORM_MYSQL_DSN=... GORM_PG_DSN=... go test -tags integration -race ./...
package gormintegration
