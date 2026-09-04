// Package logging 提供全公司统一的 JSON 结构化日志能力：
// logrus 全局日志器 + Hertz hlog 适配 + 请求追踪 ID + GORM 日志适配器。
package logging

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	hertzlogrus "github.com/hertz-contrib/logger/logrus"
	"github.com/sirupsen/logrus"
)

const (
	// RequestIDKey 是请求上下文中的 request id 键。
	RequestIDKey = "request_id"
	// RequestIDHeader 是对外透传的 request id 响应头。
	RequestIDHeader = "X-Request-Id"
)

var (
	baseLogger     *logrus.Logger
	serviceName    = "nova-api"
	hertzLogger    *hertzlogrus.Logger
	requestIDSeed  atomic.Uint64
	debugEnabled   atomic.Bool
	requestContext atomic.Pointer[RequestFieldsProvider]
)

// RequestFieldsProvider 返回请求日志的公共业务字段（如 user_id/tenant_id）。
type RequestFieldsProvider func(c *app.RequestContext) []any

// Init 初始化全局 JSON 日志器，并把 hlog 与标准库 log 都切到 logrus。
// appName 写入每条日志的 service 字段；mode 决定默认日志级别，
// 非 release 模式（debug/dev/development/local/test/docker）自动开启 debug。
func Init(appName, mode string) {
	if strings.TrimSpace(appName) != "" {
		serviceName = strings.TrimSpace(appName)
	}

	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetLevel(resolveLogrusLevel(mode))
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
	})

	baseLogger = logger
	hertzLogger = hertzlogrus.NewLogger(hertzlogrus.WithLogger(logger))
	hertzLogger.SetLevel(resolveHlogLevel(mode))
	hlog.SetLogger(hertzLogger)
	hlog.SetSystemLogger(hertzLogger)

	log.SetFlags(0)
	log.SetOutput(logger.WriterLevel(logrus.InfoLevel))

	// 非 release 模式默认开启 debug 日志
	SetDebugEnabled(!isReleaseMode(mode))
}

// SetRequestContextFields 注册请求日志公共字段提供器，字段会附加到 *Request 系列日志。
func SetRequestContextFields(provider RequestFieldsProvider) {
	if provider == nil {
		requestContext.Store(nil)
		return
	}
	requestContext.Store(&provider)
}

// Info 记录通用信息日志。
func Info(message string, kv ...any) {
	print("info", message, kv...)
}

// Debug 记录通用调试日志。
func Debug(message string, kv ...any) {
	print("debug", message, kv...)
}

// SetDebugEnabled 设置当前是否启用调试级别日志。
func SetDebugEnabled(enabled bool) {
	debugEnabled.Store(enabled)
	if baseLogger == nil {
		return
	}
	if enabled {
		baseLogger.SetLevel(logrus.DebugLevel)
		return
	}
	baseLogger.SetLevel(logrus.InfoLevel)
}

// DebugEnabled 返回当前是否启用调试级别日志。
func DebugEnabled() bool {
	return debugEnabled.Load()
}

// Warn 记录通用告警日志。
func Warn(message string, kv ...any) {
	print("warn", message, kv...)
}

// Error 记录通用错误日志。
func Error(message string, kv ...any) {
	print("error", message, kv...)
}

// Fatal 记录致命错误日志并退出进程。
func Fatal(message string, kv ...any) {
	print("fatal", message, kv...)
}

// InfoRequest 记录带请求上下文的信息日志。
func InfoRequest(c *app.RequestContext, message string, kv ...any) {
	print("info", message, append(requestFields(c), kv...)...)
}

// DebugRequest 记录带请求上下文的调试日志。
func DebugRequest(c *app.RequestContext, message string, kv ...any) {
	print("debug", message, append(requestFields(c), kv...)...)
}

// WarnRequest 记录带请求上下文的告警日志。
func WarnRequest(c *app.RequestContext, message string, kv ...any) {
	print("warn", message, append(requestFields(c), kv...)...)
}

