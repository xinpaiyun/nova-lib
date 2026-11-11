package staff

import (
	"context"
	"errors"
	"testing"

	"github.com/xinpaiyun/nova-lib/identity"
	"gorm.io/gorm"
)

// newServiceDB 在标准表基础上额外迁移 users，供手机号解析使用。
func newServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := newStaffDB(t)
	if err := db.AutoMigrate(&identity.User{}); err != nil {
		t.Fatalf("迁移 users 失败: %v", err)
	}
	return db
}

func seedUser(t *testing.T, db *gorm.DB, id uint64, phone string, status int8) {
	t.Helper()
	user := &identity.User{ID: id, Phone: phone, PasswordHash: "x"}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	// status 带 default:1 标签，GORM 插入时会忽略零值，因此单独更新。
	if status != identity.StatusEnabled {
		if err := db.Model(&identity.User{}).Where("id = ?", id).
			Update("status", status).Error; err != nil {
			t.Fatalf("更新用户状态失败: %v", err)
		}
	}
}

func managerActor(t *testing.T, tenantID, memberID uint64) *Identity {
	t.Helper()
	return &Identity{
		TenantID: tenantID,
		MemberID: memberID,
		Role:     mustRole(t, RoleManager),
	}
}

// TestGormUserResolver 验证只读 users、同号取最早、未注册与停用的语义。
func TestGormUserResolver(t *testing.T) {
	db := newServiceDB(t)
	resolver := NewGormUserResolver(db)
	ctx := context.Background()

	seedUser(t, db, 11, "13800000001", identity.StatusEnabled)
	seedUser(t, db, 12, "13800000001", identity.StatusEnabled)
	seedUser(t, db, 13, "13800000002", identity.StatusDisabled)

	if got, err := resolver.UserByPhone(ctx, " 13800000001 "); err != nil || got != 11 {
		t.Fatalf("同号应取最早一条: id=%d err=%v", got, err)
	}
	if _, err := resolver.UserByPhone(ctx, "13800000002"); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("停用用户应报 ErrUserDisabled, got %v", err)
	}
	if _, err := resolver.UserByPhone(ctx, "13800000003"); !errors.Is(err, ErrUserNotRegistered) {
		t.Fatalf("未注册应报 ErrUserNotRegistered, got %v", err)
	}
}

// TestServiceCreateMember 验证建员工时的手机号解析规则与不代建用户。
func TestServiceCreateMember(t *testing.T) {
	db := newServiceDB(t)
	svc := NewService(db)
	actor := managerActor(t, 1, 100)
	ctx := context.Background()

	seedUser(t, db, 21, "13900000001", identity.StatusEnabled)
	seedUser(t, db, 22, "13900000002", identity.StatusDisabled)

	cases := []struct {
		name string
		in   CreateMemberInput
		want error
	}{
		{"非法手机号", CreateMemberInput{Phone: "123", Name: "甲", RoleCode: RoleStaff}, ErrPhoneInvalid},
		{"未注册手机号", CreateMemberInput{Phone: "13900000003", Name: "甲", RoleCode: RoleStaff}, ErrUserNotRegistered},
		{"停用手机号", CreateMemberInput{Phone: "13900000002", Name: "甲", RoleCode: RoleStaff}, ErrUserDisabled},
		{"缺姓名", CreateMemberInput{Phone: "13900000001", Name: "  ", RoleCode: RoleStaff}, ErrMemberInputInvalid},
		{"未知角色", CreateMemberInput{Phone: "13900000001", Name: "甲", RoleCode: "therapist"}, ErrRoleUnknown},
	}
	for _, c := range cases {
		if _, err := svc.CreateMember(ctx, actor, c.in); !errors.Is(err, c.want) {
			t.Fatalf("%s 应报 %v, got %v", c.name, c.want, err)
		}
	}

	member, err := svc.CreateMember(ctx, actor, CreateMemberInput{
		OrgID: 2, Phone: "13900000001", Name: " 甲 ", RoleCode: RoleStaff,
	})
	if err != nil {
		t.Fatalf("建员工失败: %v", err)
	}
	if member.UserID != 21 || member.Name != "甲" || member.RoleCode != RoleStaff ||
		member.TenantID != 1 || member.OrgID != 2 || member.Status != StatusEnabled {
		t.Fatalf("落库结果错误: %+v", member)
	}

	// 同租户同一人不可重复。
	if _, err := svc.CreateMember(ctx, actor, CreateMemberInput{
		Phone: "13900000001", Name: "甲二", RoleCode: RoleStaff,
	}); !errors.Is(err, ErrMemberExists) {
		t.Fatalf("重复建号应报 ErrMemberExists, got %v", err)
	}

	// 迁移/内部调用可直接给 UserID，跳过手机号解析。
	migrated, err := svc.CreateMember(ctx, actor, CreateMemberInput{UserID: 999, Name: "乙", RoleCode: RoleViewer})
	if err != nil {
		t.Fatalf("按 UserID 建号失败: %v", err)
	}
	if migrated.UserID != 999 {
		t.Fatalf("UserID 错误: %d", migrated.UserID)
	}
}

