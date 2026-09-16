// 分账（profit sharing）能力：以"分账出资方（收款商户）"身份调用微信支付分账 API。
// 典型场景：商户使用自己的商户号收款，平台按比例抽成，订单完成后由商户号向平台商户号分账。
// 注意：使用前商户号需开通分账权限，且平台商户号需被添加为分账接收方。
package wechat

import (
	"context"
	"errors"
	"strings"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/services/profitsharing"
)

// ProfitSharingOrderRequest 定义请求分账参数。
type ProfitSharingOrderRequest struct {
	// TransactionID 微信支付订单号（回调返回的 transaction_id）。
	TransactionID string
	// OutOrderNo 商户分账单号，商户系统内唯一，同号重复请求等同一次（幂等键）。
	OutOrderNo string
	// ReceiverMchID 分账接收方商户号（直连商户场景为平台商户号）。
	ReceiverMchID string
	// AmountCents 分账金额（分），不能超过原订单支付金额及最大分账比例金额。
	AmountCents int64
	// Description 分账原因描述（分账账单中体现）。
	Description string
	// UnfreezeUnsplit 分账完成后是否将剩余资金解冻给出资商户（通常置 true）。
	UnfreezeUnsplit bool
}

// ProfitSharingReceiverResult 定义单个接收方的分账结果。
type ProfitSharingReceiverResult struct {
	Type        string
	Account     string
	Amount      int64
	Description string
	DetailID    string
	Result      string // PENDING 待分账 / SUCCESS 分账成功 / CLOSED 已关闭
	FailReason  string
}

// ProfitSharingOrder 定义分账单查询/创建结果。
type ProfitSharingOrder struct {
	OrderID       string // 微信分账单号
	OutOrderNo    string // 商户分账单号
	TransactionID string // 微信支付订单号
	State         string // PROCESSING 处理中 / FINISHED 分账完成
	Receivers     []ProfitSharingReceiverResult
}

// Finished 返回分账单是否已终态完成。
func (o *ProfitSharingOrder) Finished() bool {
	return o != nil && o.State == "FINISHED"
}

// Succeeded 返回分账是否全部成功（单接收方场景即 SUCCESS）。
func (o *ProfitSharingOrder) Succeeded() bool {
	if o == nil || len(o.Receivers) == 0 {
		return false
	}
	for _, r := range o.Receivers {
		if r.Result != "SUCCESS" {
			return false
		}
	}
	return true
}

// ProfitSharingReturnRequest 定义分账回退参数。
type ProfitSharingReturnRequest struct {
	// OutOrderNo 原商户分账单号（与 OrderID 二选一，优先使用 OrderID）。
	OutOrderNo string
	// OrderID 微信分账单号。
	OrderID string
	// OutReturnNo 商户回退单号，商户系统内唯一（幂等键）。
	OutReturnNo string
	// ReturnMchID 分账回退的出资商户号，即原分账的接收方（平台商户号）。
	ReturnMchID string
	// AmountCents 回退金额（分），不能超过原分账单分给该接收方的金额。
	AmountCents int64
	// Description 回退原因描述。
	Description string
}

// ProfitSharingReturn 定义分账回退结果。
type ProfitSharingReturn struct {
	OrderID     string // 微信分账单号
	OutOrderNo  string // 原商户分账单号
	OutReturnNo string // 商户回退单号
	Amount      int64
	Result      string // PROCESSING 处理中 / SUCCESS 退款成功 / FAILED 失败
	FailReason  string
}

// ProfitSharingReceiver 定义分账接收方信息。
type ProfitSharingReceiver struct {
	// Type 接收方类型：MERCHANT_ID 商户号 / PERSONAL_OPENID 个人 openid。
	Type string
	// Account 接收方账号：商户号或个人 openid。
	Account string
	// Name 接收方全名（个人类型可选，用于实名校验）。
	Name string
	// RelationType 与分账方的关系：SERVICE_PROVIDER 服务商 / CUSTOM 自定义等。
	RelationType string
}

