// Package plan 提供面向商家（租户）的 SaaS 套餐引擎：套餐目录、结构化权益、
// 购买订单与订阅生效（续费顺延）、权益校验与月度用量流水。
// 与 member/coupon 同为"契约 + 参考引擎"模式；套餐目录内容（内置档位、权益编码）
// 由项目侧定义并通过 SeedPlans 同步，引擎不感知具体业务编码。
package plan

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// 计费周期。
const (
	CycleMonthly = "monthly" // 月付
	CycleYearly  = "yearly"  // 年付
)

// 权益值类型。
const (
	ValueTypeCount   = "count"   // 计数额度（LimitValue 为上限）
	ValueTypeBoolean = "boolean" // 布尔开关（Enabled 决定）
)

// 购买单状态。
const (
	PurchasePending = 0 // 待支付
	PurchasePaid    = 1 // 已支付（已生效）
	PurchaseClosed  = 2 // 已关闭（超时/取消/支付失败）
)

// 套餐与订单错误。
var (
	ErrPlanNotFound      = errors.New("套餐不存在")
	ErrPlanDisabled      = errors.New("套餐已停用，暂不可开通")
	ErrPlanCodeExists    = errors.New("套餐编码已存在")
	ErrInvalidPlan       = errors.New("套餐配置不完整")
	ErrFreePlan          = errors.New("免费套餐无需支付开通")
	ErrOrderNotFound     = errors.New("套餐订单不存在")
	ErrOrderStateInvalid = errors.New("套餐订单状态不允许该操作")
	ErrInvalidCycle      = errors.New("不支持的计费周期")
	ErrInvalidOrder      = errors.New("套餐订单参数不完整")
	ErrSubscriptionMiss  = errors.New("主体尚未开通套餐")
	ErrEntitlementMiss   = errors.New("套餐未配置该权益")
)

// Entitlement 定义套餐单项结构化权益（布尔开关或计数额度）。
type Entitlement struct {
	// Code 权益稳定编码（如 shop.max_active），语义由项目定义。
	Code string `json:"code"`
	// ValueType 值类型：count 计数额度 / boolean 布尔开关。
	ValueType string `json:"valueType"`
	// LimitValue 计数额度上限（boolean 类型忽略）。
	LimitValue int64 `json:"limitValue"`
	// Enabled 布尔开关取值（count 类型恒为 true）。
	Enabled bool `json:"enabled"`
}

// Plan 定义套餐视图。
type Plan struct {
	ID                uint64 `json:"id"`
	Code              string `json:"code"`
	Name              string `json:"name"`
	MonthlyPriceCents int64  `json:"monthlyPriceCents"`
	YearlyPriceCents  int64  `json:"yearlyPriceCents"`
	// Capabilities 套餐包含的稳定能力编码（如 business.revenue），供项目做能力基线授权。
	Capabilities []string `json:"capabilities"`
	IsDefault    bool     `json:"isDefault"`
	// Status 状态：1 启用 / 0 停用。
	Status int    `json:"status"`
	Remark string `json:"remark"`
	// Meta 项目特有扩展属性（JSON 字符串，引擎透传不解释，如工作区模式）。
	Meta         string        `json:"meta,omitempty"`
	Entitlements []Entitlement `json:"entitlements"`
}

// PlanInput 定义创建/更新套餐入参（校验规则同引擎实现）。
type PlanInput struct {
	Code              string        `json:"code"`
	Name              string        `json:"name"`
	MonthlyPriceCents int64         `json:"monthlyPriceCents"`
	YearlyPriceCents  int64         `json:"yearlyPriceCents"`
	Capabilities      []string      `json:"capabilities"`
	IsDefault         bool          `json:"isDefault"`
	Status            int           `json:"status"`
	Remark            string        `json:"remark"`
	Meta              string        `json:"meta"`
	Entitlements      []Entitlement `json:"entitlements"`
}

// Purchase 定义套餐购买订单视图。
type Purchase struct {
	ID           uint64 `json:"id"`
	TenantID     uint64 `json:"tenantId"`
	PlanID       uint64 `json:"planId"`
	PlanName     string `json:"planName"`
	BillingCycle string `json:"billingCycle"`
	AmountCents  int64  `json:"amountCents"`
	// OutTradeNo 商户订单号（项目侧生成，全局唯一）；TransactionID 支付渠道单号。
	OutTradeNo    string `json:"outTradeNo"`
	TransactionID string `json:"transactionId"`
	// PayDeadline 支付有效期，超时未支付项目侧应关单。
	PayDeadline time.Time `json:"payDeadline"`
	// StartedAt/ExpireAt 支付成功后的订阅周期（支付前为零值）。
	StartedAt time.Time  `json:"startedAt"`
	ExpireAt  time.Time  `json:"expireAt"`
	PayTime   *time.Time `json:"payTime"`
	// Status 状态：0 待支付 / 1 已支付 / 2 已关闭；Remark 关单原因或回调摘要。
	Status     int       `json:"status"`
	Remark     string    `json:"remark,omitempty"`
	Extra      string    `json:"extra,omitempty"`
	OperatorID uint64    `json:"operatorId"`
	CreatedAt  time.Time `json:"createdAt"`
}

// PurchaseInput 定义创建购买订单入参。
type PurchaseInput struct {
	TenantID   uint64 `json:"tenantId"`
	PlanID     uint64 `json:"planId"`
	OperatorID uint64 `json:"operatorId"`
	// BillingCycle 计费周期：monthly/yearly。
	BillingCycle string `json:"billingCycle"`
	// OutTradeNo 商户订单号（必填，全局唯一）。
	OutTradeNo string `json:"outTradeNo"`
	// PayDeadline 支付有效期（可空 = 2 小时）。
	PayDeadline time.Time `json:"payDeadline"`
}

