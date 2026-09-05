package org

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// commonErr 集中定义包内可判定错误的哨兵值，避免与 gorm 错误混淆。
var (
	ErrNotFound  = gorm.ErrRecordNotFound
	ErrDuplicate = gorm.ErrDuplicatedKey
)

// Member 描述成员在某个组织内的归属快照。
type Member struct {
	OrgID    uint64
	UserID   uint64
	RoleCode string
}

// ListMemberOrgs 返回用户归属的全部有效组织（按组织名排序）。
func ListMemberOrgs(ctx context.Context, db *gorm.DB, userID uint64) ([]Member, error) {
	var rows []struct {
		OrgID    uint64 `gorm:"column:org_id"`
		RoleCode string `gorm:"column:role_code"`
	}
	err := db.WithContext(ctx).
		Model(&OrganizationMember{}).
		Select("org_id, role_code").
		Where("user_id = ? AND status = ?", userID, StatusEnabled).
		Order("org_id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, Member{OrgID: r.OrgID, UserID: userID, RoleCode: r.RoleCode})
	}
	return out, nil
}

// MemberRole 返回成员在指定组织内的角色码；非成员返回 ErrNotFound。
func MemberRole(ctx context.Context, db *gorm.DB, orgID, userID uint64) (string, error) {
	var m OrganizationMember
	err := db.WithContext(ctx).
		Where("org_id = ? AND user_id = ? AND status = ?", orgID, userID, StatusEnabled).
		First(&m).Error
	if err != nil {
		return "", err
	}
	return m.RoleCode, nil
}

// IsMember 判断用户是否为指定组织的有效成员。
func IsMember(ctx context.Context, db *gorm.DB, orgID, userID uint64) (bool, error) {
	role, err := MemberRole(ctx, db, orgID, userID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role != "", nil
}

// EnsureMember 幂等建立/恢复用户在组织内的成员关系，返回成员角色码。
// 已存在且启用则原样返回；存在但被禁用则重新启用。
func EnsureMember(ctx context.Context, db *gorm.DB, orgID, userID uint64, roleCode string) (string, error) {
	var m OrganizationMember
	err := db.WithContext(ctx).
		Where("org_id = ? AND user_id = ?", orgID, userID).
		First(&m).Error
	if err == nil {
		if m.Status == StatusEnabled && m.RoleCode != roleCode {
			m.RoleCode = roleCode
			if e := db.WithContext(ctx).Save(&m).Error; e != nil {
				return "", e
			}
		}
		return m.RoleCode, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	m = OrganizationMember{OrgID: orgID, UserID: userID, RoleCode: roleCode, Status: StatusEnabled}
	if e := db.WithContext(ctx).Create(&m).Error; e != nil {
		if errors.Is(e, ErrDuplicate) {
			// 并发冲突转幂等：重新读取
			return EnsureMember(ctx, db, orgID, userID, roleCode)
		}
		return "", e
	}
	return roleCode, nil
}
