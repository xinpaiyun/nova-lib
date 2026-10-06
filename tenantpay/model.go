package tenantpay

import (
	"time"
)

// IntegrationConfig 租户集成配置行（与宿主底座 integration_configs 表共用，列名对齐）。
type IntegrationConfig struct {
	ID         uint64    `gorm:"primaryKey" json:"id"`
	TenantID   uint64    `gorm:"index;column:tenant_id" json:"tenantId"`
	Provider   string    `gorm:"size:64" json:"provider"`
	ConfigJSON string    `gorm:"column:config_json;type:text" json:"-"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// TableName 指定与宿主底座一致的表名。
func (IntegrationConfig) TableName() string { return "integration_configs" }

// TenantWechatApp 租户绑定的小程序（与宿主底座 tenant_wechat_apps 表共用，列名对齐）。
type TenantWechatApp struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	TenantID  uint64    `gorm:"index;column:tenant_id" json:"tenantId"`
	AppID     string    `gorm:"size:32;uniqueIndex" json:"appId"`
	AppSecret string    `gorm:"size:128" json:"-"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TableName 指定与宿主底座一致的表名。
func (TenantWechatApp) TableName() string { return "tenant_wechat_apps" }

// TenantWechatConfig 租户级自有小程序 + 商户号绑定配置（参考模型，tenant_id 唯一）。
// 对应 SaaS 平台化模式规范第 3.5 节：绑定即用、解绑纯配置操作，不迁移业务数据。
// AppSecret / MchAPIV3Key 以 AES-256-GCM 密文落库（Encrypt），JSON 序列化恒不出网。
type TenantWechatConfig struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	TenantID    uint64    `gorm:"uniqueIndex;column:tenant_id" json:"tenantId"`
	AppID       string    `gorm:"size:32;index" json:"appId"`
	AppSecret   string    `gorm:"size:256" json:"-"`         // 密文（enc.v1: 前缀）
	MchID       string    `gorm:"size:32" json:"mchId"`      // 商户号，可空
	MchAPIV3Key string    `gorm:"size:256" json:"-"`         // 商户 APIv3 密钥密文（enc.v1: 前缀）
	NotifyURL   string    `gorm:"size:255" json:"notifyUrl"` // 支付回调地址
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// TableName 指定表名。
func (TenantWechatConfig) TableName() string { return "tenant_wechat_configs" }

// WechatConfigInput 租户微信配置保存入参（敏感字段为明文，落库前由 Store 加密）。
type WechatConfigInput struct {
	AppID       string
	AppSecret   string // 留空保留旧值
	MchID       string
	MchAPIV3Key string // 留空保留旧值
	NotifyURL   string
	Enabled     bool
}

// WechatConfigSecrets 租户微信配置的明文机密字段（解密后仅在服务端内存使用）。
type WechatConfigSecrets struct {
	AppSecret   string
	MchAPIV3Key string
}

// Decrypt 解出配置中的明文机密字段（AppSecret / MchAPIV3Key）。
func (c TenantWechatConfig) Decrypt(masterKey string) (WechatConfigSecrets, error) {
	secret, err := Decrypt(masterKey, c.AppSecret)
	if err != nil {
		return WechatConfigSecrets{}, err
	}
	v3Key, err := Decrypt(masterKey, c.MchAPIV3Key)
	if err != nil {
		return WechatConfigSecrets{}, err
	}
	return WechatConfigSecrets{AppSecret: secret, MchAPIV3Key: v3Key}, nil
}

// TenantCommission 平台对租户的抽成配置（tenant_id 唯一）。
// 抽成金额 = max(实付金额 × RateBP / 10000, MinCommissionCent)，且不超过实付金额。
type TenantCommission struct {
	ID                uint64    `gorm:"primaryKey" json:"id"`
	TenantID          uint64    `gorm:"uniqueIndex;column:tenant_id" json:"tenantId"`
	RateBP            int64     `gorm:"column:rate_bp" json:"rateBp"`                        // 抽成比例（基点，万分比：500 = 5%）
	MinCommissionCent int64     `gorm:"column:min_commission_cent" json:"minCommissionCent"` // 单笔保底金额（分）
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// TableName 指定表名。
func (TenantCommission) TableName() string { return "tenant_commissions" }

// 分账单状态。
const (
	PSStatusPending    = "pending"    // 待分账（订单已完成，待发起）
	PSStatusProcessing = "processing" // 已发起，微信处理中
	PSStatusSucceeded  = "succeeded"  // 分账成功
	PSStatusFailed     = "failed"     // 分账失败（可重试）
	PSStatusRefunded   = "refunded"   // 已回退（订单退款前抽成已退回商户号）
)

// ProfitSharingOrder 平台抽成分账记录（一笔订单至多一条，OutOrderNo 幂等）。
type ProfitSharingOrder struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	TenantID       uint64    `gorm:"index;column:tenant_id" json:"tenantId"`
	OrderNo        string    `gorm:"size:64;index" json:"orderNo"`                 // 宿主业务订单号
	OutOrderNo     string    `gorm:"size:64;uniqueIndex" json:"outOrderNo"`        // 商户分账单号（幂等键）
	TransactionID  string    `gorm:"size:64" json:"transactionId"`                 // 微信支付订单号
	WxOrderID      string    `gorm:"size:64" json:"wxOrderId"`                     // 微信分账单号
	AmountCent     int64     `gorm:"column:amount_cent" json:"amountCent"`         // 订单实付金额（分）
	CommissionCent int64     `gorm:"column:commission_cent" json:"commissionCent"` // 抽成金额（分）
	RateBP         int64     `gorm:"column:rate_bp" json:"rateBp"`                 // 发起时比例快照（基点）
	Status         string    `gorm:"size:16;index" json:"status"`
	FailReason     string    `gorm:"size:255" json:"failReason"`
	ReturnNo       string    `gorm:"size:64" json:"returnNo"` // 分账回退单号
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// TableName 指定表名。
func (ProfitSharingOrder) TableName() string { return "profit_sharing_orders" }
