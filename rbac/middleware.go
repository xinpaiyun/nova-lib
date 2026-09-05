package rbac

import (
	"context"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/middleware"
	"github.com/xinpaiyun/nova-lib/response"
)

// identityFromRequest 从请求上下文组装权限判定身份快照。
func identityFromRequest(c *app.RequestContext) Identity {
	userID, _ := middleware.UserIDFromContext(c)
	tenantID, _ := middleware.TenantIDFromContext(c)
	return Identity{
		UserID:   userID,
		TenantID: tenantID,
		RoleCode: middleware.RoleCodeFromContext(c),
		AppType:  middleware.AppTypeFromContext(c),
		OpenID:   middleware.OpenIDFromContext(c),
	}
}

// PermissionRequired 校验当前请求具备 permission 权限码。
// checker 非空时以其判定为准；nil 时仅校验已登录（不校验具体权限）。
// resolver 非空时在判定前重解析角色码并覆盖会话内 RoleCode。
func PermissionRequired(checker Checker, permission string, resolver RoleResolver) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		ident := identityFromRequest(c)
		if ident.UserID == 0 {
			response.Error(c, 401, "请先登录")
			c.Abort()
			return
		}
		if resolver != nil {
			code, err := resolver(ctx, ident)
			if err != nil {
				response.Error(c, 403, ErrUnauthorized.Error())
				c.Abort()
				return
			}
			ident.RoleCode = code
		}
		if checker != nil {
			ok, denyMsg := checker(ctx, ident, permission)
			if !ok {
				msg := denyMsg
				if strings.TrimSpace(msg) == "" {
					msg = ErrNoPermission.Error()
				}
				response.Error(c, 403, msg)
				c.Abort()
				return
			}
		}
		c.Next(ctx)
	}
}

// RequireRoles 校验当前会话角色属于 allow 集合之一。allow 为空表示放行。
func RequireRoles(allow ...string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		userID, _ := middleware.UserIDFromContext(c)
		if userID == 0 {
			response.Error(c, 401, "请先登录")
			c.Abort()
			return
		}
		if len(allow) > 0 {
			code := middleware.RoleCodeFromContext(c)
			if !contains(allow, code) {
				response.Error(c, 403, ErrUnauthorized.Error())
				c.Abort()
				return
			}
		}
		c.Next(ctx)
	}
}

// RequireDataScope 校验断言当前会话具备 dataScope 数据范围访问权。
// 语义（团队数据范围、商户/店铺快照、多租户对象范围）由 checker 注入。
func RequireDataScope(checker Checker, dataScope string) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		ident := identityFromRequest(c)
		if checker == nil {
			c.Next(ctx)
			return
		}
		ok, denyMsg := checker(ctx, ident, dataScope)
		if !ok {
			msg := denyMsg
			if strings.TrimSpace(msg) == "" {
				msg = ErrUnauthorized.Error()
			}
			response.Error(c, 403, msg)
			c.Abort()
			return
		}
		c.Next(ctx)
	}
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}