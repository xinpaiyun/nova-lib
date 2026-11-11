package staff

import (
	"context"
	"errors"
)

// Resolve 按 (tenant_id, user_id) 解析出一个可用的员工身份快照。
//
// 这是业务侧取身份的唯一入口：中间件、HTTP handler、Service 都调它，
// 避免各项目自己查 staff_accounts 再各自拼权限。
//
// 失败语义：
//   - 不是该租户员工 → ErrNotMember
//   - 员工已禁用     → ErrMemberDisabled
//   - 角色码未注册   → ErrRoleUnknown
func Resolve(ctx context.Context, src Source, tenantID, userID uint64) (*Identity, error) {
	if src == nil || tenantID == 0 || userID == 0 {
		return nil, ErrNotMember
	}

	member, err := src.MemberByUser(ctx, tenantID, userID)
	if err != nil {
		if errors.Is(err, ErrMemberNotFound) {
			return nil, ErrNotMember
		}
		return nil, err
	}
	if member.Status != StatusEnabled {
		return nil, ErrMemberDisabled
	}

	return assemble(ctx, src, member)
}

// ResolveMember 按员工主键解析身份（管理端查看某员工权限时使用）。
func ResolveMember(ctx context.Context, src Source, tenantID, memberID uint64) (*Identity, error) {
	if src == nil || tenantID == 0 || memberID == 0 {
		return nil, ErrMemberNotFound
	}
	member, err := src.MemberByID(ctx, tenantID, memberID)
	if err != nil {
		return nil, err
	}
	if member.Status != StatusEnabled {
		return nil, ErrMemberDisabled
	}
	return assemble(ctx, src, member)
}

// assemble 把员工行与开关实例组装成 Identity，并推导数据范围。
func assemble(ctx context.Context, src Source, member *StaffMember) (*Identity, error) {
	role, ok := ResolveRole(member.RoleCode)
	if !ok {
		return nil, ErrRoleUnknown
	}

	rows, err := src.Capabilities(ctx, member.TenantID, member.ID)
	if err != nil {
		return nil, err
	}

	caps := make([]Capability, 0, len(rows))
	subjects := make(map[string][]uint64, len(rows))
	for _, row := range rows {
		caps = append(caps, Capability{Code: row.Code, SubjectID: row.SubjectID})
		// 只有已注册且声明影响范围的开关才进入 SelfSubjects，
		// 未注册的脏数据不会意外扩大或缩小数据范围。
		if spec, exist := CapabilitySpecByCode(row.Code); exist && spec.AffectsScope() {
			subjects[row.Code] = append(subjects[row.Code], row.SubjectID)
		}
	}

	return &Identity{
		TenantID:     member.TenantID,
		UserID:       member.UserID,
		MemberID:     member.ID,
		OrgID:        member.OrgID,
		Name:         member.Name,
		Role:         role,
		Capabilities: caps,
		Scope:        DeriveScope(role, caps),
		SelfSubjects: subjects,
	}, nil
}

// ---- 上下文存取 ----

type identityKey struct{}

// WithIdentity 把身份快照放入上下文，供同一请求的后续逻辑复用。
// 中间件每请求解析一次后调用本函数，handler/Service 一律走 IdentityFromContext，
// 不再做二次查询。
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFromContext 读取身份快照；中间件未注入时返回 false。
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(*Identity)
	return id, ok && id != nil
}

// RequireIdentity 读取身份快照，缺失视为非员工。
func RequireIdentity(ctx context.Context) (*Identity, error) {
	if id, ok := IdentityFromContext(ctx); ok {
		return id, nil
	}
	return nil, ErrNotMember
}