// TestServicePermissionDenied 验证非管理身份与空身份一律拒绝。
func TestServicePermissionDenied(t *testing.T) {
	svc := NewService(newServiceDB(t))
	ctx := context.Background()
	in := CreateMemberInput{UserID: 1, Name: "甲", RoleCode: RoleStaff}

	for _, actor := range []*Identity{
		nil,
		{TenantID: 1, MemberID: 5, Role: mustRole(t, RoleStaff)},
		{TenantID: 1, MemberID: 5, Role: mustRole(t, RoleViewer)},
	} {
		if _, err := svc.CreateMember(ctx, actor, in); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("CreateMember 应拒绝: %v", err)
		}
		if err := svc.UpdateMember(ctx, actor, 1, in); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("UpdateMember 应拒绝: %v", err)
		}
		if err := svc.SetStatus(ctx, actor, 1, StatusDisabled); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("SetStatus 应拒绝: %v", err)
		}
		if _, err := svc.AddCapability(ctx, actor, 1, CapabilityInput{Code: "x"}); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("AddCapability 应拒绝: %v", err)
		}
		if err := svc.RemoveCapability(ctx, actor, 1, "x", 0); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("RemoveCapability 应拒绝: %v", err)
		}
		if _, _, err := svc.ListMembers(ctx, actor, MemberFilter{}); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("ListMembers 应拒绝: %v", err)
		}
		if _, err := svc.MemberIdentity(ctx, actor, 1); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("MemberIdentity 应拒绝: %v", err)
		}
	}
}

// TestServiceMemberLifecycle 验证改资料、启禁用与自锁保护。
func TestServiceMemberLifecycle(t *testing.T) {
	db := newServiceDB(t)
	svc := NewService(db)
	actor := managerActor(t, 1, 100)
	ctx := context.Background()

	seedUser(t, db, 31, "13700000001", identity.StatusEnabled)
	member, err := svc.CreateMember(ctx, actor, CreateMemberInput{
		Phone: "13700000001", Name: "技师", RoleCode: RoleStaff,
	})
	if err != nil {
		t.Fatalf("建员工失败: %v", err)
	}

	if err := svc.UpdateMember(ctx, actor, member.ID, CreateMemberInput{
		Name: "技师甲", Position: "理疗师", RoleCode: RoleAdmin,
	}); err != nil {
		t.Fatalf("更新员工失败: %v", err)
	}
	id, err := Resolve(ctx, svc.Source(), 1, 31)
	if err != nil {
		t.Fatalf("解析身份失败: %v", err)
	}
	if id.Name != "技师甲" || id.Role.Code != RoleAdmin {
		t.Fatalf("更新未生效: %+v", id)
	}

	// 管理员不能禁用自己，避免把自己锁在门外。
	self := managerActor(t, 1, member.ID)
	if err := svc.SetStatus(ctx, self, member.ID, StatusDisabled); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("禁用自己应报 ErrPermissionDenied, got %v", err)
	}
	if err := svc.SetStatus(ctx, actor, member.ID, StatusDisabled); err != nil {
		t.Fatalf("禁用员工失败: %v", err)
	}
	if _, err := Resolve(ctx, svc.Source(), 1, 31); !errors.Is(err, ErrMemberDisabled) {
		t.Fatalf("禁用后应报 ErrMemberDisabled, got %v", err)
	}

	if err := svc.SetStatus(ctx, actor, 999, StatusEnabled); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("不存在的员工应报 ErrMemberNotFound, got %v", err)
	}
	if err := svc.UpdateMember(ctx, actor, 999, CreateMemberInput{Name: "无"}); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("更新不存在员工应报 ErrMemberNotFound, got %v", err)
	}
}

