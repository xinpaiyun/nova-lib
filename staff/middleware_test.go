package staff

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/auth"
)

// runStaffMiddleware 直接构造请求上下文执行中间件；userID 走 Hertz 上下文的
// "user_id" 键（与 middleware.UserIDFromContext 一致），租户走 X-Tenant-Id。
func runStaffMiddleware(handler app.HandlerFunc, userID uint64, tenantHeader string) *app.RequestContext {
	c := app.NewContext(0)
	if userID > 0 {
		c.Set("user_id", userID)
	}
	if tenantHeader != "" {
		c.Request.Header.Set("X-Tenant-Id", tenantHeader)
	}
	handler(context.Background(), c)
	return c
}

func runStaffMiddlewareWithClaims(handler app.HandlerFunc, claims *auth.Claims) *app.RequestContext {
	c := app.NewContext(0)
	handler(auth.ContextWithClaims(context.Background(), claims), c)
	return c
}

func TestRequired(t *testing.T) {
	db := newStaffDB(t)
	src := NewGormSource(db)
	ctx := context.Background()

	code := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: code, SubjectKind: "therapist", SubjectRequired: true,
		ScopeEffect: ScopeEffectSelfBySubject, WorkbenchAccess: true,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}

	member := mustCreateMember(t, src, MemberInput{
		TenantID: 1, OrgID: 1, UserID: 100, Name: "技师", RoleCode: RoleStaff,
	})
	if _, err := src.AddCapability(ctx, 1, member.ID, CapabilityInput{
		TenantID: 1, MemberID: member.ID, Code: code, SubjectID: 7,
	}); err != nil {
		t.Fatalf("开启开关失败: %v", err)
	}
	disabled := mustCreateMember(t, src, MemberInput{
		TenantID: 1, OrgID: 1, UserID: 200, Name: "停用", RoleCode: RoleStaff,
	})
	if err := src.SetStatus(ctx, 1, disabled.ID, StatusDisabled); err != nil {
		t.Fatalf("禁用员工失败: %v", err)
	}
	mustCreateMember(t, src, MemberInput{
		TenantID: 1, OrgID: 1, UserID: 300, Name: "店长", RoleCode: RoleManager,
	})

	handler := Required(src)

	c := runStaffMiddleware(handler, 100, "1")
	if c.Response.StatusCode() != 200 {
		t.Fatalf("员工应放行, status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	identity, ok := FromContext(c)
	if !ok || identity.UserID != 100 || identity.TenantID != 1 {
		t.Fatalf("上下文身份错误: %+v ok=%v", identity, ok)
	}
	if identity.Scope != ScopeSelf || identity.Role.Code != RoleStaff {
		t.Fatalf("员工范围/角色错误: %+v", identity)
	}
	if got := identity.Subjects(code); len(got) != 1 || got[0] != 7 {
		t.Fatalf("档案集合错误: %v", got)
	}

	c = runStaffMiddleware(handler, 200, "1")
	if c.Response.StatusCode() != 403 || !strings.Contains(string(c.Response.Body()), ErrMemberDisabled.Error()) {
		t.Fatalf("禁用员工应 403, status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
	}

	c = runStaffMiddleware(handler, 400, "1")
	if c.Response.StatusCode() != 403 || !strings.Contains(string(c.Response.Body()), ErrNotMember.Error()) {
		t.Fatalf("非员工应 403, status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
	}

	c = runStaffMiddleware(handler, 100, "2")
	if c.Response.StatusCode() != 403 {
		t.Fatalf("跨租户应 403, status=%d", c.Response.StatusCode())
	}

	c = runStaffMiddleware(handler, 0, "1")
	if c.Response.StatusCode() != 401 {
		t.Fatalf("未登录应 401, status=%d", c.Response.StatusCode())
	}

	c = runStaffMiddleware(handler, 100, "")
	if c.Response.StatusCode() != 403 {
		t.Fatalf("缺租户应 403, status=%d", c.Response.StatusCode())
	}

	c = runStaffMiddleware(handler, 100, "abc")
	if c.Response.StatusCode() != 403 {
		t.Fatalf("非法租户头应 403, status=%d", c.Response.StatusCode())
	}
}

// TestRequiredClaimsFallback 验证未写入请求上下文时回落到登录声明。
func TestRequiredClaimsFallback(t *testing.T) {
	db := newStaffDB(t)
	src := NewGormSource(db)
	mustCreateMember(t, src, MemberInput{
		TenantID: 1, UserID: 300, Name: "店长", RoleCode: RoleManager,
	})

	c := runStaffMiddlewareWithClaims(Required(src), &auth.Claims{UserID: 300, TenantID: 1})
	if c.Response.StatusCode() != 200 {
		t.Fatalf("声明兜底应放行, status=%d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	if identity, ok := FromContext(c); !ok || identity.Scope != ScopeOrg {
		t.Fatalf("店长范围应 org: %+v ok=%v", identity, ok)
	}
}

func TestRequireRole(t *testing.T) {
	db := newStaffDB(t)
	src := NewGormSource(db)
	mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 100, Name: "员工", RoleCode: RoleStaff})
	mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 300, Name: "店长", RoleCode: RoleManager})

	staffCtx := runStaffMiddleware(Required(src), 100, "1")
	RequireRole(RoleManager)(context.Background(), staffCtx)
	if staffCtx.Response.StatusCode() != 403 {
		t.Fatalf("普通员工不应通过店长门槛, status=%d", staffCtx.Response.StatusCode())
	}

	managerCtx := runStaffMiddleware(Required(src), 300, "1")
	RequireRole(RoleManager)(context.Background(), managerCtx)
	if managerCtx.Response.StatusCode() != 200 {
		t.Fatalf("店长应通过店长门槛, status=%d", managerCtx.Response.StatusCode())
	}

	unknownCtx := runStaffMiddleware(Required(src), 300, "1")
	RequireRole("no_such_role")(context.Background(), unknownCtx)
	if unknownCtx.Response.StatusCode() != 403 {
		t.Fatalf("未注册角色门槛应 fail-closed, status=%d", unknownCtx.Response.StatusCode())
	}
}

