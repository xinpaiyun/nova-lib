package tenantpay

import (
	"github.com/cloudwego/hertz/pkg/route"

	"github.com/xinpaiyun/nova-lib/paynotify"
)

// NotifyHandler 构建微信支付回调模板处理器：内部以租户支付解析器
// （签名与 paynotify.ClientResolver 一致，带 TTL 缓存与平台层回落）完成
// 按租户定位验签客户端；落账/关单/退款钩子经 options 注入
// （如 paynotify.WithPaidHook / WithCloseHook / WithRefundHook）。
// 返回值也可由宿主自行 RegisterHertz 到任意路由组。
func (h *Handler) NotifyHandler(options ...paynotify.Option) *paynotify.Handler {
	return paynotify.NewHandler(h.service.resolver.Resolve, options...)
}

// RegisterNotifyRoutes 一行注册公开支付回调路由（安全靠 V3 验签，不走登录中间件）。
// paymentPath/refundPath 为完整路由（如 "/pay/notify/:tenantID"），路径必须包含
// :tenantID 参数，空串表示不注册；options 转发给 paynotify 模板（注入钩子等）。
// 应答规范：成功 200 SUCCESS / 失败 500 FAIL（触发微信重试）。
func (h *Handler) RegisterNotifyRoutes(group *route.RouterGroup, paymentPath, refundPath string, options ...paynotify.Option) {
	h.NotifyHandler(options...).RegisterHertz(group, paymentPath, refundPath)
}
