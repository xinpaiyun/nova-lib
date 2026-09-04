package logging

import (
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

// TestInitSetsServiceAndDebug 初始化后 service 字段与调试开关生效。
func TestInitSetsServiceAndDebug(t *testing.T) {
	Init("demo-api", "dev")
	if serviceName != "demo-api" {
		t.Fatalf("serviceName = %q, want demo-api", serviceName)
	}
	if !DebugEnabled() {
		t.Fatalf("DebugEnabled() = false, want true for dev mode")
	}
	Init("demo-api", "release")
	if DebugEnabled() {
		t.Fatalf("DebugEnabled() = true, want false for release mode")
	}
}

// TestEnsureRequestID 验证请求追踪 ID 的补齐与复用。
func TestEnsureRequestID(t *testing.T) {
	c := app.NewContext(0)
	first := EnsureRequestID(c)
	if first == "" {
		t.Fatalf("EnsureRequestID() returned empty id")
	}
	if got := EnsureRequestID(c); got != first {
		t.Fatalf("EnsureRequestID() = %q, want stable %q", got, first)
	}
	if got := c.Response.Header.Get(RequestIDHeader); got != first {
		t.Fatalf("response header = %q, want %q", got, first)
	}
}

// TestBuildFields 验证键归一化、service 字段和错误对象序列化。
func TestBuildFields(t *testing.T) {
	fields := buildFields("Request ID", "req-1", "err", strings.NewReader("ignored"))
	if fields["service"] != serviceName {
		t.Fatalf("fields[service] = %v, want %q", fields["service"], serviceName)
	}
	if fields["request_id"] != "req-1" {
		t.Fatalf("fields[request_id] = %v, want req-1", fields["request_id"])
	}
}