// TestRequireCapability 验证任一命中放行，空配置拒绝。
func TestRequireCapability(t *testing.T) {
	db := newStaffDB(t)
	src := NewGormSource(db)
	ctx := context.Background()

	code := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: code, SubjectKind: "therapist", SubjectRequired: true,
		ScopeEffect: ScopeEffectSelfBySubject,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}
	member := mustCreateMember(t, src, MemberInput{
		TenantID: 1, UserID: 100, Name: "员工", RoleCode: RoleStaff,
	})
	if _, err := src.AddCapability(ctx, 1, member.ID, CapabilityInput{
		TenantID: 1, MemberID: member.ID, Code: code, SubjectID: 7,
	}); err != nil {
		t.Fatalf("开启开关失败: %v", err)
	}
	plain := mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 101, Name: "无开关", RoleCode: RoleStaff})

	held := runStaffMiddleware(Required(src), 100, "1")
	RequireCapability(code)(context.Background(), held)
	if held.Response.StatusCode() != 200 {
		t.Fatalf("持开关应放行, status=%d", held.Response.StatusCode())
	}

	RequireCapability("other", code)(context.Background(), held)
	if held.Response.StatusCode() != 200 {
		t.Fatalf("多个开关任一命中应放行, status=%d", held.Response.StatusCode())
	}

	bare := runStaffMiddleware(Required(src), plain.UserID, "1")
	RequireCapability(code)(context.Background(), bare)
	if bare.Response.StatusCode() != 403 {
		t.Fatalf("无开关应拒绝, status=%d", bare.Response.StatusCode())
	}

	empty := runStaffMiddleware(Required(src), plain.UserID, "1")
	RequireCapability()(context.Background(), empty)
	if empty.Response.StatusCode() != 403 {
		t.Fatalf("空开关配置应 fail-closed, status=%d", empty.Response.StatusCode())
	}
}

// TestRequireScope 验证范围门槛按包含关系判定。
func TestRequireScope(t *testing.T) {
	db := newStaffDB(t)
	src := NewGormSource(db)
	ctx := context.Background()

	code := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: code, SubjectKind: "therapist", SubjectRequired: true,
		ScopeEffect: ScopeEffectSelfBySubject,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}
	member := mustCreateMember(t, src, MemberInput{
		TenantID: 1, UserID: 100, Name: "员工", RoleCode: RoleStaff,
	})
	if _, err := src.AddCapability(ctx, 1, member.ID, CapabilityInput{
		TenantID: 1, MemberID: member.ID, Code: code, SubjectID: 7,
	}); err != nil {
		t.Fatalf("开启开关失败: %v", err)
	}
	mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 300, Name: "店长", RoleCode: RoleManager})

	selfCtx := runStaffMiddleware(Required(src), 100, "1")
	RequireScope(ScopeSelf)(context.Background(), selfCtx)
	if selfCtx.Response.StatusCode() != 200 {
		t.Fatalf("self 应满足 self 门槛, status=%d", selfCtx.Response.StatusCode())
	}
	RequireScope(ScopeOrg)(context.Background(), selfCtx)
	if selfCtx.Response.StatusCode() != 403 {
		t.Fatalf("self 不应满足 org 门槛, status=%d", selfCtx.Response.StatusCode())
	}

	orgCtx := runStaffMiddleware(Required(src), 300, "1")
	RequireScope(ScopeOrg)(context.Background(), orgCtx)
	if orgCtx.Response.StatusCode() != 200 {
		t.Fatalf("org 应满足 org 门槛, status=%d", orgCtx.Response.StatusCode())
	}
	RequireScope(ScopeAll)(context.Background(), orgCtx)
	if orgCtx.Response.StatusCode() != 403 {
		t.Fatalf("org 不应满足 all 门槛, status=%d", orgCtx.Response.StatusCode())
	}
}
