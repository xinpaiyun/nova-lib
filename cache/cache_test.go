package cache

import (
	"context"
	"errors"
	"path/filepath"
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

// TestRedkaKVBackend 验证 Redka 本地缓存作为 KV 后端时的读写回环与 miss 归一化。
func TestRedkaKVBackend(t *testing.T) {
	if err := InitLocal(filepath.Join(t.TempDir(), "cache.db")); err != nil {
		t.Fatalf("InitLocal() error = %v", err)
	}
	t.Cleanup(func() { _ = CloseLocal() })

	ctx := context.Background()
	if err := Set(ctx, "cache:redka:kv", "v1", time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := Get(ctx, "cache:redka:kv")
	if err != nil || got != "v1" {
		t.Fatalf("Get() = (%q, %v), want (v1, nil)", got, err)
	}
	if _, err := Get(ctx, "cache:redka:miss"); !errors.Is(err, ErrMiss) {
		t.Fatalf("Get() miss error = %v, want ErrMiss", err)
	}

	if err := SetJSON(ctx, "cache:redka:json", map[string]int{"n": 1}, time.Minute); err != nil {
		t.Fatalf("SetJSON() error = %v", err)
	}
	var out map[string]int
	ok, err := GetJSON(ctx, "cache:redka:json", &out)
	if err != nil || !ok || out["n"] != 1 {
		t.Fatalf("GetJSON() = (out=%v ok=%v err=%v), want (n=1, true, nil)", out, ok, err)
	}

	if _, err := GetDel(ctx, "cache:redka:kv"); err != nil {
		t.Fatalf("GetDel() error = %v", err)
	}
	if _, err := Get(ctx, "cache:redka:kv"); !errors.Is(err, ErrMiss) {
		t.Fatalf("Get() after GetDel error = %v, want ErrMiss", err)
	}

	if err := Set(ctx, "cache:redka:del", "x", time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := Del(ctx, "cache:redka:del"); err != nil {
		t.Fatalf("Del() error = %v", err)
	}
	if _, err := Get(ctx, "cache:redka:del"); !errors.Is(err, ErrMiss) {
		t.Fatalf("Get() after Del error = %v, want ErrMiss", err)
	}
}

// TestRedkaListBackend 验证 List 队列操作在 Redka 后端下的回环与 miss 语义。
func TestRedkaListBackend(t *testing.T) {
	if err := InitLocal(filepath.Join(t.TempDir(), "cache-list.db")); err != nil {
		t.Fatalf("InitLocal() error = %v", err)
	}
	t.Cleanup(func() { _ = CloseLocal() })

	ctx := context.Background()
	key := "cache:redka:queue"
	if err := ListPushBack(ctx, key, "a", "b", "c"); err != nil {
		t.Fatalf("ListPushBack() error = %v", err)
	}
	if n, err := ListLen(ctx, key); err != nil || n != 3 {
		t.Fatalf("ListLen() = (%d, %v), want (3, nil)", n, err)
	}
	first, err := ListPopFront(ctx, key)
	if err != nil || first != "a" {
		t.Fatalf("ListPopFront() = (%q, %v), want (a, nil)", first, err)
	}
	rest, err := ListPopFrontN(ctx, key, 10)
	if err != nil || len(rest) != 2 || rest[0] != "b" || rest[1] != "c" {
		t.Fatalf("ListPopFrontN() = (%v, %v), want ([b c], nil)", rest, err)
	}
	if _, err := ListPopFront(ctx, key); !errors.Is(err, ErrMiss) {
		t.Fatalf("ListPopFront() empty error = %v, want ErrMiss", err)
	}
	if _, err := ListPopFrontN(ctx, key, 1); !errors.Is(err, ErrMiss) {
		t.Fatalf("ListPopFrontN() empty error = %v, want ErrMiss", err)
	}
}

// TestListRejectsMemoryFallback 验证即使内存兜底开启，List 操作也不入进程内存：
// 无后端时显式返回 ErrUnavailable，保证任务队列不发生静默降级。
func TestListRejectsMemoryFallback(t *testing.T) {
	_ = CloseLocal()
	original := clientGetter
	clientGetter = func() *goredis.Client { return nil }
	t.Cleanup(func() { clientGetter = original })

	ctx := context.Background()
	if err := ListPushBack(ctx, "cache:list:nobackend", "v"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ListPushBack() error = %v, want ErrUnavailable", err)
	}
}

// TestUnavailableWithoutBackend 验证 DisableMemoryFallback 后，无后端的 KV 读写
// 显式返回 ErrUnavailable（fail fast），而不再静默写入进程内存。
func TestUnavailableWithoutBackend(t *testing.T) {
	_ = CloseLocal()
	original := clientGetter
	clientGetter = func() *goredis.Client { return nil }
	t.Cleanup(func() {
		clientGetter = original
		memoryFallbackEnabled = true
	})
	DisableMemoryFallback()

	ctx := context.Background()
	if err := Set(ctx, "cache:unavailable", "v", time.Minute); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Set() error = %v, want ErrUnavailable", err)
	}
	if _, err := Get(ctx, "cache:unavailable"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get() error = %v, want ErrUnavailable", err)
	}
	if _, err := GetDel(ctx, "cache:unavailable"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("GetDel() error = %v, want ErrUnavailable", err)
	}
	if err := Del(ctx, "cache:unavailable"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Del() error = %v, want ErrUnavailable", err)
	}
	if _, err := ListLen(ctx, "cache:unavailable:q"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ListLen() error = %v, want ErrUnavailable", err)
	}
	if _, err := ListPopFront(ctx, "cache:unavailable:q"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ListPopFront() error = %v, want ErrUnavailable", err)
	}
	if _, err := ListPopFrontN(ctx, "cache:unavailable:q", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ListPopFrontN() error = %v, want ErrUnavailable", err)
	}
}
