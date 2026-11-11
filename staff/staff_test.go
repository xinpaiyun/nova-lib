package staff

import (
	"errors"
	"fmt"
	"testing"
)

// uniqCode 生成测试内唯一码，避免全局注册表在用例间互相污染。
func uniqCode(t *testing.T, suffix string) string {
	t.Helper()
	return fmt.Sprintf("%s_%s", t.Name(), suffix)
}

func mustRole(t *testing.T, code string) RoleSpec {
	t.Helper()
	spec, ok := ResolveRole(code)
	if !ok {
		t.Fatalf("角色 %q 未注册", code)
	}
	return spec
}

// TestBuiltinRoles 验证内置 5 个管理权限角色已注册，且业务身份不能当角色用。
func TestBuiltinRoles(t *testing.T) {
	for _, code := range []string{RoleOwner, RoleAdmin, RoleManager, RoleStaff, RoleViewer} {
		if _, ok := ResolveRole(code); !ok {
			t.Fatalf("内置角色 %q 未注册", code)
		}
	}
	cases := map[string]bool{
		RoleOwner:   true,
		RoleAdmin:   true,
		RoleManager: true,
		RoleStaff:   false,
		RoleViewer:  false,
	}
	for code, want := range cases {
		if got := mustRole(t, code).CanManage(); got != want {
			t.Fatalf("%q.CanManage() = %v, want %v", code, got, want)
		}
	}
	// 旧方案里 therapist 曾是角色，现在必须解析失败。
	if _, ok := ResolveRole("therapist"); ok {
		t.Fatal("业务身份不应被注册成角色")
	}
}

// TestRoleSpecsOrdered 验证角色列表按权限等级从高到低排列。
func TestRoleSpecsOrdered(t *testing.T) {
	specs := RoleSpecs()
	if len(specs) < 5 {
		t.Fatalf("内置角色数量不足: %d", len(specs))
	}
	for i := 1; i < len(specs); i++ {
		if specs[i-1].Rank < specs[i].Rank {
			t.Fatalf("角色未按 Rank 降序: %q(%d) < %q(%d)",
				specs[i-1].Code, specs[i-1].Rank, specs[i].Code, specs[i].Rank)
		}
	}
}

// TestRoleAlias 验证存量角色码可映射到标准码，且重复/冲突/目标缺失被拒绝。
func TestRoleAlias(t *testing.T) {
	alias := uniqCode(t, "alias")
	if err := RegisterRoleAlias(alias, RoleManager); err != nil {
		t.Fatalf("注册别名失败: %v", err)
	}
	spec, ok := ResolveRole(alias)
	if !ok || spec.Code != RoleManager {
		t.Fatalf("别名解析结果 = %+v, ok=%v", spec, ok)
	}
	if err := RegisterRoleAlias(alias, RoleManager); !errors.Is(err, ErrRoleDuplicate) {
		t.Fatalf("重复别名应报 ErrRoleDuplicate, got %v", err)
	}
	if err := RegisterRoleAlias(RoleStaff, RoleManager); !errors.Is(err, ErrRoleDuplicate) {
		t.Fatalf("别名与标准码同名应报 ErrRoleDuplicate, got %v", err)
	}
	if err := RegisterRoleAlias(uniqCode(t, "missing"), "no_such_role"); !errors.Is(err, ErrRoleUnknown) {
		t.Fatalf("目标角色不存在应报 ErrRoleUnknown, got %v", err)
	}
}

// TestRegisterRoleValidation 验证角色注册的入参校验。
func TestRegisterRoleValidation(t *testing.T) {
	if err := RegisterRole(RoleSpec{Code: "  "}); !errors.Is(err, ErrRoleCodeEmpty) {
		t.Fatalf("空角色码应报 ErrRoleCodeEmpty, got %v", err)
	}
	if err := RegisterRole(RoleSpec{Code: uniqCode(t, "badscope"), DefaultScope: ScopeNone}); !errors.Is(err, ErrRoleScopeInvalid) {
		t.Fatalf("非法默认范围应报 ErrRoleScopeInvalid, got %v", err)
	}
	code := uniqCode(t, "custom")
	if err := RegisterRole(RoleSpec{Code: " " + code + " ", Name: "自定义", Rank: 50, DefaultScope: ScopeSelf}); err != nil {
		t.Fatalf("注册自定义角色失败: %v", err)
	}
	spec, ok := ResolveRole(code)
	if !ok || spec.Code != code {
		t.Fatalf("自定义角色应去空格后可解析: %+v", spec)
	}
	if err := RegisterRole(RoleSpec{Code: code, DefaultScope: ScopeSelf}); !errors.Is(err, ErrRoleDuplicate) {
		t.Fatalf("重复角色应报 ErrRoleDuplicate, got %v", err)
	}
}

