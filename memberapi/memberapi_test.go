package memberapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route/param"
	"github.com/glebarez/sqlite"
	"github.com/xinpaiyun/nova-lib/identity"
	"github.com/xinpaiyun/nova-lib/member"
	"github.com/xinpaiyun/nova-lib/staff"
	"github.com/xinpaiyun/nova-lib/staffapi"
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
	staff   *staff.Service
	src     staff.Source
	svc     *member.CustomerService
	api     *MemberAPI
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
	models := append(staff.ModelTypes(), member.CustomerModelTypes()...)
	models = append(models, member.ModelTypes()...)
	models = append(models, &identity.User{})
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("迁移表失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.Create(&identity.User{ID: 900, Phone: "13800000900", PasswordHash: "x"}).Error; err != nil {
		t.Fatalf("创建管理员用户失败: %v", err)
	}
	staffSvc := staff.NewService(db)
	role, _ := staff.ResolveRole(staff.RoleManager)
	manager := &staff.Identity{TenantID: 1, MemberID: 1, Role: role}
	if _, err := staffSvc.CreateMember(context.Background(), manager, staff.CreateMemberInput{
		UserID: 900, Name: "店长", RoleCode: staff.RoleManager,
	}); err != nil {
		t.Fatalf("创建管理员员工失败: %v", err)
	}

	return &apiEnv{
		db:      db,
		staff:   staffSvc,
		src:     staff.NewGormSource(db),
		svc:     member.NewCustomerService(db),
		api:     NewMemberAPI(member.NewCustomerService(db)),
		manager: manager,
	}
}

func seedUser(t *testing.T, db *gorm.DB, userID uint64) {
	t.Helper()
	if err := db.Create(&identity.User{
		ID: userID, Phone: fmt.Sprintf("139%08d", userID), PasswordHash: "x",
	}).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
}

