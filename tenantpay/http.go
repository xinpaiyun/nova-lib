package tenantpay

import (
	"context"
	"errors"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
)

// RouteDeps 装配路由所需的宿主中间件（lib 不感知宿主鉴权实现）。
type RouteDeps struct {
	// Auth 登录态校验（商户端与平台端均需）。
	Auth app.HandlerFunc
	// RequireTenantID 商户端校验：从登录态取出租户 ID（未绑定商家返回 403）。
	RequireTenantID func(ctx context.Context, c *app.RequestContext) (uint64, bool)
	// RequirePlatformAdmin 平台端校验：仅平台超管可访问（非超管 403）。
	RequirePlatformAdmin app.HandlerFunc
}

// Handler 租户支付 HTTP 处理器。
type Handler struct {
	service *Service
	routeGroup
}

// NewHandler 创建处理器。
func NewHandler(service *Service) *Handler {
	return &Handler{service: service, routeGroup: routeGroup{service: service}}
}

// RegisterRoutes 注册租户支付路由。
//
// 商户端（Auth + RequireTenantID，挂 /admin/tenant-pay）：
//
//	GET/PUT  /admin/tenant-pay/wechat-config     微信支付配置（商户自助）
//	GET/POST /admin/tenant-pay/wechat-apps       小程序绑定
//	GET      /admin/tenant-pay/commission        抽成配置（只读）
//	GET      /admin/tenant-pay/profit-sharing-orders  分账流水（只读）
//
// 平台端（Auth + RequirePlatformAdmin，挂 /admin/tenant-pay/platform）：
//
//	GET/PUT  /admin/tenant-pay/platform/wechat-config          平台自身支付配置（tenant 0）
//	GET/PUT  /admin/tenant-pay/platform/tenants/:tenantID/wechat-config  按租户代配
//	GET/PUT  /admin/tenant-pay/platform/tenants/:tenantID/commission     租户抽成设置
//	GET      /admin/tenant-pay/platform/profit-sharing-orders  全平台分账记录（可按租户/状态过滤）
//	POST     /admin/tenant-pay/platform/profit-sharing-orders/:id/retry  手动补发
func (h *Handler) RegisterRoutes(group *route.RouterGroup, deps RouteDeps) {
	h.deps = deps
	h.Register(group)
}

// routeGroup 组织一组路由与其鉴权依赖。
type routeGroup struct {
	deps    RouteDeps
	service *Service
}

// Register 注册全部路由。
func (g *routeGroup) Register(group *route.RouterGroup) {
	merchant := group.Group("/admin/tenant-pay", g.deps.Auth)
	// 商户端（需登录 + 租户绑定）。
	merchant.GET("/wechat-config", g.merchantTenant(func(ctx context.Context, tenantID uint64) (any, error) {
		return g.service.GetWechatPayConfig(ctx, tenantID)
	}))
	merchant.PUT("/wechat-config", g.saveMerchantWechatConfig)
	merchant.GET("/wechat-apps", g.listWechatApps)
	merchant.POST("/wechat-apps", g.saveWechatApp)
	merchant.GET("/commission", g.getMyCommission)
	merchant.GET("/profit-sharing-orders", g.listMyPSOrders)

	// 平台端（需登录 + 平台超管）。
	platform := group.Group("/admin/tenant-pay/platform", g.deps.Auth, g.deps.RequirePlatformAdmin)
	platform.GET("/wechat-config", g.getWechatConfigOf(0))
	platform.PUT("/wechat-config", g.saveWechatConfigOf(0))
	platform.GET("/tenants/:tenantID/wechat-config", g.getWechatConfigOfParam)
	platform.PUT("/tenants/:tenantID/wechat-config", g.saveWechatConfigOfParam)
	platform.GET("/tenants/:tenantID/commission", g.getCommissionOfParam)
	platform.PUT("/tenants/:tenantID/commission", g.saveCommissionOfParam)
	platform.GET("/profit-sharing-orders", g.listAllPSOrders)
	platform.POST("/profit-sharing-orders/:id/retry", g.retryPSOrder)
}

