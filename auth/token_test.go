package auth

import (
	"testing"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestIssueTokenIncludesTokenVersion 验证访问令牌携带会话版本号。
func TestIssueTokenIncludesTokenVersion(t *testing.T) {
	cfg := config.JWTConfig{Secret: "test-secret-with-enough-length", ExpireHour: 1}
	token, _, err := IssueToken(cfg, 1, 2, "tenant_admin", "session-1", 7)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	claims, err := ParseToken(cfg, token)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if claims.TokenVersion != 7 {
		t.Fatalf("TokenVersion = %d, want 7", claims.TokenVersion)
	}
	if claims.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want session-1", claims.SessionID)
	}
}
