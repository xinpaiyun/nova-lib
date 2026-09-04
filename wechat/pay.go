package wechat

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/jsapi"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"

	"github.com/xinpaiyun/nova-lib/config"
)

// PayClient 封装微信支付 V3 小程序支付能力。
type PayClient struct {
	cfg           config.WechatConfig
	client        *core.Client
	notifyHandler *notify.Handler
	enabled       bool
}

// MiniProgramPrepayRequest 定义小程序支付预下单参数。
type MiniProgramPrepayRequest struct {
	Description string
	OutTradeNo  string
	NotifyURL   string
	AmountCents int64
	OpenID      string
}

// MiniProgramPrepayResponse 定义小程序拉起支付参数。
type MiniProgramPrepayResponse struct {
	PrepayID  string `json:"prepayId"`
	AppID     string `json:"appId"`
	TimeStamp string `json:"timeStamp"`
	NonceStr  string `json:"nonceStr"`
	Package   string `json:"package"`
	SignType  string `json:"signType"`
	PaySign   string `json:"paySign"`
}

// RefundRequest 定义微信支付退款申请参数。
type RefundRequest struct {
	OutTradeNo  string
	OutRefundNo string
	Reason      string
	TotalCents  int64
	RefundCents int64
}

// NewPayClient 创建微信支付客户端，缺少配置时返回禁用态客户端。
func NewPayClient(cfg config.WechatConfig) *PayClient {
	pay := &PayClient{cfg: cfg}
	if !cfg.HasPayConfig() {
		return pay
	}
	privateKey, err := utils.LoadPrivateKeyWithPath(strings.TrimSpace(cfg.CertPath))
	if err != nil {
		return pay
	}
	publicKey, err := utils.LoadPublicKeyWithPath(strings.TrimSpace(cfg.PublicKeyPath))
	if err != nil {
		return pay
	}
	client, err := core.NewClient(
		context.Background(),
		option.WithWechatPayPublicKeyAuthCipher(
			strings.TrimSpace(cfg.MchID),
			strings.TrimSpace(cfg.CertSerialNo),
			privateKey,
			strings.TrimSpace(cfg.PublicKeyID),
			publicKey,
		),
	)
	if err != nil {
		return pay
	}
	notifyHandler, err := notify.NewRSANotifyHandler(
		strings.TrimSpace(cfg.APIKeyV3),
		verifiers.NewSHA256WithRSAPubkeyVerifier(strings.TrimSpace(cfg.PublicKeyID), *publicKey),
	)
	if err != nil {
		return pay
	}
	pay.client = client
	pay.notifyHandler = notifyHandler
	pay.enabled = true
	return pay
}

// Enabled 返回微信支付客户端是否可用。
func (c *PayClient) Enabled() bool {
	return c != nil && c.enabled && c.client != nil && c.notifyHandler != nil
}

// PrepayMiniProgram 创建微信支付预下单并返回小程序支付参数。
func (c *PayClient) PrepayMiniProgram(ctx context.Context, req MiniProgramPrepayRequest) (*MiniProgramPrepayResponse, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	notifyURL := strings.TrimSpace(req.NotifyURL)
	if notifyURL == "" {
		notifyURL = strings.TrimSpace(c.cfg.NotifyURL)
	}
	resp, _, err := (&jsapi.JsapiApiService{Client: c.client}).PrepayWithRequestPayment(ctx, jsapi.PrepayRequest{
		Appid:       core.String(strings.TrimSpace(c.cfg.AppID)),
		Mchid:       core.String(strings.TrimSpace(c.cfg.MchID)),
		Description: core.String(strings.TrimSpace(req.Description)),
		OutTradeNo:  core.String(strings.TrimSpace(req.OutTradeNo)),
		NotifyUrl:   core.String(notifyURL),
		Amount: &jsapi.Amount{
			Currency: core.String("CNY"),
			Total:    core.Int64(req.AmountCents),
		},
		Payer: &jsapi.Payer{Openid: core.String(strings.TrimSpace(req.OpenID))},
	})
	if err != nil {
		return nil, err
	}
	return &MiniProgramPrepayResponse{
		PrepayID:  stringValue(resp.PrepayId),
		AppID:     stringValue(resp.Appid),
		TimeStamp: stringValue(resp.TimeStamp),
		NonceStr:  stringValue(resp.NonceStr),
		Package:   stringValue(resp.Package),
		SignType:  stringValue(resp.SignType),
		PaySign:   stringValue(resp.PaySign),
	}, nil
}

