package paynotify

import (
	"bytes"
	"context"
	"net/http"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
)

// 回调路径参数名约定（租户标识内嵌于路径，伪造请求无有效签名必被拒）。
const tenantIDParam = "tenantID"

// RegisterHertz 注册公开回调路由（安全靠 V3 验签，不走登录中间件）。
// paymentPath/refundPath 为完整路由（如 "/pay/notify/:tenantID"），空串表示不注册；
// 路径必须包含 :tenantID 参数。
func (h *Handler) RegisterHertz(group *route.RouterGroup, paymentPath, refundPath string) {
	if paymentPath != "" {
		group.POST(paymentPath, hertzAdapter(h.HandlePayment))
	}
	if refundPath != "" {
		group.POST(refundPath, hertzAdapter(h.HandleRefund))
	}
}

// hertzAdapter 适配单个回调端点：路径取租户 → Hertz 请求转 net/http → 模板处理 →
// 按结果回微信标准应答（成功 200 SUCCESS / 失败 500 FAIL 触发重试）。
func hertzAdapter(handle func(ctx context.Context, tenantID uint64, request *http.Request) error) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		tenantID, err := strconv.ParseUint(c.Param(tenantIDParam), 10, 64)
		if err != nil {
			notifyFail(c, "回调地址缺少有效租户标识")
			return
		}
		request, err := hertzHTTPRequest(ctx, c)
		if err != nil {
			notifyFail(c, "请求解析失败")
			return
		}
		if err := handle(ctx, tenantID, request); err != nil {
			notifyFail(c, err.Error())
			return
		}
		notifySuccess(c)
	}
}

// hertzHTTPRequest 将 Hertz 请求转换为 net/http 请求（微信验签需原始头与原始体）。
func hertzHTTPRequest(ctx context.Context, c *app.RequestContext) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, string(c.URI().FullURI()), bytes.NewReader(c.Request.Body()))
	if err != nil {
		return nil, err
	}
	c.Request.Header.VisitAll(func(key []byte, value []byte) {
		req.Header.Set(string(key), string(value))
	})
	return req, nil
}

// notifySuccess 微信回调成功应答（2xx 即视为送达成功）。
func notifySuccess(c *app.RequestContext) {
	c.JSON(200, map[string]string{"code": "SUCCESS", "message": "成功"})
}

// notifyFail 微信回调失败应答（非 2xx 触发微信按策略重试）。
func notifyFail(c *app.RequestContext, msg string) {
	c.JSON(500, map[string]string{"code": "FAIL", "message": msg})
}
