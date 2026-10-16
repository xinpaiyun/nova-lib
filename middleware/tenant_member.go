package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"gorm.io/gorm"

	"github.com/xinpaiyun/nova-lib/logging"
	"github.com/xinpaiyun/nova-lib/org"
	"github.com/xinpaiyun/nova-lib/response"
)

// defaultTenantCacheTTL 为成员校验结果的默认进程内缓存时长。
const defaultTenantCacheTTL = 60 * time.Second

// PlatformAdminChecker 判断当前登录用户是否平台超管；返回 true 时跳过成员校验放行。
type PlatformAdminChecker func(ctx context.Context, userID uint64) bool

// TenantFallbackResolver 在 X-Tenant-Id 缺失时按用户身份推导兜底租户（兼容期过渡用），
// 返回 0 表示用户当前无租户。前端全量带上头后应移除兜底，回归纯头解析。
type TenantFallbackResolver func(ctx context.Context, userID uint64) (uint64, error)

// TenantMemberOptions 定义 RequireTenantMember 的可配置项。
type TenantMemberOptions struct {
	// DB 用于 org_members 成员关系查询，必填。
	DB *gorm.DB
	// PlatformAdmin 平台超管判断回调，可为 nil（无超管放行）。
	PlatformAdmin PlatformAdminChecker
	// Fallback 头缺失时的兜底解析，可为 nil（严格模式：缺头直接 403）。
	Fallback TenantFallbackResolver
	// CacheTTL 成员校验结果缓存时长，<=0 时使用默认 60s。
	CacheTTL time.Duration
}

// RequireTenantMember 在鉴权之后解析 X-Tenant-Id 请求头并校验成员关系：
// 头缺失时先走 Fallback 兜底（若配置）；随后校验当前用户是该租户的 org_members
// 有效成员（结果带进程内 TTL 缓存），平台超管直接放行；通过后把租户 ID 覆盖写入
// 请求上下文，后续经 TenantIDFromContext 读取。
// 必须挂在 RequireAuth / RequireSessionAuth 之后使用。
func RequireTenantMember(opts TenantMemberOptions) app.HandlerFunc {
	ttl := opts.CacheTTL
	if ttl <= 0 {
		ttl = defaultTenantCacheTTL
	}
	cache := newMemberCache(ttl)
	return func(ctx context.Context, c *app.RequestContext) {
		userID, ok := UserIDFromContext(c)
		if !ok {
			response.Error(c, consts.StatusUnauthorized, "请先登录")
			c.Abort()
			return
		}
		tenantID, _ := parseTenantID(string(c.GetHeader("X-Tenant-Id")))
		if tenantID == 0 && opts.Fallback != nil {
			resolved, err := opts.Fallback(ctx, userID)
			if err != nil {
				logging.Error("resolve tenant fallback failed",
					"request_id", RequestIDFromContext(c),
					"user_id", userID,
					"error", err,
				)
				response.Error(c, consts.StatusInternalServerError, "服务暂时不可用")
				c.Abort()
				return
			}
			tenantID = resolved
		}
		if tenantID == 0 {
			response.Error(c, consts.StatusForbidden, "请选择品牌")
			c.Abort()
			return
		}
		if opts.PlatformAdmin != nil && opts.PlatformAdmin(ctx, userID) {
			c.Set(tenantIDKey, tenantID)
			c.Next(ctx)
			return
		}
		allowed, err := memberAllowed(ctx, opts.DB, cache, tenantID, userID)
		if err != nil {
			logging.Error("check tenant member failed",
				"request_id", RequestIDFromContext(c),
				"user_id", userID,
				"tenant_id", tenantID,
				"error", err,
			)
			response.Error(c, consts.StatusInternalServerError, "服务暂时不可用")
			c.Abort()
			return
		}
		if !allowed {
			response.Error(c, consts.StatusForbidden, "无该品牌访问权限")
			c.Abort()
			return
		}
		c.Set(tenantIDKey, tenantID)
		c.Next(ctx)
	}
}

// memberAllowed 查询并缓存成员关系判定结果（正/负结果都缓存，
// 成员被移除后最迟一个 TTL 内生效）。
func memberAllowed(ctx context.Context, db *gorm.DB, cache *memberCache, orgID, userID uint64) (bool, error) {
	if allowed, hit := cache.get(orgID, userID); hit {
		return allowed, nil
	}
	allowed, err := org.IsMember(ctx, db, orgID, userID)
	if err != nil {
		return false, err
	}
	cache.set(orgID, userID, allowed)
	return allowed, nil
}

// memberCacheKey 为成员校验缓存的键。
type memberCacheKey struct {
	orgID  uint64
	userID uint64
}

// memberCache 是成员校验结果的进程内 TTL 缓存，只在中间件实例内生效。
type memberCache struct {
	ttl   time.Duration
	mu    sync.RWMutex
	items map[memberCacheKey]time.Time
	// maxItems 触发惰性清理的条目上限，防止长期运行下异常膨胀。
	maxItems int
}

func newMemberCache(ttl time.Duration) *memberCache {
	return &memberCache{ttl: ttl, items: make(map[memberCacheKey]time.Time), maxItems: 100000}
}

// get 返回 (允许访问, 是否命中)。
func (m *memberCache) get(orgID, userID uint64) (bool, bool) {
	m.mu.RLock()
	expireAt, ok := m.items[memberCacheKey{orgID, userID}]
	m.mu.RUnlock()
	if !ok || time.Now().After(expireAt) {
		return false, false
	}
	return true, true
}

func (m *memberCache) set(orgID, userID uint64, allowed bool) {
	if !allowed {
		// 负结果缓存较短 TTL：成员新增后尽快生效。
		m.mu.Lock()
		m.items[memberCacheKey{orgID, userID}] = time.Now().Add(m.ttl / 6)
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	if len(m.items) >= m.maxItems {
		now := time.Now()
		for key, expireAt := range m.items {
			if now.After(expireAt) {
				delete(m.items, key)
			}
		}
	}
	m.items[memberCacheKey{orgID, userID}] = time.Now().Add(m.ttl)
	m.mu.Unlock()
}
