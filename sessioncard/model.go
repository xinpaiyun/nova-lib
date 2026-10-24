package sessioncard

import "time"

// 次卡模板状态。
const (
	TemplateStatusEnabled  = "enabled"  // 启用
	TemplateStatusDisabled = "disabled" // 停用
)

// 次卡实例状态。
const (
	CardActive    = "active"    // 使用中
	CardExhausted = "exhausted" // 次数用尽
	CardExpired   = "expired"   // 已过期
)

// SessionCardTemplate 定义次卡模板（参考引擎模型，表 session_card_templates）：
// 商家配置总次数/有效期/绑定门店，发放时生成卡实例。
type SessionCardTemplate struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键。
	TenantID uint64 `json:"tenantId" gorm:"not null;index"`
	// ShopID 绑定门店（org-shop-member 契约绑店可选）：0=品牌通用，>0 仅该店可消费。
	ShopID uint64 `json:"shopId" gorm:"not null;default:0;index"`
	Name   string `json:"name" gorm:"not null;size:80"`
	// TotalTimes 单卡总次数。
	TotalTimes int `json:"totalTimes" gorm:"not null;default:1"`
	// ValidDays 发放后有效天数。
	ValidDays int `json:"validDays" gorm:"not null;default:30"`
	// TotalCount 发放上限，0=不限。
	TotalCount  int       `json:"totalCount" gorm:"not null;default:0"`
	IssuedCount int       `json:"issuedCount" gorm:"not null;default:0"`
	// UsedTimes 已核销次数统计（全部卡实例累计）。
	UsedTimes int       `json:"usedTimes" gorm:"not null;default:0"`
	Status    string    `json:"status" gorm:"not null;size:16;default:enabled;index"`
	Remark    string    `json:"remark,omitempty" gorm:"size:200"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TableName 返回次卡模板表名。
func (SessionCardTemplate) TableName() string { return "session_card_templates" }

// SessionCardModel 定义已发放的次卡实例的 GORM 模型（表 session_cards；契约视图为 Card，
// 模型更名 SessionCardModel 以避免与视图类型重名）。
type SessionCardModel struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键。
	TenantID   uint64 `json:"tenantId" gorm:"not null;index"`
	TemplateID uint64 `json:"templateId" gorm:"not null;index"`
	// HolderID 归属持有者（C 端用户、客户、宠物、车辆等，语义由项目决定）。
	HolderID uint64 `json:"holderId" gorm:"not null;index"`
	// ShopID 绑定门店快照（0=品牌通用）。
	ShopID uint64 `json:"shopId" gorm:"not null;default:0;index"`
	// Name 卡名快照。
	Name string `json:"name" gorm:"not null;size:80"`
	// TotalTimes 总次数快照。
	TotalTimes int `json:"totalTimes" gorm:"not null;default:1"`
	// RemainingTimes 剩余次数。
	RemainingTimes int `json:"remainingTimes" gorm:"not null;default:0"`
	// UsedTimes 已核销次数。
	UsedTimes int `json:"usedTimes" gorm:"not null;default:0"`
	// Status 状态：active/exhausted/expired。
	Status string `json:"status" gorm:"not null;size:16;default:active;index"`
	// ValidUntil 有效期至。
	ValidUntil time.Time `json:"validUntil"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// TableName 返回次卡实例表名。
func (SessionCardModel) TableName() string { return "session_cards" }

// SessionCardConsumeLog 定义次卡消费流水（表 session_card_consume_logs；
// org-shop-member 契约：核销记录必带 shop_id，品牌级上下文为 0）。
type SessionCardConsumeLog struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键。
	TenantID uint64 `json:"tenantId" gorm:"not null;index"`
	// CardID 消费的卡实例。
	CardID uint64 `json:"cardId" gorm:"not null;index"`
	// HolderID 归属持有者。
	HolderID uint64 `json:"holderId" gorm:"not null;index"`
	// ShopID 消费门店（品牌级上下文为 0）。
	ShopID uint64 `json:"shopId" gorm:"not null;default:0;index"`
	// RefType/RefID/RefNo 关联业务单据。
	RefType   string    `json:"refType" gorm:"size:16;index:idx_session_card_logs_ref,priority:1"`
	RefID     uint64    `json:"refId" gorm:"not null;default:0;index:idx_session_card_logs_ref,priority:2"`
	RefNo     string    `json:"refNo,omitempty" gorm:"size:40"`
	CreatedAt time.Time `json:"createdAt"`
}

// TableName 返回次卡消费流水表名。
func (SessionCardConsumeLog) TableName() string { return "session_card_consume_logs" }

// ModelTypes 返回次卡模块需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&SessionCardTemplate{}, &SessionCardModel{}, &SessionCardConsumeLog{}}
}
