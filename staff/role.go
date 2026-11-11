package staff

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// 角色与开关相关的业务错误。
var (
	// ErrUserNotRegistered 手机号/身份尚未注册，需对方先完成注册。
	ErrUserNotRegistered = errors.New("该手机号尚未注册，请让对方先注册")
	// ErrRoleUnknown 角色码未注册。
	ErrRoleUnknown = errors.New("未知角色")
	// ErrRoleDuplicate 角色码重复注册。
	ErrRoleDuplicate = errors.New("角色码已注册")
	// ErrRoleCodeEmpty 角色码为空。
	ErrRoleCodeEmpty = errors.New("角色码不能为空")
	// ErrCapabilitySpecUnknown 开关未注册。
	ErrCapabilitySpecUnknown = errors.New("开关未注册")
	// ErrCapabilitySpecDuplicate 开关码重复注册。
	ErrCapabilitySpecDuplicate = errors.New("开关码已注册")
	// ErrCapabilityCodeEmpty 开关码为空。
	ErrCapabilityCodeEmpty = errors.New("开关码不能为空")
	// ErrCapabilitySubjectRequired 该开关必须关联档案。
	ErrCapabilitySubjectRequired = errors.New("请选择关联档案")
	// ErrCapabilitySubjectTaken 档案已被其他员工绑定。
	ErrCapabilitySubjectTaken = errors.New("该档案已被其他员工绑定")
	// ErrCapabilitySubjectKindMissing 要求关联档案但未声明档案类型。
	ErrCapabilitySubjectKindMissing = errors.New("声明 SubjectRequired 时必须提供 SubjectKind")
	// ErrCapabilitySubjectNotAllowed 该开关无需关联档案，却传入了档案 ID。
	ErrCapabilitySubjectNotAllowed = errors.New("该开关无需关联档案")
	// ErrScopeEffectInvalid 范围影响类型非法。
	ErrScopeEffectInvalid = errors.New("非法的 ScopeEffect")
	// ErrRoleScopeInvalid 角色默认范围非法。
	ErrRoleScopeInvalid = errors.New("非法的角色默认范围")
	// ErrNotMember 当前用户不是该租户员工。
	ErrNotMember = errors.New("当前账号不是该商家员工")
	// ErrMemberDisabled 员工账号已禁用。
	ErrMemberDisabled = errors.New("员工账号已被禁用")
	// ErrMemberNotFound 员工不存在。
	ErrMemberNotFound = errors.New("员工不存在")
	// ErrMemberExists 同一租户内该用户已是员工。
	ErrMemberExists = errors.New("该员工已存在于当前租户")
	// ErrMemberInputInvalid 员工入参不完整。
	ErrMemberInputInvalid = errors.New("员工信息不完整")
	// ErrUserDisabled 手机号对应用户已被停用。
	ErrUserDisabled = errors.New("该手机号账号已停用")
	// ErrPhoneInvalid 手机号格式不正确。
	ErrPhoneInvalid = errors.New("手机号格式不正确")
	// ErrPermissionDenied 当前身份无权管理员工。
	ErrPermissionDenied = errors.New("无权限管理员工")
)

// DataScope 表示数据可见范围，数值越大范围越广。
// 只允许三档，避免各项目自行发明范围语义。
type DataScope int8

const (
	// ScopeNone 无任何数据权限（未命中员工关系时使用）。
	ScopeNone DataScope = 0
	// ScopeSelf 仅本人（按开关关联的档案收敛）。
	ScopeSelf DataScope = 1
	// ScopeOrg 本门店/本组织。
	ScopeOrg DataScope = 2
	// ScopeAll 全租户。
	ScopeAll DataScope = 3
)

// Allows 判断当前范围是否覆盖目标范围。
func (s DataScope) Allows(target DataScope) bool {
	return s >= target
}

// String 返回可读名称，便于日志与错误信息。
func (s DataScope) String() string {
	switch s {
	case ScopeSelf:
		return "self"
	case ScopeOrg:
		return "org"
	case ScopeAll:
		return "all"
	default:
		return "none"
	}
}

