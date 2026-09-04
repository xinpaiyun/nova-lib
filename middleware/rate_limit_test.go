package middleware

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type fakeRedisRateLimitClient struct {
	count     int64
	ttl       time.Duration
	incrErr   error
	expireErr error
	ttlErr    error
	expired   bool
}

// Incr 模拟 Redis INCR 命令。
func (f *fakeRedisRateLimitClient) Incr(_ context.Context, _ string) *goredis.IntCmd {
	if f.incrErr != nil {
		return goredis.NewIntResult(0, f.incrErr)
	}
	f.count++
	return goredis.NewIntResult(f.count, nil)
}

// Expire 模拟 Redis EXPIRE 命令。
func (f *fakeRedisRateLimitClient) Expire(_ context.Context, _ string, _ time.Duration) *goredis.BoolCmd {
	if f.expireErr != nil {
		return goredis.NewBoolResult(false, f.expireErr)
	}
	f.expired = true
	return goredis.NewBoolResult(true, nil)
}

// TTL 模拟 Redis TTL 命令。
func (f *fakeRedisRateLimitClient) TTL(_ context.Context, _ string) *goredis.DurationCmd {
	if f.ttlErr != nil {
		return goredis.NewDurationResult(0, f.ttlErr)
	}
	return goredis.NewDurationResult(f.ttl, nil)
}

// TestFixedWindowLimiter 验证内存固定窗口限流和窗口重置。
func TestFixedWindowLimiter(t *testing.T) {
	limiter := newFixedWindowLimiter(2)
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	if ok, _ := limiter.allow("client", now); !ok {
		t.Fatal("first request rejected, want allowed")
	}
	if ok, _ := limiter.allow("client", now.Add(10*time.Second)); !ok {
		t.Fatal("second request rejected, want allowed")
	}
	if ok, retryAfter := limiter.allow("client", now.Add(20*time.Second)); ok || retryAfter <= 0 {
		t.Fatalf("third request = (%v, %v), want rejected with retryAfter", ok, retryAfter)
	}
	if ok, _ := limiter.allow("client", now.Add(rateLimitWindow)); !ok {
		t.Fatal("request after window rejected, want allowed")
	}
}

// TestAllowRedisRateLimit 验证 Redis 固定窗口限流会设置过期时间并返回 TTL。
func TestAllowRedisRateLimit(t *testing.T) {
	client := &fakeRedisRateLimitClient{ttl: 30 * time.Second}
	ctx := context.Background()
	if ok, retryAfter, err := allowRedisRateLimit(ctx, client, "client", 2); !ok || retryAfter != 0 || err != nil {
		t.Fatalf("first allowRedisRateLimit() = (%v, %v, %v), want allowed", ok, retryAfter, err)
	}
	if !client.expired {
		t.Fatal("first redis counter did not set expire")
	}
	if ok, _, err := allowRedisRateLimit(ctx, client, "client", 2); !ok || err != nil {
		t.Fatalf("second allowRedisRateLimit() = (%v, %v), want allowed", ok, err)
	}
	if ok, retryAfter, err := allowRedisRateLimit(ctx, client, "client", 2); ok || retryAfter != 30*time.Second || err != nil {
		t.Fatalf("third allowRedisRateLimit() = (%v, %v, %v), want rejected with ttl", ok, retryAfter, err)
	}
}

// TestAllowRedisRateLimitFallbackTTL 验证 Redis TTL 不可用时返回保守重试时间。
func TestAllowRedisRateLimitFallbackTTL(t *testing.T) {
	client := &fakeRedisRateLimitClient{count: 2, ttlErr: errors.New("ttl failed")}
	ok, retryAfter, err := allowRedisRateLimit(context.Background(), client, "client", 2)
	if ok || retryAfter != rateLimitWindow || err != nil {
		t.Fatalf("allowRedisRateLimit() = (%v, %v, %v), want rejected with default ttl", ok, retryAfter, err)
	}
}

// TestForwardedRateLimitIP 验证代理 IP 只接受合法地址并回退到 X-Real-IP。
func TestForwardedRateLimitIP(t *testing.T) {
	tests := []struct {
		name      string
		forwarded string
		realIP    string
		want      string
	}{
		{name: "first forwarded ip", forwarded: "203.0.113.10, 10.0.0.1", want: "203.0.113.10"},
		{name: "ipv6 forwarded ip", forwarded: "2001:db8::1", want: "2001:db8::1"},
		{name: "invalid forwarded fallback real ip", forwarded: "bad-ip, 203.0.113.10", realIP: "198.51.100.20", want: "198.51.100.20"},
		{name: "invalid real ip", forwarded: "", realIP: "127.0.0.1\nspoofed", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forwardedRateLimitIP(tt.forwarded, tt.realIP); got != tt.want {
				t.Fatalf("forwardedRateLimitIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
