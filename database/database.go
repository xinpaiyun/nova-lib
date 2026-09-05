// Package database 管理 GORM 数据库连接与事务上下文传递。
package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/logging"
)

var db *gorm.DB

// Init 根据配置初始化 GORM 数据库连接。
func Init(cfg config.DatabaseConfig) error {
	dialector, err := buildDialector(cfg)
	if err != nil {
		return err
	}
	conn, err := gorm.Open(dialector, &gorm.Config{
		SkipDefaultTransaction:                   true,
		Logger:                                   logging.NewGormLogger(),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return err
	}
	sqlDB, err := conn.DB()
	if err != nil {
		return err
	}
	if err := sqlDB.Ping(); err != nil {
		return err
	}
	applyPoolConfig(sqlDB, cfg)
	db = conn
	return nil
}

// applyPoolConfig 将连接池参数应用到底层 sql.DB。
func applyPoolConfig(sqlDB interface {
	SetMaxOpenConns(int)
	SetMaxIdleConns(int)
	SetConnMaxLifetime(time.Duration)
}, cfg config.DatabaseConfig) {
	// 未显式配置时使用统一默认值，避免漏配导致连接无上限。
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 100
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 10
	}
	lifetimeMinutes := cfg.ConnMaxLifetimeMinutes
	if lifetimeMinutes <= 0 {
		lifetimeMinutes = 60
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(lifetimeMinutes) * time.Minute)
}

// buildDialector 根据驱动类型创建 GORM 方言。
func buildDialector(cfg config.DatabaseConfig) (gorm.Dialector, error) {
	switch cfg.Driver {
	case "mysql":
		return mysql.Open(cfg.DSN()), nil
	case "sqlite":
		if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o755); err != nil {
			return nil, err
		}
		return sqlite.Open(cfg.SQLitePath), nil
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}
}

// DB 返回已初始化的 GORM 数据库连接。
func DB() *gorm.DB {
	return db
}

// AutoMigrate 使用全局连接执行模型自动迁移；未初始化时返回 ErrStorageDisabled。
func AutoMigrate(models ...any) error {
	if db == nil {
		return ErrStorageDisabled
	}
	return db.AutoMigrate(models...)
}

// Close 关闭数据库连接。
func Close() error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