// TestDataScopeAllows 验证三档范围的包含关系。
func TestDataScopeAllows(t *testing.T) {
	if !ScopeAll.Allows(ScopeOrg) || !ScopeOrg.Allows(ScopeSelf) || !ScopeSelf.Allows(ScopeNone) {
		t.Fatal("范围包含关系错误")
	}
	if ScopeSelf.Allows(ScopeOrg) || ScopeOrg.Allows(ScopeAll) {
		t.Fatal("小范围不应覆盖大范围")
	}
	if ScopeSelf.String() != "self" || ScopeAll.String() != "all" || ScopeNone.String() != "none" {
		t.Fatal("范围名称错误")
	}
}

// TestRegisterCapability 验证开关注册与校验规则。
func TestRegisterCapability(t *testing.T) {
	code := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: code, Name: "服务者", SubjectKind: "therapist",
		SubjectRequired: true, ScopeEffect: ScopeEffectSelfBySubject, WorkbenchAccess: true,
	}); err != nil {
		t.Fatalf("注册服务者开关失败: %v", err)
	}
	spec, ok := CapabilitySpecByCode(code)
	if !ok || spec.ScopeEffect != ScopeEffectSelfBySubject || !spec.SubjectRequired || !spec.WorkbenchAccess {
		t.Fatalf("开关定义回读不一致: %+v", spec)
	}

	if err := RegisterCapability(CapabilitySpec{Code: " "}); !errors.Is(err, ErrCapabilityCodeEmpty) {
		t.Fatalf("空开关码应报 ErrCapabilityCodeEmpty, got %v", err)
	}
	if err := RegisterCapability(CapabilitySpec{Code: code}); !errors.Is(err, ErrCapabilitySpecDuplicate) {
		t.Fatalf("重复开关应报 ErrCapabilitySpecDuplicate, got %v", err)
	}
	if err := RegisterCapability(CapabilitySpec{
		Code: uniqCode(t, "nokind"), SubjectRequired: true,
	}); !errors.Is(err, ErrCapabilitySubjectKindMissing) {
		t.Fatalf("缺 SubjectKind 应报 ErrCapabilitySubjectKindMissing, got %v", err)
	}
	if err := RegisterCapability(CapabilitySpec{
		Code: uniqCode(t, "badscope"), ScopeEffect: "bad",
	}); !errors.Is(err, ErrScopeEffectInvalid) {
		t.Fatalf("非法 ScopeEffect 应报 ErrScopeEffectInvalid, got %v", err)
	}

	// 未声明 ScopeEffect 时默认 none，即不影响数据范围。
	noop := uniqCode(t, "cashier")
	if err := RegisterCapability(CapabilitySpec{Code: noop, Name: "收银"}); err != nil {
		t.Fatalf("注册收银开关失败: %v", err)
	}
	spec, ok = CapabilitySpecByCode(noop)
	if !ok || spec.ScopeEffect != ScopeEffectNone {
		t.Fatalf("默认 ScopeEffect 应为 none: %+v", spec)
	}
}

// TestCapabilitySpecsSorted 验证开关列表按 Code 升序，供前端稳定渲染。
func TestCapabilitySpecsSorted(t *testing.T) {
	specs := CapabilitySpecs()
	if len(specs) == 0 {
		t.Fatal("开关列表不应为空")
	}
	for i := 1; i < len(specs); i++ {
		if specs[i-1].Code > specs[i].Code {
			t.Fatalf("开关列表未按 Code 升序: %q > %q", specs[i-1].Code, specs[i].Code)
		}
	}
}

// TestIdentityCapabilities 验证身份快照的开关查询与档案聚合。
func TestIdentityCapabilities(t *testing.T) {
	id := &Identity{
		Role: mustRole(t, RoleManager),
		Capabilities: []Capability{
			{Code: "practitioner", SubjectID: 7},
			{Code: "cashier"},
		},
		SelfSubjects: map[string][]uint64{
			"practitioner": {7},
			"teacher":      {8, 7, 0},
		},
	}
	if !id.HasCapability("practitioner") || id.HasCapability("teacher") {
		t.Fatal("HasCapability 结果错误")
	}
	c, ok := id.Capability("practitioner")
	if !ok || c.SubjectID != 7 {
		t.Fatalf("Capability 结果错误: %+v", c)
	}
	if len(id.Subjects("teacher")) != 3 {
		t.Fatalf("Subjects 应返回原始列表: %v", id.Subjects("teacher"))
	}

	// 多个 self_by_subject 开关合并去重，并跳过 0。
	got := map[uint64]bool{}
	for _, v := range id.SelfSubjectIDs() {
		got[v] = true
	}
	if len(got) != 2 || !got[7] || !got[8] {
		t.Fatalf("SelfSubjectIDs 去重结果错误: %v", id.SelfSubjectIDs())
	}

	if !id.CanManage() {
		t.Fatal("店长应具备管理权限")
	}
	id.Role = mustRole(t, RoleStaff)
	if id.CanManage() {
		t.Fatal("普通员工不应具备管理权限")
	}
}

