package staff

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/xinpaiyun/nova-lib/auth"
	"github.com/xinpaiyun/nova-lib/logging"
	"github.com/xinpaiyun/nova-lib/middleware"
	"github.com/xinpaiyun/nova-lib/response"
)

// identityContextKey 为员工身份在 Hertz 请求上下文中的存储键。
const identityContextKey = "staff_identity"

// Required 解析当前请求的员工身份，并写入请求上下文与 ctx。
//
// 依赖关系：必须挂在登录中间件（RequireAuth / RequireSessionAuth）之后。
// 租户来源依次为请求上下文、X-Tenant-Id 请求头、登录声明中的租户。
// 解析成功后，后续 handler 可用 FromContext(c) 或 IdentityFromContext(ctx) 取用；
// 未命中员工关系、员工被禁用均返回 403，基础设施错误返回 500。
func Required(src Source) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		userID, ok := middleware.UserIDFromContext(c)
		if !ok {
			// 兼容把身份写入 context.Context 的项目鉴权中间件。
			if claims, found := auth.ClaimsFromContext(ctx); found && claims.UserID > 0 {
				userID, ok = claims.UserID, true
			}
		}
		if !ok {
			response.Error(c, consts.StatusUnauthorized, "请先登录")
			c.Abort()
			return
		}

		tenantID, _ := middleware.TenantIDFromContext(c)
		if tenantID == 0 {
			tenantID, _ = tenantIDFromHeader(string(c.GetHeader("X-Tenant-Id")))
		}
		if tenantID == 0 {
			if claims, ok := auth.ClaimsFromContext(ctx); ok {
				tenantID = claims.TenantID
			}
		}
		if tenantID == 0 {
			response.Error(c, consts.StatusForbidden, "请选择品牌")
			c.Abort()
			return
		}

		identity, err := Resolve(ctx, src, tenantID, userID)
		if err != nil {
			if errors.Is(err, ErrNotMember) || errors.Is(err, ErrMemberDisabled) {
				response.Error(c, consts.StatusForbidden, err.Error())
				c.Abort()
				return
			}
			logging.Error("resolve staff identity failed",
				"request_id", middleware.RequestIDFromContext(c),
				"user_id", userID,
				"tenant_id", tenantID,
				"error", err,
			)
			response.Error(c, consts.StatusInternalServerError, "服务暂时不可用")
			c.Abort()
			return
		}

		c.Set(identityContextKey, identity)
		c.Next(WithIdentity(ctx, identity))
	}
}

// FromContext 从 Hertz 请求上下文读取员工身份，未经过 Required 时返回 false。
func FromContext(c *app.RequestContext) (*Identity, bool) {
	value, ok := c.Get(identityContextKey)
	if !ok {
		return nil, false
	}
	identity, ok := value.(*Identity)
	return identity, ok && identity != nil
}

// RequireRole 要求当前员工角色不低于 minCode 的权限等级。
// minCode 走 ResolveRole，因此存量别名同样可用；未注册的角色码一律拒绝。
func RequireRole(minCode string) app.HandlerFunc {
	minimum, ok := ResolveRole(minCode)
	if !ok {
		return denyAll("未知角色")
	}
	return requireIdentity(func(identity *Identity) bool {
		return identity.Role.Rank >= minimum.Rank
	}, "角色权限不足")
}

// RequireCapability 要求当前员工持有 codes 中任意一个开关。
// codes 为空时一律拒绝，避免误配置成全员放行。
func RequireCapability(codes ...string) app.HandlerFunc {
	if len(codes) == 0 {
		return denyAll("未配置所需开关")
	}
	normalized := make([]string, 0, len(codes))
	for _, code := range codes {
		if code = strings.TrimSpace(code); code != "" {
			normalized = append(normalized, code)
		}
	}
	if len(normalized) == 0 {
		return denyAll("未配置所需开关")
	}
	return requireIdentity(func(identity *Identity) bool {
		for _, code := range normalized {
			if identity.HasCapability(code) {
				return true
			}
		}
		return false
	}, "缺少所需权限")
}

// RequireScope 要求当前员工的数据范围至少覆盖 min。
func RequireScope(min DataScope) app.HandlerFunc {
	return requireIdentity(func(identity *Identity) bool {
		return identity.Scope.Allows(min)
	}, "数据权限不足")
}

// requireIdentity 在 Required 之后执行断言，未解析身份或断言失败均拒绝。
func requireIdentity(check func(*Identity) bool, message string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		identity, ok := FromContext(c)
		if !ok {
			response.Error(c, consts.StatusForbidden, "无员工身份")
			c.Abort()
			return
		}
		if !check(identity) {
			response.Error(c, consts.StatusForbidden, message)
			c.Abort()
			return
		}
		c.Next(ctx)
	}
}

// denyAll 返回恒定拒绝的 handler，用于中间件配置本身非法时 fail-closed。
func denyAll(message string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		response.Error(c, consts.StatusForbidden, message)
		c.Abort()
	}
}

// tenantIDFromHeader 解析 X-Tenant-Id 请求头。
func tenantIDFromHeader(value string) (uint64, bool) {
	tenantID, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return tenantID, err == nil
}
