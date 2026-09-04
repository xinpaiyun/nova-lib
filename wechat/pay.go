package wechat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// PayClient 封装微信支付 V3 小程序支付、查单、退款、商家转账与免确认收款授权能力。
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

// SceneReportInfo 定义转账场景报备信息条目。
type SceneReportInfo struct {
	InfoType    string
	InfoContent string
}

// MerchantTransferRequest 定义微信支付商家转账请求参数。
type MerchantTransferRequest struct {
	AppID              string
	OpenID             string
	OutBillNo          string
	TransferSceneID    string
	TransferAmount     int64
	TransferRemark     string
	UserRecvPerception string
	NotifyURL          string
	SceneReportInfos   []SceneReportInfo
	AuthorizationID    string // 免确认转账时优先使用
	OutAuthorizationNo string // 免确认转账时兜底使用
}

// MerchantTransferResponse 定义微信支付商家转账响应。
type MerchantTransferResponse struct {
	OutBillNo      string
	TransferBillNo string
	State          string
	CreateTime     string
	PackageInfo    string
}

// AuthorizationRequest 定义免确认收款授权请求参数。
type AuthorizationRequest struct {
	OutAuthorizationNo string
	AppID              string
	OpenID             string
	TransferSceneID    string
	UserDisplayName    string
	UserRecvPerception string
	NotifyURL          string
}

// AuthorizationResponse 定义免确认收款授权响应。
type AuthorizationResponse struct {
	OutAuthorizationNo string
	State              string
	PackageInfo        string
	CreateTime         string
}

// MerchantTransferNotify 定义微信支付商家转账结果通知。
type MerchantTransferNotify struct {
	OutBillNo      string `json:"out_bill_no"`
	TransferBillNo string `json:"transfer_bill_no"`
	State          string `json:"state"`
	TransferState  string `json:"transfer_state"`
	FailReason     string `json:"fail_reason"`
	UpdateTime     string `json:"update_time"`
}

