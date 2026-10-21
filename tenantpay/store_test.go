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
	const masterKey = "0123456789abcdef0123456789abcdef"

	if _, err := store.SaveWechatApp(ctx, 7, "wx-app-7", "secret-7", true, masterKey); err != nil {
		t.Fatalf("保存小程序失败: %v", err)
	}
	// secret 应密文落库。
	var row TenantWechatApp
	if err := store.db.Where("app_id = ?", "wx-app-7").First(&row).Error; err != nil {
		t.Fatalf("查询绑定行失败: %v", err)
	}
	if row.AppSecret == "secret-7" || len(row.AppSecret) < 8 {
		t.Fatalf("secret 应为密文: %q", row.AppSecret)
	}
	tenantID, err := store.FindTenantIDByAppID(ctx, "wx-app-7")
	if err != nil || tenantID != 7 {
		t.Fatalf("反查租户失败: tenantID=%d err=%v", tenantID, err)
	}
	if _, err := store.FindTenantIDByAppID(ctx, "wx-missing"); err != ErrNotFound {
		t.Fatalf("未知 appid 应返回 ErrNotFound: %v", err)
	}
	// 禁用后不可反查（secret 留空保留旧值）。
	if _, err := store.SaveWechatApp(ctx, 7, "wx-app-7", "", false, masterKey); err != nil {
		t.Fatalf("更新小程序失败: %v", err)
	}
	if _, err := store.FindTenantIDByAppID(ctx, "wx-app-7"); err != ErrNotFound {
		t.Fatalf("禁用后应返回 ErrNotFound: %v", err)
	}
}

// TestWechatAppLoginLookup 覆盖平台代管绑定（官方 tenant_id=0）、密文落库与登录反查。
func TestWechatAppLoginLookup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const masterKey = "0123456789abcdef0123456789abcdef"
	svc := NewService(store, nil, nil, masterKey)

	// 官方小程序（tenant_id=0）与租户绑定并存。
	if _, err := svc.SaveWechatApp(ctx, 0, SaveWechatAppRequest{AppID: "wx-official", AppSecret: "sec-official", Enabled: true}); err != nil {
		t.Fatalf("保存官方绑定失败: %v", err)
	}
	if _, err := svc.SaveWechatApp(ctx, 5, SaveWechatAppRequest{AppID: "wx-t5", AppSecret: "sec-t5", Enabled: true}); err != nil {
		t.Fatalf("保存租户绑定失败: %v", err)
	}
	// masterKey 缺失时禁止写入新 secret。
	if _, err := NewService(store, nil, nil, "").SaveWechatApp(ctx, 6, SaveWechatAppRequest{AppID: "wx-t6", AppSecret: "sec-t6", Enabled: true}); err != ErrMasterKeyMissing {
		t.Fatalf("缺主密钥应报 ErrMasterKeyMissing: %v", err)
	}
	// 全平台列表含官方与租户。
	all, err := svc.ListAllWechatApps(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("全平台列表: len=%d err=%v", len(all), err)
	}
	if all[0].TenantID != 0 || all[0].AppID != "wx-official" || !all[0].HasSecret {
		t.Fatalf("官方绑定 VO 不正确: %+v", all[0])
	}
	// 登录反查：解密 secret、绑定 appid 与租户。
	for _, tc := range []struct {
		appID, secret string
		tenantID      uint64
	}{
		{"wx-official", "sec-official", 0},
		{"wx-t5", "sec-t5", 5},
	} {
		tenantID, boundAppID, sec, err := svc.WechatAppForLogin(ctx, tc.appID)
		if err != nil || tenantID != tc.tenantID || boundAppID != tc.appID || sec != tc.secret {
			t.Fatalf("登录反查 %s: tenant=%d app=%s sec=%s err=%v", tc.appID, tenantID, boundAppID, sec, err)
		}
	}
	if _, _, _, err := svc.WechatAppForLogin(ctx, "wx-missing"); err != ErrNotFound {
		t.Fatalf("未绑定 appid 应返回 ErrNotFound: %v", err)
	}
	// 历史明文行兼容读取（直插明文 secret）。
	if err := store.db.Create(&TenantWechatApp{TenantID: 8, AppID: "wx-legacy", AppSecret: "legacy-plain", Enabled: true}).Error; err != nil {
		t.Fatalf("插入历史行失败: %v", err)
	}
	tenantID, _, sec, err := svc.WechatAppForLogin(ctx, "wx-legacy")
	if err != nil || tenantID != 8 || sec != "legacy-plain" {
		t.Fatalf("历史明文行应兼容: tenant=%d sec=%s err=%v", tenantID, sec, err)
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
