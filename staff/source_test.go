package staff

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newStaffDB 创建内存 SQLite 测试库并迁移标准表。
func newStaffDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取底层连接失败: %v", err)
	}
	// 内存库必须单连接，否则不同连接看不到已建的表。
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(ModelTypes()...); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func mustCreateMember(t *testing.T, src *GormSource, in MemberInput) *StaffMember {
	t.Helper()
	member, err := src.CreateMember(context.Background(), in)
	if err != nil {
		t.Fatalf("创建员工失败: %v", err)
	}
	return member
}

// TestCreateMemberUniqueness 验证租户内同人唯一、跨租户可重复。
func TestCreateMemberUniqueness(t *testing.T) {
	src := NewGormSource(newStaffDB(t))
	ctx := context.Background()

	mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 100, Name: "张三", RoleCode: RoleStaff})

	_, err := src.CreateMember(ctx, MemberInput{TenantID: 1, UserID: 100, Name: "张三二", RoleCode: RoleStaff})
	if !errors.Is(err, ErrMemberExists) {
		t.Fatalf("同租户同人应报 ErrMemberExists, got %v", err)
	}

	// 同一自然人可以同时是另一个租户的员工。
	mustCreateMember(t, src, MemberInput{TenantID: 2, UserID: 100, Name: "张三", RoleCode: RoleManager})
}

// TestCreateMemberValidation 验证入参校验与角色别名归一。
func TestCreateMemberValidation(t *testing.T) {
	src := NewGormSource(newStaffDB(t))
	ctx := context.Background()

	if _, err := src.CreateMember(ctx, MemberInput{TenantID: 1, UserID: 0, Name: "无用户"}); !errors.Is(err, ErrMemberInputInvalid) {
		t.Fatalf("缺少 user_id 应报 ErrMemberInputInvalid, got %v", err)
	}
	if _, err := src.CreateMember(ctx, MemberInput{TenantID: 1, UserID: 1, Name: "某人", RoleCode: "no_such_role"}); !errors.Is(err, ErrRoleUnknown) {
		t.Fatalf("未知角色应报 ErrRoleUnknown, got %v", err)
	}

	// 存量角色码通过别名归一，落库就是标准码。
	alias := uniqCode(t, "store_admin")
	if err := RegisterRoleAlias(alias, RoleManager); err != nil {
		t.Fatalf("注册别名失败: %v", err)
	}
	member := mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 2, Name: "李四", RoleCode: alias})
	if member.RoleCode != RoleManager {
		t.Fatalf("别名应归一为 manager, got %q", member.RoleCode)
	}
}

// TestResolveIdentity 验证身份解析、范围推导与禁用拒绝。
func TestResolveIdentity(t *testing.T) {
	db := newStaffDB(t)
	src := NewGormSource(db)
	ctx := context.Background()

	code := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: code, Name: "服务者", SubjectKind: "therapist",
		SubjectRequired: true, ScopeEffect: ScopeEffectSelfBySubject, WorkbenchAccess: true,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}

	staff := mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 11, Name: "技师甲", RoleCode: RoleStaff})
	if _, err := src.AddCapability(ctx, 1, staff.ID, CapabilityInput{Code: code, SubjectID: 7}); err != nil {
		t.Fatalf("开启开关失败: %v", err)
	}

	id, err := Resolve(ctx, src, 1, 11)
	if err != nil {
		t.Fatalf("解析身份失败: %v", err)
	}
	if id.Scope != ScopeSelf {
		t.Fatalf("持范围开关的普通员工应为 self, got %v", id.Scope)
	}
	if got := id.SelfSubjects[code]; len(got) != 1 || got[0] != 7 {
		t.Fatalf("SelfSubjects 错误: %v", got)
	}
	if !id.CanEnterWorkbench() {
		t.Fatal("持准入开关的员工应可进入工作台")
	}

	manager := mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 12, Name: "店长", RoleCode: RoleManager})
	managerID, err := Resolve(ctx, src, 1, 12)
	if err != nil {
		t.Fatalf("解析店长失败: %v", err)
	}
	if managerID.Scope != ScopeOrg {
		t.Fatalf("店长应为 org, got %v", managerID.Scope)
	}
	if managerID.MemberID != manager.ID {
		t.Fatalf("MemberID 错误: %d", managerID.MemberID)
	}

	// 禁用后拒绝，语义是 ErrMemberDisabled 而不是不存在。
	if err := src.SetStatus(ctx, 1, staff.ID, StatusDisabled); err != nil {
		t.Fatalf("禁用失败: %v", err)
	}
	if _, err := Resolve(ctx, src, 1, 11); !errors.Is(err, ErrMemberDisabled) {
		t.Fatalf("禁用员工应报 ErrMemberDisabled, got %v", err)
	}

	if _, err := Resolve(ctx, src, 1, 999); !errors.Is(err, ErrNotMember) {
		t.Fatalf("非员工应报 ErrNotMember, got %v", err)
	}
}

