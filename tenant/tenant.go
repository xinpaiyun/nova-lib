// Package tenant 提供租户上下文的读取能力，与 middleware 包的写入侧（TenantFromHeader）配对使用。
package tenant

import "github.com/cloudwego/hertz/pkg/app"

const (
	// TenantIDKey 是请求上下文中的租户 ID 键。
	TenantIDKey = "tenant_id"
	// UserIDKey 是请求上下文中的用户 ID 键。
	UserIDKey = "user_id"
)

// TenantID 从请求上下文读取租户 ID。
func TenantID(c *app.RequestContext) uint64 {
	return GetUint64(c, TenantIDKey)
}

// UserID 从请求上下文读取用户 ID。
func UserID(c *app.RequestContext) uint64 {
	return GetUint64(c, UserIDKey)
}

// GetUint64 从请求上下文读取无符号整数业务字段（如门店/团队范围）。
func GetUint64(c *app.RequestContext, key string) uint64 {
	value, ok := c.Get(key)
	if !ok {
		return 0
	}
	id, _ := value.(uint64)
	return id
}
