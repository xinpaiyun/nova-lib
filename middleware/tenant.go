package middleware

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
)

// TenantDomainResolver 根据租户域名返回租户 ID。
type TenantDomainResolver func(domain string) (uint64, bool)

// TenantFromHeader 从请求头解析租户 ID 并写入请求上下文。
func TenantFromHeader() app.HandlerFunc {
	return TenantFromRequest(nil)
}

// TenantFromRequest 从请求头或访问域名解析租户 ID 并写入请求上下文。
func TenantFromRequest(resolve TenantDomainResolver) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		tenantIDHeader := string(c.GetHeader("X-Tenant-ID"))
		if tenantID, ok := parseTenantID(tenantIDHeader); ok {
			c.Set(tenantIDKey, tenantID)
			c.Next(ctx)
			return
		}
		domain := tenantDomain(c)
		if resolve != nil && domain != "" {
			if tenantID, ok := resolve(domain); ok {
				c.Set(tenantIDKey, tenantID)
			}
		}
		c.Next(ctx)
	}
}

// TenantIDFromContext 从请求上下文中读取租户 ID。
func TenantIDFromContext(c *app.RequestContext) (uint64, bool) {
	value, ok := c.Get(tenantIDKey)
	if !ok {
		return 0, false
	}
	tenantID, ok := value.(uint64)
	return tenantID, ok
}

// parseTenantID 解析租户 ID 请求头。
func parseTenantID(value string) (uint64, bool) {
	tenantID, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return tenantID, err == nil
}

// tenantDomain 返回显式租户域名或 Host 中的域名部分。
func tenantDomain(c *app.RequestContext) string {
	domain := normalizeTenantDomain(string(c.GetHeader("X-Tenant-Domain")))
	if domain != "" {
		return domain
	}
	return normalizeHostDomain(string(c.GetHeader("Host")))
}

// normalizeTenantDomain 规范化显式传入的租户域名。
func normalizeTenantDomain(value string) string {
	domain := strings.TrimSpace(value)
	if domain == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(domain); err == nil {
		domain = host
	}
	return strings.ToLower(strings.TrimSuffix(domain, "."))
}

// normalizeHostDomain 规范化 Host，并跳过本地开发 Host。
func normalizeHostDomain(value string) string {
	domain := normalizeTenantDomain(value)
	switch domain {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1", "[::1]":
		return ""
	default:
		return domain
	}
}
