package wechat

import (
	"context"
	"errors"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestNewClientSessionDisabledWithoutConfig 验证未配置时不发起外部请求。
func TestNewClientSessionDisabledWithoutConfig(t *testing.T) {
	client := NewClient(config.WechatConfig{})
	if client.Enabled() {
		t.Fatal("client should be disabled without appid/secret")
	}
	if _, err := client.Session(context.Background(), "code"); err == nil {
		t.Fatal("expected error for disabled client")
	}
	if _, err := client.Code2Session("code"); err == nil {
		t.Fatal("expected error for Code2Session on disabled client")
	}
}

// TestAccessTokenTTL 验证 access_token 缓存有效期预留刷新窗口。
func TestAccessTokenTTL(t *testing.T) {
	if got := accessTokenTTL(7200); got != 6600*time.Second {
		t.Fatalf("accessTokenTTL(7200) = %v", got)
	}
	if got := accessTokenTTL(300); got != 100*time.Minute {
		t.Fatalf("accessTokenTTL(300) = %v", got)
	}
}

// TestSafeRequestErrorRedactsURL 验证错误信息不回显完整请求 URL。
func TestSafeRequestErrorRedactsURL(t *testing.T) {
	raw := &neturl.Error{Op: "Get", URL: "https://api.weixin.qq.com/sns/jscode2session?secret=TOPSECRET", Err: errors.New("dial timeout")}
	err := safeRequestError(raw)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatalf("error leaked secret: %v", err)
	}
	if !strings.Contains(err.Error(), "dial timeout") {
		t.Fatalf("error lost cause: %v", err)
	}
}
