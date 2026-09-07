package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/config"
)

// TestNormalizeRequestID 验证请求追踪 ID 会被限制长度和字符集。
func TestNormalizeRequestID(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "valid", value: "req-20260826_A.1", want: "req-20260826_A.1"},
		{name: "trim", value: " req-1 ", want: "req-1"},
		{name: "empty", value: "   ", want: ""},
		{name: "newline", value: "req-1\nstatus=200", want: ""},
		{name: "slash", value: "trace/1", want: ""},
		{name: "too long", value: strings.Repeat("a", maxRequestID+1), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeRequestID(tt.value); got != tt.want {
				t.Fatalf("normalizeRequestID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveAllowedOrigin 验证 CORS 来源匹配策略。
func TestResolveAllowedOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		cfg    config.CORSConfig
		want   string
		ok     bool
	}{
		{
			name: "default wildcard",
			cfg:  config.CORSConfig{},
			want: "*",
			ok:   true,
		},
		{
			name:   "wildcard with credentials echoes origin",
			origin: "https://admin.example.com",
			cfg: config.CORSConfig{
				AllowedOrigins:   []string{"*"},
				AllowCredentials: true,
			},
			want: "https://admin.example.com",
			ok:   true,
		},
		{
			name:   "allowed explicit origin",
			origin: "https://admin.example.com",
			cfg: config.CORSConfig{
				AllowedOrigins: []string{"https://admin.example.com"},
			},
			want: "https://admin.example.com",
			ok:   true,
		},
		{
			name:   "reject unknown origin",
			origin: "https://evil.example.com",
			cfg: config.CORSConfig{
				AllowedOrigins: []string{"https://admin.example.com"},
			},
			want: "",
			ok:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveAllowedOrigin(tt.origin, tt.cfg)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("resolveAllowedOrigin() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestSecurityHeaders 验证默认安全响应头可按配置启停。
func TestSecurityHeaders(t *testing.T) {
	c := app.NewContext(0)
	c.SetHandlers(app.HandlersChain{SecurityHeaders(config.SecurityHeadersConfig{Enabled: true})})
	c.Next(context.Background())
	if got := string(c.Response.Header.Peek("X-Content-Type-Options")); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := string(c.Response.Header.Peek("X-Frame-Options")); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}

	disabled := app.NewContext(0)
	disabled.SetHandlers(app.HandlersChain{SecurityHeaders(config.SecurityHeadersConfig{Enabled: false})})
	disabled.Next(context.Background())
	if got := string(disabled.Response.Header.Peek("X-Content-Type-Options")); got != "" {
		t.Fatalf("disabled X-Content-Type-Options = %q, want empty", got)
	}
}

// TestRecoveryWritesGenericError 验证 panic 会被统一错误响应兜底且不泄露 panic 内容。
func TestRecoveryWritesGenericError(t *testing.T) {
	c := app.NewContext(0)
	c.Set(requestIDKey, "req-recovery")
	c.SetHandlers(app.HandlersChain{
		Recovery(),
		func(_ context.Context, _ *app.RequestContext) {
			panic("database password leaked")
		},
	})

	c.Next(context.Background())

	body := string(c.Response.Body())
	if c.Response.StatusCode() != 500 {
		t.Fatalf("status = %d, want 500", c.Response.StatusCode())
	}
	for _, want := range []string{"服务暂时不可用", "req-recovery"} {
		if !strings.Contains(body, want) {
			t.Fatalf("response body = %q, want contains %q", body, want)
		}
	}
	if strings.Contains(body, "database password leaked") {
		t.Fatalf("response body leaked panic content: %q", body)
	}
}

// TestPanicValue 验证 panic 日志值格式化兼容字符串和错误对象。
func TestPanicValue(t *testing.T) {
	if got := panicValue("panic message"); got != "panic message" {
		t.Fatalf("panicValue(string) = %q", got)
	}
	if got := panicValue(errors.New("panic error")); got != "panic error" {
		t.Fatalf("panicValue(error) = %q", got)
	}
}

// TestTenantFromHeaderAndContext 验证租户请求头解析与上下文读取。
func TestTenantFromHeaderAndContext(t *testing.T) {
	c := app.NewContext(0)
	c.Request.Header.Set("X-Tenant-ID", "42")
	TenantFromHeader()(context.Background(), c)
	if tenantID, ok := TenantIDFromContext(c); !ok || tenantID != 42 {
		t.Fatalf("TenantIDFromContext() = (%d, %v), want (42, true)", tenantID, ok)
	}

	missing := app.NewContext(0)
	TenantFromHeader()(context.Background(), missing)
	if _, ok := TenantIDFromContext(missing); ok {
		t.Fatalf("TenantIDFromContext() ok = true, want false for missing header")
	}
}

// TestNormalizeHostDomain 验证 Host 域名规范化并跳过本地开发 Host。
func TestNormalizeHostDomain(t *testing.T) {
	if got := normalizeHostDomain("Tenant.Example.com.:443"); got != "tenant.example.com" {
		t.Fatalf("normalizeHostDomain() = %q", got)
	}
	for _, host := range []string{"localhost:8080", "127.0.0.1", "[::1]"} {
		if got := normalizeHostDomain(host); got != "" {
			t.Fatalf("normalizeHostDomain(%q) = %q, want empty", host, got)
		}
	}
}

// TestAccessLogWithConfigRecordsAndWarns 验证访问日志回调与慢请求告警路径。
func TestAccessLogWithConfigRecordsAndWarns(t *testing.T) {
	called := false
	recorder := func(c *app.RequestContext, method string, path string, status int, latency time.Duration) {
		called = true
		if method != "POST" || path != "/v1/slow" || status != 200 {
			t.Fatalf("recorder args = (%s, %s, %d), want (POST, /v1/slow, 200)", method, path, status)
		}
	}
	c := app.NewContext(0)
	c.Request.SetMethod("POST")
	c.Request.URI().SetPath("/v1/slow")
	c.Set(requestIDKey, "req-slow")
	c.SetHandlers(app.HandlersChain{
		AccessLogWithConfig(AccessLogOptions{Recorder: recorder, SlowThreshold: time.Nanosecond}),
		func(_ context.Context, _ *app.RequestContext) {
			time.Sleep(2 * time.Millisecond)
		},
	})
	c.Next(context.Background())

	if !called {
		t.Fatalf("recorder should be invoked")
	}
	if c.Response.StatusCode() != 200 {
		t.Fatalf("status = %d, want 200", c.Response.StatusCode())
	}
}

// TestAccessLogWithRecorderDelegatesToConfig 验证旧入口与新配置入口行为一致。
func TestAccessLogWithRecorderDelegatesToConfig(t *testing.T) {
	called := false
	c := app.NewContext(0)
	c.SetHandlers(app.HandlersChain{
		AccessLogWithRecorder(func(_ *app.RequestContext, _ string, _ string, _ int, _ time.Duration) {
			called = true
		}),
	})
	c.Next(context.Background())
	if !called {
		t.Fatalf("recorder should be invoked")
	}
}

// TestAccessLogSkipPaths 验证跳过路径不触发访问日志回调与告警。
func TestAccessLogSkipPaths(t *testing.T) {
	called := false
	c := app.NewContext(0)
	c.Request.URI().SetPath("/v1/health")
	c.SetHandlers(app.HandlersChain{
		AccessLogWithConfig(AccessLogOptions{
			Recorder: func(_ *app.RequestContext, _ string, _ string, _ int, _ time.Duration) {
				called = true
			},
			SlowThreshold: time.Nanosecond,
			SkipPaths:     []string{"/v1/health"},
		}),
		func(_ context.Context, _ *app.RequestContext) {
			time.Sleep(2 * time.Millisecond)
		},
	})
	c.Next(context.Background())

	if called {
		t.Fatalf("recorder should be skipped for path in SkipPaths")
	}

	// 不在跳过列表的路径仍正常记录。
	other := false
	normal := app.NewContext(0)
	normal.Request.URI().SetPath("/v1/users")
	normal.SetHandlers(app.HandlersChain{
		AccessLogWithConfig(AccessLogOptions{
			Recorder: func(_ *app.RequestContext, _ string, _ string, _ int, _ time.Duration) {
				other = true
			},
			SkipPaths: []string{"/v1/health"},
		}),
	})
	normal.Next(context.Background())
	if !other {
		t.Fatalf("recorder should be invoked for path not in SkipPaths")
	}
}
