package rbac

import (
	"context"
	"errors"
)

// Checker 判定当前身份是否具备指定权限码。
// 各项目把自身的业务判定语义（团队数据范围、商户/店铺快照、多租户对象校验）实现在这里。
// 返回 (false, message) 表示拒绝；(true, "") 表示放行。
type Checker func(ctx context.Context, identity Identity, permission string) (bool, string)

// RoleResolver 根据身份解析入会话的角色码；nil 时中间件直接使用会话内 RoleCode。
type RoleResolver func(ctx context.Context, identity Identity) (string, error)

// Identity 是权限判定收到的统一身份快照。
type Identity struct {
	UserID   uint64
	TenantID uint64
	RoleCode string
	AppType  string
	OpenID   string
	// Perms 是预加载到会话的角色权限码集合（按需由 Enforcer 填充，业务 Checker 可复用）。
	Perms map[string]struct{}
}

// RBAC 域错误。
var (
	ErrUnauthorized = errors.New("无权访问该资源")
	ErrNoPermission = errors.New("缺少所需权限")
)