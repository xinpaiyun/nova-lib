package member

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
)

// C 端会员关系状态：1 正常，0 停用，2 已并入其他档案。
const (
	CustomerStatusDisabled int8 = 0
	CustomerStatusEnabled  int8 = 1
	CustomerStatusMerged   int8 = 2
)

// 会员来源。
const (
	CustomerSourceScanQR       = "scan_qr"       // 扫码加入
	CustomerSourceOnline       = "online"        // 线上下单/注册
	CustomerSourceReferral     = "referral"      // 推荐
	CustomerSourceStaffCreated = "staff_created" // 后台建档
)

// 会员关系相关错误。
var (
	// ErrCustomerUserRequired 会员必须关联已注册用户，user_id=0 一律拒绝。
	ErrCustomerUserRequired = errors.New("会员必须关联已注册用户")
	// ErrCustomerInputInvalid 会员入参不完整。
	ErrCustomerInputInvalid = errors.New("会员信息不完整")
	// ErrCustomerNotFound 会员不存在（已并入的档案也不参与普通查询）。
	ErrCustomerNotFound = errors.New("会员不存在")
	// ErrCustomerMerged 该账号的会员档案已并入其他档案。
	ErrCustomerMerged = errors.New("该会员已并入其他档案")
	// ErrCustomerMergeInvalid 合并参数不合法。
	ErrCustomerMergeInvalid = errors.New("合并参数不合法")
)

// CustomerMember 是 C 端会员关系（租户内的客户档案），表 customer_members。
// 唯一口径只有 (tenant_id, user_id)：手机号仅做快照与检索，不作为唯一键。
type CustomerMember struct {
	ID uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	// TenantID 数据隔离键。
	TenantID uint64 `json:"tenantId" gorm:"column:tenant_id;not null;uniqueIndex:uk_customer_members_tenant_user,priority:1"`
	// OrgID 归属门店，0=未归属。
	OrgID uint64 `json:"orgId" gorm:"column:org_id;not null;default:0;index"`
	// UserID 关联 identity.users.id，必须 > 0。
	UserID uint64 `json:"userId" gorm:"column:user_id;not null;uniqueIndex:uk_customer_members_tenant_user,priority:2"`
	// Name 客户姓名（可空，展示优先取 Nickname）。
	Name     string `json:"name" gorm:"column:name;size:80"`
	Nickname string `json:"nickname" gorm:"column:nickname;size:80"`
	// Phone 租户内手机号快照；换号不回写唯一键。
	Phone     string `json:"phone" gorm:"column:phone;size:20;index"`
	Gender    string `json:"gender" gorm:"column:gender;size:10"`
	Birthday  string `json:"birthday" gorm:"column:birthday;size:20"`
	LevelCode string `json:"levelCode" gorm:"column:level_code;size:40;index"`
	Points    int64  `json:"points" gorm:"column:points;not null;default:0"`
	// Source 建档来源，取值见 CustomerSource* 常量。
	Source string `json:"source" gorm:"column:source;size:20;not null;default:scan_qr"`
	// Tags JSON 数组字符串，规则由项目定义。
	Tags   string `json:"tags" gorm:"column:tags;size:500"`
	Remark string `json:"remark" gorm:"column:remark;size:500"`
	// LegacyID 被并入本档案的旧会员 ID（首次合并写入），供存量外键追溯。
	LegacyID uint64 `json:"legacyId" gorm:"column:legacy_id;not null;default:0"`
	// MergedIntoID 本档案被并入的目标会员 ID，仅 Status=merged 时有值。
	MergedIntoID uint64    `json:"mergedIntoId" gorm:"column:merged_into_id;not null;default:0;index"`
	Status       int8      `json:"status" gorm:"column:status;not null;default:1;index"`
	CreatedAt    time.Time `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt    time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

// TableName 返回会员关系表名。
func (CustomerMember) TableName() string { return "customer_members" }

// CustomerModelTypes 返回 C 端会员关系需要迁移的数据模型。
// 与储值表的 ModelTypes 分开，已上线项目可只迁移需要的新表。
func CustomerModelTypes() []any {
	return []any{&CustomerMember{}}
}

// CustomerInput 是会员建档入参。UserID 必须来自登录会话，不能由前端传入。
type CustomerInput struct {
	TenantID  uint64
	OrgID     uint64
	UserID    uint64
	Name      string
	Nickname  string
	Phone     string
	Gender    string
	Birthday  string
	LevelCode string
	Tags      string
	Remark    string
	// Source 为空时，扫码入会默认 scan_qr，后台建档默认 staff_created。
	Source string
}

// CustomerFilter 会员列表查询条件。
type CustomerFilter struct {
	OrgID     uint64
	Keyword   string // 姓名/昵称/手机号模糊匹配
	LevelCode string
	Status    *int8 // nil 表示不限（但仍排除已并入档案）
	Page      int
	PageSize  int
}

// MergeResult 会员合并结果。
type MergeResult struct {
	// Into 合并后的目标会员档案。
	Into *CustomerMember `json:"into"`
	// Merged 本次实际被并入的会员 ID（跳过已并入同一目标的行）。
	Merged []uint64 `json:"merged"`
	// Balance 合并后目标会员的储值余额（分）。
	Balance int64 `json:"balance"`
}

// MergeHook 可选的业务外键改写钩子：项目在同一事务内把自有业务表
// （预约、订单、健康档案等）从 fromID 改写到 intoID。nil 表示跳过。
type MergeHook func(tx *gorm.DB, tenantID, fromID, intoID uint64) error

// CustomerSource 是 C 端会员的只读数据源。
type CustomerSource interface {
	// CustomerByUser 按 (tenant_id, user_id) 查会员；已并入档案不返回。
	CustomerByUser(ctx context.Context, tenantID, userID uint64) (*CustomerMember, error)
	// CustomerByID 按主键查会员；已并入档案不返回。
	CustomerByID(ctx context.Context, tenantID, id uint64) (*CustomerMember, error)
	// Customers 分页查询会员。
	Customers(ctx context.Context, tenantID uint64, filter CustomerFilter) ([]CustomerMember, int64, error)
}

// CustomerWriter 是 C 端会员的写入接口。
type CustomerWriter interface {
	// CreateCustomer 建档：userID 必须 > 0；同租户已有会员时幂等返回已有记录。
	CreateCustomer(ctx context.Context, in CustomerInput) (*CustomerMember, error)
	// UpdateCustomer 更新会员资料（不允许改绑 user_id）。
	UpdateCustomer(ctx context.Context, tenantID, id uint64, in CustomerInput) error
	// MergeCustomers 合并会员：关系、积分与储值账户一并并入，原 ID 记入 legacy_id。
	MergeCustomers(ctx context.Context, tenantID uint64, fromIDs []uint64, intoID uint64, hook MergeHook) (*MergeResult, error)
}