// request 构造请求；withStaff 为 true 时先跑 staff.Required 以模拟管理端路由。
func request(env *apiEnv, handler app.HandlerFunc, userID uint64, tenant string, body any, params map[string]string, withStaff bool) *app.RequestContext {
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
	if withStaff {
		staff.Required(env.src)(ctx, c)
	}
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

func TestCreateCustomerContract(t *testing.T) {
	env := newAPIEnv(t)

	// user_id 一律取自会话，请求体里的 userId 必须被忽略。
	c := request(env, env.api.Create, 3001, "1", map[string]any{
		"userId": 9999, "name": "扫码客户", "orgId": 5,
	}, nil, false)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("扫码入会应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var customer member.CustomerMember
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &customer); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if customer.UserID != 3001 || customer.Source != member.CustomerSourceScanQR {
		t.Fatalf("会话 user_id 应生效且来源为 scan_qr: %+v", customer)
	}

	// 重复扫码幂等，返回同一条档案。
	c = request(env, env.api.Create, 3001, "1", map[string]any{"name": "改名无效"}, nil, false)
	body := parseEnvelope(t, c)
	var again member.CustomerMember
	if err := json.Unmarshal(body.Data, &again); err != nil || again.ID != customer.ID {
		t.Fatalf("重复扫码应幂等: %+v err=%v", again, err)
	}

	// 缺租户直接 400。
	c = request(env, env.api.Create, 3001, "", map[string]any{}, nil, false)
	if c.Response.StatusCode() != 400 {
		t.Fatalf("缺租户应 400, got %d", c.Response.StatusCode())
	}
}

func TestCreateByPhoneContract(t *testing.T) {
	env := newAPIEnv(t)

	c := request(env, env.api.CreateByPhone, 900, "1", map[string]any{
		"phone": "13700000001", "name": "未注册客户",
	}, nil, true)
	if c.Response.StatusCode() != 400 {
		t.Fatalf("未注册手机号应 400, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	if body := parseEnvelope(t, c); body.Code != staffapi.CodeUserNotRegistered {
		t.Fatalf("应返回 1301, got %+v", body)
	}

	seedUser(t, env.db, 4001)
	c = request(env, env.api.CreateByPhone, 900, "1", map[string]any{
		"phone": "13900004001", "name": "后台客户",
	}, nil, true)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("后台建档应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var customer member.CustomerMember
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &customer); err != nil || customer.UserID != 4001 {
		t.Fatalf("后台建档结果错误: %+v err=%v", customer, err)
	}
	if customer.Source != member.CustomerSourceStaffCreated {
		t.Fatalf("后台建档来源应为 staff_created: %+v", customer)
	}

	// 非管理角色不得后台建档。
	seedUser(t, env.db, 4002)
	if _, err := env.staff.CreateMember(context.Background(), env.manager, staff.CreateMemberInput{
		UserID: 4002, Name: "店员", RoleCode: staff.RoleStaff,
	}); err != nil {
		t.Fatalf("创建店员失败: %v", err)
	}
	c = request(env, env.api.CreateByPhone, 4002, "1", map[string]any{
		"phone": "13900004001", "name": "越权",
	}, nil, true)
	if c.Response.StatusCode() != 403 {
		t.Fatalf("店员后台建档应 403, got %d", c.Response.StatusCode())
	}
}

func TestMergeContract(t *testing.T) {
	env := newAPIEnv(t)
	ctx := context.Background()
	into, err := env.svc.Create(ctx, member.CustomerInput{TenantID: 1, UserID: 3101, Name: "保留"})
	if err != nil {
		t.Fatalf("创建目标会员失败: %v", err)
	}
	from, err := env.svc.Create(ctx, member.CustomerInput{TenantID: 1, UserID: 3102, Name: "被并"})
	if err != nil {
		t.Fatalf("创建被并会员失败: %v", err)
	}

	c := request(env, env.api.Merge, 900, "1", map[string]any{
		"fromIds": []uint64{from.ID},
	}, map[string]string{"id": fmt.Sprint(into.ID)}, true)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("合并应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var result member.MergeResult
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &result); err != nil {
		t.Fatalf("data 解析失败: %v", err)
	}
	if result.Into == nil || result.Into.ID != into.ID || len(result.Merged) != 1 || result.Merged[0] != from.ID {
		t.Fatalf("合并结果错误: %+v", result)
	}

	// 重复合并同一目标幂等。
	c = request(env, env.api.Merge, 900, "1", map[string]any{
		"fromIds": []uint64{from.ID},
	}, map[string]string{"id": fmt.Sprint(into.ID)}, true)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("重复合并应幂等 200, got %d", c.Response.StatusCode())
	}

	// 目标不存在 404。
	c = request(env, env.api.Merge, 900, "1", map[string]any{
		"fromIds": []uint64{from.ID},
	}, map[string]string{"id": "9999"}, true)
	if c.Response.StatusCode() != 404 {
		t.Fatalf("目标不存在应 404, got %d", c.Response.StatusCode())
	}

	// 合并到自己 400。
	c = request(env, env.api.Merge, 900, "1", map[string]any{
		"fromIds": []uint64{into.ID},
	}, map[string]string{"id": fmt.Sprint(into.ID)}, true)
	if c.Response.StatusCode() != 400 {
		t.Fatalf("合并到自己应 400, got %d", c.Response.StatusCode())
	}
}

func TestListContract(t *testing.T) {
	env := newAPIEnv(t)
	for i := uint64(3201); i <= 3203; i++ {
		if _, err := env.svc.Create(context.Background(), member.CustomerInput{TenantID: 1, UserID: i}); err != nil {
			t.Fatalf("创建会员失败: %v", err)
		}
	}

	c := request(env, env.api.List, 900, "1", nil, nil, true)
	if c.Response.StatusCode() != 200 {
		t.Fatalf("列表应 200, got %d body=%s", c.Response.StatusCode(), c.Response.Body())
	}
	var list CustomerListResponse
	if err := json.Unmarshal(parseEnvelope(t, c).Data, &list); err != nil || list.Total != 3 {
		t.Fatalf("列表结果错误: %+v err=%v", list, err)
	}

	// 非员工身份不得查会员列表。
	c = request(env, env.api.List, 3201, "1", nil, nil, true)
	if c.Response.StatusCode() != 403 {
		t.Fatalf("非员工查列表应 403, got %d", c.Response.StatusCode())
	}
}