// Subscription 定义当前生效订阅快照；ExpireAt 零值表示永久有效（免费版/管理端开通）。
type Subscription struct {
	TenantID  uint64    `json:"tenantId"`
	PlanID    uint64    `json:"planId"`
	PlanCode  string    `json:"planCode"`
	PlanName  string    `json:"planName"`
	StartedAt time.Time `json:"startedAt"`
	ExpireAt  time.Time `json:"expireAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// PurchaseQuery 定义购买订单查询参数；Status 为空表示全部状态。
type PurchaseQuery struct {
	TenantID uint64 `json:"tenantId"`
	Status   *int   `json:"status"`
	Page     int    `json:"page"`
	PageSize int    `json:"pageSize"`
}

// PurchasePage 定义购买订单分页结果。
type PurchasePage struct {
	Items []Purchase `json:"items"`
	Total int64      `json:"total"`
	Page  int        `json:"page"`
	Size  int        `json:"size"`
}

// NotEnabledError 表示当前套餐未开通指定布尔权益。
type NotEnabledError struct {
	Code  string
	Label string
}

// Error 返回可直接展示给经营者的提示。
func (e *NotEnabledError) Error() string {
	if e.Label != "" {
		return "当前套餐未开通" + e.Label + "，请升级套餐后使用"
	}
	return "当前套餐未开通该能力，请升级套餐后使用"
}

// QuotaExceededError 表示套餐计数额度已达上限。
type QuotaExceededError struct {
	Code  string
	Label string
	Limit int64
	Used  int64
}

// Error 返回可直接展示给经营者的额度提示。
func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("当前套餐%s上限为 %d，现已使用 %d，请升级套餐后继续新增", e.Label, e.Limit, e.Used)
}

// IsNotEnabled 判断错误是否为套餐布尔权益未开通。
func IsNotEnabled(err error) bool {
	var target *NotEnabledError
	return errors.As(err, &target)
}

// IsQuotaExceeded 判断错误是否为套餐计数额度不足。
func IsQuotaExceeded(err error) bool {
	var target *QuotaExceededError
	return errors.As(err, &target)
}

// Engine 套餐引擎契约。
type Engine interface {
	// ── 套餐目录（平台侧管理）──
	// Plans 返回套餐目录；includeDisabled=false 仅返回启用套餐。
	Plans(ctx context.Context, includeDisabled bool) ([]Plan, error)
	// Plan 按 ID 查询套餐（含权益）。
	Plan(ctx context.Context, id uint64) (Plan, error)
	// CreatePlan 创建套餐；编码唯一。
	CreatePlan(ctx context.Context, in PlanInput) (Plan, error)
	// UpdatePlan 更新套餐并整体替换权益配置。
	UpdatePlan(ctx context.Context, id uint64, in PlanInput) error
	// SeedPlans 按编码幂等同步内置套餐目录（存在则更新，缺失则创建），
	// 用于项目启动时同步内置档位；权益编码由项目定义。
	SeedPlans(ctx context.Context, defs []PlanInput) error

	// ── 订阅 ──
	// Assign 管理端直接开通套餐（永久有效，免费版/人工开通）。
	Assign(ctx context.Context, tenantID, planID, operatorID uint64) (Subscription, error)
	// Subscription 查询当前生效订阅；未开通返回 ErrSubscriptionMiss。
	Subscription(ctx context.Context, tenantID uint64) (Subscription, error)

	// ── 购买订单 ──
	// CreatePurchase 创建支付订单；套餐须启用且金额大于 0（免费版走 Assign）。
	CreatePurchase(ctx context.Context, in PurchaseInput) (Purchase, error)
	// Purchase 按商户订单号查询。
	Purchase(ctx context.Context, outTradeNo string) (Purchase, error)
	// Purchases 分页查询购买订单。
	Purchases(ctx context.Context, q PurchaseQuery) (PurchasePage, error)
	// MarkPurchasePaid 支付成功生效：订单置已支付（幂等），并在同一事务内生效订阅——
	// 同套餐未到期则从当前到期时间顺延，否则从支付时间起算新周期。
	MarkPurchasePaid(ctx context.Context, outTradeNo, transactionID string, payTime time.Time) (Purchase, error)
	// ClosePurchase 关闭待支付订单（超时/取消/支付失败）；已支付订单不可关闭。
	ClosePurchase(ctx context.Context, outTradeNo, reason string) error

	// ── 权益校验 ──
	// EnsureEnabled 校验布尔权益已开通；未开通返回 *NotEnabledError。
	EnsureEnabled(ctx context.Context, tenantID uint64, code, label string) error
	// EnforceCount 校验计数额度；used 为业务侧统计的当前用量，超限返回 *QuotaExceededError。
	EnforceCount(ctx context.Context, tenantID uint64, code string, used int64, label string) error
	// EnforceCountTx 在业务事务内锁定订阅行后校验计数额度（防并发超限），
	// 必须在业务侧事务内调用，used 为同事务内统计的用量。
	EnforceCountTx(tx *gorm.DB, tenantID uint64, code string, used int64, label string) error
	// ConsumeMonthly 原子校验并记录按月计数权益消耗（sourceKey 非空时按来源幂等）。
	ConsumeMonthly(ctx context.Context, tenantID uint64, code string, operatorID uint64, sourceType, sourceKey string) error
}

// PriceFor 返回套餐在指定计费周期下的价格（分）。
func PriceFor(p Plan, cycle string) int64 {
	if cycle == CycleYearly {
		return p.YearlyPriceCents
	}
	return p.MonthlyPriceCents
}