// TestCapabilitySpecValidate 验证开关实例的纯校验规则。
func TestCapabilitySpecValidate(t *testing.T) {
	withSubject := CapabilitySpec{
		Code: uniqCode(t, "practitioner"), SubjectRequired: true,
		ScopeEffect: ScopeEffectSelfBySubject,
	}
	plain := CapabilitySpec{Code: uniqCode(t, "mentor")}

	if err := withSubject.Validate(CapabilityInput{}); !errors.Is(err, ErrCapabilitySubjectRequired) {
		t.Fatalf("必选档案缺失应报 ErrCapabilitySubjectRequired, got %v", err)
	}
	if err := withSubject.Validate(CapabilityInput{SubjectID: 9}); err != nil {
		t.Fatalf("合法实例不应报错: %v", err)
	}
	if err := plain.Validate(CapabilityInput{SubjectID: 5}); !errors.Is(err, ErrCapabilitySubjectNotAllowed) {
		t.Fatalf("无档案开关携带档案应报 ErrCapabilitySubjectNotAllowed, got %v", err)
	}
	if err := plain.Validate(CapabilityInput{}); err != nil {
		t.Fatalf("无档案开关空实例不应报错: %v", err)
	}

	if !withSubject.AffectsScope() || plain.AffectsScope() {
		t.Fatal("AffectsScope 判定错误")
	}
}

// TestCanEnterWorkbench 验证工作台准入：管理角色放行，普通员工需持有准入开关。
func TestCanEnterWorkbench(t *testing.T) {
	gate := uniqCode(t, "gate")
	if err := RegisterCapability(CapabilitySpec{Code: gate, WorkbenchAccess: true}); err != nil {
		t.Fatalf("注册准入开关失败: %v", err)
	}
	other := uniqCode(t, "other")
	if err := RegisterCapability(CapabilitySpec{Code: other}); err != nil {
		t.Fatalf("注册普通开关失败: %v", err)
	}

	manager := &Identity{Role: mustRole(t, RoleManager)}
	if !manager.CanEnterWorkbench() {
		t.Fatal("店长应可直接进入工作台")
	}

	withGate := &Identity{Role: mustRole(t, RoleStaff), Capabilities: []Capability{{Code: gate}}}
	if !withGate.CanEnterWorkbench() {
		t.Fatal("持有准入开关的员工应可进入工作台")
	}

	noGate := &Identity{Role: mustRole(t, RoleStaff), Capabilities: []Capability{{Code: other}}}
	if noGate.CanEnterWorkbench() {
		t.Fatal("仅持有无准入开关不应放行")
	}

	bare := &Identity{Role: mustRole(t, RoleStaff)}
	if bare.CanEnterWorkbench() {
		t.Fatal("无任何开关的员工不应放行")
	}
}

// TestDeriveScope 验证角色与开关共同决定数据范围。
func TestDeriveScope(t *testing.T) {
	capCode := uniqCode(t, "practitioner")
	if err := RegisterCapability(CapabilitySpec{
		Code: capCode, SubjectKind: "therapist", SubjectRequired: true,
		ScopeEffect: ScopeEffectSelfBySubject,
	}); err != nil {
		t.Fatalf("注册范围开关失败: %v", err)
	}
	noopCode := uniqCode(t, "cashier")
	if err := RegisterCapability(CapabilitySpec{Code: noopCode}); err != nil {
		t.Fatalf("注册无范围开关失败: %v", err)
	}

	cases := []struct {
		name string
		role string
		caps []Capability
		want DataScope
	}{
		{"owner 全店", RoleOwner, nil, ScopeAll},
		{"admin 全店", RoleAdmin, nil, ScopeAll},
		{"manager 本店", RoleManager, nil, ScopeOrg},
		{"员工持范围开关", RoleStaff, []Capability{{Code: capCode, SubjectID: 7}}, ScopeSelf},
		{"员工仅持无范围开关", RoleStaff, []Capability{{Code: noopCode}}, ScopeNone},
		{"员工无开关", RoleStaff, nil, ScopeNone},
		{"只读无开关", RoleViewer, nil, ScopeNone},
		{"店长兼服务者仍按管理范围", RoleManager, []Capability{{Code: capCode, SubjectID: 7}}, ScopeOrg},
	}
	for _, c := range cases {
		if got := DeriveScope(mustRole(t, c.role), c.caps); got != c.want {
			t.Fatalf("%s: DeriveScope = %v, want %v", c.name, got, c.want)
		}
	}
}