// TestAddCapabilityRules 验证开关注入的校验、档案占用与单实例改绑。
func TestAddCapabilityRules(t *testing.T) {
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

	m1 := mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 21, Name: "甲", RoleCode: RoleStaff})
	m2 := mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 22, Name: "乙", RoleCode: RoleStaff})

	if _, err := src.AddCapability(ctx, 1, m1.ID, CapabilityInput{Code: "no_such_cap"}); !errors.Is(err, ErrCapabilitySpecUnknown) {
		t.Fatalf("未注册开关应报 ErrCapabilitySpecUnknown, got %v", err)
	}
	if _, err := src.AddCapability(ctx, 1, m1.ID, CapabilityInput{Code: code}); !errors.Is(err, ErrCapabilitySubjectRequired) {
		t.Fatalf("缺档案应报 ErrCapabilitySubjectRequired, got %v", err)
	}

	cap7, err := src.AddCapability(ctx, 1, m1.ID, CapabilityInput{Code: code, SubjectID: 7})
	if err != nil {
		t.Fatalf("开启档案 7 失败: %v", err)
	}
	if cap7.SubjectID != 7 || cap7.Status != StatusEnabled {
		t.Fatalf("开关实例错误: %+v", cap7)
	}

	// 档案 7 已被甲占用，乙不能再挂。
	if _, err := src.AddCapability(ctx, 1, m2.ID, CapabilityInput{Code: code, SubjectID: 7}); !errors.Is(err, ErrCapabilitySubjectTaken) {
		t.Fatalf("档案被占用应报 ErrCapabilitySubjectTaken, got %v", err)
	}

	// 重复开启同一实例是幂等的。
	if _, err := src.AddCapability(ctx, 1, m1.ID, CapabilityInput{Code: code, SubjectID: 7}); err != nil {
		t.Fatalf("重复开启应幂等: %v", err)
	}

	// 单实例开关改绑：换成档案 8，旧实例置为关闭。
	if _, err := src.AddCapability(ctx, 1, m1.ID, CapabilityInput{Code: code, SubjectID: 8}); err != nil {
		t.Fatalf("改绑失败: %v", err)
	}
	var old StaffCapability
	if err := db.Where("member_id = ? AND code = ? AND subject_id = ?", m1.ID, code, 7).
		Take(&old).Error; err != nil {
		t.Fatalf("读取旧实例失败: %v", err)
	}
	if old.Status != StatusDisabled {
		t.Fatalf("改绑后旧实例应关闭, got %d", old.Status)
	}

	id, err := Resolve(ctx, src, 1, 21)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got := id.SelfSubjects[code]; len(got) != 1 || got[0] != 8 {
		t.Fatalf("改绑后档案应为 8: %v", got)
	}
}

// TestRemoveCapability 验证关闭开关后不再参与身份解析。
func TestRemoveCapability(t *testing.T) {
	src := NewGormSource(newStaffDB(t))
	ctx := context.Background()

	code := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: code, SubjectKind: "therapist", SubjectRequired: true,
		ScopeEffect: ScopeEffectSelfBySubject,
	}); err != nil {
		t.Fatalf("注册开关失败: %v", err)
	}

	member := mustCreateMember(t, src, MemberInput{TenantID: 1, UserID: 31, Name: "丙", RoleCode: RoleStaff})
	if _, err := src.AddCapability(ctx, 1, member.ID, CapabilityInput{Code: code, SubjectID: 9}); err != nil {
		t.Fatalf("开启失败: %v", err)
	}

	if err := src.RemoveCapability(ctx, 1, member.ID, code, 9); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	caps, err := src.Capabilities(ctx, 1, member.ID)
	if err != nil {
		t.Fatalf("读取开关失败: %v", err)
	}
	if len(caps) != 0 {
		t.Fatalf("关闭后不应再有启用开关: %+v", caps)
	}

	// 失去范围性开关后，普通员工范围回落到 none。
	id, err := Resolve(ctx, src, 1, 31)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if id.Scope != ScopeNone {
		t.Fatalf("关闭范围开关后应为 none, got %v", id.Scope)
	}

	if err := src.RemoveCapability(ctx, 1, member.ID, "no_such_cap", 0); !errors.Is(err, ErrCapabilitySpecUnknown) {
		t.Fatalf("关闭不存在的开关应报 ErrCapabilitySpecUnknown, got %v", err)
	}
}

// TestMembersPagination 验证分页、关键字与状态过滤。
func TestMembersPagination(t *testing.T) {
	src := NewGormSource(newStaffDB(t))
	ctx := context.Background()

	for i, name := range []string{"张三", "李四", "王五"} {
		mustCreateMember(t, src, MemberInput{
			TenantID: 1, UserID: uint64(100 + i), Name: name,
			EmployeeNo: "E00" + string(rune('1'+i)), RoleCode: RoleStaff,
		})
	}
	// 另一个租户的数据不应出现。
	mustCreateMember(t, src, MemberInput{TenantID: 2, UserID: 200, Name: "张三", RoleCode: RoleStaff})

	list, total, err := src.Members(ctx, 1, MemberFilter{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if total != 3 || len(list) != 2 {
		t.Fatalf("分页结果错误: total=%d len=%d", total, len(list))
	}
	if list[0].ID < list[1].ID {
		t.Fatal("列表应按 ID 倒序")
	}

	byName, total, err := src.Members(ctx, 1, MemberFilter{Keyword: "李四"})
	if err != nil || total != 1 || len(byName) != 1 {
		t.Fatalf("关键字过滤错误: total=%d len=%d err=%v", total, len(byName), err)
	}

	disabled := StatusDisabled
	byStatus, total, err := src.Members(ctx, 1, MemberFilter{Status: &disabled})
	if err != nil || total != 0 || len(byStatus) != 0 {
		t.Fatalf("状态过滤错误: total=%d len=%d err=%v", total, len(byStatus), err)
	}
}
