package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/auth"
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

// TestCORSHeadersAndMaxAge 验证 AllowHeaders 覆盖与 Max-Age 输出。
func TestCORSHeadersAndMaxAge(t *testing.T) {
	c := app.NewContext(0)
	c.Request.Header.Set("Origin", "https://admin.example.com")
	c.Request.Header.SetMethod("OPTIONS")
	c.SetHandlers(app.HandlersChain{CORS(config.CORSConfig{
		AllowedOrigins: []string{"https://admin.example.com"},
		AllowHeaders:   []string{"Content-Type", "Authorization", "X-Requested-With"},
		MaxAgeSeconds:  86400,
	})})
	c.Next(context.Background())

	if got := string(c.Response.Header.Peek("Access-Control-Allow-Origin")); got != "https://admin.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := string(c.Response.Header.Peek("Access-Control-Allow-Headers")); got != "Content-Type, Authorization, X-Requested-With" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
	if got := string(c.Response.Header.Peek("Access-Control-Max-Age")); got != "86400" {
		t.Fatalf("Access-Control-Max-Age = %q, want 86400", got)
	}
	if c.Response.StatusCode() != 204 {
		t.Fatalf("preflight status = %d, want 204", c.Response.StatusCode())
	}
}

// TestCORSDefaultHeadersKeepBackwardCompatible 验证未配置时保持默认允许头且不输出 Max-Age。
func TestCORSDefaultHeadersKeepBackwardCompatible(t *testing.T) {
	c := app.NewContext(0)
	c.SetHandlers(app.HandlersChain{CORS(config.CORSConfig{})})
	c.Next(context.Background())

	if got := string(c.Response.Header.Peek("Access-Control-Allow-Headers")); got != "Authorization, Content-Type, X-Request-ID, X-Tenant-ID, X-Tenant-Domain" {
		t.Fatalf("Access-Control-Allow-Headers = %q", got)
	}
	if got := string(c.Response.Header.Peek("Access-Control-Max-Age")); got != "" {
		t.Fatalf("Access-Control-Max-Age = %q, want empty", got)
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

// stubFailingStore 模拟 Redis 连接故障等基础设施错误。
type stubFailingStore struct{}

func (stubFailingStore) SetJSON(context.Context, string, any, time.Duration) error {
	return errors.New("redis connection refused")
}

func (stubFailingStore) GetJSON(context.Context, string, any) (bool, error) {
	return false, errors.New("redis connection refused")
}

func (stubFailingStore) Del(context.Context, string) error {
	return errors.New("redis connection refused")
}

// TestRequireSessionAuthDistinguishesAuthFromInfra 验证会话失效返回 401、
// 基础设施故障返回 500，Redis 故障不再被伪装成「登录状态已失效」。
func TestRequireSessionAuthDistinguishesAuthFromInfra(t *testing.T) {
	auth.SetCache(auth.NewMemoryStore())
	t.Cleanup(func() { auth.SetCache(nil) })

	// run 执行一次经过鉴权中间件的请求，返回状态码、响应体和请求上下文。
	run := func(t *testing.T, authorization string) (int, string, *app.RequestContext) {
		t.Helper()
		c := app.NewContext(0)
		if authorization != "" {
			c.Request.Header.Set("Authorization", authorization)
		}
		c.Set(requestIDKey, "req-auth")
		c.SetHandlers(app.HandlersChain{
			RequireSessionAuth(),
			func(_ context.Context, c *app.RequestContext) {
				c.Status(200)
			},
		})
		c.Next(context.Background())
		return c.Response.StatusCode(), string(c.Response.Body()), c
	}

	// 未携带 Token → 401 请先登录
	if status, body, _ := run(t, ""); status != 401 || !strings.Contains(body, "请先登录") {
		t.Fatalf("missing token: status=%d body=%q", status, body)
	}

	// 会话不存在 → 401 登录状态已失效（业务语义）
	if status, body, _ := run(t, "Bearer no-such-token"); status != 401 || !strings.Contains(body, "登录状态已失效") {
		t.Fatalf("unknown token: status=%d body=%q", status, body)
	}

	// 有效会话 → 200，身份声明写入请求上下文
	err := auth.StoreSession(context.Background(), "valid-token", auth.Session{
		UserID:    42,
		TenantID:  7,
		RoleCode:  "super_admin",
		AppType:   "admin",
		OpenID:    "open-abc",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("store session err=%v", err)
	}
	status, body, c := run(t, "Bearer valid-token")
	if status != 200 {
		t.Fatalf("valid token: status=%d body=%q", status, body)
	}
	if userID, ok := UserIDFromContext(c); !ok || userID != 42 {
		t.Fatalf("UserIDFromContext() = (%d, %v), want (42, true)", userID, ok)
	}
	if RoleCodeFromContext(c) != "super_admin" || AppTypeFromContext(c) != "admin" {
		t.Fatalf("identity mismatch: role=%q appType=%q", RoleCodeFromContext(c), AppTypeFromContext(c))
	}

	// Redis 故障 → 500 服务暂时不可用（基础设施错误必须暴露，不能返回 401）
	auth.SetCache(stubFailingStore{})
	if status, body, _ := run(t, "Bearer valid-token"); status != 500 || !strings.Contains(body, "服务暂时不可用") {
		t.Fatalf("infra failure: status=%d body=%q", status, body)
	}
}
