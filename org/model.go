// Package org 提供全公司统一的组织（团队/机构/商家/品牌/子部门）数据模型。
//
// 统一各项目"归属组织"概念：baoxian 团队(Team)、app 租户(Tenant)、xingxueji
// 机构/店铺(Shop)、chehuixing 商家(Tenant/Shop) 全部归并为一张 orgs 树形表 +
// org_members 成员-角色绑定。org 是树形组织：根级 org 即各业务的数据归属边界
// （等价于既有 tenant_id / team_id），其子级通过 parent_id 表达门店/小组/分店。
//
// org 只提供"组织树 + 成员归属"数据面，不内嵌业务判定：组织内 RBAC 与数据作用域
// （baoxian 团队范围、xingxueji 店铺 scope、app 多租户对象鉴权）由项目经
// github.com/xinpaiyun/nova-lib/rbac 的 Checker / RoleResolver 回调注入。
package org

import "time"

// 状态常量：1 正常，0 停用。
const (
	StatusDisabled int8 = 0
	StatusEnabled  int8 = 1
)

// 组织类型常量（宽松约定，项目可按自身扩展 Type 值）。
const (
	TypeRoot  = "org"   // 根级组织（团队/机构/商家/租户）
	TypeShop  = "shop"  // 门店/经营点
	TypeGroup = "group" // 小组/分部门
)

// Organization 是统一的数据归属组织，物理表名 `orgs`。
// 根级组织即业务数据归属边界（等同既有 tenant_id/team_id）；子组织通过 ParentID 表达层级。
type Organization struct {
	ID        uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	ParentID  uint64     `gorm:"column:parent_id;not null;default:0;index" json:"parentId"` // 根级为 0
	TenantID  uint64     `gorm:"column:tenant_id;not null;default:0;index" json:"tenantId"` // 归属根租户（数据隔离键，根级 org 通常等于自身 ID）
	Name      string     `gorm:"column:name;size:100;not null" json:"name"`
	Logo      string     `gorm:"column:logo;size:500" json:"logo"`
	Type      string     `gorm:"column:type;size:32;not null;default:org" json:"type"` // root / shop / group 见上方常量
	Status    int8       `gorm:"column:status;not null;default:1;index" json:"status"` // 1 正常 0 禁用
	CreatedAt time.Time  `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time  `gorm:"column:updated_at" json:"updatedAt"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index" json:"-"`
}

// TableName 返回组织表名。
func (Organization) TableName() string { return "orgs" }

// OrganizationMember 记录用户在组织内的成员关系与角色，物理表名 `org_members`。
type OrganizationMember struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	OrgID     uint64    `gorm:"column:org_id;not null;index;uniqueIndex:uk_org_members_org_user,priority:1" json:"orgId"`
	UserID    uint64    `gorm:"column:user_id;not null;index;uniqueIndex:uk_org_members_org_user,priority:2" json:"userId"`
	RoleCode  string    `gorm:"column:role_code;size:80;not null;default:member;index" json:"roleCode"` // owner / admin / member 等，具体语义由项目定义
	Status    int8      `gorm:"column:status;not null;default:1;index" json:"status"`                   // 1 正常 0 禁用
	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 返回组织成员表名。
func (OrganizationMember) TableName() string { return "org_members" }

// ModelTypes 返回组织域需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&Organization{}, &OrganizationMember{}}
}
