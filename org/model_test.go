package org

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "org.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		SkipDefaultTransaction:                   true,
		Logger:                                   gormlogger.Default.LogMode(gormlogger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		NamingStrategy:                           schema.NamingStrategy{},
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

func TestOrgTreeAndMembership(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// 根机构（数据归属边界）+ 两个店铺子节点
	school := Organization{Name: "星学迹总部", Type: TypeRoot, TenantID: 0}
	storeA := Organization{ParentID: 1, Name: "北京校区", Type: TypeShop}
	storeB := Organization{ParentID: 1, Name: "上海校区", Type: TypeShop}
	for i, o := range []*Organization{&school, &storeA, &storeB} {
		if err := db.Create(o).Error; err != nil {
			t.Fatalf("create org[%d]: %v", i, err)
		}
	}
	_ = storeA
	_ = storeB

	// 成员-角色绑定
	if _, err := EnsureMember(ctx, db, school.ID, 11, "owner"); err != nil {
		t.Fatalf("ensure owner: %v", err)
	}
	if _, err := EnsureMember(ctx, db, storeA.ID, 11, "admin"); err != nil {
		t.Fatalf("ensure storeA admin: %v", err)
	}
	if _, err := EnsureMember(ctx, db, storeB.ID, 22, "member"); err != nil {
		t.Fatalf("ensure storeB member: %v", err)
	}

	// 多组织归属
	orgs, err := ListMemberOrgs(ctx, db, 11)
	if err != nil {
		t.Fatalf("list member orgs: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("user 11 orgs = %d, want 2", len(orgs))
	}

	// 角色判定
	if role, _ := MemberRole(ctx, db, school.ID, 11); role != "owner" {
		t.Fatalf("school role = %q, want owner", role)
	}
	isMem, err := IsMember(ctx, db, storeB.ID, 11)
	if err != nil || isMem {
		t.Fatalf("user11 in storeB = %v(,%v), want false", isMem, err)
	}

	// 非成员 → ErrNotFound
	if _, err := MemberRole(ctx, db, school.ID, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member err = %v, want ErrNotFound", err)
	}
}

func TestEnsureMemberIdempotentAndRoleUpdate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	org := Organization{Name: "车队", Type: TypeRoot}
	if err := db.Create(&org).Error; err != nil {
		t.Fatalf("create org: %v", err)
	}

	if _, err := EnsureMember(ctx, db, org.ID, 7, "member"); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	// 幂等：再次调用不新增
	if _, err := EnsureMember(ctx, db, org.ID, 7, "member"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	var cnt int64
	if err := db.Model(&OrganizationMember{}).Where("org_id = ? AND user_id = ?", org.ID, 7).Count(&cnt).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("membership rows = %d, want 1", cnt)
	}

	// 升角色
	role, err := EnsureMember(ctx, db, org.ID, 7, "admin")
	if err != nil || role != "admin" {
		t.Fatalf("upgrade role = %q(,%v), want admin", role, err)
	}
	got, _ := MemberRole(ctx, db, org.ID, 7)
	if got != "admin" {
		t.Fatalf("role after upgrade = %q, want admin", got)
	}
}
