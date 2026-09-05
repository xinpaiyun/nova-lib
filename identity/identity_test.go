package identity

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"

	"github.com/xinpaiyun/nova-lib/cache"
	"github.com/xinpaiyun/nova-lib/config"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "identity.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		SkipDefaultTransaction:                   true,
		Logger:                                   gormlogger.Default.LogMode(gormlogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		NamingStrategy: schema.NamingStrategy{},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(ModelTypes()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestHashVerifyPassword(t *testing.T) {
	hash, err := HashPassword("secret66")
	if err != nil {
		t.Fatalf("HashPassword err=%v", err)
	}
	if !IsBcryptHash(hash) {
		t.Fatalf("expected bcrypt hash, got %s", hash)
	}
	if !VerifyPassword("secret66", hash) {
		t.Fatal("VerifyPassword should match correct password")
	}
	if VerifyPassword("wrong", hash) {
		t.Fatal("VerifyPassword should reject wrong password")
	}
	// 历史明文兜底
	if !VerifyPassword("plaintext", "plaintext") {
		t.Fatal("plaintext fallback should match")
	}
	if VerifyPassword("a", "") {
		t.Fatal("empty stored should not match")
	}
}

func TestRegisterAndPasswordLogin(t *testing.T) {
	db := newTestDB(t)
	svc := NewLoginService(db)
	ctx := context.Background()

	if _, err := svc.RegisterPassword(ctx, "alice", "alice123", "alice_nick"); err != nil {
		t.Fatalf("register err=%v", err)
	}
	// 弱密码拒绝
	if _, err := svc.RegisterPassword(ctx, "bob", "123", "b"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("want ErrWeakPassword, got %v", err)
	}
	user, err := svc.PasswordLogin(ctx, "alice", "alice123")
	if err != nil {
		t.Fatalf("login err=%v", err)
	}
	if user.Nickname != "alice_nick" {
		t.Fatalf("nickname=%q", user.Nickname)
	}
	// 错误密码
	if _, err := svc.PasswordLogin(ctx, "alice", "wrong"); !errors.Is(err, ErrCredential) {
		t.Fatalf("want ErrCredential, got %v", err)
	}
	// 不存在
	if _, err := svc.PasswordLogin(ctx, "nobody", "123"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	db := newTestDB(t)
	svc := NewLoginService(db)
	ctx := context.Background()
	user, err := svc.RegisterPassword(ctx, "carol", "oldpass", "")
	if err != nil {
		t.Fatalf("register err=%v", err)
	}
	// 旧密码错误
	if err := svc.ChangePassword(ctx, user.ID, "bad", "newpass66"); !errors.Is(err, ErrCredential) {
		t.Fatalf("want ErrCredential, got %v", err)
	}
	// 弱新密码
	if err := svc.ChangePassword(ctx, user.ID, "oldpass", "12"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("want ErrWeakPassword, got %v", err)
	}
	if err := svc.ChangePassword(ctx, user.ID, "oldpass", "newpass66"); err != nil {
		t.Fatalf("change err=%v", err)
	}
	// 新密码可登录
	if _, err := svc.PasswordLogin(ctx, "carol", "newpass66"); err != nil {
		t.Fatalf("login with new pwd err=%v", err)
	}
}

func TestWechatCodeLoginDevFallback(t *testing.T) {
	db := newTestDB(t)
	svc := NewLoginService(db)
	ctx := context.Background()
	// 无微信客户端 + dev: 前缀回退
	u1, err := svc.WechatCodeLogin(ctx, "dev:test-openid-1")
	if err != nil {
		t.Fatalf("dev login err=%v", err)
	}
	// 同一 dev openid 再次登录应复用同一用户
	u2, err := svc.WechatCodeLogin(ctx, "dev:test-openid-1")
	if err != nil {
		t.Fatalf("dev login2 err=%v", err)
	}
	if u1.ID != u2.ID {
		t.Fatalf("dev openid should map to same user, got %d vs %d", u1.ID, u2.ID)
	}
	// 不同 dev openid → 不同用户
	u3, err := svc.WechatCodeLogin(ctx, "dev:another")
	if err != nil {
		t.Fatalf("dev login3 err=%v", err)
	}
	if u1.ID == u3.ID {
		t.Fatal("different openid should map to different user")
	}
	// 无微信且非 dev 前缀 → 微信未配置
	if _, err := svc.WechatCodeLogin(ctx, "real-code"); !errors.Is(err, ErrWechatMissing) {
		t.Fatalf("want ErrWechatMissing, got %v", err)
	}
}

func TestEnsureSuperAdminIdempotent(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	bindCalls := 0
	scope := SuperAdminScope{
		Config: SuperAdminConfig{
			Enabled:  true,
			Account:  "admin",
			Password: "adminpass123",
			Nickname: "超管",
		},
		AccountField: AccountFieldUsername,
		BindPlatformRole: func(ctx context.Context, db *gorm.DB, user *User) error {
			bindCalls++
			return nil
		},
		DefaultNickname: "默认超管",
	}
	if err := EnsureSuperAdmin(ctx, db, scope); err != nil {
		t.Fatalf("ensure#1 err=%v", err)
	}
	if err := EnsureSuperAdmin(ctx, db, scope); err != nil {
		t.Fatalf("ensure#2 err=%v", err)
	}
	if bindCalls != 2 {
		t.Fatalf("bindCalls=%d, want 2", bindCalls)
	}
	// 幂等：仅一条记录
	var count int64
	db.Model(&User{}).Where("username = ?", "admin").Count(&count)
	if count != 1 {
		t.Fatalf("admin count=%d, want 1", count)
	}
	// 弱密码拒绝
	bad := scope
	bad.Config.Password = "12"
	if err := EnsureSuperAdmin(ctx, db, bad); err == nil {
		t.Fatal("expect weak-password error")
	}
	// 禁用时不执行
	disabled := scope
	disabled.Config.Enabled = false
	if err := EnsureSuperAdmin(ctx, db, disabled); err != nil {
		t.Fatalf("disabled ensure err=%v", err)
	}
}

func TestIssuerSessionRoundTrip(t *testing.T) {
	defer cache.Flush()
	db := newTestDB(t)
	_ = db
	user := &User{ID: 42, Status: StatusEnabled}
	issuer := NewIssuer(config.JWTConfig{ExpireHour: 1}, WithRoleResolver(
		func(ctx context.Context, u *User) (string, error) {
			return "super_admin", nil
		},
	))
	ctx := context.Background()
	res, err := issuer.Issue(ctx, user, 7, "", WithAppType("admin"), WithOpenID("open-abc"))
	if err != nil {
		t.Fatalf("issue err=%v", err)
	}
	if res.RoleCode != "super_admin" {
		t.Fatalf("roleCode=%q", res.RoleCode)
	}
	claims, err := issuer.Resolve(ctx, res.Token)
	if err != nil {
		t.Fatalf("resolve err=%v", err)
	}
	if claims.UserID != 42 || claims.TenantID != 7 || claims.RoleCode != "super_admin" ||
		claims.AppType != "admin" || claims.OpenID != "open-abc" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	// 显式 role 优先于 resolver
	res2, err := issuer.Issue(ctx, user, 0, "agent")
	if err != nil {
		t.Fatalf("issue2 err=%v", err)
	}
	if res2.RoleCode != "agent" {
		t.Fatalf("explicit roleCode=%q", res2.RoleCode)
	}
	// 登出
	if err := issuer.Revoke(ctx, res.Token); err != nil {
		t.Fatalf("revoke err=%v", err)
	}
	if _, err := issuer.Resolve(ctx, res.Token); err == nil {
		t.Fatal("resolved after revoke should fail")
	}
}

func TestPasswordLoginByPhone(t *testing.T) {
	db := newTestDB(t)
	svc := NewLoginService(db)
	ctx := context.Background()
	hash, _ := HashPassword("pass123")
	if err := db.Create(&User{Phone: "13800000000", PasswordHash: hash, Status: StatusEnabled}).Error; err != nil {
		t.Fatalf("create user err=%v", err)
	}
	u, err := svc.PasswordLogin(ctx, "13800000000", "pass123")
	if err != nil {
		t.Fatalf("login by phone err=%v", err)
	}
	if strings.TrimSpace(u.Phone) != "13800000000" {
		t.Fatalf("phone mismatch")
	}
}