// AddProfitSharingReceiver 添加分账接收方（建立商户号 → 平台商户号的分账关系）。
func (c *PayClient) AddProfitSharingReceiver(ctx context.Context, req ProfitSharingReceiver) error {
	if !c.Enabled() {
		return errors.New("微信支付未配置完成")
	}
	receiver := profitsharing.AddReceiverRequest{
		Appid:   core.String(strings.TrimSpace(c.cfg.AppID)),
		Type:    profitsharing.RECEIVERTYPE_MERCHANT_ID.Ptr(),
		Account: core.String(strings.TrimSpace(req.Account)),
	}
	if receiver.Account == nil || *receiver.Account == "" {
		return errors.New("缺少分账接收方商户号")
	}
	relation := strings.TrimSpace(req.RelationType)
	if relation == "" {
		relation = "SERVICE_PROVIDER"
	}
	receiver.RelationType = profitsharing.ReceiverRelationType(relation).Ptr()
	if name := strings.TrimSpace(req.Name); name != "" {
		receiver.Name = core.String(name)
	}
	_, _, err := (&profitsharing.ReceiversApiService{Client: c.client}).AddReceiver(ctx, receiver)
	return err
}

// CreateProfitSharingOrder 请求分账（异步受理，结果通过 QueryProfitSharingOrder 查询）。
func (c *PayClient) CreateProfitSharingOrder(ctx context.Context, req ProfitSharingOrderRequest) (*ProfitSharingOrder, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	if strings.TrimSpace(req.TransactionID) == "" {
		return nil, errors.New("缺少微信支付订单号")
	}
	if strings.TrimSpace(req.OutOrderNo) == "" {
		return nil, errors.New("缺少商户分账单号")
	}
	if strings.TrimSpace(req.ReceiverMchID) == "" {
		return nil, errors.New("缺少分账接收方商户号")
	}
	if req.AmountCents <= 0 {
		return nil, errors.New("分账金额必须大于 0")
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = "平台服务费分账"
	}
	resp, _, err := (&profitsharing.OrdersApiService{Client: c.client}).CreateOrder(ctx, profitsharing.CreateOrderRequest{
		Appid:           core.String(strings.TrimSpace(c.cfg.AppID)),
		OutOrderNo:      core.String(strings.TrimSpace(req.OutOrderNo)),
		TransactionId:   core.String(strings.TrimSpace(req.TransactionID)),
		UnfreezeUnsplit: core.Bool(req.UnfreezeUnsplit),
		Receivers: []profitsharing.CreateOrderReceiver{{
			Type:        core.String("MERCHANT_ID"),
			Account:     core.String(strings.TrimSpace(req.ReceiverMchID)),
			Amount:      core.Int64(req.AmountCents),
			Description: core.String(description),
		}},
	})
	if err != nil {
		return nil, err
	}
	return decodeProfitSharingOrder(resp), nil
}

// QueryProfitSharingOrder 根据商户分账单号查询分账结果。
func (c *PayClient) QueryProfitSharingOrder(ctx context.Context, outOrderNo, transactionID string) (*ProfitSharingOrder, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	if strings.TrimSpace(outOrderNo) == "" || strings.TrimSpace(transactionID) == "" {
		return nil, errors.New("缺少分账单号或微信支付订单号")
	}
	resp, _, err := (&profitsharing.OrdersApiService{Client: c.client}).QueryOrder(ctx, profitsharing.QueryOrderRequest{
		OutOrderNo:    core.String(strings.TrimSpace(outOrderNo)),
		TransactionId: core.String(strings.TrimSpace(transactionID)),
	})
	if err != nil {
		return nil, err
	}
	return decodeProfitSharingOrder(resp), nil
}

