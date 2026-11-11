package staff

// Capability 表示某员工实际持有的一个开关实例。
type Capability struct {
	Code      string
	SubjectID uint64
}

// Identity 是每请求解析出的员工身份快照，由 Resolver 填充（S2）。
// 业务层只读它，不做二次查询。
type Identity struct {
	TenantID uint64
	UserID   uint64
	MemberID uint64
	OrgID    uint64
	Name     string

	// Role 已解析成标准角色（别名也已归一）。
	Role RoleSpec
	// Capabilities 该员工当前持有的全部开关。
	Capabilities []Capability
	// Scope 最终数据范围：管理权限给上限，否则由开关决定。
	Scope DataScope
	// SelfSubjects 各 self_by_subject 开关对应的档案 ID，key 为开关码。
	SelfSubjects map[string][]uint64
}

// HasCapability 判断是否持有指定开关。
func (i *Identity) HasCapability(code string) bool {
	_, ok := i.Capability(code)
	return ok
}

// Capability 返回指定开关的第一个实例。
func (i *Identity) Capability(code string) (Capability, bool) {
	for _, c := range i.Capabilities {
		if c.Code == code {
			return c, true
		}
	}
	return Capability{}, false
}

// Subjects 返回指定开关关联的档案 ID 列表。
func (i *Identity) Subjects(code string) []uint64 {
	return i.SelfSubjects[code]
}

// CanManage 判断是否具备管理权限（manager 及以上）。
func (i *Identity) CanManage() bool {
	return i.Role.CanManage()
}

// SelfSubjectIDs 返回所有 self_by_subject 开关的档案 ID 合并去重结果，
// 用于 WHERE subject_col IN (...) 过滤；同时持有多个此类开关时全部生效。
func (i *Identity) SelfSubjectIDs() []uint64 {
	seen := make(map[uint64]struct{}, len(i.SelfSubjects))
	result := make([]uint64, 0, len(i.SelfSubjects))
	for _, ids := range i.SelfSubjects {
		for _, id := range ids {
			if id == 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result
}

// CanEnterWorkbench 判断能否进入商家工作台：管理角色直接放行；
// 普通员工必须持有任一声明了 WorkbenchAccess 的开关（如「服务者」）。
// 未注册的开关码不参与放行判定——开关实例由写入层保证对应已注册 spec，
// 这里按保守策略处理脏数据，宁可拒绝也不误放行。
func (i *Identity) CanEnterWorkbench() bool {
	if i.Role.CanManage() {
		return true
	}
	for _, c := range i.Capabilities {
		if spec, ok := CapabilitySpecByCode(c.Code); ok && spec.WorkbenchAccess {
			return true
		}
	}
	return false
}

// DeriveScope 依据角色与开关推导数据范围：
//   - 管理角色（manager 及以上）→ 角色默认范围（owner/admin 全店、manager 本店）；
//   - 普通角色持有任一 self_by_subject 开关 → ScopeSelf，档案集合见 Identity.SelfSubjects；
//   - 普通角色没有任何范围性开关 → ScopeNone（无可看数据，与「员工须挂准入开关
//     才能进工作台看数据」的规则一致）。
//
// staff 角色的 DefaultScope=ScopeSelf 只是语义占位，实际能否落到 self
// 完全取决于其持有的开关，因此统一从本函数取值，不要直接读 Role.DefaultScope。
func DeriveScope(role RoleSpec, caps []Capability) DataScope {
	if role.CanManage() {
		return role.DefaultScope
	}
	for _, c := range caps {
		if spec, ok := CapabilitySpecByCode(c.Code); ok && spec.AffectsScope() {
			return ScopeSelf
		}
	}
	return ScopeNone
}
