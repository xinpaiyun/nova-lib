// Package staff 提供全公司统一的「员工用工关系 + 业务开关 + 数据范围」模型。
//
// 定位：identity.users 是全局自然人，也是唯一的登录凭据来源；本包只在其上
// 建立租户内的用工关系（staff_members）与业务开关（staff_capabilities）。
//
// 三条硬约定：
//  1. lib 永不创建 users；调用方必须先确认用户已注册（见 ErrUserNotRegistered）。
//  2. role_code 只表达管理权限（owner/admin/manager/staff/viewer），
//     服务者、收银、老师、技师等业务身份一律通过 CapabilitySpec 注册为开关。
//  3. 数据范围只有 all / org / self 三档，由 CapabilitySpec.ScopeEffect 决定
//     某个开关是否会影响「本人」范围。
package staff

import (
	"time"
)

// 员工状态：1 正常，0 禁用。
const (
	StatusDisabled int8 = 0
	StatusEnabled  int8 = 1
)

// 内置管理权限角色码。业务身份不要扩展到这里，改用 CapabilitySpec。
const (
	RoleOwner   = "owner"
	RoleAdmin   = "admin"
	RoleManager = "manager"
	RoleStaff   = "staff"
	RoleViewer  = "viewer"
)

// StaffMember 表示租户内的员工用工关系，物理表名 `staff_members`。
// 同一自然人在同一租户只有一条记录；跨租户可有独立的多条记录。
type StaffMember struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID   uint64     `gorm:"column:tenant_id;not null;index;uniqueIndex:uk_staff_members_tenant_user,priority:1" json:"tenantId"`
	OrgID      uint64     `gorm:"column:org_id;not null;default:0;index" json:"orgId"`
	UserID     uint64     `gorm:"column:user_id;not null;index;uniqueIndex:uk_staff_members_tenant_user,priority:2" json:"userId"`
	Name       string     `gorm:"column:name;size:40;not null" json:"name"`
	EmployeeNo string     `gorm:"column:employee_no;size:40" json:"employeeNo"`
	Position   string     `gorm:"column:position;size:40" json:"position"` // 岗位自由文本，仅展示，不参与鉴权
	RoleCode   string     `gorm:"column:role_code;size:32;not null;default:staff;index" json:"roleCode"`
	Status     int8       `gorm:"column:status;not null;default:1;index" json:"status"`
	CreatedAt  time.Time  `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt  time.Time  `gorm:"column:updated_at" json:"updatedAt"`
	DeletedAt  *time.Time `gorm:"column:deleted_at;index" json:"-"`
}

// TableName 返回员工用工关系表名。
func (StaffMember) TableName() string { return "staff_members" }

// StaffCapability 表示员工的业务开关，物理表名 `staff_capabilities`。
// 表结构不承载任何业务语义：开关叫什么、是否必须关联档案、是否影响数据范围，
// 全部由 CapabilitySpec 注册表描述，因此新增开关不需要改表。
//
// 唯一键为 (member_id, code, subject_id)：无档案的单实例开关固定 subject_id=0，
// 有档案的单实例开关由 Service 层保证同码只留一条；多实例开关允许同码多个档案。
type StaffCapability struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	TenantID  uint64    `gorm:"column:tenant_id;not null;index" json:"tenantId"`
	MemberID  uint64    `gorm:"column:member_id;not null;index;uniqueIndex:uk_staff_caps_member_code_subject,priority:1" json:"memberId"`
	Code      string    `gorm:"column:code;size:40;not null;index;uniqueIndex:uk_staff_caps_member_code_subject,priority:2" json:"code"`
	SubjectID uint64    `gorm:"column:subject_id;not null;default:0;index;uniqueIndex:uk_staff_caps_member_code_subject,priority:3" json:"subjectId"`
	Status    int8      `gorm:"column:status;not null;default:1;index" json:"status"`
	CreatedBy uint64    `gorm:"column:created_by;not null;default:0" json:"createdBy"`
	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 返回员工业务开关表名。
func (StaffCapability) TableName() string { return "staff_capabilities" }

// ModelTypes 返回员工域需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&StaffMember{}, &StaffCapability{}}
}
