// Package logging 提供全公司统一的 JSON 结构化日志能力：
// logrus 全局日志器 + Hertz hlog 适配 + 请求追踪 ID + GORM 日志适配器。
package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
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

// ==================== 请求体/响应体摘要能力 ====================

const (
	// summaryMaxKeys 限制对象摘要的字段数量，避免日志过长。
	summaryMaxKeys = 8
	// summaryMaxItems 限制数组摘要的采样数量，避免日志过长。
	summaryMaxItems = 3
	// summaryMaxDepth 限制摘要递归层级，避免深层结构刷屏。
	summaryMaxDepth = 2
	// summaryMaxText 限制文本摘要长度，避免日志过长。
	summaryMaxText = 256
)

// 默认敏感字段列表（其他项目可在此基础上扩展）
var defaultSensitiveSummaryKeys = map[string]struct{}{
	"access_key":        {},
	"access_key_id":     {},
	"access_key_secret": {},
	"api_key":           {},
	"apikeyv3":          {},
	"app_secret":        {},
	"appsecret":         {},
	"authorization":     {},
	"client_secret":     {},
	"clientsecret":      {},
	"code":              {},
	"encrypt_key":       {},
	"encryptkey":        {},
	"open_id":           {},
	"openid":            {},
	"password":          {},
	"phone":             {},
	"private_key":       {},
	"privatekey":        {},
	"refresh_token":     {},
	"secret":            {},
	"session_key":       {},
	"token":             {},
}

// summarizeQueryString 返回脱敏后的查询参数摘要。
func SummarizeQueryString(raw string) string {
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return fmt.Sprintf("invalid_query len=%d", len(raw))
	}
	return MarshalSummary(SummarizeValues(values))
}

// summarizePayload 返回脱敏后的请求体或响应体摘要。
func SummarizePayload(body []byte, contentType string) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		values, err := url.ParseQuery(string(trimmed))
		if err != nil {
			return fmt.Sprintf("invalid_form body_bytes=%d", len(trimmed))
		}
		return MarshalSummary(SummarizeValues(values))
	}
	if strings.Contains(contentType, "application/json") || LooksLikeJSON(trimmed) {
		var payload any
		if err := json.Unmarshal(trimmed, &payload); err == nil {
			return MarshalSummary(SummarizeJSONValue(payload, 0))
		}
	}
	return fmt.Sprintf("content_type=%q body_bytes=%d", contentType, len(trimmed))
}

// SummarizeValues 返回查询参数或表单参数的脱敏摘要。
func SummarizeValues(values url.Values) map[string]any {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > summaryMaxKeys {
		keys = keys[:summaryMaxKeys]
	}
	summary := make(map[string]any, len(keys)+1)
	for _, key := range keys {
		items := values[key]
		if len(items) == 1 {
			summary[key] = MaskSummaryValue(key, items[0])
			continue
		}
		masked := make([]string, 0, MinInt(len(items), summaryMaxItems))
		for idx, item := range items {
			if idx >= summaryMaxItems {
				break
			}
			masked = append(masked, fmt.Sprint(MaskSummaryValue(key, item)))
		}
		summary[key] = map[string]any{
			"count":  len(items),
			"sample": masked,
		}
	}
	if len(values) > len(keys) {
		summary["_truncated_keys"] = len(values) - len(keys)
	}
	return summary
}

// SummarizeJSONValue 返回 JSON 结构的脱敏摘要。
func SummarizeJSONValue(value any, depth int) any {
	if depth >= summaryMaxDepth {
		switch typed := value.(type) {
		case map[string]any:
			return fmt.Sprintf("object(keys=%d)", len(typed))
		case []any:
			return fmt.Sprintf("array(len=%d)", len(typed))
		default:
			return summarizeScalar("", typed)
		}
	}
	switch typed := value.(type) {
	case map[string]any:
		return SummarizeJSONObject(typed, depth)
	case []any:
		return SummarizeJSONArray(typed, depth)
	default:
		return summarizeScalar("", typed)
	}
}

// SummarizeJSONObject 返回 JSON 对象的脱敏摘要。
func SummarizeJSONObject(object map[string]any, depth int) map[string]any {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > summaryMaxKeys {
		keys = keys[:summaryMaxKeys]
	}
	summary := make(map[string]any, len(keys)+1)
	for _, key := range keys {
		summary[key] = SummarizeFieldValue(key, object[key], depth+1)
	}
	if len(object) > len(keys) {
		summary["_truncated_keys"] = len(object) - len(keys)
	}
	return summary
}

// SummarizeJSONArray 返回 JSON 数组的脱敏摘要。
func SummarizeJSONArray(items []any, depth int) map[string]any {
	summary := map[string]any{
		"type": "array",
		"len":  len(items),
	}
	if len(items) == 0 {
		return summary
	}
	sample := make([]any, 0, MinInt(len(items), summaryMaxItems))
	for idx, item := range items {
		if idx >= summaryMaxItems {
			break
		}
		sample = append(sample, SummarizeJSONValue(item, depth+1))
	}
	summary["sample"] = sample
	return summary
}

// SummarizeFieldValue 返回单个字段的脱敏摘要。
func SummarizeFieldValue(key string, value any, depth int) any {
	if IsSensitiveSummaryKey(key) {
		return MaskSensitiveString(fmt.Sprint(value))
	}
	switch typed := value.(type) {
	case map[string]any:
		return SummarizeJSONObject(typed, depth)
	case []any:
		return SummarizeJSONArray(typed, depth)
	default:
		return summarizeScalar(key, typed)
	}
}

// summarizeScalar 返回标量值的脱敏摘要。
func summarizeScalar(key string, value any) any {
	switch typed := value.(type) {
	case string:
		return MaskSummaryValue(key, typed)
	default:
		return value
	}
}

// MaskSummaryValue 对查询参数、表单参数或字符串字段做脱敏摘要。
func MaskSummaryValue(key string, value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if IsSensitiveSummaryKey(key) {
		return MaskSensitiveString(value)
	}
	if len(value) > summaryMaxText {
		return value[:summaryMaxText] + "...(truncated)"
	}
	return value
}

// IsSensitiveSummaryKey 判断字段是否属于敏感字段。
func IsSensitiveSummaryKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	_, ok := defaultSensitiveSummaryKeys[key]
	return ok
}

// SetSensitiveSummaryKeys 设置自定义敏感字段列表（可选）。
func SetSensitiveSummaryKeys(keys map[string]struct{}) {
	defaultSensitiveSummaryKeys = keys
}

// MaskSensitiveString 返回敏感文本的脱敏结果。
func MaskSensitiveString(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 6 {
		return value[:1] + "***"
	}
	return value[:3] + "***" + value[len(value)-3:]
}

// LooksLikeJSON 根据首尾字符粗略判断是否为 JSON。
func LooksLikeJSON(body []byte) bool {
	if len(body) < 2 {
		return false
	}
	return (body[0] == '{' && body[len(body)-1] == '}') || (body[0] == '[' && body[len(body)-1] == ']')
}

// MarshalSummary 将摘要对象序列化为稳定的 JSON 字符串。
func MarshalSummary(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "summary_unavailable"
	}
	if len(data) > summaryMaxText {
		return string(data[:summaryMaxText]) + "...(truncated)"
	}
	return string(data)
}

// MinInt 返回两个整数中的较小值。
func MinInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

// ==================== 请求体/响应体摘要能力结束 ====================
