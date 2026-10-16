package middleware

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/xinpaiyun/nova-lib/org"
)

func newMemberTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "member.db")), &gorm.Config{
		SkipDefaultTransaction:                   true,
		Logger:                                   gormlogger.Default.LogMode(gormlogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		NamingStrategy:                           schema.NamingStrategy{},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(org.ModelTypes()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// runTenantMember 直接构造请求上下文执行中间件，返回 (状态码, 响应体, 上下文租户 ID)。
func runTenantMember(t *testing.T, handler app.HandlerFunc, userID uint64, tenantHeader string) (int, string, uint64) {
	t.Helper()
	c := app.NewContext(0)
	if userID > 0 {
		c.Set(userIDKey, userID)
	}
	if tenantHeader != "" {
		c.Request.Header.Set("X-Tenant-Id", tenantHeader)
	}
	handler(context.Background(), c)
	tenantID, _ := TenantIDFromContext(c)
	return c.Response.StatusCode(), string(c.Response.Body()), tenantID
}

func TestRequireTenantMember(t *testing.T) {
	db := newMemberTestDB(t)
	ctx := context.Background()
	orgID, err := org.CreateRootOrg(ctx, db, "品牌A", "")
	if err != nil {
		t.Fatalf("create root org: %v", err)
	}
	if _, err := org.EnsureMember(ctx, db, orgID, 100, "owner"); err != nil {
		t.Fatalf("ensure member: %v", err)
	}
	handler := RequireTenantMember(TenantMemberOptions{DB: db})
	header := strconv.FormatUint(orgID, 10)

	// 成员 + 有效头：放行，租户写入上下文。
	status, _, tenantID := runTenantMember(t, handler, 100, header)
	if status != 200 || tenantID != orgID {
		t.Fatalf("成员应放行，status=%d tenantID=%d want orgID=%d", status, tenantID, orgID)
	}

	// 非成员：403。
	status, body, _ := runTenantMember(t, handler, 200, "7")
	if status != 403 || body == "" {
		t.Fatalf("非成员应 403，status=%d body=%q", status, body)
	}

	// 缺头：403 请选择品牌。
	status, body, _ = runTenantMember(t, handler, 100, "")
	if status != 403 {
		t.Fatalf("缺头应 403，status=%d body=%q", status, body)
	}

	// 非法头：403。
	status, _, _ = runTenantMember(t, handler, 100, "abc")
	if status != 403 {
		t.Fatalf("非法头应 403，status=%d", status)
	}

	// 未登录：401。
	status, _, _ = runTenantMember(t, handler, 0, "7")
	if status != 401 {
		t.Fatalf("未登录应 401，status=%d", status)
	}
}

func TestRequireTenantMemberPlatformAdmin(t *testing.T) {
	db := newMemberTestDB(t)
	handler := RequireTenantMember(TenantMemberOptions{
		DB:            db,
		PlatformAdmin: func(ctx context.Context, userID uint64) bool { return true },
	})
	status, _, tenantID := runTenantMember(t, handler, 200, "42")
	if status != 200 || tenantID != 42 {
		t.Fatalf("平台超管应放行任意租户，status=%d tenantID=%d", status, tenantID)
	}
}

func TestRequireTenantMemberFallback(t *testing.T) {
	db := newMemberTestDB(t)
	ctx := context.Background()
	orgID, err := org.CreateRootOrg(ctx, db, "品牌B", "")
	if err != nil {
		t.Fatalf("create root org: %v", err)
	}
	if _, err := org.EnsureMember(ctx, db, orgID, 100, "owner"); err != nil {
		t.Fatalf("ensure member: %v", err)
	}

	// 缺头回落到成员租户。
	handler := RequireTenantMember(TenantMemberOptions{
		DB:       db,
		Fallback: func(ctx context.Context, userID uint64) (uint64, error) { return orgID, nil },
	})
	status, _, tenantID := runTenantMember(t, handler, 100, "")
	if status != 200 || tenantID != orgID {
		t.Fatalf("缺头应回落兜底租户，status=%d tenantID=%d", status, tenantID)
	}

	// 兜底返回 0：仍 403。
	handler = RequireTenantMember(TenantMemberOptions{
		DB:       db,
		Fallback: func(ctx context.Context, userID uint64) (uint64, error) { return 0, nil },
	})
	status, _, _ = runTenantMember(t, handler, 100, "")
	if status != 403 {
		t.Fatalf("兜底无租户应 403，status=%d", status)
	}

	// 兜底报错：500。
	handler = RequireTenantMember(TenantMemberOptions{
		DB:       db,
		Fallback: func(ctx context.Context, userID uint64) (uint64, error) { return 0, errors.New("db down") },
	})
	status, _, _ = runTenantMember(t, handler, 100, "")
	if status != 500 {
		t.Fatalf("兜底报错应 500，status=%d", status)
	}
}

func TestMemberCache(t *testing.T) {
	cache := newMemberCache(60 * time.Millisecond)
	cache.set(1, 2, true)
	if allowed, hit := cache.get(1, 2); !hit || !allowed {
		t.Fatal("正结果应命中")
	}
	cache.set(3, 4, false)
	if _, hit := cache.get(3, 4); !hit {
		t.Fatal("负结果应命中")
	}
	if _, hit := cache.get(9, 9); hit {
		t.Fatal("未写入不应命中")
	}
	time.Sleep(80 * time.Millisecond)
	if _, hit := cache.get(1, 2); hit {
		t.Fatal("过期后不应命中")
	}
}
