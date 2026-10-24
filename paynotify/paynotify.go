// Package paynotify 提供微信支付回调的统一处理模板：按租户定位验签客户端 →
// 官方 SDK V3 验签解密 → 状态分发（成功落账/终态失败关单/其余忽略等重试）→
// 落账动作由项目注入（金额校验 + 幂等流转 + 入账语义均属项目差异面）。
//
// 各项目重复的回调骨架（公开路由 → 验签 → 字段提取 → 应答语义）收敛于此；
// HTTP 适配层（Hertz）提供应答常量：成功 200 {"code":"SUCCESS"}，
// 失败 500 {"code":"FAIL"}（微信收到非 2xx 会按策略重试）。
package paynotify

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"

	"github.com/xinpaiyun/nova-lib/wechat"
)

// 微信支付通知交易状态常量。
const (
	StateSuccess = "SUCCESS"
	StateClosed  = "CLOSED"
	StateRevoked = "REVOKED"
	StatePayErr  = "PAYERROR"
)

// 默认关单状态集合（终态失败，通知到达时可选触发关单回调）。
var defaultClosedStates = map[string]bool{StateClosed: true, StateRevoked: true, StatePayErr: true}

// ClientResolver 按租户解析验签用微信支付客户端
// （tenantpay.Resolver.Resolve 的签名可直接适配）。
type ClientResolver func(ctx context.Context, tenantID uint64) (*wechat.PayClient, error)

// TransactionEvent 已验签解密的支付结果通知事件。
type TransactionEvent struct {
	OutTradeNo    string
	TransactionID string
	TradeState    string
	Success       bool
	PaidAt        time.Time // SuccessTime 解析（RFC3339），缺失/解析失败取当前时间
	AmountCents   int64     // 订单金额（分），金额与本地单一致性校验由落账回调负责
	Raw           *payments.Transaction
}

// PaidHook 支付落账回调（项目实现：按单号反查本地单 → 金额校验 →
// 条件更新幂等流转 → 入账动作）。返回 error 时模板回非成功应答触发微信重试；
// 重复回调幂等命中时直接返回 nil。
type PaidHook func(ctx context.Context, tenantID uint64, evt TransactionEvent) error

// CloseHook 关单回调（可选）：通知为终态失败（默认 CLOSED/REVOKED/PAYERROR）时触发。
type CloseHook func(ctx context.Context, tenantID uint64, evt TransactionEvent) error

// RefundEvent 已验签解密的退款结果通知事件。
type RefundEvent struct {
	OutRefundNo  string
	OutTradeNo   string
	RefundID     string
	RefundStatus string // SUCCESS / CLOSED / ABNORMAL / ...
	Success      bool
	SuccessAt    time.Time // SuccessTime 解析（RFC3339），缺失/解析失败取当前时间
	Raw          *refunddomestic.Refund
}

// RefundHook 退款落账回调（项目实现：退款单反查 → 幂等流转 → 权益回退/关单等）。
type RefundHook func(ctx context.Context, tenantID uint64, evt RefundEvent) error

// Handler 微信支付回调模板：验签解密与状态分发收敛于此，落账动作项目注入。
type Handler struct {
	resolve      ClientResolver
	onPaid       PaidHook
	onClose      CloseHook
	onRefund     RefundHook
	closedStates map[string]bool
}

// Option 配置回调模板。
type Option func(*Handler)

// WithPaidHook 注入支付落账回调（支付回调必配，缺省时成功通知返回错误）。
func WithPaidHook(hook PaidHook) Option {
	return func(h *Handler) { h.onPaid = hook }
}

// WithCloseHook 注入关单回调（可选，未注入时终态失败通知忽略）。
func WithCloseHook(hook CloseHook) Option {
	return func(h *Handler) { h.onClose = hook }
}

// WithRefundHook 注入退款落账回调（退款回调必配，缺省时退款通知返回错误）。
func WithRefundHook(hook RefundHook) Option {
	return func(h *Handler) { h.onRefund = hook }
}