// CreateProfitSharingReturn 请求分账回退（已分账订单退款前调用，将抽成资金退回商户号）。
func (c *PayClient) CreateProfitSharingReturn(ctx context.Context, req ProfitSharingReturnRequest) (*ProfitSharingReturn, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	orderID := strings.TrimSpace(req.OrderID)
	outOrderNo := strings.TrimSpace(req.OutOrderNo)
	if orderID == "" && outOrderNo == "" {
		return nil, errors.New("缺少微信分账单号或商户分账单号")
	}
	if strings.TrimSpace(req.OutReturnNo) == "" {
		return nil, errors.New("缺少商户回退单号")
	}
	if strings.TrimSpace(req.ReturnMchID) == "" {
		return nil, errors.New("缺少分账回退出资商户号")
	}
	if req.AmountCents <= 0 {
		return nil, errors.New("分账回退金额必须大于 0")
	}
	description := strings.TrimSpace(req.Description)
	if description == "" {
		description = "订单退款分账回退"
	}
	createRequest := profitsharing.CreateReturnOrderRequest{
		OutReturnNo: core.String(strings.TrimSpace(req.OutReturnNo)),
		ReturnMchid: core.String(strings.TrimSpace(req.ReturnMchID)),
		Amount:      core.Int64(req.AmountCents),
		Description: core.String(description),
	}
	if orderID != "" {
		createRequest.OrderId = core.String(orderID)
	} else {
		createRequest.OutOrderNo = core.String(outOrderNo)
	}
	resp, _, err := (&profitsharing.ReturnOrdersApiService{Client: c.client}).CreateReturnOrder(ctx, createRequest)
	if err != nil {
		return nil, err
	}
	return decodeProfitSharingReturn(resp), nil
}

// QueryProfitSharingReturn 根据商户回退单号查询分账回退结果。
func (c *PayClient) QueryProfitSharingReturn(ctx context.Context, outReturnNo, outOrderNo string) (*ProfitSharingReturn, error) {
	if !c.Enabled() {
		return nil, errors.New("微信支付未配置完成")
	}
	if strings.TrimSpace(outReturnNo) == "" || strings.TrimSpace(outOrderNo) == "" {
		return nil, errors.New("缺少回退单号或商户分账单号")
	}
	resp, _, err := (&profitsharing.ReturnOrdersApiService{Client: c.client}).QueryReturnOrder(ctx, profitsharing.QueryReturnOrderRequest{
		OutReturnNo: core.String(strings.TrimSpace(outReturnNo)),
		OutOrderNo:  core.String(strings.TrimSpace(outOrderNo)),
	})
	if err != nil {
		return nil, err
	}
	return decodeProfitSharingReturn(resp), nil
}

// decodeProfitSharingOrder 将 SDK 分账单实体转换为 lib 结果结构。
func decodeProfitSharingOrder(resp *profitsharing.OrdersEntity) *ProfitSharingOrder {
	order := &ProfitSharingOrder{
		OrderID:       stringValue(resp.OrderId),
		OutOrderNo:    stringValue(resp.OutOrderNo),
		TransactionID: stringValue(resp.TransactionId),
	}
	if resp.State != nil {
		order.State = string(*resp.State)
	}
	for _, r := range resp.Receivers {
		item := ProfitSharingReceiverResult{
			Type:        string(*r.Type),
			Account:     stringValue(r.Account),
			Amount:      int64Value(r.Amount),
			Description: stringValue(r.Description),
			DetailID:    stringValue(r.DetailId),
		}
		if r.Result != nil {
			item.Result = string(*r.Result)
		}
		if r.FailReason != nil {
			item.FailReason = string(*r.FailReason)
		}
		order.Receivers = append(order.Receivers, item)
	}
	return order
}

// decodeProfitSharingReturn 将 SDK 分账回退实体转换为 lib 结果结构。
func decodeProfitSharingReturn(resp *profitsharing.ReturnOrdersEntity) *ProfitSharingReturn {
	ret := &ProfitSharingReturn{
		OrderID:     stringValue(resp.OrderId),
		OutOrderNo:  stringValue(resp.OutOrderNo),
		OutReturnNo: stringValue(resp.OutReturnNo),
		Amount:      int64Value(resp.Amount),
	}
	if resp.Result != nil {
		ret.Result = string(*resp.Result)
	}
	if resp.FailReason != nil {
		ret.FailReason = string(*resp.FailReason)
	}
	return ret
}

// int64Value 返回 int64 指针安全值。
func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