// TestServiceCapability 验证开关读写后身份范围随之变化。
func TestServiceCapability(t *testing.T) {
	db := newServiceDB(t)
	svc := NewService(db)
	actor := managerActor(t, 1, 100)
	ctx := context.Background()

	code := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: code, Name: "服务者", SubjectKind: "therapist",
		SubjectRequired: true, ScopeEffect: ScopeEffectSelfBySubject, WorkbenchAccess: true,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}

	seedUser(t, db, 41, "13600000001", identity.StatusEnabled)
	member, err := svc.CreateMember(ctx, actor, CreateMemberInput{
		Phone: "13600000001", Name: "技师乙", RoleCode: RoleStaff,
	})
	if err != nil {
		t.Fatalf("建员工失败: %v", err)
	}

	if _, err := svc.AddCapability(ctx, actor, member.ID, CapabilityInput{Code: "no_such_cap"}); !errors.Is(err, ErrCapabilitySpecUnknown) {
		t.Fatalf("未注册开关应报 ErrCapabilitySpecUnknown, got %v", err)
	}
	if _, err := svc.AddCapability(ctx, actor, member.ID, CapabilityInput{Code: code}); !errors.Is(err, ErrCapabilitySubjectRequired) {
		t.Fatalf("缺档案应报 ErrCapabilitySubjectRequired, got %v", err)
	}
	if _, err := svc.AddCapability(ctx, actor, 999, CapabilityInput{Code: code, SubjectID: 7}); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("员工不存在应报 ErrMemberNotFound, got %v", err)
	}

	capability, err := svc.AddCapability(ctx, actor, member.ID, CapabilityInput{Code: code, SubjectID: 7})
	if err != nil {
		t.Fatalf("开启开关失败: %v", err)
	}
	if capability.TenantID != 1 || capability.MemberID != member.ID ||
		capability.SubjectID != 7 || capability.Status != StatusEnabled {
		t.Fatalf("开关实例错误: %+v", capability)
	}

	id, err := svc.MemberIdentity(ctx, actor, member.ID)
	if err != nil {
		t.Fatalf("读取员工身份失败: %v", err)
	}
	if id.Scope != ScopeSelf || !id.CanEnterWorkbench() {
		t.Fatalf("持开关后应可进工作台且为 self: %+v", id)
	}
	if got := id.SelfSubjects[code]; len(got) != 1 || got[0] != 7 {
		t.Fatalf("档案集合错误: %v", got)
	}

	if err := svc.RemoveCapability(ctx, actor, member.ID, code, 7); err != nil {
		t.Fatalf("关闭开关失败: %v", err)
	}
	id, err = svc.MemberIdentity(ctx, actor, member.ID)
	if err != nil {
		t.Fatalf("关闭后读取身份失败: %v", err)
	}
	if id.Scope != ScopeNone || id.CanEnterWorkbench() {
		t.Fatalf("关闭开关后应回落到 none: %+v", id)
	}
}

// TestServiceListMembers 验证管理端列表按租户隔离。
func TestServiceListMembers(t *testing.T) {
	db := newServiceDB(t)
	svc := NewService(db)
	ctx := context.Background()

	seedUser(t, db, 51, "13500000001", identity.StatusEnabled)
	seedUser(t, db, 52, "13500000002", identity.StatusEnabled)

	if _, err := svc.CreateMember(ctx, managerActor(t, 1, 100), CreateMemberInput{
		Phone: "13500000001", Name: "甲", RoleCode: RoleStaff,
	}); err != nil {
		t.Fatalf("建员工失败: %v", err)
	}
	if _, err := svc.CreateMember(ctx, managerActor(t, 2, 200), CreateMemberInput{
		Phone: "13500000002", Name: "乙", RoleCode: RoleStaff,
	}); err != nil {
		t.Fatalf("建员工失败: %v", err)
	}

	list, total, err := svc.ListMembers(ctx, managerActor(t, 1, 100), MemberFilter{})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].Name != "甲" {
		t.Fatalf("租户隔离错误: total=%d list=%+v", total, list)
	}
}
