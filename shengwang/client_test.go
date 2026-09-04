package shengwang

import (
	"strings"
	"testing"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
)

// testClient 返回配置完整的测试客户端。
func testClient() *Client {
	return NewClient(config.ShengwangConfig{
		Enabled:            true,
		AppID:              "d423b5fd22f44c7e921a1e9b0a447a01",
		AppCertificate:     "a1b2c3d4e5f60718293a4b5c6d7e8f90",
		TokenExpireSeconds: 3600,
	})
}

// TestBuildRTCToken 验证 RTC 频道 token 可生成且为声网 007 协议格式。
func TestBuildRTCToken(t *testing.T) {
	client := testClient()
	token, expireAt, err := client.BuildRTCToken("channel-1", "user-1")
	if err != nil {
		t.Fatalf("BuildRTCToken: %v", err)
	}
	if !strings.HasPrefix(token, "007") {
		t.Fatalf("token prefix = %q", token[:min(3, len(token))])
	}
	if time.Until(expireAt) < 59*time.Minute {
		t.Fatalf("expireAt too soon: %v", expireAt)
	}
}

// TestBuildRoomUserToken 验证灵动课堂房间用户 token 可生成。
func TestBuildRoomUserToken(t *testing.T) {
	client := testClient()
	token, _, err := client.BuildRoomUserToken("room-uuid", "user-uuid", 1)
	if err != nil {
		t.Fatalf("BuildRoomUserToken: %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
}

// TestUnconfiguredClientRejects 验证未配置客户端拒绝签发 token。
func TestUnconfiguredClientRejects(t *testing.T) {
	client := NewClient(config.ShengwangConfig{})
	if client.IsConfigured() {
		t.Fatal("empty config should not be considered configured")
	}
	if _, _, err := client.BuildRTCToken("c", "u"); err == nil {
		t.Fatal("expected error for unconfigured client")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
