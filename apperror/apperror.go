// Package apperror 定义带业务码的统一应用错误类型。
package apperror

const (
	// CodeOK 表示请求处理成功。
	CodeOK = "OK"
	// CodeBadRequest 表示请求参数错误。
	CodeBadRequest = "BAD_REQUEST"
	// CodeUnauthorized 表示用户未登录或登录态失效。
	CodeUnauthorized = "UNAUTHORIZED"
	// CodeForbidden 表示当前用户没有权限。
	CodeForbidden = "FORBIDDEN"
	// CodeNotFound 表示资源不存在。
	CodeNotFound = "NOT_FOUND"
	// CodeConflict 表示资源状态冲突。
	CodeConflict = "CONFLICT"
	// CodeTooManyRequests 表示请求频率过高。
	CodeTooManyRequests = "TOO_MANY_REQUESTS"
	// CodeInternal 表示服务端内部错误。
	CodeInternal = "INTERNAL_ERROR"
)

// Error 表示带业务码的应用错误。
type Error struct {
	Code       int
	ErrorCode  string
	Message    string
	HTTPStatus int
}

// Error 返回错误文案。
func (e Error) Error() string {
	return e.Message
}

// Status 返回应用错误对应的 HTTP 状态码。
func (e Error) Status() int {
	if e.HTTPStatus > 0 {
		return e.HTTPStatus
	}
	if e.Code >= 100 && e.Code <= 599 {
		return e.Code
	}
	return 500
}

// StableCode 返回前端和跨端调用方可依赖的稳定错误码。
func (e Error) StableCode() string {
	if e.ErrorCode != "" {
		return e.ErrorCode
	}
	return CodeInternal
}

var (
	// ErrBadRequest 表示请求参数错误。
	ErrBadRequest = Error{Code: 400, ErrorCode: CodeBadRequest, Message: "请求参数不正确", HTTPStatus: 400}
	// ErrUnauthorized 表示用户未登录或登录态失效。
	ErrUnauthorized = Error{Code: 401, ErrorCode: CodeUnauthorized, Message: "请先登录", HTTPStatus: 401}
	// ErrForbidden 表示当前用户没有权限。
	ErrForbidden = Error{Code: 403, ErrorCode: CodeForbidden, Message: "没有操作权限", HTTPStatus: 403}
	// ErrNotFound 表示资源不存在。
	ErrNotFound = Error{Code: 404, ErrorCode: CodeNotFound, Message: "资源不存在", HTTPStatus: 404}
	// ErrInternal 表示服务端内部错误。
	ErrInternal = Error{Code: 500, ErrorCode: CodeInternal, Message: "服务暂时不可用", HTTPStatus: 500}
)

// New 创建自定义业务错误。
func New(code int, message string) Error {
	return NewWithCode(code, statusToCode(code), message)
}

// NewWithCode 创建带稳定错误码的业务错误。
func NewWithCode(status int, errorCode string, message string) Error {
	return Error{Code: status, ErrorCode: errorCode, Message: message, HTTPStatus: status}
}

// statusToCode 将 HTTP 状态映射为默认稳定错误码。
func statusToCode(status int) string {
	switch status {
	case 400:
		return CodeBadRequest
	case 401:
		return CodeUnauthorized
	case 403:
		return CodeForbidden
	case 404:
		return CodeNotFound
	case 409:
		return CodeConflict
	case 429:
		return CodeTooManyRequests
	default:
		return CodeInternal
	}
}