// AuthorizationNotify 定义微信支付免确认收款授权结果通知。
type AuthorizationNotify struct {
	OutAuthorizationNo string `json:"out_authorization_no"`
	AuthorizationID    string `json:"authorization_id"`
	State              string `json:"state"`
	OpenID             string `json:"openid"`
	AppID              string `json:"appid"`
	TransferSceneID    string `json:"transfer_scene_id"`
	FailReason         string `json:"fail_reason"`
	UpdateTime         string `json:"update_time"`
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

// Config 返回当前微信支付客户端使用的配置快照。
func (c *PayClient) Config() config.WechatConfig {
	if c == nil {
		return config.WechatConfig{}
	}
	return c.cfg
}

// PrepayMiniProgram 创建微信支付预下单并返回小程序支付参数。
func (c *PayClient) PrepayMiniProgram(ctx context.Context, req MiniProgramPrepayRequest) (*MiniProgramPrepayResponse, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	if strings.TrimSpace(req.OpenID) == "" {
		return nil, errors.New("缺少微信 openId")
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

// TransferToBalance 发起微信支付商家转账到零钱。
func (c *PayClient) TransferToBalance(ctx context.Context, req MerchantTransferRequest) (*MerchantTransferResponse, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	appID := strings.TrimSpace(req.AppID)
	if appID == "" {
		appID = strings.TrimSpace(c.cfg.AppID)
	}
	if strings.TrimSpace(req.OpenID) == "" {
		return nil, errors.New("缺少收款用户 openId")
	}
	if req.TransferAmount <= 0 {
		return nil, errors.New("转账金额必须大于 0")
	}
	sceneID := strings.TrimSpace(req.TransferSceneID)
	if sceneID == "" {
		sceneID = "1005"
	}
	remark := strings.TrimSpace(req.TransferRemark)
	if remark == "" {
		return nil, errors.New("转账备注不能为空")
	}
	body := map[string]any{
		"appid":             appID,
		"out_bill_no":       strings.TrimSpace(req.OutBillNo),
		"transfer_scene_id": sceneID,
		"openid":            strings.TrimSpace(req.OpenID),
		"transfer_amount":   req.TransferAmount,
		"transfer_remark":   remark,
	}
	if perception := strings.TrimSpace(req.UserRecvPerception); perception != "" {
		body["user_recv_perception"] = perception
	}
	if notifyURL := strings.TrimSpace(req.NotifyURL); notifyURL != "" {
		body["notify_url"] = notifyURL
	}
	if len(req.SceneReportInfos) > 0 {
		infos := make([]map[string]string, 0, len(req.SceneReportInfos))
		for _, info := range req.SceneReportInfos {
			infos = append(infos, map[string]string{
				"info_type":    strings.TrimSpace(info.InfoType),
				"info_content": strings.TrimSpace(info.InfoContent),
			})
		}
		body["transfer_scene_report_infos"] = infos
	}
	result, err := c.client.Post(ctx, "https://api.mch.weixin.qq.com/v3/fund-app/mch-transfer/transfer-bills", body)
	if err != nil {
		return nil, err
	}
	return decodeMerchantTransferResponse(result.Response, "解析微信转账响应失败")
}

// TransferWithAuthorization 使用已授权关系发起免确认转账，直接到账无需用户确认。
func (c *PayClient) TransferWithAuthorization(ctx context.Context, req MerchantTransferRequest) (*MerchantTransferResponse, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	appID := strings.TrimSpace(req.AppID)
	if appID == "" {
		appID = strings.TrimSpace(c.cfg.AppID)
	}
	if req.TransferAmount <= 0 {
		return nil, errors.New("转账金额必须大于 0")
	}
	authID := strings.TrimSpace(req.AuthorizationID)
	outAuthNo := strings.TrimSpace(req.OutAuthorizationNo)
	if authID == "" && outAuthNo == "" {
		return nil, errors.New("缺少免确认收款授权单号")
	}
	sceneID := strings.TrimSpace(req.TransferSceneID)
	if sceneID == "" {
		sceneID = "1005"
	}
	remark := strings.TrimSpace(req.TransferRemark)
	if remark == "" {
		return nil, errors.New("转账备注不能为空")
	}
	body := map[string]any{
		"appid":             appID,
		"out_bill_no":       strings.TrimSpace(req.OutBillNo),
		"transfer_scene_id": sceneID,
		"transfer_amount":   req.TransferAmount,
		"transfer_remark":   remark,
	}
	if authID != "" {
		body["authorization_id"] = authID
	} else {
		body["out_authorization_no"] = outAuthNo
	}
	if perception := strings.TrimSpace(req.UserRecvPerception); perception != "" {
		body["user_recv_perception"] = perception
	}
	if len(req.SceneReportInfos) > 0 {
		infos := make([]map[string]string, 0, len(req.SceneReportInfos))
		for _, info := range req.SceneReportInfos {
			infos = append(infos, map[string]string{
				"info_type":    strings.TrimSpace(info.InfoType),
				"info_content": strings.TrimSpace(info.InfoContent),
			})
		}
		body["transfer_scene_report_infos"] = infos
	}
	result, err := c.client.Post(ctx, "https://api.mch.weixin.qq.com/v3/fund-app/mch-transfer/transfer-bills/transfer", body)
	if err != nil {
		return nil, err
	}
	return decodeMerchantTransferResponse(result.Response, "解析微信免确认转账响应失败")
}

// ApplyUserConfirmAuthorization 申请免确认收款授权，返回 package_info 供小程序端唤起微信授权页。
func (c *PayClient) ApplyUserConfirmAuthorization(ctx context.Context, req AuthorizationRequest) (*AuthorizationResponse, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	appID := strings.TrimSpace(req.AppID)
	if appID == "" {
		appID = strings.TrimSpace(c.cfg.AppID)
	}
	if strings.TrimSpace(req.OpenID) == "" {
		return nil, errors.New("缺少授权用户 openId")
	}
	outAuthNo := strings.TrimSpace(req.OutAuthorizationNo)
	if outAuthNo == "" {
		return nil, errors.New("缺少商户授权单号")
	}
	sceneID := strings.TrimSpace(req.TransferSceneID)
	if sceneID == "" {
		sceneID = "1005"
	}
	body := map[string]any{
		"out_authorization_no": outAuthNo,
		"appid":                appID,
		"openid":               strings.TrimSpace(req.OpenID),
		"transfer_scene_id":    sceneID,
		"user_display_name":    strings.TrimSpace(req.UserDisplayName),
	}
	if notifyURL := strings.TrimSpace(req.NotifyURL); notifyURL != "" {
		body["authorization_notify_url"] = notifyURL
	}
	if perception := strings.TrimSpace(req.UserRecvPerception); perception != "" {
		body["user_recv_perception"] = perception
	}
	result, err := c.client.Post(ctx, "https://api.mch.weixin.qq.com/v3/fund-app/mch-transfer/user-confirm-authorization", body)
	if err != nil {
		return nil, err
	}
	var authPayload struct {
		OutAuthorizationNo string `json:"out_authorization_no"`
		State              string `json:"state"`
		PackageInfo        string `json:"package_info"`
		CreateTime         string `json:"create_time"`
	}
	if err := core.UnMarshalResponse(result.Response, &authPayload); err != nil {
		return nil, fmt.Errorf("解析微信授权响应失败: %w", err)
	}
	return &AuthorizationResponse{
		OutAuthorizationNo: authPayload.OutAuthorizationNo,
		State:              authPayload.State,
		PackageInfo:        authPayload.PackageInfo,
		CreateTime:         authPayload.CreateTime,
	}, nil
}

// CloseUserConfirmAuthorization 关闭免确认收款授权（释放微信侧的授权名额）。
func (c *PayClient) CloseUserConfirmAuthorization(ctx context.Context, outAuthorizationNo string) error {
	if !c.Enabled() {
		return errors.New("微信支付未配置完成")
	}
	target := fmt.Sprintf("https://api.mch.weixin.qq.com/v3/fund-app/mch-transfer/user-confirm-authorization/out-authorization-no/%s/close",
		url.PathEscape(strings.TrimSpace(outAuthorizationNo)))
	_, err := c.client.Post(ctx, target, map[string]any{})
	return err
}

// ParseMerchantTransferNotify 解析并验签微信商家转账结果通知。
func (c *PayClient) ParseMerchantTransferNotify(ctx context.Context, request *http.Request) (*MerchantTransferNotify, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	var content MerchantTransferNotify
	if _, err := c.notifyHandler.ParseNotifyRequest(ctx, request, &content); err != nil {
		return nil, err
	}
	if content.State == "" {
		content.State = content.TransferState
	}
	return &content, nil
}

// ParseAuthorizationNotify 解析并验签微信免确认收款授权结果通知。
func (c *PayClient) ParseAuthorizationNotify(ctx context.Context, request *http.Request) (*AuthorizationNotify, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	var content AuthorizationNotify
	if _, err := c.notifyHandler.ParseNotifyRequest(ctx, request, &content); err != nil {
		return nil, err
	}
	return &content, nil
}

// decodeMerchantTransferResponse 解析商家转账响应中的账单信息。
func decodeMerchantTransferResponse(response *http.Response, failMessage string) (*MerchantTransferResponse, error) {
	var payload struct {
		OutBillNo      string `json:"out_bill_no"`
		TransferBillNo string `json:"transfer_bill_no"`
		State          string `json:"state"`
		CreateTime     string `json:"create_time"`
		PackageInfo    string `json:"package_info"`
	}
	if err := core.UnMarshalResponse(response, &payload); err != nil {
		return nil, fmt.Errorf("%s: %w", failMessage, err)
	}
	return &MerchantTransferResponse{
		OutBillNo:      payload.OutBillNo,
		TransferBillNo: payload.TransferBillNo,
		State:          payload.State,
		CreateTime:     payload.CreateTime,
		PackageInfo:    payload.PackageInfo,
	}, nil
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
