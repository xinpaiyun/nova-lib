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
)

var db *gorm.DB

// Init 根据配置初始化 GORM 数据库连接。
func Init(cfg config.DatabaseConfig) error {
	dialector, err := buildDialector(cfg)
	if err != nil {
		return err
	}
	conn, err := gorm.Open(dialector, &gorm.Config{})
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
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetimeMinutes > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeMinutes) * time.Minute)
	}
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
