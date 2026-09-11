package cache

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

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

// TestGetJSONMissAndCorrupt 验证未命中返回 (false, nil)，损坏数据返回错误。
func TestGetJSONMissAndCorrupt(t *testing.T) {
	if err := InitLocal(filepath.Join(t.TempDir(), "cache-json.db")); err != nil {
		t.Fatalf("InitLocal() error = %v", err)
	}
	t.Cleanup(func() { _ = CloseLocal() })

	ctx := context.Background()
	ok, err := GetJSON(ctx, "cache:getjson:miss", &struct{}{})
	if err != nil || ok {
		t.Fatalf("GetJSON() miss = (%v, %v), want (false, nil)", ok, err)
	}

	if err := Set(ctx, "cache:getjson:corrupt", "not-json", time.Minute); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	var out map[string]any
	if _, err := GetJSON(ctx, "cache:getjson:corrupt", &out); err == nil {
		t.Fatal("GetJSON() corrupt payload error = nil, want error")
	}
}

// TestGetJSONPropagatesRedisFailure 验证 Redis 故障不会被伪装成未命中，
// 而是作为真实错误透传给上层（区别于 ErrMiss）。
func TestGetJSONPropagatesRedisFailure(t *testing.T) {
	_ = CloseLocal()
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

// TestUnavailableWithoutBackend 验证无任何后端（未调 InitLocal 且 Redis 未初始化）时，
// 所有读写显式返回 ErrUnavailable（fail fast），不再静默降级为进程内存。
func TestUnavailableWithoutBackend(t *testing.T) {
	_ = CloseLocal()
	original := clientGetter
	clientGetter = func() *goredis.Client { return nil }
	t.Cleanup(func() { clientGetter = original })

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
