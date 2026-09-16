package tenantpay

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newTestStore 创建内存 SQLite 测试仓库（避免 Windows 下文件句柄占用临时目录）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("初始化 store 失败: %v", err)
	}
	return store
}

func TestPayConfigSaveAndFind(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.FindPayConfig(ctx, 1, WechatPayProvider); err != ErrNotFound {
		t.Fatalf("未配置应返回 ErrNotFound: %v", err)
	}
	saved, err := store.SavePayConfig(ctx, 1, WechatPayProvider, `{"mchId":"m1"}`, true)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if saved.TenantID != 1 || !saved.Enabled {
		t.Fatalf("保存结果不正确: %+v", saved)
	}
	// 幂等更新（同一行）。
	updated, err := store.SavePayConfig(ctx, 1, WechatPayProvider, `{"mchId":"m2"}`, false)
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if updated.ID != saved.ID || updated.Enabled {
		t.Fatalf("更新应复用同一行并生效: %+v vs %+v", updated, saved)
	}
}

func TestWechatAppLookup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.SaveWechatApp(ctx, 7, "wx-app-7", "secret-7", true); err != nil {
		t.Fatalf("保存小程序失败: %v", err)
	}
	tenantID, err := store.FindTenantIDByAppID(ctx, "wx-app-7")
	if err != nil || tenantID != 7 {
		t.Fatalf("反查租户失败: tenantID=%d err=%v", tenantID, err)
	}
	if _, err := store.FindTenantIDByAppID(ctx, "wx-missing"); err != ErrNotFound {
		t.Fatalf("未知 appid 应返回 ErrNotFound: %v", err)
	}
	// 禁用后不可反查。
	if _, err := store.SaveWechatApp(ctx, 7, "wx-app-7", "", false); err != nil {
		t.Fatalf("更新小程序失败: %v", err)
	}
	if _, err := store.FindTenantIDByAppID(ctx, "wx-app-7"); err != ErrNotFound {
		t.Fatalf("禁用后应返回 ErrNotFound: %v", err)
	}
}

func TestCommissionUpsert(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.GetCommission(ctx, 3); err != ErrNotFound {
		t.Fatalf("未设置应返回 ErrNotFound: %v", err)
	}
	cfg, err := store.SaveCommission(ctx, TenantCommission{TenantID: 3, RateBP: 500, MinCommissionCent: 30, Enabled: true})
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if cfg.RateBP != 500 || !cfg.Enabled {
		t.Fatalf("保存结果不正确: %+v", cfg)
	}
	cfg, err = store.SaveCommission(ctx, TenantCommission{TenantID: 3, RateBP: 300, MinCommissionCent: 10, Enabled: false})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if cfg.RateBP != 300 || cfg.Enabled {
		t.Fatalf("更新结果不正确: %+v", cfg)
	}
	if cfg.ID == 0 {
		t.Fatal("更新应复用同一行")
	}
}

func TestPSOrderListAndLookup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i, tenantID := range []uint64{1, 1, 2} {
		_, err := store.CreatePSOrder(ctx, ProfitSharingOrder{
			TenantID: tenantID, OrderNo: string(rune('A'+i)) + "-NO", OutOrderNo: "PS" + string(rune('A'+i)),
			AmountCent: 1000, CommissionCent: 50, RateBP: 500, Status: PSStatusPending,
		})
		if err != nil {
			t.Fatalf("创建分账记录失败: %v", err)
		}
	}
	if _, total, err := store.ListPSOrders(ctx, 0, "", 1, 10); err != nil || total != 3 {
		t.Fatalf("全平台查询: total=%d err=%v", total, err)
	}
	if _, total, err := store.ListPSOrders(ctx, 1, "", 1, 10); err != nil || total != 2 {
		t.Fatalf("按租户查询: total=%d err=%v", total, err)
	}
	if _, total, err := store.ListPSOrders(ctx, 2, PSStatusPending, 1, 10); err != nil || total != 1 {
		t.Fatalf("按状态查询: total=%d err=%v", total, err)
	}
	ps, err := store.FindPSOrderByOrderNo(ctx, "B-NO")
	if err != nil || ps.TenantID != 1 {
		t.Fatalf("按订单号查询失败: %+v err=%v", ps, err)
	}
	pending, err := store.ListPendingPSOrders(ctx, 10)
	if err != nil || len(pending) != 3 {
		t.Fatalf("待同步查询: len=%d err=%v", len(pending), err)
	}
}
