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

func TestWechatConfigSaveAndDecrypt(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const masterKey = "0123456789abcdef0123456789abcdef"

	if _, err := store.GetWechatConfig(ctx, 9); err != ErrNotFound {
		t.Fatalf("未配置应返回 ErrNotFound: %v", err)
	}
	saved, err := store.SaveWechatConfig(ctx, 9, WechatConfigInput{
		AppID: "wx-tenant-9", AppSecret: "secret-9",
		MchID: "mch-9", MchAPIV3Key: "v3key-9",
		NotifyURL: "https://example.com/pay/notify", Enabled: true,
	}, masterKey)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if saved.AppSecret == "secret-9" || saved.MchAPIV3Key == "v3key-9" {
		t.Fatalf("落库应为密文: %+v", saved)
	}
	// 解密还原明文。
	secrets, err := saved.Decrypt(masterKey)
	if err != nil || secrets.AppSecret != "secret-9" || secrets.MchAPIV3Key != "v3key-9" {
		t.Fatalf("解密结果不正确: %+v err=%v", secrets, err)
	}
	// 幂等更新：敏感字段留空保留旧值，其余字段生效。
	updated, err := store.SaveWechatConfig(ctx, 9, WechatConfigInput{
		AppID: "wx-tenant-9", NotifyURL: "https://example.com/notify2", Enabled: false,
	}, masterKey)
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if updated.ID != saved.ID || updated.Enabled || updated.NotifyURL != "https://example.com/notify2" {
		t.Fatalf("更新应复用同一行并生效: %+v", updated)
	}
	secrets, err = updated.Decrypt(masterKey)
	if err != nil || secrets.AppSecret != "secret-9" || secrets.MchAPIV3Key != "v3key-9" {
		t.Fatalf("留空敏感字段应保留旧值: %+v err=%v", secrets, err)
	}
	// 提供非空敏感字段但无主密钥时应报错，禁止明文落库。
	if _, err := store.SaveWechatConfig(ctx, 10, WechatConfigInput{AppID: "wx-tenant-10", AppSecret: "plain-10"}, ""); err != ErrInvalidMasterKey {
		t.Fatalf("无主密钥应返回 ErrInvalidMasterKey: %v", err)
	}
	// 敏感字段留空且无主密钥时允许保存。
	plain, err := store.SaveWechatConfig(ctx, 10, WechatConfigInput{AppID: "wx-tenant-10"}, "")
	if err != nil {
		t.Fatalf("无敏感字段保存失败: %v", err)
	}
	if plain.AppSecret != "" {
		t.Fatalf("未提供敏感字段应存空: %+v", plain)
	}
}

func TestWechatConfigLookupByAppID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if _, err := store.SaveWechatConfig(ctx, 5, WechatConfigInput{AppID: "wx-app-5", Enabled: true}, ""); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	cfg, err := store.FindEnabledWechatConfigByAppID(ctx, "wx-app-5")
	if err != nil || cfg.TenantID != 5 {
		t.Fatalf("按 appid 反查失败: %+v err=%v", cfg, err)
	}
	if _, err := store.FindEnabledWechatConfigByAppID(ctx, "wx-missing"); err != ErrNotFound {
		t.Fatalf("未知 appid 应返回 ErrNotFound: %v", err)
	}
	// 禁用后不可反查。
	if _, err := store.SaveWechatConfig(ctx, 5, WechatConfigInput{AppID: "wx-app-5", Enabled: false}, ""); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if _, err := store.FindEnabledWechatConfigByAppID(ctx, "wx-app-5"); err != ErrNotFound {
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
