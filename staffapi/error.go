// Package staffapi 提供员工域的标准 HTTP handler 与固定错误码契约。
//
// 项目只需把 handler 挂到路由即可，接口路径、请求/响应字段与错误码统一，
// 因此前端页面可跨项目复用：
//
//	GET    /v1/staff/members
//	POST   /v1/staff/members
//	PATCH  /v1/staff/members/:id
//	POST   /v1/staff/members/:id/caps
//	GET    /v1/staff/capabilities
//	GET    /v1/staff/identities
//
// 挂载前提：鉴权中间件 + staff.Required(src) 已在本组生效。
package staffapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/apperror"
	"github.com/xinpaiyun/nova-lib/auth"
	"github.com/xinpaiyun/nova-lib/middleware"
	"github.com/xinpaiyun/nova-lib/response"
	"github.com/xinpaiyun/nova-lib/staff"
)

// 员工域固定错误码。未注册是最高频的引导类错误，单独占 1301。
const (
	CodeUserNotRegistered           = 1301
	CodeCapabilitySpecUnknown       = 1302
	CodeCapabilitySubjectRequired   = 1303
	CodeCapabilitySubjectTaken      = 1304
	StableUserNotRegistered         = "USER_NOT_REGISTERED"
	StableCapabilitySpecUnknown     = "CAPABILITY_SPEC_UNKNOWN"
	StableCapabilitySubjectRequired = "CAPABILITY_SUBJECT_REQUIRED"
	StableCapabilitySubjectTaken    = "CAPABILITY_SUBJECT_TAKEN"
)

// MapError 把员工域错误映射成固定错误码。未知错误一律脱敏为 500。
func MapError(err error) apperror.Error {
	// 已经是应用错误的直接透传，避免二次映射丢失业务码。
	var appErr apperror.Error
	if errors.As(err, &appErr) {
		return appErr
	}
	switch {
	case errors.Is(err, staff.ErrUserNotRegistered):
		return biz(400, CodeUserNotRegistered, StableUserNotRegistered, err.Error())
	case errors.Is(err, staff.ErrCapabilitySpecUnknown):
		return biz(400, CodeCapabilitySpecUnknown, StableCapabilitySpecUnknown, err.Error())
	case errors.Is(err, staff.ErrCapabilitySubjectRequired):
		return biz(400, CodeCapabilitySubjectRequired, StableCapabilitySubjectRequired, err.Error())
	case errors.Is(err, staff.ErrCapabilitySubjectTaken):
		return biz(409, CodeCapabilitySubjectTaken, StableCapabilitySubjectTaken, err.Error())
	case errors.Is(err, staff.ErrPermissionDenied),
		errors.Is(err, staff.ErrNotMember),
		errors.Is(err, staff.ErrMemberDisabled):
		return apperror.NewWithCode(403, apperror.CodeForbidden, err.Error())
	case errors.Is(err, staff.ErrMemberNotFound):
		return apperror.NewWithCode(404, apperror.CodeNotFound, err.Error())
	case errors.Is(err, staff.ErrMemberExists):
		return apperror.NewWithCode(409, apperror.CodeConflict, err.Error())
	case errors.Is(err, staff.ErrMemberInputInvalid),
		errors.Is(err, staff.ErrPhoneInvalid),
		errors.Is(err, staff.ErrRoleUnknown),
		errors.Is(err, staff.ErrRoleScopeInvalid),
		errors.Is(err, staff.ErrUserDisabled),
		errors.Is(err, staff.ErrCapabilitySubjectNotAllowed),
		errors.Is(err, staff.ErrCapabilitySubjectKindMissing),
		errors.Is(err, staff.ErrScopeEffectInvalid),
		errors.Is(err, staff.ErrRoleCodeEmpty),
		errors.Is(err, staff.ErrCapabilityCodeEmpty):
		return apperror.NewWithCode(400, apperror.CodeBadRequest, err.Error())
	default:
		return apperror.ErrInternal
	}
}

// WriteError 写入统一错误响应。
func WriteError(c *app.RequestContext, err error) {
	response.AppError(c, MapError(err))
}

// BadRequestError 构造参数错误，供 handler 校验失败时使用。
func BadRequestError(message string) apperror.Error {
	return apperror.NewWithCode(400, apperror.CodeBadRequest, message)
}

// biz 构造带业务码与稳定码的错误。
func biz(status, code int, stable, message string) apperror.Error {
	return apperror.Error{Code: code, ErrorCode: stable, Message: message, HTTPStatus: status}
}

// DecodeJSON 解析请求体，失败时写入 400 并返回 false。
func DecodeJSON(c *app.RequestContext, dst any) bool {
	if err := json.Unmarshal(c.Request.Body(), dst); err != nil {
		WriteError(c, apperror.ErrBadRequest)
		return false
	}
	return true
}

// ParamID 解析路径参数中的正整数 ID，失败时写入 400。
func ParamID(c *app.RequestContext, name string) (uint64, bool) {
	value := strings.TrimSpace(c.Param(name))
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		WriteError(c, BadRequestError("参数 "+name+" 不正确"))
		return 0, false
	}
	return id, true
}

// PageQuery 解析 page/pageSize，缺省 1/20，超出范围返回 false。
func PageQuery(c *app.RequestContext) (page, pageSize int, ok bool) {
	page, pageSize = 1, 20
	if value := strings.TrimSpace(c.Query("page")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return 0, 0, false
		}
		page = parsed
	}
	if value := strings.TrimSpace(c.Query("pageSize")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 || parsed > 100 {
			return 0, 0, false
		}
		pageSize = parsed
	}
	return page, pageSize, true
}

// QueryUint64 解析非必填的无符号整型查询参数；空值返回 0，非法返回 false。
func QueryUint64(c *app.RequestContext, name string) (uint64, bool) {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// QueryStatus 解析可选状态（0/1），空值返回 nil。
func QueryStatus(c *app.RequestContext) (*int8, bool) {
	value := strings.TrimSpace(c.Query("status"))
	if value == "" {
		return nil, true
	}
	parsed, err := strconv.ParseInt(value, 10, 8)
	if err != nil || parsed < 0 || parsed > 1 {
		return nil, false
	}
	status := int8(parsed)
	return &status, true
}

// SessionUserID 从请求上下文或登录声明读取当前登录用户 ID。
func SessionUserID(ctx context.Context, c *app.RequestContext) (uint64, bool) {
	if userID, ok := middleware.UserIDFromContext(c); ok && userID > 0 {
		return userID, true
	}
	if claims, ok := auth.ClaimsFromContext(ctx); ok && claims.UserID > 0 {
		return claims.UserID, true
	}
	return 0, false
}

// SessionTenantID 依次从请求上下文、X-Tenant-Id、登录声明读取租户。
func SessionTenantID(ctx context.Context, c *app.RequestContext) uint64 {
	if tenantID, ok := middleware.TenantIDFromContext(c); ok && tenantID > 0 {
		return tenantID
	}
	if value := strings.TrimSpace(string(c.GetHeader("X-Tenant-Id"))); value != "" {
		if tenantID, err := strconv.ParseUint(value, 10, 64); err == nil {
			return tenantID
		}
	}
	if claims, ok := auth.ClaimsFromContext(ctx); ok {
		return claims.TenantID
	}
	return 0
}