// QueryOrderByOutTradeNo 根据商户订单号查询微信支付结果。
func (c *PayClient) QueryOrderByOutTradeNo(ctx context.Context, outTradeNo string) (*payments.Transaction, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	resp, _, err := (&jsapi.JsapiApiService{Client: c.client}).QueryOrderByOutTradeNo(ctx, jsapi.QueryOrderByOutTradeNoRequest{
		OutTradeNo: core.String(strings.TrimSpace(outTradeNo)),
		Mchid:      core.String(strings.TrimSpace(c.cfg.MchID)),
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CloseOrder 根据商户订单号关闭未支付订单。
func (c *PayClient) CloseOrder(ctx context.Context, outTradeNo string) error {
	if !c.Enabled() {
		return errors.New("微信支付未配置完成")
	}
	_, err := (&jsapi.JsapiApiService{Client: c.client}).CloseOrder(ctx, jsapi.CloseOrderRequest{
		OutTradeNo: core.String(strings.TrimSpace(outTradeNo)),
		Mchid:      core.String(strings.TrimSpace(c.cfg.MchID)),
	})
	return err
}

// CreateRefund 提交微信支付退款申请。
func (c *PayClient) CreateRefund(ctx context.Context, req RefundRequest) (*refunddomestic.Refund, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	resp, _, err := (&refunddomestic.RefundsApiService{Client: c.client}).Create(ctx, refunddomestic.CreateRequest{
		OutTradeNo:  core.String(strings.TrimSpace(req.OutTradeNo)),
		OutRefundNo: core.String(strings.TrimSpace(req.OutRefundNo)),
		Reason:      optionalString(req.Reason),
		NotifyUrl:   optionalString(c.cfg.NotifyURL),
		Amount: &refunddomestic.AmountReq{
			Refund:   core.Int64(req.RefundCents),
			Total:    core.Int64(req.TotalCents),
			Currency: core.String("CNY"),
		},
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// QueryRefundByOutRefundNo 根据商户退款单号查询退款状态。
func (c *PayClient) QueryRefundByOutRefundNo(ctx context.Context, outRefundNo string) (*refunddomestic.Refund, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	resp, _, err := (&refunddomestic.RefundsApiService{Client: c.client}).QueryByOutRefundNo(ctx, refunddomestic.QueryByOutRefundNoRequest{
		OutRefundNo: core.String(strings.TrimSpace(outRefundNo)),
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ParseTransactionNotify 解析并验签微信支付结果通知。
func (c *PayClient) ParseTransactionNotify(ctx context.Context, request *http.Request) (*payments.Transaction, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	var content payments.Transaction
	if _, err := c.notifyHandler.ParseNotifyRequest(ctx, request, &content); err != nil {
		return nil, err
	}
	return &content, nil
}

// ParseRefundNotify 解析并验签微信退款结果通知。
func (c *PayClient) ParseRefundNotify(ctx context.Context, request *http.Request) (*refunddomestic.Refund, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	var content refunddomestic.Refund
	if _, err := c.notifyHandler.ParseNotifyRequest(ctx, request, &content); err != nil {
		return nil, err
	}
	return &content, nil
}

// stringValue 返回字符串指针安全值。
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// optionalString 将空字符串转为空指针，避免向微信支付提交空可选字段。
func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return core.String(value)
}
