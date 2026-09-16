package middleware

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
)

// appIDKey X-App-Id 解析结果的上下文键。
const appIDKey = "nova:app_id"

// AppIDResolver 根据小程序 AppID 反查租户 ID（查不到返回 false）。
type AppIDResolver func(appID string) (uint64, bool)

// CachedAppIDResolver 为 AppIDResolver 叠加进程内 TTL 缓存（减少每次请求反查 DB）。
func CachedAppIDResolver(resolve AppIDResolver, ttl time.Duration) AppIDResolver {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	type entry struct {
		tenantID uint64
		ok       bool
		expireAt time.Time
	}
	var (
		mu    sync.Mutex
		cache = make(map[string]entry)
	)
	return func(appID string) (uint64, bool) {
		appID = strings.TrimSpace(appID)
		if appID == "" {
			return 0, false
		}
		mu.Lock()
		if cached, ok := cache[appID]; ok && time.Now().Before(cached.expireAt) {
			mu.Unlock()
			return cached.tenantID, cached.ok
		}
		mu.Unlock()
		tenantID, ok := resolve(appID)
		mu.Lock()
		cache[appID] = entry{tenantID: tenantID, ok: ok, expireAt: time.Now().Add(ttl)}
		mu.Unlock()
		return tenantID, ok
	}
}

// TenantFromAppID 从 X-App-Id 请求头反查租户 ID 并写入请求上下文。
// 须挂在 TenantFromRequest 之后（未知 appid 直接 404，避免无租户上下文的脏请求继续执行）。
func TenantFromAppID(resolve AppIDResolver) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if _, ok := TenantIDFromContext(c); ok {
			// 租户已解析（如显式 X-Tenant-ID），不覆盖。
			c.Next(ctx)
			return
		}
		appID := strings.TrimSpace(string(c.GetHeader("X-App-Id")))
		if appID != "" && resolve != nil {
			if tenantID, ok := resolve(appID); ok {
				c.Set(tenantIDKey, tenantID)
				c.Set(appIDKey, appID)
				c.Next(ctx)
				return
			}
		}
		c.AbortWithStatusJSON(404, map[string]any{"code": 404, "message": "应用不存在或未启用"})
	}
}

// AppIDFromContext 从请求上下文读取 X-App-Id。
func AppIDFromContext(c *app.RequestContext) string {
	if value, ok := c.Get(appIDKey); ok {
		if appID, ok := value.(string); ok {
			return appID
		}
	}
	return ""
}
