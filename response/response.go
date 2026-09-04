// Package response 提供基于 Hertz 的统一 API 响应封装。
package response

import (
	"errors"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/xinpaiyun/nova-lib/apperror"
)

// Body 定义统一 API 响应结构。
type Body struct {
	Code      int    `json:"code"`
	ErrorCode string `json:"errorCode,omitempty"`
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

// Success 写入统一成功响应。
func Success(c *app.RequestContext, data any) {
	c.JSON(200, Body{Code: 0, ErrorCode: apperror.CodeOK, Message: "成功", Data: data, RequestID: requestID(c)})
}

// Error 写入统一失败响应。
func Error(c *app.RequestContext, status int, message string) {
	appErr := apperror.New(status, message)
	c.JSON(appErr.Status(), Body{Code: appErr.Code, ErrorCode: appErr.StableCode(), Message: appErr.Message, RequestID: requestID(c)})
}

// AppError 根据业务错误写入统一失败响应。
func AppError(c *app.RequestContext, err error) {
	var appErr apperror.Error
	if errors.As(err, &appErr) {
		c.JSON(appErr.Status(), Body{Code: appErr.Code, ErrorCode: appErr.StableCode(), Message: appErr.Message, RequestID: requestID(c)})
		return
	}
	c.JSON(500, Body{Code: 500, ErrorCode: apperror.CodeInternal, Message: err.Error(), RequestID: requestID(c)})
}

// requestID 从请求上下文读取请求追踪 ID。
func requestID(c *app.RequestContext) string {
	value, ok := c.Get("request_id")
	if !ok {
		return ""
	}
	requestID, _ := value.(string)
	return requestID
}
