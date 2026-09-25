package plan

import "time"

// ModelTypes 返回套餐模块需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{
		&PlanModel{},
		&PlanEntitlement{},
		&PlanPurchase{},
		&PlanSubscription{},
		&PlanUsageEvent{},
	}
}

// PlanModel 定义套餐定义模型（表 plans；编码全局唯一，套餐为平台级目录）。
type PlanModel struct {
	ID                uint64 `json:"id" gorm:"primaryKey"`
	Code              string `json:"code" gorm:"size:32;uniqueIndex:uniq_plan_code"`
	Name              string `json:"name" gorm:"size:64;not null"`
	MonthlyPriceCents int64  `json:"monthlyPriceCents" gorm:"not null;default:0"`
	YearlyPriceCents  int64  `json:"yearlyPriceCents" gorm:"not null;default:0"`
	// CapabilitiesJSON 套餐包含的稳定能力编码（JSON 字符串数组）。
	CapabilitiesJSON string `json:"capabilitiesJson" gorm:"type:text"`
	IsDefault        bool   `json:"isDefault" gorm:"not null;default:false;index"`
	Status           int    `json:"status" gorm:"not null;default:1;index"`
	Remark           string `json:"remark" gorm:"size:255"`
	// Meta 项目特有扩展属性（JSON 字符串，引擎透传不解释）。
	Meta      string    `json:"meta" gorm:"type:text"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TableName 返回套餐定义表名。
func (PlanModel) TableName() string { return "plans" }

// PlanEntitlement 定义套餐结构化权益（布尔开关或计数额度）。
type PlanEntitlement struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// PlanID 归属套餐；Code 权益稳定编码，套餐内唯一。
	PlanID uint64 `json:"planId" gorm:"not null;uniqueIndex:uniq_plan_entitlement,priority:1;index"`
	Code   string `json:"code" gorm:"size:64;not null;uniqueIndex:uniq_plan_entitlement,priority:2"`
	// ValueType 值类型：count/boolean；LimitValue 计数上限；Enabled 布尔取值。
	ValueType  string    `json:"valueType" gorm:"not null;size:16;default:count"`
	LimitValue int64     `json:"limitValue" gorm:"not null;default:0"`
	Enabled    bool      `json:"enabled" gorm:"not null;default:false"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// TableName 返回套餐权益表名。
func (PlanEntitlement) TableName() string { return "plan_entitlements" }

// PlanPurchase 定义套餐购买订单（支付成功后生效订阅）。
type PlanPurchase struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 购买主体；PlanID/PlanName 下单时套餐快照。
	TenantID uint64 `json:"tenantId" gorm:"not null;index:idx_plan_purchases_tenant_status,priority:1"`
	PlanID   uint64 `json:"planId" gorm:"not null;index"`
	PlanName string `json:"planName" gorm:"not null;size:64"`
	// BillingCycle 计费周期（monthly/yearly）；AmountCents 应付金额（分）。
	BillingCycle string `json:"billingCycle" gorm:"not null;size:16;index"`
	AmountCents  int64  `json:"amountCents" gorm:"not null;default:0"`
	// OutTradeNo 商户订单号（全局唯一）；TransactionID 支付渠道单号。
	OutTradeNo    string `json:"outTradeNo" gorm:"not null;size:64;uniqueIndex:uniq_plan_purchases_out_trade_no"`
	TransactionID string `json:"transactionId" gorm:"size:64;index"`
	// PayDeadline 支付有效期；StartedAt/ExpireAt 支付成功后的订阅周期。
	PayDeadline time.Time  `json:"payDeadline"`
	StartedAt   time.Time  `json:"startedAt"`
	ExpireAt    time.Time  `json:"expireAt"`
	PayTime     *time.Time `json:"payTime"`
	// Status 状态：0 待支付 / 1 已支付 / 2 已关闭；Remark 关单原因或回调摘要。
	Status int    `json:"status" gorm:"not null;default:0;index:idx_plan_purchases_tenant_status,priority:2"`
	Remark string `json:"remark" gorm:"size:255"`
	// Extra 项目扩展信息（JSON 字符串，如支付渠道身份 prepay_id/pay_app_id/payer_open_id、
	// 回调原文 notify_body），引擎透传不解释，更新订单状态时不覆盖本列。
	Extra      string    `json:"extra" gorm:"type:text"`
	OperatorID uint64    `json:"operatorId" gorm:"index"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// TableName 返回套餐购买订单表名。
func (PlanPurchase) TableName() string { return "plan_purchases" }

// PlanSubscription 定义主体当前生效订阅快照（每主体一行，购买生效/续费时更新）。
type PlanSubscription struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 订阅主体（唯一一行）；PlanID/PlanCode/PlanName 当前套餐快照。
	TenantID uint64 `json:"tenantId" gorm:"not null;uniqueIndex:uniq_plan_subscriptions_tenant"`
	PlanID   uint64 `json:"planId" gorm:"not null;index"`
	PlanCode string `json:"planCode" gorm:"not null;size:32"`
	PlanName string `json:"planName" gorm:"not null;size:64"`
	// StartedAt 当前周期起始；ExpireAt 到期时间（零值 = 永久有效）。
	StartedAt time.Time `json:"startedAt"`
	ExpireAt  time.Time `json:"expireAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TableName 返回主体订阅表名。
func (PlanSubscription) TableName() string { return "plan_subscriptions" }

// PlanUsageEvent 定义按月计数权益的真实消耗流水（只增不改）。
type PlanUsageEvent struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 订阅主体；EntitlementCode 权益编码；两者与时间构成用量统计范围。
	TenantID        uint64 `json:"tenantId" gorm:"not null;index:idx_plan_usage_scope,priority:1"`
	EntitlementCode string `json:"entitlementCode" gorm:"not null;size:64;index:idx_plan_usage_scope,priority:2"`
	OperatorID      uint64 `json:"operatorId" gorm:"index"`
	// UsageValue 消耗数量；SourceType/SourceKey 来源业务类型与业务键（幂等去重）。
	UsageValue int64     `json:"usageValue" gorm:"not null;default:1"`
	SourceType string    `json:"sourceType" gorm:"not null;size:64;index"`
	SourceKey  string    `json:"sourceKey" gorm:"size:128;index"`
	CreatedAt  time.Time `json:"createdAt" gorm:"index:idx_plan_usage_scope,priority:3"`
}

// TableName 返回套餐用量流水表名。
func (PlanUsageEvent) TableName() string { return "plan_usage_events" }