// ---------- 通用工具 ----------

// pathUint 解析路径参数为 uint64，非法返回 0。
func pathUint(c *app.RequestContext, name string) uint64 {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// writeError 统一业务错误映射：400 / 500。
func writeError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		responseNotFound(c)
	case errors.Is(err, ErrMasterKeyMissing), errors.Is(err, ErrMchRequired),
		errors.Is(err, ErrInvalidConfig), errors.Is(err, ErrAppIDRequired),
		errors.Is(err, ErrRateInvalid), errors.Is(err, ErrMinInvalid),
		errors.Is(err, ErrPayNotConfigured), errors.Is(err, ErrNoCommission),
		errors.Is(err, ErrPSAlreadySucceeded):
		responseBadRequest(c, err.Error())
	default:
		responseInternalError(c, err.Error())
	}
}

// queryInt 解析查询参数为 int。
func queryInt(c *app.RequestContext, name string) int {
	value, err := strconv.Atoi(c.Query(name))
	if err != nil {
		return 0
	}
	return value
}

// ---------- 商户端 handlers ----------

// merchantTenant 包装 RequireTenantID 校验。
func (g *routeGroup) merchantTenant(next func(ctx context.Context, tenantID uint64) (any, error)) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		tenantID, ok := g.deps.RequireTenantID(ctx, c)
		if !ok {
			return
		}
		result, err := next(ctx, tenantID)
		if err != nil {
			writeError(c, err)
			return
		}
		responseSuccess(c, result)
	}
}

// saveMerchantWechatConfig 商户端保存微信支付配置。
func (g *routeGroup) saveMerchantWechatConfig(ctx context.Context, c *app.RequestContext) {
	tenantID, ok := g.deps.RequireTenantID(ctx, c)
	if !ok {
		return
	}
	var req SaveWechatPayConfigRequest
	if err := c.BindJSON(&req); err != nil {
		responseBadRequest(c, "请求体格式不正确")
		return
	}
	result, err := g.service.SaveWechatPayConfig(ctx, tenantID, req)
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// listWechatApps 商户端列出小程序绑定。
func (g *routeGroup) listWechatApps(ctx context.Context, c *app.RequestContext) {
	tenantID, ok := g.deps.RequireTenantID(ctx, c)
	if !ok {
		return
	}
	list, err := g.service.ListWechatApps(ctx, tenantID)
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, list)
}

// saveWechatApp 商户端保存小程序绑定。
func (g *routeGroup) saveWechatApp(ctx context.Context, c *app.RequestContext) {
	tenantID, ok := g.deps.RequireTenantID(ctx, c)
	if !ok {
		return
	}
	var req SaveWechatAppRequest
	if err := c.BindJSON(&req); err != nil {
		responseBadRequest(c, "请求体格式不正确")
		return
	}
	result, err := g.service.SaveWechatApp(ctx, tenantID, req)
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// getMyCommission 商户端查询自己的抽成配置（只读）。
func (g *routeGroup) getMyCommission(ctx context.Context, c *app.RequestContext) {
	tenantID, ok := g.deps.RequireTenantID(ctx, c)
	if !ok {
		return
	}
	result, err := g.service.GetCommission(ctx, tenantID)
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// listMyPSOrders 商户端查询自己的分账流水（只读）。
func (g *routeGroup) listMyPSOrders(ctx context.Context, c *app.RequestContext) {
	tenantID, ok := g.deps.RequireTenantID(ctx, c)
	if !ok {
		return
	}
	list, total, err := g.service.ListPSOrders(ctx, tenantID, c.Query("status"), queryInt(c, "page"), queryInt(c, "size"))
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, map[string]any{"list": list, "total": total})
}

// ---------- 平台端 handlers ----------

// getWechatConfigOf 生成固定租户的配置查询 handler（平台自身传 0）。
func (g *routeGroup) getWechatConfigOf(tenantID uint64) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		result, err := g.service.GetWechatPayConfig(ctx, tenantID)
		if err != nil {
			writeError(c, err)
			return
		}
		responseSuccess(c, result)
	}
}

