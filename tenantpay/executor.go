package tenantpay

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/xinpaiyun/nova-lib/wechat"
)

// 分账单号前缀与默认描述。
const (
	psOutOrderNoPrefix = "PS"
	psDescription      = "平台服务费分账"
	psReturnPrefix     = "PSR"
)

// ProfitSharingInput 定义宿主提交的分账任务（一笔已完成订单）。
type ProfitSharingInput struct {
	TenantID      uint64 // 收款商户（租户）
	OrderNo       string // 宿主业务订单号
	TransactionID string // 微信支付订单号（回调返回）
	PaidCent      int64  // 实付金额（分）
}

// Executor 平台抽成分账执行器。
// 仅对"商户层收款"的微信单分账：订单预下单时需带 settle_info.profit_sharing=true，
// 且当前租户配置了收款商户号（平台层代收的单子走平台商户号，不参与分账）。
type Executor struct {
	store       *Store
	resolver    *Resolver
	masterKey   string
	platformMch string // 平台商户号（分账接收方）
	// AppIDOf 返回租户支付下单 appid（商户小程序）；为空时执行器内部按启用小程序解析。
	AppIDOf func(ctx context.Context, tenantID uint64) string
}

// NewExecutor 创建分账执行器。
// masterKey 用于解密商户配置；platformMch 为平台商户号（分账接收方）。
func NewExecutor(store *Store, resolver *Resolver, masterKey, platformMch string) *Executor {
	return &Executor{
		store:       store,
		resolver:    resolver,
		masterKey:   masterKey,
		platformMch: strings.TrimSpace(platformMch),
	}
}

// 业务错误。
var (
	ErrNoCommission      = errors.New("该租户未启用抽成")
	ErrPlatformMchMissed = errors.New("平台商户号未配置，无法分账")
	ErrPSAlreadySucceeded = errors.New("分账已成功，无需重复操作")
)

// psOutOrderNo 由业务订单号派生幂等分账单号（一笔订单只做一次分账）。
func psOutOrderNo(orderNo string) string {
	return psOutOrderNoPrefix + orderNo
}

// psOutReturnNo 由业务订单号派生幂等回退单号。
func psOutReturnNo(orderNo string) string {
	return psReturnPrefix + orderNo
}

// Submit 提交一笔已完成订单的分账任务（订单完成钩子调用，幂等）。
// 流程：查抽成配置 → 计算金额 → 落库 pending → 尝试发起分账（失败留待 Retry/Sync）。
func (e *Executor) Submit(ctx context.Context, in ProfitSharingInput) (ProfitSharingOrder, error) {
	orderNo := strings.TrimSpace(in.OrderNo)
	if in.TenantID == 0 || orderNo == "" || in.TransactionID == "" || in.PaidCent <= 0 {
		return ProfitSharingOrder{}, errors.New("分账任务参数不完整")
	}
	// 幂等：已有记录直接返回（重复回调/重复完成事件）。
	if existing, err := e.store.FindPSOrderByOrderNo(ctx, orderNo); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return ProfitSharingOrder{}, err
	}
	cfg, err := e.store.GetCommission(ctx, in.TenantID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ProfitSharingOrder{}, ErrNoCommission
		}
		return ProfitSharingOrder{}, err
	}
	commission := CalcCommission(in.PaidCent, cfg)
	if commission <= 0 {
		return ProfitSharingOrder{}, ErrNoCommission
	}
	ps := ProfitSharingOrder{
		TenantID:       in.TenantID,
		OrderNo:        orderNo,
		OutOrderNo:     psOutOrderNo(orderNo),
		TransactionID:  in.TransactionID,
		AmountCent:     in.PaidCent,
		CommissionCent: commission,
		RateBP:         cfg.RateBP,
		Status:         PSStatusPending,
	}
	ps, err = e.store.CreatePSOrder(ctx, ps)
	if err != nil {
		return ProfitSharingOrder{}, err
	}
	// 发起分账（失败不阻断提交，保留 pending 供定时任务重试）。
	if _, err := e.launch(ctx, &ps); err != nil {
		ps.Status = PSStatusPending
		ps.FailReason = truncateReason(err.Error())
		_ = e.store.UpdatePSOrder(ctx, &ps)
	}
	return ps, nil
}

