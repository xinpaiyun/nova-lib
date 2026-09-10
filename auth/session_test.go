package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// 会话测试注入进程内存存储作为后端；默认 Redis 存储在客户端未初始化时会 fail fast。
	SetCache(NewMemoryStore())
	code := m.Run()
	SetCache(nil)
	os.Exit(code)
}

func TestSessionRoundTrip(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if TokenHash(token) == "" {
		t.Fatalf("TokenHash should not be empty")
	}
	session := Session{
		UserID:    7,
		TenantID:  3,
		RoleCode:  "admin",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := StoreSession(context.Background(), token, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	loaded, err := ResolveSession(context.Background(), token)
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	if loaded.UserID != 7 || loaded.TenantID != 3 || loaded.RoleCode != "admin" {
		t.Fatalf("loaded session mismatch: %#v", loaded)
	}
	claims, err := ResolveClaims(context.Background(), token)
	if err != nil {
		t.Fatalf("ResolveClaims: %v", err)
	}
	if claims.UserID != 7 || claims.RoleCode != "admin" {
		t.Fatalf("claims mismatch: %#v", claims)
	}
}

func TestStoreSessionValidation(t *testing.T) {
	if err := StoreSession(context.Background(), "  ", Session{ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatalf("empty token should be rejected")
	}
	if err := StoreSession(context.Background(), "tok", Session{ExpiresAt: time.Now().Add(-time.Minute)}); err == nil {
		t.Fatalf("expired session should be rejected")
	}
	if err := StoreSession(context.Background(), "tok", Session{UserID: 0, ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatalf("session without user should be rejected")
	}
}

func TestResolveAndRevoke(t *testing.T) {
	if _, err := ResolveSession(context.Background(), ""); err == nil {
		t.Fatalf("empty token should fail")
	}
	if _, err := ResolveSession(context.Background(), "missing"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("unknown token error = %v, want ErrSessionInvalid", err)
	}
	token, _ := GenerateToken()
	if err := StoreSession(context.Background(), token, Session{UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := RevokeSession(context.Background(), token); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := ResolveSession(context.Background(), token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked token error = %v, want ErrSessionInvalid", err)
	}
}

// TestDefaultStoreFailsFastWithoutRedis 验证默认存储在 Redis 客户端未初始化时直接报错，
// 而不是静默降级写入进程内存（否则服务重启即全员掉线）。
func TestDefaultStoreFailsFastWithoutRedis(t *testing.T) {
	SetCache(nil) // 恢复默认 Redis 存储
	defer SetCache(NewMemoryStore())

	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	err = StoreSession(context.Background(), token, Session{UserID: 1, ExpiresAt: time.Now().Add(time.Hour)})
	if !errors.Is(err, ErrRedisNotInitialized) {
		t.Fatalf("StoreSession error = %v, want ErrRedisNotInitialized", err)
	}
	if _, err := ResolveSession(context.Background(), token); !errors.Is(err, ErrRedisNotInitialized) {
		t.Fatalf("ResolveSession error = %v, want ErrRedisNotInitialized", err)
	}
	if err := RevokeSession(context.Background(), token); !errors.Is(err, ErrRedisNotInitialized) {
		t.Fatalf("RevokeSession error = %v, want ErrRedisNotInitialized", err)
	}
}
