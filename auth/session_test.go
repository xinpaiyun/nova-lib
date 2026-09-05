package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/xinpaiyun/nova-lib/cache"
)

func TestMain(m *testing.M) {
	// 会话测试依赖 cache 内存回退作为后端；结束后 Flush 防止污染其他包测试。
	code := m.Run()
	cache.Flush()
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
	if _, err := ResolveSession(context.Background(), "missing"); err == nil {
		t.Fatalf("unknown token should fail")
	}
	token, _ := GenerateToken()
	if err := StoreSession(context.Background(), token, Session{UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	if err := RevokeSession(context.Background(), token); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := ResolveSession(context.Background(), token); err == nil {
		t.Fatalf("revoked token should fail")
	}
}
