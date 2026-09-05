// Package middleware 提供基于 Hertz 的通用 HTTP 中间件：
// 请求追踪 ID、跨域、安全响应头、异常恢复、访问日志、限流、租户解析与 JWT 鉴权。
// 业务侧的会话校验、访问日志落库等依赖项目模型的逻辑，通过回调参数化注入。
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/auth"
	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/logging"
	"github.com/xinpaiyun/nova-lib/metrics"
	"github.com/xinpaiyun/nova-lib/response"
)

const (
	requestIDKey = "request_id"
	userIDKey    = "user_id"
	tenantIDKey  = "tenant_id"
	roleCodeKey  = "role_code"
	maxRequestID = 64
)

// SessionValidator 在 JWT 解析成功后执行业务侧会话校验，
// 返回 false 时以 message 拒绝请求；传 nil 表示无状态校验。
type SessionValidator func(ctx context.Context, claims *auth.Claims, c *app.RequestContext) (bool, string)

// AccessLogRecorder 在每次请求结束后回调，用于业务侧访问日志落库等扩展。
type AccessLogRecorder func(c *app.RequestContext, method string, path string, status int, latency time.Duration)

// RequestID 为每个请求补充追踪 ID，并写入响应头。
func RequestID() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		requestID := normalizeRequestID(string(c.GetHeader("X-Request-ID")))
		if requestID == "" {
			requestID = randomRequestID()
		}
		c.Set(requestIDKey, requestID)
		c.Response.Header.Set("X-Request-ID", requestID)
		c.Next(ctx)
	}
}

// CORS 根据配置设置跨域响应头，默认适合本地调试，生产可收敛允许来源。
func CORS(cfg config.CORSConfig) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		origin := strings.TrimSpace(string(c.GetHeader("Origin")))
		if allowedOrigin, ok := resolveAllowedOrigin(origin, cfg); ok {
			c.Response.Header.Set("Access-Control-Allow-Origin", allowedOrigin)
			c.Response.Header.Set("Vary", "Origin")
		}
		if cfg.AllowCredentials {
			c.Response.Header.Set("Access-Control-Allow-Credentials", "true")
		}
		c.Response.Header.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, X-Tenant-ID, X-Tenant-Domain")
		c.Response.Header.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if string(c.Method()) == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next(ctx)
	}
}

// SecurityHeaders 为浏览器访问补充基础安全响应头。
func SecurityHeaders(cfg config.SecurityHeadersConfig) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if cfg.Enabled {
			c.Response.Header.Set("X-Content-Type-Options", "nosniff")
			c.Response.Header.Set("X-Frame-Options", "DENY")
			c.Response.Header.Set("Referrer-Policy", "no-referrer")
			c.Response.Header.Set("X-XSS-Protection", "0")
		}
		c.Next(ctx)
	}
}

// resolveAllowedOrigin 返回当前请求允许写入的跨域来源。
func resolveAllowedOrigin(origin string, cfg config.CORSConfig) (string, bool) {
	if len(cfg.AllowedOrigins) == 0 {
		return "*", true
	}
	for _, allowed := range cfg.AllowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if allowed == "*" {
			if cfg.AllowCredentials && origin != "" {
				return origin, true
			}
			return "*", true
		}
		if origin != "" && strings.EqualFold(origin, allowed) {
			return origin, true
		}
	}
	return "", false
}

// Recovery 捕获未处理 panic，返回统一错误结构并保留 requestId 便于排查。
func Recovery() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		defer func() {
			if value := recover(); value != nil {
				logging.Error("request panic recovered",
					"request_id", RequestIDFromContext(c),
					"panic", panicValue(value),
					"stack", string(debug.Stack()),
				)
				response.Error(c, 500, "服务暂时不可用")
				c.Abort()
			}
		}()
		c.Next(ctx)
	}
}

// AccessLog 记录请求访问日志（JSON 结构化输出）和进程内指标。
func AccessLog() app.HandlerFunc {
	return AccessLogWithRecorder(nil)
}

