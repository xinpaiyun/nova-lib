package coupon

import "time"

// 优惠券模板状态。
const (
	TemplateStatusEnabled  = "enabled"  // 启用
	TemplateStatusDisabled = "disabled" // 停用
)

// 优惠券实例状态。
const (
	CouponUnused  = "unused"  // 未使用
	CouponUsed    = "used"    // 已核销
	CouponExpired = "expired" // 已过期
)

// CouponTemplate 定义优惠券模板（参考引擎模型，表 coupon_templates）：
// 商家配置面额/门槛/有效期，发放时生成券实例。
type CouponTemplate struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键。
	TenantID uint64 `json:"tenantId" gorm:"not null;index"`
	Name     string `json:"name" gorm:"not null;size:80"`
	// Amount 面额（分），核销时按面额抵扣。
	Amount int64 `json:"amount" gorm:"not null;default:0"`
	// MinSpend 使用门槛（分），订单满该金额可用；0=无门槛。
	MinSpend int64 `json:"minSpend" gorm:"not null;default:0"`
	// ValidDays 发放后有效天数。
	ValidDays int `json:"validDays" gorm:"not null;default:30"`
	// TotalCount 发放上限，0=不限。
	TotalCount  int       `json:"totalCount" gorm:"not null;default:0"`
	IssuedCount int       `json:"issuedCount" gorm:"not null;default:0"`
	UsedCount   int       `json:"usedCount" gorm:"not null;default:0"`
	Status      string    `json:"status" gorm:"not null;size:16;default:enabled;index"`
	Remark      string    `json:"remark,omitempty" gorm:"size:200"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// TableName 返回优惠券模板表名。
func (CouponTemplate) TableName() string { return "coupon_templates" }

// CouponModel 定义已发放的优惠券实例的 GORM 模型（表 coupons；契约视图为 Coupon，
// 模型更名 CouponModel 以避免与视图类型重名）。
type CouponModel struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键。
	TenantID   uint64 `json:"tenantId" gorm:"not null;index"`
	TemplateID uint64 `json:"templateId" gorm:"not null;index"`
	// HolderID 归属持有者（C 端用户、客户、宠物、车辆等，语义由项目决定）。
	HolderID uint64 `json:"holderId" gorm:"not null;index"`
	// Name 券名快照。
	Name string `json:"name" gorm:"not null;size:80"`
	// Amount 面额快照（分）。
	Amount int64 `json:"amount" gorm:"not null;default:0"`
	// MinSpend 使用门槛快照（分）。
	MinSpend int64 `json:"minSpend" gorm:"not null;default:0"`
	// Status 状态：unused/used/expired。
	Status string `json:"status" gorm:"not null;size:16;default:unused;index"`
	// ValidUntil 有效期至。
	ValidUntil time.Time `json:"validUntil"`
	// UsedAt 核销时间；未核销为空。
	UsedAt *time.Time `json:"usedAt,omitempty"`
	// RefType/RefID/RefNo 关联业务单据（替代历史 work_order_id 硬编码列）。
	RefType   string    `json:"refType" gorm:"size:16;index:idx_coupons_ref,priority:1"`
	RefID     uint64    `json:"refId" gorm:"not null;default:0;index:idx_coupons_ref,priority:2"`
	RefNo     string    `json:"refNo,omitempty" gorm:"size:40"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TableName 返回优惠券实例表名。
func (CouponModel) TableName() string { return "coupons" }

// ModelTypes 返回优惠券模块需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&CouponTemplate{}, &CouponModel{}}
}
