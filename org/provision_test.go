package org

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestCreateRootOrg(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	orgID, err := CreateRootOrg(ctx, db, "  测试品牌  ", "https://logo.example.com/a.png")
	if err != nil {
		t.Fatalf("CreateRootOrg: %v", err)
	}
	if orgID == 0 {
		t.Fatal("orgID 应大于 0")
	}
	var row Organization
	if err := db.First(&row, orgID).Error; err != nil {
		t.Fatalf("load org: %v", err)
	}
	if row.Name != "测试品牌" || row.Type != TypeRoot || row.TenantID != orgID || row.Status != StatusEnabled {
		t.Fatalf("字段不符: %+v", row)
	}
}

func TestCreateRootOrgTxRollback(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := CreateRootOrgTx(tx, "回滚品牌", ""); err != nil {
			return err
		}
		return errors.New("主动失败")
	})
	if err == nil {
		t.Fatal("期望事务失败")
	}
	var count int64
	db.Model(&Organization{}).Count(&count)
	if count != 0 {
		t.Fatalf("事务回滚后不应有组织，实际 %d", count)
	}
}

func TestCreateRootOrgEmptyName(t *testing.T) {
	db := newTestDB(t)
	if _, err := CreateRootOrg(context.Background(), db, "  ", ""); err == nil {
		t.Fatal("空名称应报错")
	}
}