// AccessLogWithRecorder 在 AccessLog 基础上追加业务侧日志记录回调（如落库）。
func AccessLogWithRecorder(recorder AccessLogRecorder) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		start := time.Now()
		c.Next(ctx)
		status := c.Response.StatusCode()
		latency := time.Since(start)
		method := string(c.Method())
		path := string(c.Path())
		metrics.RecordRequest(method, path, status, latency)
		logging.Info("request access log",
			"request_id", RequestIDFromContext(c),
			"method", method,
			"path", path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"client_ip", c.ClientIP(),
		)
		if recorder != nil {
			recorder(c, method, path, status, latency)
		}
	}
}

// RequireAuth 校验 Bearer Token，把用户、租户和角色写入请求上下文，
// validator 非空时执行业务侧会话校验（如校验账号状态、会话与令牌版本）。
func RequireAuth(cfg config.JWTConfig, validator SessionValidator) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		tokenValue := strings.TrimPrefix(string(c.GetHeader("Authorization")), "Bearer ")
		if tokenValue == "" {
			response.Error(c, 401, "请先登录")
			c.Abort()
			return
		}
		claims, err := auth.ParseToken(cfg, tokenValue)
		if err != nil {
			response.Error(c, 401, "登录状态已失效")
			c.Abort()
			return
		}
		if validator != nil {
			if ok, message := validator(ctx, claims, c); !ok {
				response.Error(c, 401, message)
				c.Abort()
				return
			}
		}
		c.Set(userIDKey, claims.UserID)
		c.Set(tenantIDKey, claims.TenantID)
		c.Set(roleCodeKey, claims.RoleCode)
		c.Next(ctx)
	}
}

// RequireSessionAuth 基于 Redis 服务端会话校验 Bearer Token 并写入用户上下文。
// 与 RequireAuth 的区别：信任源为 Redis 会话而非 JWT 签名，登录时校验账号状态、
// 会话期内不再查询数据库，封禁可通过删除会话键即时生效。
func RequireSessionAuth() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		tokenValue := strings.TrimPrefix(string(c.GetHeader("Authorization")), "Bearer ")
		if tokenValue == "" {
			response.Error(c, 401, "请先登录")
			c.Abort()
			return
		}
		claims, err := auth.ResolveClaims(ctx, tokenValue)
		if err != nil {
			response.Error(c, 401, "登录状态已失效")
			c.Abort()
			return
		}
		c.Set(userIDKey, claims.UserID)
		c.Set(tenantIDKey, claims.TenantID)
		c.Set(roleCodeKey, claims.RoleCode)
		c.Next(ctx)
	}
}

// RequestIDFromContext 从请求上下文读取追踪 ID。
func RequestIDFromContext(c *app.RequestContext) string {
	value, ok := c.Get(requestIDKey)
	if !ok {
		return ""
	}
	requestID, _ := value.(string)
	return requestID
}

// UserIDFromContext 从请求上下文读取当前用户 ID。
func UserIDFromContext(c *app.RequestContext) (uint64, bool) {
	value, ok := c.Get(userIDKey)
	if !ok {
		return 0, false
	}
	userID, ok := value.(uint64)
	return userID, ok && userID > 0
}

// RoleCodeFromContext 从请求上下文读取当前角色编码。
func RoleCodeFromContext(c *app.RequestContext) string {
	value, ok := c.Get(roleCodeKey)
	if !ok {
		return ""
	}
	roleCode, _ := value.(string)
	return roleCode
}

// normalizeRequestID 规范化外部传入的请求追踪 ID，避免日志污染和异常长响应头。
func normalizeRequestID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxRequestID {
		return ""
	}
	for _, item := range value {
		if item >= 'a' && item <= 'z' {
			continue
		}
		if item >= 'A' && item <= 'Z' {
			continue
		}
		if item >= '0' && item <= '9' {
			continue
		}
		if item == '-' || item == '_' || item == '.' {
			continue
		}
		return ""
	}
	return value
}

// randomRequestID 生成短请求追踪 ID。
func randomRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "req-fallback"
	}
	return hex.EncodeToString(buf)
}

// panicValue 将 panic 值格式化为日志字符串。
func panicValue(value any) string {
	switch item := value.(type) {
	case error:
		return item.Error()
	default:
		return fmt.Sprint(item)
	}
}
