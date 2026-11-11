package staffapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route/param"
	"github.com/glebarez/sqlite"
	"github.com/xinpaiyun/nova-lib/identity"
	"github.com/xinpaiyun/nova-lib/staff"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type envelope struct {
	Code      int             `json:"code"`
	ErrorCode string          `json:"errorCode"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
}

type apiEnv struct {
	db      *gorm.DB
	svc     *staff.Service
	src     staff.Source
	api     *StaffAPI
	manager *staff.Identity
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(staff.ModelTypes()...); err != nil {
		t.Fatalf("迁移员工表失败: %v", err)
	}
	if err := db.AutoMigrate(&identity.User{}); err != nil {
		t.Fatalf("迁移 users 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	svc := staff.NewService(db)
	role, _ := staff.ResolveRole(staff.RoleManager)
	manager := &staff.Identity{TenantID: 1, MemberID: 1, Role: role}
	seedAPIMember(t, db, svc, manager, 900, staff.RoleManager)

	return &apiEnv{db: db, svc: svc, src: staff.NewGormSource(db), api: NewStaffAPI(svc), manager: manager}
}

func seedAPIMember(t *testing.T, db *gorm.DB, svc *staff.Service, actor *staff.Identity, userID uint64, role string) *staff.StaffMember {
	t.Helper()
	if err := db.Create(&identity.User{
		ID: userID, Phone: fmt.Sprintf("138%08d", userID), PasswordHash: "x",
	}).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	member, err := svc.CreateMember(context.Background(), actor, staff.CreateMemberInput{
		UserID: userID, Name: fmt.Sprintf("员工%d", userID), RoleCode: role,
	})
	if err != nil {
		t.Fatalf("创建员工失败: %v", err)
	}
	return member
}

func request(env *apiEnv, handler app.HandlerFunc, userID uint64, tenant string, body any, params map[string]string) *app.RequestContext {
	c := app.NewContext(0)
	if userID > 0 {
		c.Set("user_id", userID)
	}
	if tenant != "" {
		c.Request.Header.Set("X-Tenant-Id", tenant)
	}
	if body != nil {
		payload, _ := json.Marshal(body)
		c.Request.SetBody(payload)
	}
	for key, value := range params {
		c.Params = append(c.Params, param.Param{Key: key, Value: value})
	}
	ctx := context.Background()
	staff.Required(env.src)(ctx, c)
	handler(ctx, c)
	return c
}

func parseEnvelope(t *testing.T, c *app.RequestContext) envelope {
	t.Helper()
	var body envelope
	if err := json.Unmarshal(c.Response.Body(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", c.Response.Body())
	}
	return body
}

func TestCapabilityCatalogContract(t *testing.T) {
	env := newAPIEnv(t)
	code := fmt.Sprintf("test_%s", t.Name())
	if err := staff.RegisterCapability(staff.CapabilitySpec{
		Code: code, Name: "服务者", SubjectKind: "therapist",
		SubjectRequired: true, ScopeEffect: staff.ScopeEffectSelfBySubject, WorkbenchAccess: true,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}

	c := request(env, env.api.ListCapabilities, 900, "1", nil, nil)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("开关列表应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	body := parseEnvelope(t, c)
	var options []CapabilityOption
	if err := json.Unmarshal(body.Data, &options); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	var found *CapabilityOption
	for i := range options {
		if options[i].Code == code {
			found = &options[i]
		}
	}
	if found == nil || !found.SubjectRequired || found.SubjectKind != "therapist" || !found.WorkbenchAccess {
		t.Fatalf("开关契约字段错误: %+v", found)
	}
}

func TestCreateMemberContract(t *testing.T) {
	env := newAPIEnv(t)

	c := request(env, env.api.CreateMember, 900, "1", map[string]any{
		"phone": "13900000001", "name": "张三", "roleCode": staff.RoleStaff,
	}, nil)
	if c.Response.StatusCode() != 400 {
		t.Fatalf("未注册手机号应 400, got %d", c.Response.StatusCode())
	}
	body := parseEnvelope(t, c)
	if body.Code != CodeUserNotRegistered || body.ErrorCode != StableUserNotRegistered {
		t.Fatalf("1301 契约错误: %+v", body)
	}

	if err := env.db.Create(&identity.User{ID: 1001, Phone: "13900000002", PasswordHash: "x"}).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	c = request(env, env.api.CreateMember, 900, "1", map[string]any{
		"phone": "13900000002", "name": "李四", "roleCode": staff.RoleStaff,
	}, nil)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("建员工应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var member staff.StaffMember
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &member); err != nil || member.UserID != 1001 {
		t.Fatalf("建员工响应错误: %+v err=%v", member, err)
	}

	seedAPIMember(t, env.db, env.svc, env.manager, 1002, staff.RoleStaff)
	c = request(env, env.api.CreateMember, 1002, "1", map[string]any{
		"userID": 1001, "name": "无权限",
	}, nil)
	if c.Response.StatusCode() != 403 {
		t.Fatalf("普通员工建号应 403, got %d", c.Response.StatusCode())
	}
}

func TestSetCapabilityContract(t *testing.T) {
	env := newAPIEnv(t)
	code := fmt.Sprintf("test_%s", t.Name())
	if err := staff.RegisterCapability(staff.CapabilitySpec{
		Code: code, SubjectKind: "therapist", SubjectRequired: true,
		ScopeEffect: staff.ScopeEffectSelfBySubject,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}
	m1 := seedAPIMember(t, env.db, env.svc, env.manager, 1101, staff.RoleStaff)
	m2 := seedAPIMember(t, env.db, env.svc, env.manager, 1102, staff.RoleStaff)

	c := request(env, env.api.SetCapability, 900, "1", map[string]any{
		"code": code, "subjectId": 7, "enabled": true,
	}, map[string]string{"id": fmt.Sprint(m1.ID)})
	if c.Response.StatusCode() != 200 {
		t.Fatalf("开启开关应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var capability staff.StaffCapability
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &capability); err != nil || capability.SubjectID != 7 {
		t.Fatalf("开关响应错误: %+v err=%v", capability, err)
	}

	c = request(env, env.api.SetCapability, 900, "1", map[string]any{
		"code": code, "subjectId": 7, "enabled": true,
	}, map[string]string{"id": fmt.Sprint(m2.ID)})
	if c.Response.StatusCode() != 409 || parseEnvelope(t, c).Code != CodeCapabilitySubjectTaken {
		t.Fatalf("档案占用应 409/1304, got %d", c.Response.StatusCode())
	}

	c = request(env, env.api.SetCapability, 900, "1", map[string]any{
		"code": code, "enabled": true,
	}, map[string]string{"id": fmt.Sprint(m1.ID)})
	if parseEnvelope(t, c).Code != CodeCapabilitySubjectRequired {
		t.Fatalf("缺档案应 1303, got %+v", parseEnvelope(t, c))
	}

	c = request(env, env.api.SetCapability, 900, "1", map[string]any{
		"code": "no_such", "enabled": true,
	}, map[string]string{"id": fmt.Sprint(m1.ID)})
	if parseEnvelope(t, c).Code != CodeCapabilitySpecUnknown {
		t.Fatalf("未注册开关应 1302, got %+v", parseEnvelope(t, c))
	}

	c = request(env, env.api.SetCapability, 900, "1", map[string]any{
		"code": code, "subjectId": 7, "enabled": false,
	}, map[string]string{"id": fmt.Sprint(m1.ID)})
	if c.Response.StatusCode() != 200 {
		t.Fatalf("关闭开关应 200, got %d", c.Response.StatusCode())
	}
}

func TestUpdateMemberContract(t *testing.T) {
	env := newAPIEnv(t)
	role, _ := staff.ResolveRole(staff.RoleStaff)
	member, err := env.svc.CreateMember(context.Background(), env.manager, staff.CreateMemberInput{
		UserID: 1201, Name: "原名", EmployeeNo: "E1", Position: "P1", RoleCode: staff.RoleStaff,
	})
	if err != nil {
		t.Fatalf("创建员工失败: %v", err)
	}
	_ = role
	if err := env.db.Create(&identity.User{ID: 1201, Phone: "13700001201", PasswordHash: "x"}).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}

	c := request(env, env.api.UpdateMember, 900, "1", map[string]any{"name": "新名"},
		map[string]string{"id": fmt.Sprint(member.ID)})
	if c.Response.StatusCode() != 200 {
		t.Fatalf("更新应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var updated staff.StaffMember
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &updated); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if updated.Name != "新名" || updated.EmployeeNo != "E1" || updated.Position != "P1" {
		t.Fatalf("未传字段应保持原值: %+v", updated)
	}

	c = request(env, env.api.UpdateMember, 900, "1", map[string]any{"status": 0},
		map[string]string{"id": fmt.Sprint(member.ID)})
	if c.Response.StatusCode() != 200 || parseEnvelope(t, c).Data == nil {
		t.Fatalf("改状态应 200, got %d", c.Response.StatusCode())
	}

	c = request(env, env.api.UpdateMember, 900, "1", map[string]any{"name": "x"},
		map[string]string{"id": "abc"})
	if c.Response.StatusCode() != 400 {
		t.Fatalf("非法 ID 应 400, got %d", c.Response.StatusCode())
	}

	c = request(env, env.api.UpdateMember, 900, "1", map[string]any{"name": "x"},
		map[string]string{"id": "9999"})
	if c.Response.StatusCode() != 404 {
		t.Fatalf("不存在的员工应 404, got %d", c.Response.StatusCode())
	}
}

func TestListAndIdentitiesContract(t *testing.T) {
	env := newAPIEnv(t)
	seedAPIMember(t, env.db, env.svc, env.manager, 1301, staff.RoleStaff)
	tenant2Role, _ := staff.ResolveRole(staff.RoleManager)
	tenant2 := &staff.Identity{TenantID: 2, MemberID: 2, Role: tenant2Role}
	if _, err := env.svc.CreateMember(context.Background(), tenant2, staff.CreateMemberInput{
		UserID: 1301, Name: "跨租户", RoleCode: staff.RoleManager,
	}); err != nil {
		t.Fatalf("创建跨租户员工失败: %v", err)
	}

	c := request(env, env.api.ListMembers, 900, "1", nil, nil)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("列表应 200, got %d", c.Response.StatusCode())
	}
	var list MemberListResponse
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &list); err != nil || list.Total != 2 {
		t.Fatalf("列表响应错误: %+v err=%v", list, err)
	}

	c = request(env, env.api.ListIdentities, 1301, "1", nil, nil)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("商家态列表应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var identities []IdentityOption
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &identities); err != nil || len(identities) != 2 {
		t.Fatalf("商家态列表错误: %+v err=%v", identities, err)
	}
}
