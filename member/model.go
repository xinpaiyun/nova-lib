package member

import "time"

// 会员流水的业务类型。
const (
	TypeRecharge = "recharge" // 充值
	TypeSpend    = "spend"    // 消费抵扣
)

// 会员流水关联的业务单据类型（Ref.Type 取值约定）。
const (
	RefTypeRecharge     = "recharge"      // 充值
	RefTypeWorkOrder    = "work_order"    // 施工工单结算抵扣
	RefTypeServiceOrder = "service_order" // 服务单结算抵扣
	RefTypeAdmin        = "admin"         // 后台人工调整
)

// MemberAccount 定义会员储值账户（参考引擎模型，表 member_accounts）：
// 一个持有者至多一个账户，首次充值自动开户。
type MemberAccount struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键。
	TenantID uint64 `json:"tenantId" gorm:"not null;uniqueIndex:uk_member_accounts_tenant_holder,priority:1"`
	// HolderID 归属持有者（C 端用户、客户、宠物、车辆等，语义由项目决定），租户内唯一。
	HolderID uint64 `json:"holderId" gorm:"not null;uniqueIndex:uk_member_accounts_tenant_holder,priority:2"`
	// Balance 储值余额（分）。
	Balance int64 `json:"balance" gorm:"not null;default:0"`
	// TotalRecharged 累计充值（分）。
	TotalRecharged int64 `json:"totalRecharged" gorm:"not null;default:0"`
	// TotalSpent 累计消费抵扣（分）。
	TotalSpent int64     `json:"totalSpent" gorm:"not null;default:0"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// TableName 返回会员账户表名。
func (MemberAccount) TableName() string { return "member_accounts" }

// MemberTransaction 定义会员账户资金流水（只增不改，余额变动必留痕）。
type MemberTransaction struct {
	ID uint64 `json:"id" gorm:"primaryKey"`
	// TenantID 数据隔离键。
	TenantID uint64 `json:"tenantId" gorm:"not null;index"`
	// HolderID 归属持有者（同 MemberAccount）。
	HolderID uint64 `json:"holderId" gorm:"not null;index"`
	// Type 类型：recharge 充值 / spend 消费抵扣。
	Type string `json:"type" gorm:"not null;size:16"`
	// Amount 变动金额（分），恒为正值。
	Amount int64 `json:"amount" gorm:"not null"`
	// BalanceAfter 变动后余额（分）。
	BalanceAfter int64 `json:"balanceAfter" gorm:"not null"`
	// RefType 关联业务单据类型（Ref.Type 取值约定）。
	RefType string `json:"refType" gorm:"not null;size:16"`
	// RefID 关联业务单据 ID（充值无单据时为 0）。
	RefID uint64 `json:"refId"`
	// RefNo 关联业务单据号（可选）。
	RefNo     string    `json:"refNo,omitempty" gorm:"size:40"`
	Remark    string    `json:"remark,omitempty" gorm:"size:200"`
	CreatedAt time.Time `json:"createdAt"`
}

// TableName 返回会员流水表名。
func (MemberTransaction) TableName() string { return "member_transactions" }

// ModelTypes 返回会员模块需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&MemberAccount{}, &MemberTransaction{}}
}
