// Package rbac 提供全公司统一的角色权限（RBAC）数据模型与中间件骨架。
//
// 统一了 6 项目各自独立的角色权限表结构与权限校验中间件形态（Permission/Role/
// RolePermission/UserRole），但各项目的业务判定语义（如 baoxian 团队数据范围、
// xingxueji 商户/店铺快照、app 多租户对象校验）不写死在库内，通过
// Checker/RoleResolver 回调由项目注入。
package rbac

import "time"

// Role 表示平台级或租户级角色。
// TenantID=0 表示平台内置角色；Code 为角色唯一编码。
type Role struct {
	ID          uint64    `gorm:"primaryKey" json:"id"`
	TenantID    uint64    `gorm:"not null;default:0;index" json:"tenantId"`
	Code        string    `gorm:"size:80;not null;index" json:"code"`
	Name        string    `gorm:"size:80;not null" json:"name"`
	Level       string    `gorm:"size:32;not null;index" json:"level"` // platform / tenant
	Description string    `gorm:"size:255" json:"description"`
	Builtin     bool      `gorm:"not null;default:false" json:"builtin"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// TableName 返回角色表名。
func (Role) TableName() string { return "roles" }

// Permission 表示菜单、按钮或 API 权限节点。
type Permission struct {
	ID          uint64 `gorm:"primaryKey" json:"id"`
	Code        string `gorm:"code;size:120;uniqueIndex;not null" json:"code"`
	Name        string `gorm:"name;size:120;not null" json:"name"`
	Type        string `gorm:"type;size:32;not null;index" json:"type"` // menu / button / api
	Description string `gorm:"description;size:255" json:"description"`
}

// TableName 返回权限节点表名。
func (Permission) TableName() string { return "permissions" }

// RolePermission 表示角色与权限节点的授权关系（角色 → 权限码）。
type RolePermission struct {
	ID             uint64    `gorm:"primaryKey" json:"id"`
	TenantID       uint64    `gorm:"not null;default:0;index" json:"tenantId"`
	RoleCode       string    `gorm:"code;size:80;not null;index" json:"roleCode"`
	PermissionCode string    `gorm:"permission_code;size:120;not null;index" json:"permissionCode"`
	CreatedAt      time.Time `json:"createdAt"`
}

// TableName 返回角色权限关联表名。
func (RolePermission) TableName() string { return "role_permissions" }

// UserRole 表示用户与角色的绑定关系。
type UserRole struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	UserID    uint64    `gorm:"user_id;not null;index;uniqueIndex:(user_role)" json:"userId"`
	RoleCode  string    `gorm:"role_code;size:80;not null;index;uniqueIndex:(user_role)" json:"roleCode"`
	TenantID  uint64    `gorm:"tenant_id;not null;default:0;index" json:"tenantId"`
	CreatedAt time.Time `json:"createdAt"`
}

// TableName 返回用户角色绑定表名。
func (UserRole) TableName() string { return "user_roles" }

// ModelTypes 返回 RBAC 域需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&Role{}, &Permission{}, &RolePermission{}, &UserRole{}}
}