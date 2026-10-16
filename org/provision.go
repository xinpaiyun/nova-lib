package org

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// CreateRootOrgTx 在调用方事务内创建根级组织（Type=org、tenant_id=自身 ID），
// 返回组织 ID。供项目在同一事务内继续写入 org_profiles 等项目扩展表；
// owner 等成员绑定随后在同事务内调用 EnsureMember(tx, ...) 完成。
func CreateRootOrgTx(tx *gorm.DB, name, logo string) (uint64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("组织名称不能为空")
	}
	created := Organization{Name: name, Logo: logo, Type: TypeRoot, Status: StatusEnabled}
	if err := tx.Create(&created).Error; err != nil {
		return 0, err
	}
	// 根级组织 tenant_id = 自身 ID（数据归属边界）。
	if err := tx.Model(&Organization{}).Where("id = ?", created.ID).Update("tenant_id", created.ID).Error; err != nil {
		return 0, err
	}
	return created.ID, nil
}

// CreateRootOrg 用独立事务创建根级组织并返回组织 ID；
// 需要与项目扩展表同事务落库时改用 CreateRootOrgTx。
func CreateRootOrg(ctx context.Context, db *gorm.DB, name, logo string) (uint64, error) {
	var orgID uint64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		id, txErr := CreateRootOrgTx(tx, name, logo)
		orgID = id
		return txErr
	})
	if err != nil {
		return 0, err
	}
	return orgID, nil
}