// saveWechatConfigOf 生成固定租户的配置保存 handler（平台自身传 0）。
func (g *routeGroup) saveWechatConfigOf(tenantID uint64) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		var req SaveWechatPayConfigRequest
		if err := c.BindJSON(&req); err != nil {
			responseBadRequest(c, "请求体格式不正确")
			return
		}
		result, err := g.service.SaveWechatPayConfig(ctx, tenantID, req)
		if err != nil {
			writeError(c, err)
			return
		}
		responseSuccess(c, result)
	}
}

// getWechatConfigOfParam 平台端按租户查询配置。
func (g *routeGroup) getWechatConfigOfParam(ctx context.Context, c *app.RequestContext) {
	result, err := g.service.GetWechatPayConfig(ctx, pathUint(c, "tenantID"))
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// saveWechatConfigOfParam 平台端按租户保存配置。
func (g *routeGroup) saveWechatConfigOfParam(ctx context.Context, c *app.RequestContext) {
	var req SaveWechatPayConfigRequest
	if err := c.BindJSON(&req); err != nil {
		responseBadRequest(c, "请求体格式不正确")
		return
	}
	result, err := g.service.SaveWechatPayConfig(ctx, pathUint(c, "tenantID"), req)
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// getCommissionOfParam 平台端按租户查询抽成配置。
func (g *routeGroup) getCommissionOfParam(ctx context.Context, c *app.RequestContext) {
	result, err := g.service.GetCommission(ctx, pathUint(c, "tenantID"))
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// saveCommissionOfParam 平台端按租户保存抽成配置。
func (g *routeGroup) saveCommissionOfParam(ctx context.Context, c *app.RequestContext) {
	var req SaveCommissionRequest
	if err := c.BindJSON(&req); err != nil {
		responseBadRequest(c, "请求体格式不正确")
		return
	}
	result, err := g.service.SaveCommission(ctx, pathUint(c, "tenantID"), req)
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// listAllPSOrders 平台端查询全平台分账记录（可按租户/状态过滤）。
func (g *routeGroup) listAllPSOrders(ctx context.Context, c *app.RequestContext) {
	list, total, err := g.service.ListPSOrders(ctx, pathUint(c, "tenantID"), c.Query("status"), queryInt(c, "page"), queryInt(c, "size"))
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, map[string]any{"list": list, "total": total})
}

// retryPSOrder 平台端手动补发分账。
func (g *routeGroup) retryPSOrder(ctx context.Context, c *app.RequestContext) {
	result, err := g.service.RetryPSOrder(ctx, pathUint(c, "id"))
	if err != nil {
		writeError(c, err)
		return
	}
	responseSuccess(c, result)
}

// ---------- 响应助手（与宿主 response 包对齐的轻量实现） ----------

// responseSuccess 统一成功响应 {code:0, data:...}。
func responseSuccess(c *app.RequestContext, data any) {
	c.JSON(200, map[string]any{"code": 0, "data": data})
}

// responseBadRequest 统一 400 响应。
func responseBadRequest(c *app.RequestContext, message string) {
	c.JSON(400, map[string]any{"code": 400, "message": message})
}

// responseNotFound 统一 404 响应。
func responseNotFound(c *app.RequestContext) {
	c.JSON(404, map[string]any{"code": 404, "message": "资源不存在"})
}

// responseInternalError 统一 500 响应。
func responseInternalError(c *app.RequestContext, message string) {
	c.JSON(500, map[string]any{"code": 500, "message": message})
}
