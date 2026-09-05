package rbac

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/middleware"
)

// setIdentity 按统一身份上下文 key（与 middleware.writeIdentity 对齐的字面量）注入身份快照。
func setIdentity(c *app.RequestContext, userID, tenantID uint64, roleCode, appType, openID string) {
	c.Set("user_id", userID)
	c.Set("tenant_id", tenantID)
	c.Set("role_code", roleCode)
	c.Set("app_type", appType)
	c.Set("open_id", openID)
}

// runChain 依次执行中间件并返回最终状态码；
// Abort() 会终止链路，故错误响应状态码由 response.Error 直接写入 c.Response，
// 未写（成功放行）时归一化为 200。
func runChain(c *app.RequestContext, handlers ...app.HandlerFunc) int {
	c.SetHandlers(handlers)
	c.Next(context.Background())
	if code := c.Response.StatusCode(); code != 0 {
		return code
	}
	return 200
}

func TestRequireRoles(t *testing.T) {
	// 未登录 → 401
	c := app.NewContext(0)
	if got := runChain(c, RequireRoles("admin")); got != 401 {
		t.Fatalf("unauthenticated status = %d, want 401", got)
	}
	// 角色命中 → 200
	c = app.NewContext(0)
	setIdentity(c, 7, 0, "admin", "admin", "")
	if got := runChain(c, RequireRoles("admin", "super_admin")); got != 200 {
		t.Fatalf("allowed status = %d, want 200", got)
	}
	// 角色不命中 → 403
	c = app.NewContext(0)
	setIdentity(c, 7, 0, "agent", "admin", "")
	if got := runChain(c, RequireRoles("admin")); got != 403 {
		t.Fatalf("denied status = %d, want 403", got)
	}
	// 空 allow → 已登录即放行
	c = app.NewContext(0)
	setIdentity(c, 7, 0, "agent", "admin", "")
	if got := runChain(c, RequireRoles()); got != 200 {
		t.Fatalf("empty-allow status = %d, want 200", got)
	}
}

func TestPermissionRequired(t *testing.T) {
	allow := func(_ context.Context, _ Identity, _ string) (bool, string) { return true, "" }
	deny := func(_ context.Context, _ Identity, _ string) (bool, string) { return false, "团队数据范围外" }

	// 未登录 → 401
	c := app.NewContext(0)
	if got := runChain(c, PermissionRequired(allow, "order:read", nil)); got != 401 {
		t.Fatalf("unauthenticated status = %d, want 401", got)
	}
	// checker 放行 → 200
	c = app.NewContext(0)
	setIdentity(c, 7, 0, "admin", "admin", "")
	if got := runChain(c, PermissionRequired(allow, "order:read", nil)); got != 200 {
		t.Fatalf("allowed status = %d, want 200", got)
	}
	// checker 拒绝 → 403 + 业务拒绝文案
	c = app.NewContext(0)
	setIdentity(c, 7, 0, "admin", "admin", "")
	if got := runChain(c, PermissionRequired(deny, "order:read", nil)); got != 403 {
		t.Fatalf("denied status = %d, want 403", got)
	}
	// nil checker → 已登录即放行
	c = app.NewContext(0)
	setIdentity(c, 7, 0, "admin", "admin", "")
	if got := runChain(c, PermissionRequired(nil, "order:read", nil)); got != 200 {
		t.Fatalf("nil-checker status = %d, want 200", got)
	}
	// resolver 重新解析角色码并入会话
	role := "super_admin"
	resolver := func(_ context.Context, _ Identity) (string, error) { return role, nil }
	c = app.NewContext(0)
	setIdentity(c, 7, 0, "anonymous", "admin", "")
	if got := runChain(c, PermissionRequired(allow, "order:read", resolver)); got != 200 {
		t.Fatalf("resolver status = %d, want 200", got)
	}
	_ = middleware.TenantIDFromContext // 保持 import 引用（identity 组装经此读取）
}

func TestRequireDataScope(t *testing.T) {
	checker := func(_ context.Context, ident Identity, scope string) (bool, string) {
		return ident.TenantID == 9 && scope == "team:data", ""
	}
	// 无 checker → 放行
	c := app.NewContext(0)
	if got := runChain(c, RequireDataScope(nil, "team:data")); got != 200 {
		t.Fatalf("nil-checker status = %d, want 200", got)
	}
	// 命中 → 200
	c = app.NewContext(0)
	setIdentity(c, 7, 9, "member", "admin", "")
	if got := runChain(c, RequireDataScope(checker, "team:data")); got != 200 {
		t.Fatalf("allowed status = %d, want 200", got)
	}
	// 不命中 → 403
	c = app.NewContext(0)
	setIdentity(c, 7, 3, "member", "admin", "")
	if got := runChain(c, RequireDataScope(checker, "team:data")); got != 403 {
		t.Fatalf("denied status = %d, want 403", got)
	}
}