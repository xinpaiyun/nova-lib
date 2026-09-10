package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
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

// TestGetJSONMissAndCorrupt 验证未命中返回 (false, nil)，损坏数据返回错误。
func TestGetJSONMissAndCorrupt(t *testing.T) {
	ctx := context.Background()
	ok, err := GetJSON(ctx, "cache:test:getjson:miss", &struct{}{})
	if err != nil || ok {
		t.Fatalf("GetJSON() miss = (%v, %v), want (false, nil)", ok, err)
	}

	if err := Set(ctx, "cache:test:getjson:corrupt", "not-json", time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	var out map[string]any
	if _, err := GetJSON(ctx, "cache:test:getjson:corrupt", &out); err == nil {
		t.Fatal("GetJSON() corrupt payload error = nil, want error")
	}
}

// TestGetJSONPropagatesRedisFailure 验证 Redis 故障不会被伪装成未命中，
// 而是作为真实错误透传给上层（区别于 ErrMiss）。
func TestGetJSONPropagatesRedisFailure(t *testing.T) {
	original := clientGetter
	broken := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	clientGetter = func() *goredis.Client { return broken }
	t.Cleanup(func() {
		clientGetter = original
		_ = broken.Close()
	})

	ctx := context.Background()
	if _, err := Get(ctx, "cache:test:broken"); err == nil || errors.Is(err, ErrMiss) {
		t.Fatalf("Get() error = %v, want non-miss error", err)
	}
	var out struct{}
	ok, err := GetJSON(ctx, "cache:test:broken", &out)
	if ok || err == nil {
		t.Fatalf("GetJSON() = (%v, %v), want (false, real error)", ok, err)
	}
	if errors.Is(err, ErrMiss) {
		t.Fatalf("GetJSON() error should not be ErrMiss: %v", err)
	}
}
