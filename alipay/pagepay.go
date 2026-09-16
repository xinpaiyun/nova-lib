package alipay

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// PagePayRequest 定义电脑网站支付（alipay.trade.page.pay）下单参数。
type PagePayRequest struct {
	// OutTradeNo 商户订单号，需保证在商户侧唯一。
	OutTradeNo string
	// Subject 订单标题，展示在支付宝收银台。
	Subject string
	// AmountCents 订单金额，单位分。
	AmountCents int64
	// ReturnURL 支付完成后浏览器回跳地址，留空时使用配置默认值。
	ReturnURL string
	// NotifyURL 支付结果异步通知地址，留空时使用配置默认值。
	NotifyURL string
}

// CreatePagePayURL 构建电脑网站支付跳转地址：签名后的支付宝网关 URL，
// 前端以页面跳转或新窗口方式打开即可进入支付宝收银台（扫码或登录支付）。
// 该方法仅做本地签名组装，不调用开放平台接口，因此不会产生网络请求。
func (c *Client) CreatePagePayURL(req PagePayRequest) (string, error) {
	if strings.TrimSpace(req.OutTradeNo) == "" {
		return "", errors.New("支付宝商户订单号不能为空")
	}
	if strings.TrimSpace(req.Subject) == "" {
		return "", errors.New("支付宝订单标题不能为空")
	}
	if req.AmountCents <= 0 {
		return "", errors.New("支付宝支付金额必须大于 0")
	}
	bizContent, err := json.Marshal(map[string]any{
		"out_trade_no":  strings.TrimSpace(req.OutTradeNo),
		"product_code":  "FAST_INSTANT_TRADE_PAY",
		"total_amount":  centsToYuan(req.AmountCents),
		"subject":       strings.TrimSpace(req.Subject),
	})
	if err != nil {
		return "", err
	}
	params := c.baseParams("alipay.trade.page.pay")
	params.Set("biz_content", string(bizContent))
	notifyURL := strings.TrimSpace(req.NotifyURL)
	if notifyURL == "" {
		notifyURL = strings.TrimSpace(c.cfg.NotifyURL)
	}
	if notifyURL != "" {
		params.Set("notify_url", notifyURL)
	}
	returnURL := strings.TrimSpace(req.ReturnURL)
	if returnURL == "" {
		returnURL = strings.TrimSpace(c.cfg.ReturnURL)
	}
	if returnURL != "" {
		params.Set("return_url", returnURL)
	}
	if err := c.sign(params); err != nil {
		return "", err
	}
	return c.gatewayURL() + "?" + params.Encode(), nil
}

// ParsePagePayReturn 校验支付宝电脑网站支付回跳（return_url GET 参数）签名。
// 回跳参数仅用于提示，支付结果必须以异步通知或对账为准。
func (c *Client) ParsePagePayReturn(query url.Values) (*TradeNotifyResult, error) {
	if len(query) == 0 {
		return nil, errors.New("支付宝回跳参数不能为空")
	}
	appID := strings.TrimSpace(query.Get("app_id"))
	if configured := strings.TrimSpace(c.cfg.AppID); configured != "" && appID != configured {
		return nil, errors.New("支付宝回跳 appid 与配置不匹配")
	}
	if err := c.verifySignature(query); err != nil {
		return nil, err
	}
	result := &TradeNotifyResult{
		AppID:       appID,
		OutTradeNo:  strings.TrimSpace(query.Get("out_trade_no")),
		TradeNo:     strings.TrimSpace(query.Get("trade_no")),
		TradeStatus: strings.TrimSpace(query.Get("trade_status")),
		BuyerID:     strings.TrimSpace(firstNonEmpty(query.Get("buyer_open_id"), query.Get("buyer_id"), query.Get("buyer_user_id"))),
	}
	if result.OutTradeNo == "" {
		return nil, errors.New("支付宝回跳缺少商户订单号")
	}
	return result, nil
}
