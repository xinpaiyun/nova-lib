package cache

import (
	"context"
	"testing"
	"time"
)

// TestGetDelConsumesMemoryValue 验证内存缓存的读取并删除是一次性消费。
func TestGetDelConsumesMemoryValue(t *testing.T) {
	ctx := context.Background()
	key := "cache:test:getdel"
	if err := Set(ctx, key, "value", time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := GetDel(ctx, key)
	if err != nil {
		t.Fatalf("GetDel() error = %v", err)
	}
	if got != "value" {
		t.Fatalf("GetDel() = %q, want value", got)
	}
	if _, err := Get(ctx, key); err == nil {
		t.Fatal("Get() after GetDel error = nil, want cache miss")
	}
}

// TestGetDelDropsExpiredMemoryValue 验证过期缓存被读取并删除时不会返回旧值。
func TestGetDelDropsExpiredMemoryValue(t *testing.T) {
	ctx := context.Background()
	key := "cache:test:getdel:expired"
	if err := Set(ctx, key, "value", -time.Second); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if _, err := GetDel(ctx, key); err == nil {
		t.Fatal("GetDel() expired error = nil, want cache miss")
	}
}