// RoleSpec 描述一个管理权限角色的语义。Rank 用于「至少需要某级别」的判断。
type RoleSpec struct {
	Code         string
	Name         string
	Rank         int
	DefaultScope DataScope
}

// CanManage 表示该角色默认具备管理能力（manager 及以上）。
func (r RoleSpec) CanManage() bool {
	return r.Rank >= roleRankManager
}

const (
	roleRankOwner   = 100
	roleRankAdmin   = 80
	roleRankManager = 60
	roleRankStaff   = 40
	roleRankViewer  = 20
)

type roleRegistryStore struct {
	sync.RWMutex
	specs   map[string]RoleSpec
	aliases map[string]string
}

var roleRegistry = &roleRegistryStore{
	specs:   map[string]RoleSpec{},
	aliases: map[string]string{},
}

func init() {
	// 内置角色由 lib 统一注册，项目不要重复定义同名角色。
	for _, spec := range []RoleSpec{
		{Code: RoleOwner, Name: "品牌主", Rank: roleRankOwner, DefaultScope: ScopeAll},
		{Code: RoleAdmin, Name: "管理员", Rank: roleRankAdmin, DefaultScope: ScopeAll},
		{Code: RoleManager, Name: "店长", Rank: roleRankManager, DefaultScope: ScopeOrg},
		{Code: RoleStaff, Name: "员工", Rank: roleRankStaff, DefaultScope: ScopeSelf},
		{Code: RoleViewer, Name: "只读", Rank: roleRankViewer, DefaultScope: ScopeOrg},
	} {
		if err := RegisterRole(spec); err != nil {
			panic(err)
		}
	}
}

// RegisterRole 注册一个管理权限角色。重复注册返回 ErrRoleDuplicate。
func RegisterRole(spec RoleSpec) error {
	code := strings.TrimSpace(spec.Code)
	if code == "" {
		return ErrRoleCodeEmpty
	}
	if spec.DefaultScope < ScopeSelf || spec.DefaultScope > ScopeAll {
		return ErrRoleScopeInvalid
	}
	spec.Code = code
	roleRegistry.Lock()
	defer roleRegistry.Unlock()
	if _, ok := roleRegistry.specs[code]; ok {
		return ErrRoleDuplicate
	}
	roleRegistry.specs[code] = spec
	return nil
}

// RegisterRoleAlias 把项目存量角色码映射到标准角色码（如 store_admin → manager）。
// 别名与标准码都走 ResolveRole，存量数据不需要改码。
func RegisterRoleAlias(alias, code string) error {
	alias = strings.TrimSpace(alias)
	code = strings.TrimSpace(code)
	if alias == "" || code == "" {
		return ErrRoleCodeEmpty
	}
	roleRegistry.Lock()
	defer roleRegistry.Unlock()
	if _, ok := roleRegistry.specs[alias]; ok {
		return ErrRoleDuplicate
	}
	if _, ok := roleRegistry.aliases[alias]; ok {
		return ErrRoleDuplicate
	}
	if _, ok := roleRegistry.specs[code]; !ok {
		return ErrRoleUnknown
	}
	roleRegistry.aliases[alias] = code
	return nil
}

// ResolveRole 解析角色码，先命中标准码，再走别名。
func ResolveRole(code string) (RoleSpec, bool) {
	code = strings.TrimSpace(code)
	roleRegistry.RLock()
	defer roleRegistry.RUnlock()
	if spec, ok := roleRegistry.specs[code]; ok {
		return spec, true
	}
	if target, ok := roleRegistry.aliases[code]; ok {
		spec, ok := roleRegistry.specs[target]
		return spec, ok
	}
	return RoleSpec{}, false
}

// RoleSpecs 返回全部标准角色，按 Rank 从高到低排序。
func RoleSpecs() []RoleSpec {
	roleRegistry.RLock()
	defer roleRegistry.RUnlock()
	result := make([]RoleSpec, 0, len(roleRegistry.specs))
	for _, spec := range roleRegistry.specs {
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Rank > result[j].Rank })
	return result
}
