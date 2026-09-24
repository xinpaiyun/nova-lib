package database

import (
	"path/filepath"
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestInitAppliesPoolConfig 验证数据库初始化会应用连接池配置。
func TestInitAppliesPoolConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "nova.db")
	cfg := config.DatabaseConfig{
		Driver:                 "sqlite",
		SQLitePath:             dbPath,
		MaxOpenConns:           7,
		MaxIdleConns:           3,
		ConnMaxLifetimeMinutes: 15,
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	t.Cleanup(func() {
		if err := Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	sqlDB, err := DB().DB()
	if err != nil {
		t.Fatalf("DB().DB() error = %v", err)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != cfg.MaxOpenConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", got, cfg.MaxOpenConns)
	}
}

// TestResolveDriver 验证统一驱动解析规则：host 与 name 均非空时使用 MySQL，否则回退 SQLite。
func TestResolveDriver(t *testing.T) {
	mysqlCfg := config.DatabaseConfig{Host: "127.0.0.1", Name: "nova"}
	if got := ResolveDriver(mysqlCfg); got != "mysql" {
		t.Fatalf("ResolveDriver() = %q, want mysql", got)
	}
	sqliteCfg := config.DatabaseConfig{Host: "127.0.0.1", Name: ""}
	if got := ResolveDriver(sqliteCfg); got != "sqlite" {
		t.Fatalf("ResolveDriver() = %q, want sqlite when name empty", got)
	}
	zeroCfg := config.DatabaseConfig{}
	if got := ResolveDriver(zeroCfg); got != "sqlite" {
		t.Fatalf("ResolveDriver() = %q, want sqlite for zero config", got)
	}
}