// Retry 手动补发/重试分账（平台端 admin 调用）。
func (e *Executor) Retry(ctx context.Context, psID uint64) (ProfitSharingOrder, error) {
	ps, err := e.findByID(ctx, psID)
	if err != nil {
		return ProfitSharingOrder{}, err
	}
	if ps.Status == PSStatusSucceeded || ps.Status == PSStatusRefunded {
		return ps, ErrPSAlreadySucceeded
	}
	if _, err := e.launch(ctx, &ps); err != nil {
		ps.FailReason = truncateReason(err.Error())
		_ = e.store.UpdatePSOrder(ctx, &ps)
		return ps, err
	}
	return ps, nil
}

// Sync 同步微信侧分账结果（定时任务调用）：待发起的重试发起，处理中的查询落状态。
func (e *Executor) Sync(ctx context.Context, limit int) {
	pending, err := e.store.ListPendingPSOrders(ctx, limit)
	if err != nil {
		log.Printf("[tenantpay] 查询待同步分账记录失败: %v", err)
		return
	}
	for i := range pending {
		ps := &pending[i]
		switch ps.Status {
		case PSStatusPending, PSStatusFailed:
			if _, err := e.launch(ctx, ps); err != nil {
				ps.FailReason = truncateReason(err.Error())
				_ = e.store.UpdatePSOrder(ctx, ps)
			}
		case PSStatusProcessing:
			e.queryAndUpdate(ctx, ps)
		}
	}
}

// platformMchOf 返回分账接收方（平台商户号）：优先构造参数，未配置时惰性读取
// 平台层（tenant_id=0）微信支付配置中的商户号，支持 admin 后配即生效。
func (e *Executor) platformMchOf(ctx context.Context) string {
	if e.platformMch != "" {
		return e.platformMch
	}
	return e.resolver.MerchantMchID(ctx, 0)
}

// ReturnForRefund 订单退款前的分账回退入口（宿主退款逻辑调用）。
// 返回 (已回退, error)：已分账成功的订单先回退抽成再退款；未分账直接放行。
func (e *Executor) ReturnForRefund(ctx context.Context, orderNo string) (bool, error) {
	ps, err := e.store.FindPSOrderByOrderNo(ctx, strings.TrimSpace(orderNo))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil // 从未分账，直接放行退款
		}
		return false, err
	}
	if ps.Status == PSStatusRefunded {
		return true, nil // 已回退
	}
	if ps.Status != PSStatusSucceeded {
		// 分账未成功（pending/processing/failed）：先尝试查询微信侧终态。
		e.queryAndUpdate(ctx, &ps)
		if ps.Status != PSStatusSucceeded {
			// 微信侧无成功分账，无需回退；标记作废避免后续再分。
			if ps.Status != PSStatusFailed && ps.Status != PSStatusProcessing {
				ps.Status = PSStatusFailed
				ps.FailReason = "订单退款，分账取消"
				_ = e.store.UpdatePSOrder(ctx, &ps)
			}
			return false, nil
		}
	}
	if e.platformMchOf(ctx) == "" {
		return true, ErrPlatformMchMissed
	}
	client, err := e.resolver.Resolve(ctx, ps.TenantID)
	if err != nil {
		return true, err
	}
	ret, err := client.CreateProfitSharingReturn(ctx, wechat.ProfitSharingReturnRequest{
		OrderID:     ps.WxOrderID,
		OutOrderNo:  ps.OutOrderNo,
		OutReturnNo: psOutReturnNo(ps.OrderNo),
		ReturnMchID: e.platformMchOf(ctx),
		AmountCents: ps.CommissionCent,
		Description: "订单退款分账回退",
	})
	if err != nil {
		return true, fmt.Errorf("分账回退失败: %w", err)
	}
	switch ret.Result {
	case "SUCCESS":
		ps.Status = PSStatusRefunded
		ps.ReturnNo = ret.OutReturnNo
		_ = e.store.UpdatePSOrder(ctx, &ps)
		return true, nil
	case "FAILED":
		ps.FailReason = "回退失败: " + ret.FailReason
		_ = e.store.UpdatePSOrder(ctx, &ps)
		return true, fmt.Errorf("分账回退失败: %s", ret.FailReason)
	default:
		// PROCESSING：同步接口一般实时返回，处理中留给下次重试。
		return true, fmt.Errorf("分账回退处理中，请稍后重试退款")
	}
}