// WithClosedStates 覆盖关单状态集合（默认 CLOSED/REVOKED/PAYERROR）。
func WithClosedStates(states ...string) Option {
	return func(h *Handler) {
		set := make(map[string]bool, len(states))
		for _, s := range states {
			set[s] = true
		}
		h.closedStates = set
	}
}

// NewHandler 创建微信支付回调模板处理器。
func NewHandler(resolve ClientResolver, options ...Option) *Handler {
	h := &Handler{resolve: resolve, closedStates: defaultClosedStates}
	for _, o := range options {
		o(h)
	}
	return h
}

// HandlePayment 处理支付结果通知：验签解密 → 状态分发 → 注入落账。
// request 为原始 HTTP 请求（官方 SDK 验签需原始头与原始体）；
// 返回 error 时由 HTTP 适配层回非成功应答，触发微信重试。
func (h *Handler) HandlePayment(ctx context.Context, tenantID uint64, request *http.Request) error {
	client, err := h.client(ctx, tenantID)
	if err != nil {
		return err
	}
	txn, err := client.ParseTransactionNotify(ctx, request)
	if err != nil {
		// 验签失败/解密失败：如实报错，适配层回非成功应答等微信重试。
		return err
	}
	state := derefString(txn.TradeState)
	evt := TransactionEvent{
		OutTradeNo:    derefString(txn.OutTradeNo),
		TransactionID: derefString(txn.TransactionId),
		TradeState:    state,
		Success:       state == StateSuccess,
		PaidAt:        parseRFC3339OrNow(txn.SuccessTime),
		AmountCents:   amountTotal(txn.Amount),
		Raw:           txn,
	}
	if evt.Success {
		if h.onPaid == nil {
			return errors.New("支付落账回调未配置")
		}
		return h.onPaid(ctx, tenantID, evt)
	}
	if h.closedStates[state] && h.onClose != nil {
		return h.onClose(ctx, tenantID, evt)
	}
	// 其余状态（NOTPAY/USERPAYING/PROCESSING 等）不流转本地订单，等待后续通知或查单。
	return nil
}

// HandleRefund 处理退款结果通知：验签解密 → 注入退款落账。
func (h *Handler) HandleRefund(ctx context.Context, tenantID uint64, request *http.Request) error {
	client, err := h.client(ctx, tenantID)
	if err != nil {
		return err
	}
	refund, err := client.ParseRefundNotify(ctx, request)
	if err != nil {
		return err
	}
	if h.onRefund == nil {
		return errors.New("退款落账回调未配置")
	}
	status := ""
	if refund.Status != nil {
		status = string(*refund.Status)
	}
	successAt := time.Now()
	if refund.SuccessTime != nil {
		successAt = *refund.SuccessTime
	}
	return h.onRefund(ctx, tenantID, RefundEvent{
		OutRefundNo:  derefString(refund.OutRefundNo),
		OutTradeNo:   derefString(refund.OutTradeNo),
		RefundID:     derefString(refund.RefundId),
		RefundStatus: status,
		Success:      status == "SUCCESS",
		SuccessAt:    successAt,
		Raw:          refund,
	})
}

// client 按租户解析验签客户端（每次回调实时解析，凭据变更即时生效）。
func (h *Handler) client(ctx context.Context, tenantID uint64) (*wechat.PayClient, error) {
	if h.resolve == nil {
		return nil, errors.New("支付客户端解析器未配置")
	}
	return h.resolve(ctx, tenantID)
}

// derefString 安全取字符串指针值。
func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// amountTotal 安全取通知订单金额（分）。
func amountTotal(amount *payments.TransactionAmount) int64 {
	if amount == nil || amount.Total == nil {
		return 0
	}
	return *amount.Total
}

// parseRFC3339OrNow 解析 RFC3339 时间（微信通知 SuccessTime 格式），失败取当前时间。
func parseRFC3339OrNow(p *string) time.Time {
	if p == nil || *p == "" {
		return time.Now()
	}
	if parsed, err := time.Parse(time.RFC3339, *p); err == nil {
		return parsed
	}
	return time.Now()
}
