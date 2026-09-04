package middleware

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	goredis "github.com/redis/go-redis/v9"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/response"
	sharedredis "github.com/xinpaiyun/nova-lib/redis"
)

const rateLimitWindow = time.Minute

// RateLimit 按客户端 IP 对请求做固定窗口限流，Redis 启用时跨实例共享计数。
func RateLimit(cfg config.RateLimitConfig) app.HandlerFunc {
	limiter := newFixedWindowLimiter(cfg.RequestsPerMin)
	return func(ctx context.Context, c *app.RequestContext) {
		if !cfg.Enabled || cfg.RequestsPerMin <= 0 || string(c.Method()) == "OPTIONS" {
			c.Next(ctx)
			return
		}
		key := rateLimitKey(c, cfg.TrustForwardedIP)
		if client := sharedredis.Client(); client != nil {
			ok, retryAfter, err := allowRedisRateLimit(ctx, client, key, cfg.RequestsPerMin)
			if err == nil {
				if !ok {
					rejectRateLimited(c, retryAfter)
					return
				}
				c.Next(ctx)
				return
			}
		}
		if ok, retryAfter := limiter.allow(key, time.Now()); !ok {
			rejectRateLimited(c, retryAfter)
			return
		}
		c.Next(ctx)
	}
}

// rejectRateLimited 写入统一限流响应。
func rejectRateLimited(c *app.RequestContext, retryAfter time.Duration) {
	c.Response.Header.Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
	response.Error(c, 429, "请求过于频繁，请稍后再试")
	c.Abort()
}

// allowRedisRateLimit 使用 Redis INCR 和 TTL 实现跨实例固定窗口限流。
func allowRedisRateLimit(ctx context.Context, client redisRateLimitClient, key string, limit int) (bool, time.Duration, error) {
	redisKey := "nova:rate_limit:" + key
	count, err := client.Incr(ctx, redisKey).Result()
	if err != nil {
		return false, 0, err
	}
	if count == 1 {
		if err := client.Expire(ctx, redisKey, rateLimitWindow).Err(); err != nil {
			return false, 0, err
		}
	}
	if count <= int64(limit) {
		return true, 0, nil
	}
	retryAfter, err := client.TTL(ctx, redisKey).Result()
	if err != nil || retryAfter <= 0 {
		retryAfter = rateLimitWindow
	}
	return false, retryAfter, nil
}

type fixedWindowLimiter struct {
	limit   int
	mu      sync.Mutex
	windows map[string]rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

type redisRateLimitClient interface {
	Incr(ctx context.Context, key string) *goredis.IntCmd
	Expire(ctx context.Context, key string, expiration time.Duration) *goredis.BoolCmd
	TTL(ctx context.Context, key string) *goredis.DurationCmd
}

// newFixedWindowLimiter 创建固定窗口限流器。
func newFixedWindowLimiter(limit int) *fixedWindowLimiter {
	if limit <= 0 {
		limit = 120
	}
	return &fixedWindowLimiter{limit: limit, windows: map[string]rateWindow{}}
}

// allow 判断当前请求是否允许通过，并返回建议重试时间。
func (l *fixedWindowLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	window := l.windows[key]
	if window.start.IsZero() || now.Sub(window.start) >= rateLimitWindow {
		l.windows[key] = rateWindow{start: now, count: 1}
		l.cleanup(now)
		return true, 0
	}
	if window.count >= l.limit {
		return false, rateLimitWindow - now.Sub(window.start)
	}
	window.count++
	l.windows[key] = window
	return true, 0
}

// cleanup 清理过期窗口，避免长期运行时键集合无限增长。
func (l *fixedWindowLimiter) cleanup(now time.Time) {
	for key, window := range l.windows {
		if now.Sub(window.start) >= 2*rateLimitWindow {
			delete(l.windows, key)
		}
	}
}

// rateLimitKey 返回限流键，默认使用连接 IP。
func rateLimitKey(c *app.RequestContext, trustForwardedIP bool) string {
	if trustForwardedIP {
		if forwarded := forwardedRateLimitIP(string(c.GetHeader("X-Forwarded-For")), string(c.GetHeader("X-Real-IP"))); forwarded != "" {
			return forwarded
		}
	}
	return c.ClientIP()
}

// forwardedRateLimitIP 返回可信代理头中的合法客户端 IP。
func forwardedRateLimitIP(forwarded string, realIP string) string {
	if ip := firstForwardedIP(forwarded); ip != "" {
		return ip
	}
	return normalizeRateLimitIP(realIP)
}

// firstForwardedIP 返回 X-Forwarded-For 中第一个合法 IP。
func firstForwardedIP(value string) string {
	for _, item := range strings.Split(value, ",") {
		if ip := normalizeRateLimitIP(item); ip != "" {
			return ip
		}
		break
	}
	return ""
}

// normalizeRateLimitIP 规范化限流使用的 IP，避免异常 Header 污染限流键。
func normalizeRateLimitIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	return addr.String()
}