// queryAndUpdate 查询微信侧分账结果并更新记录状态。
func (e *Executor) queryAndUpdate(ctx context.Context, ps *ProfitSharingOrder) {
	client, err := e.resolver.Resolve(ctx, ps.TenantID)
	if err != nil {
		return
	}
	order, err := client.QueryProfitSharingOrder(ctx, ps.OutOrderNo, ps.TransactionID)
	if err != nil {
		return
	}
	if order.Finished() && order.Succeeded() {
		ps.Status = PSStatusSucceeded
		ps.FailReason = ""
		ps.WxOrderID = order.OrderID
	} else if order.Finished() {
		ps.Status = PSStatusFailed
		ps.FailReason = "分账已关闭"
		ps.WxOrderID = order.OrderID
	} else {
		return
	}
	_ = e.store.UpdatePSOrder(ctx, ps)
}

// launch 以收款商户身份发起分账（先确保平台商户号在接收方列表）。
func (e *Executor) launch(ctx context.Context, ps *ProfitSharingOrder) (ProfitSharingOrder, error) {
	platformMch := e.platformMchOf(ctx)
	if platformMch == "" {
		return *ps, ErrPlatformMchMissed
	}
	// 仅商户层收款可分账：租户必须配置了自己的商户号。
	if e.resolver.MerchantMchID(ctx, ps.TenantID) == "" {
		return *ps, errors.New("商户未配置自己的微信支付，无需分账")
	}
	client, err := e.resolver.Resolve(ctx, ps.TenantID)
	if err != nil {
		return *ps, err
	}
	if err := e.ensureReceiver(ctx, client, ps.TenantID); err != nil {
		return *ps, err
	}
	order, err := client.CreateProfitSharingOrder(ctx, wechat.ProfitSharingOrderRequest{
		TransactionID:   ps.TransactionID,
		OutOrderNo:      ps.OutOrderNo,
		ReceiverMchID:   platformMch,
		AmountCents:     ps.CommissionCent,
		Description:     psDescription,
		UnfreezeUnsplit: true,
	})
	if err != nil {
		return *ps, err
	}
	ps.WxOrderID = order.OrderID
	if order.Finished() {
		if order.Succeeded() {
			ps.Status = PSStatusSucceeded
			ps.FailReason = ""
		} else {
			ps.Status = PSStatusFailed
			ps.FailReason = "分账已关闭"
		}
	} else {
		ps.Status = PSStatusProcessing
		ps.FailReason = ""
	}
	if err := e.store.UpdatePSOrder(ctx, ps); err != nil {
		return *ps, err
	}
	return *ps, nil
}

// ensureReceiver 确保平台商户号在分账接收方列表中（微信侧幂等，重复添加无副作用）。
func (e *Executor) ensureReceiver(ctx context.Context, client *wechat.PayClient, tenantID uint64) error {
	appID := ""
	if e.AppIDOf != nil {
		appID = e.AppIDOf(ctx, tenantID)
	}
	if appID == "" {
		if app, err := e.store.FindEnabledWechatAppByTenant(ctx, tenantID); err == nil {
			appID = app.AppID
		}
	}
	if appID == "" {
		return errors.New("商户未绑定小程序，无法添加分账接收方")
	}
	// AddProfitSharingReceiver 内部使用客户端配置的 appid，这里仅校验绑定关系存在。
	return client.AddProfitSharingReceiver(ctx, wechat.ProfitSharingReceiver{
		Type:         "MERCHANT_ID",
		Account:      e.platformMch,
		RelationType: "SERVICE_PROVIDER",
	})
}

// findByID 按主键读取分账记录。
func (e *Executor) findByID(ctx context.Context, id uint64) (ProfitSharingOrder, error) {
	var ps ProfitSharingOrder
	if err := e.store.db.WithContext(ctx).First(&ps, id).Error; err != nil {
		return ProfitSharingOrder{}, normalizeError(err)
	}
	return ps, nil
}

// truncateReason 截断失败原因到字段长度以内。
func truncateReason(reason string) string {
	reason = strings.TrimSpace(reason)
	const maxLen = 250
	runes := []rune(reason)
	if len(runes) > maxLen {
		reason = string(runes[:maxLen])
	}
	return reason
}
