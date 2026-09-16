package tenantpay

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrNotFound 统一 404 语义。
var ErrNotFound = errors.New("记录不存在")

// Store 基于 GORM 的租户支付数据访问（兼容 MySQL 与 SQLite）。
type Store struct {
	db *gorm.DB
}

// NewStore 创建数据访问对象，并自动迁移本包涉及的表。
// integration_configs / tenant_wechat_apps 与宿主底座共用（AutoMigrate 只增列不删列，
// 与宿主迁移兼容）；tenant_commissions / profit_sharing_orders 为本包自建。
func NewStore(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("db 不能为空")
	}
	if err := db.AutoMigrate(
		&IntegrationConfig{},
		&TenantWechatApp{},
		&TenantCommission{},
		&ProfitSharingOrder{},
	); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// normalizeError 归一化 gorm 错误。
func normalizeError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// ---------- 集成配置（integration_configs） ----------

// FindPayConfig 读取租户指定 provider 的集成配置（未配置返回 ErrNotFound）。
func (s *Store) FindPayConfig(ctx context.Context, tenantID uint64, provider string) (IntegrationConfig, error) {
	var cfg IntegrationConfig
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND provider = ?", tenantID, provider).
		First(&cfg).Error; err != nil {
		return IntegrationConfig{}, normalizeError(err)
	}
	return cfg, nil
}

// SavePayConfig 保存租户集成配置（存在则更新，否则创建）。
func (s *Store) SavePayConfig(ctx context.Context, tenantID uint64, provider, configJSON string, enabled bool) (IntegrationConfig, error) {
	var cfg IntegrationConfig
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND provider = ?", tenantID, provider).
			First(&cfg).Error; err == nil {
			cfg.ConfigJSON = configJSON
			cfg.Enabled = enabled
			return tx.Save(&cfg).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		cfg = IntegrationConfig{
			TenantID:   tenantID,
			Provider:   provider,
			ConfigJSON: configJSON,
			Enabled:    enabled,
		}
		return tx.Create(&cfg).Error
	})
	if err != nil {
		return IntegrationConfig{}, err
	}
	return cfg, nil
}

// ---------- 小程序绑定（tenant_wechat_apps） ----------

// SaveWechatApp 保存租户小程序绑定（appid 幂等 upsert，留空 secret 保留旧值）。
func (s *Store) SaveWechatApp(ctx context.Context, tenantID uint64, appID, appSecret string, enabled bool) (TenantWechatApp, error) {
	var app TenantWechatApp
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("app_id = ?", appID).First(&app).Error; err == nil {
			app.TenantID = tenantID
			if appSecret != "" {
				app.AppSecret = appSecret
			}
			app.Enabled = enabled
			return tx.Save(&app).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		app = TenantWechatApp{
			TenantID:  tenantID,
			AppID:     appID,
			AppSecret: appSecret,
			Enabled:   enabled,
		}
		return tx.Create(&app).Error
	})
	if err != nil {
		return TenantWechatApp{}, err
	}
	return app, nil
}

// ListWechatApps 列出租户绑定的全部小程序。
func (s *Store) ListWechatApps(ctx context.Context, tenantID uint64) ([]TenantWechatApp, error) {
	var apps []TenantWechatApp
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("id ASC").
		Find(&apps).Error; err != nil {
		return nil, err
	}
	return apps, nil
}

// FindEnabledWechatAppByTenant 返回租户第一个启用的小程序（appid 为支付 JSAPI 下单方）。
func (s *Store) FindEnabledWechatAppByTenant(ctx context.Context, tenantID uint64) (TenantWechatApp, error) {
	var app TenantWechatApp
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND enabled = ?", tenantID, true).
		Order("id ASC").
		First(&app).Error; err != nil {
		return TenantWechatApp{}, normalizeError(err)
	}
	return app, nil
}

// FindTenantIDByAppID 根据已启用 appid 反查租户 ID（C 端 X-App-Id 场景）。
func (s *Store) FindTenantIDByAppID(ctx context.Context, appID string) (uint64, error) {
	var app TenantWechatApp
	if err := s.db.WithContext(ctx).
		Where("app_id = ? AND enabled = ?", appID, true).
		First(&app).Error; err != nil {
		return 0, normalizeError(err)
	}
	return app.TenantID, nil
}

// ---------- 抽成配置（tenant_commissions） ----------

// GetCommission 读取租户抽成配置（未设置返回 ErrNotFound）。
func (s *Store) GetCommission(ctx context.Context, tenantID uint64) (TenantCommission, error) {
	var cfg TenantCommission
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		First(&cfg).Error; err != nil {
		return TenantCommission{}, normalizeError(err)
	}
	return cfg, nil
}

// SaveCommission 保存租户抽成配置（upsert）。
func (s *Store) SaveCommission(ctx context.Context, cfg TenantCommission) (TenantCommission, error) {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old TenantCommission
		if err := tx.Where("tenant_id = ?", cfg.TenantID).First(&old).Error; err == nil {
			old.RateBP = cfg.RateBP
			old.MinCommissionCent = cfg.MinCommissionCent
			old.Enabled = cfg.Enabled
			return tx.Save(&old).Error
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return tx.Create(&cfg).Error
	})
	if err != nil {
		return TenantCommission{}, err
	}
	return s.GetCommission(ctx, cfg.TenantID)
}

// ListCommissions 列出全部租户抽成配置（平台端分账记录页联表展示用）。
func (s *Store) ListCommissions(ctx context.Context) ([]TenantCommission, error) {
	var list []TenantCommission
	if err := s.db.WithContext(ctx).Order("tenant_id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ---------- 分账记录（profit_sharing_orders） ----------

// CreatePSOrder 创建分账记录（OutOrderNo 唯一）。
func (s *Store) CreatePSOrder(ctx context.Context, ps ProfitSharingOrder) (ProfitSharingOrder, error) {
	if err := s.db.WithContext(ctx).Create(&ps).Error; err != nil {
		return ProfitSharingOrder{}, err
	}
	return ps, nil
}

// UpdatePSOrder 更新分账记录。
func (s *Store) UpdatePSOrder(ctx context.Context, ps *ProfitSharingOrder) error {
	return s.db.WithContext(ctx).Save(ps).Error
}

// FindPSOrderByOrderNo 按业务订单号读取分账记录（未创建返回 ErrNotFound）。
func (s *Store) FindPSOrderByOrderNo(ctx context.Context, orderNo string) (ProfitSharingOrder, error) {
	var ps ProfitSharingOrder
	if err := s.db.WithContext(ctx).
		Where("order_no = ?", orderNo).
		First(&ps).Error; err != nil {
		return ProfitSharingOrder{}, normalizeError(err)
	}
	return ps, nil
}

// ListPSOrders 分页列出分账记录（tenantID=0 表示全平台）。
func (s *Store) ListPSOrders(ctx context.Context, tenantID uint64, status string, page, size int) ([]ProfitSharingOrder, int64, error) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	query := s.db.WithContext(ctx).Model(&ProfitSharingOrder{})
	if tenantID > 0 {
		query = query.Where("tenant_id = ?", tenantID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []ProfitSharingOrder
	if err := query.Order("id DESC").
		Offset((page - 1) * size).Limit(size).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// ListPendingPSOrders 列出待同步/待重试的分账记录（发起时分账失败或微信侧处理中）。
func (s *Store) ListPendingPSOrders(ctx context.Context, limit int) ([]ProfitSharingOrder, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var list []ProfitSharingOrder
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []string{PSStatusPending, PSStatusProcessing, PSStatusFailed}).
		Order("id ASC").
		Limit(limit).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