// ErrorRequest 记录带请求上下文的错误日志。
func ErrorRequest(c *app.RequestContext, message string, kv ...any) {
	print("error", message, append(requestFields(c), kv...)...)
}

// EnsureRequestID 为请求补齐 request id，并写回响应头。
func EnsureRequestID(c *app.RequestContext) string {
	if value, ok := c.Get(RequestIDKey); ok {
		if requestID, ok := value.(string); ok && strings.TrimSpace(requestID) != "" {
			c.Header(RequestIDHeader, requestID)
			return requestID
		}
	}
	requestID := strings.TrimSpace(string(c.GetHeader(RequestIDHeader)))
	if requestID == "" {
		requestID = generateRequestID()
	}
	c.Set(RequestIDKey, requestID)
	c.Header(RequestIDHeader, requestID)
	return requestID
}

// RequestID 返回请求上下文中的 request id。
func RequestID(c *app.RequestContext) string {
	if value, ok := c.Get(RequestIDKey); ok {
		if requestID, ok := value.(string); ok {
			return requestID
		}
	}
	return ""
}

// generateRequestID 生成轻量级 request id，便于串联单次请求日志。
func generateRequestID() string {
	seed := requestIDSeed.Add(1)
	return fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), seed)
}

// requestFields 组装请求日志的公共字段。
func requestFields(c *app.RequestContext) []any {
	fields := []any{
		"request_id", EnsureRequestID(c),
		"method", string(c.Method()),
		"path", string(c.Path()),
		"ip", c.ClientIP(),
	}
	if provider := requestContext.Load(); provider != nil {
		fields = append(fields, (*provider)(c)...)
	}
	return fields
}

// print 将日志输出为单行 JSON，便于 Loki 直接采集。
func print(level string, message string, kv ...any) {
	logger := baseLogger
	if logger == nil {
		// 未显式 Init 时自动落一个默认日志器，保证包可用。
		Init("", "")
		logger = baseLogger
	}
	entry := logger.WithFields(buildFields(kv...))
	switch level {
	case "debug":
		entry.Debug(message)
	case "warn":
		entry.Warn(message)
	case "error":
		entry.Error(message)
	case "fatal":
		entry.Fatal(message)
	default:
		entry.Info(message)
	}
}

// sanitizeKey 将任意日志键归一化为易检索格式。
func sanitizeKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return "unknown"
	}
	key = strings.ReplaceAll(key, " ", "_")
	return key
}

// newBaseLogger 创建默认的 JSON logrus logger。
func newBaseLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(os.Stdout)
	logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
	})
	logger.SetLevel(logrus.InfoLevel)
	return logger
}

// buildFields 将键值对转换为 logrus 字段。
func buildFields(kv ...any) logrus.Fields {
	fields := logrus.Fields{"service": serviceName}
	for i := 0; i < len(kv); i += 2 {
		key := sanitizeKey(fmt.Sprint(kv[i]))
		var value any = "<missing>"
		if i+1 < len(kv) {
			value = normalizeValue(kv[i+1])
		}
		fields[key] = value
	}
	return fields
}

// normalizeValue 将日志字段值转换为适合 JSON 序列化的形式。
func normalizeValue(value any) any {
	switch actual := value.(type) {
	case nil:
		return nil
	case error:
		return actual.Error()
	case []byte:
		return string(actual)
	case fmt.Stringer:
		return actual.String()
	default:
		return actual
	}
}

// isReleaseMode 判断运行模式是否为生产模式。
func isReleaseMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "release", "prod", "production":
		return true
	default:
		return false
	}
}

// resolveLogrusLevel 根据运行模式返回 logrus 日志级别。
func resolveLogrusLevel(mode string) logrus.Level {
	if isReleaseMode(mode) {
		return logrus.InfoLevel
	}
	return logrus.DebugLevel
}

// resolveHlogLevel 根据运行模式返回 hlog 日志级别。
func resolveHlogLevel(mode string) hlog.Level {
	if isReleaseMode(mode) {
		return hlog.LevelInfo
	}
	return hlog.LevelDebug
}
