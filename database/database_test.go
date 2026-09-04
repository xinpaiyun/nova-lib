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
