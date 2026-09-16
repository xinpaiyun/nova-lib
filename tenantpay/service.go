package tenantpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Service 租户支付业务层：支付/小程序配置 CRUD、抽成配置、分账记录与补发。
type Service struct {
	store    *Store
	resolver *Resolver
	executor *Executor
	// masterKey 为空时禁止保存新配置（未配置平台主密钥）。
	masterKey string
	// nowTime 测试可注入。
	nowTime func() time.Time
}

// NewService 创建业务服务。
func NewService(store *Store, resolver *Resolver, executor *Executor, masterKey string) *Service {
	return &Service{
		store:     store,
		resolver:  resolver,
		executor:  executor,
		masterKey: strings.TrimSpace(masterKey),
		nowTime:   time.Now,
	}
}

// Executor 暴露分账执行器（宿主订单完成钩子/定时任务使用）。
func (s *Service) Executor() *Executor { return s.executor }

// Store 暴露数据访问（宿主需要直接查询时使用）。
func (s *Service) Store() *Store { return s.store }

// 业务错误（handler 统一映射 HTTP 状态码）。
var (
	ErrMasterKeyMissing = errors.New("平台支付主密钥未配置，无法保存支付配置")
	ErrMchRequired      = errors.New("请输入商户号")
	ErrInvalidConfig    = errors.New("支付配置不完整，无法启用")
	ErrAppIDRequired    = errors.New("请输入小程序 AppID")
	ErrRateInvalid      = errors.New("抽成比例不合法（0-3000 基点）")
	ErrMinInvalid       = errors.New("保底金额不合法")
)

// ---------- VO ----------

// WechatPayConfigVO 微信支付配置视图（机密字段只回显是否已配置）。
type WechatPayConfigVO struct {
	TenantID     uint64    `json:"tenantId"`
	MchID        string    `json:"mchId"`
	CertSerialNo string    `json:"certSerialNo"`
	PublicKeyID  string    `json:"publicKeyId"`
	AppID        string    `json:"appId"`
	Enabled      bool      `json:"enabled"`
	HasAPIKeyV3  bool      `json:"hasApiKeyV3"`
	HasPrivKey   bool      `json:"hasPrivKey"`
	HasPubKey    bool      `json:"hasPubKey"`
	NotifyURL    string    `json:"notifyUrl"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// SaveWechatPayConfigRequest 保存微信支付配置请求（机密字段留空保留旧值）。
type SaveWechatPayConfigRequest struct {
	MchID         string `json:"mchId"`
	APIKeyV3      string `json:"apiKeyV3"`
	CertSerialNo  string `json:"certSerialNo"`
	PrivateKeyPEM string `json:"privateKeyPem"`
	PublicKeyPEM  string `json:"publicKeyPem"`
	PublicKeyID   string `json:"publicKeyId"`
	Enabled       bool   `json:"enabled"`
}

// WechatAppVO 小程序绑定视图。
type WechatAppVO struct {
	ID        uint64    `json:"id"`
	TenantID  uint64    `json:"tenantId"`
	AppID     string    `json:"appId"`
	Enabled   bool      `json:"enabled"`
	HasSecret bool      `json:"hasSecret"`
	CreatedAt time.Time `json:"createdAt"`
}

// SaveWechatAppRequest 保存小程序绑定请求（secret 留空保留旧值）。
type SaveWechatAppRequest struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
	Enabled   bool   `json:"enabled"`
}

// CommissionVO 抽成配置视图。
type CommissionVO struct {
	TenantID          uint64 `json:"tenantId"`
	RateBP            int64  `json:"rateBp"`
	RatePercent       string `json:"ratePercent"` // 展示用百分比（如 5.00）
	MinCommissionCent int64  `json:"minCommissionCent"`
	Enabled           bool   `json:"enabled"`
}

// SaveCommissionRequest 保存抽成配置请求。
type SaveCommissionRequest struct {
	RateBP            int64 `json:"rateBp"`
	MinCommissionCent int64 `json:"minCommissionCent"`
	Enabled           bool  `json:"enabled"`
}

// PSOrderVO 分账记录视图。
type PSOrderVO struct {
	ProfitSharingOrder
	RatePercent string `json:"ratePercent"`
}

// ---------- 微信支付配置 ----------

// SaveWechatPayConfig 保存租户微信支付配置（商户自助或 admin 代配，同一套校验）。
func (s *Service) SaveWechatPayConfig(ctx context.Context, tenantID uint64, req SaveWechatPayConfigRequest) (WechatPayConfigVO, error) {
	if s.masterKey == "" {
		return WechatPayConfigVO{}, ErrMasterKeyMissing
	}
	req.MchID = strings.TrimSpace(req.MchID)
	if req.MchID == "" {
		return WechatPayConfigVO{}, ErrMchRequired
	}
	// 读取旧配置合并（请求为空的字段保留旧值）。
	stored := wechatPayStoredConfig{}
	if old, err := s.store.FindPayConfig(ctx, tenantID, WechatPayProvider); err == nil {
		_ = json.Unmarshal([]byte(old.ConfigJSON), &stored)
	} else if !errors.Is(err, ErrNotFound) {
		return WechatPayConfigVO{}, err
	}
	stored.MchID = req.MchID
	if v := strings.TrimSpace(req.CertSerialNo); v != "" {
		stored.CertSerialNo = v
	}
	if v := strings.TrimSpace(req.PublicKeyID); v != "" {
		stored.PublicKeyID = v
	}
	if v := strings.TrimSpace(req.PublicKeyPEM); v != "" {
		stored.PublicKeyPEM = v
	}
	if v := strings.TrimSpace(req.APIKeyV3); v != "" {
		cipher, err := Encrypt(s.masterKey, v)
		if err != nil {
			return WechatPayConfigVO{}, err
		}
		stored.APIKeyV3 = cipher
	}
	if v := strings.TrimSpace(req.PrivateKeyPEM); v != "" {
		cipher, err := Encrypt(s.masterKey, v)
		if err != nil {
			return WechatPayConfigVO{}, err
		}
		stored.PrivateKeyPEM = cipher
	}
	if req.Enabled && (stored.APIKeyV3 == "" || stored.PrivateKeyPEM == "" ||
		stored.CertSerialNo == "" || stored.PublicKeyPEM == "" || stored.PublicKeyID == "") {
		return WechatPayConfigVO{}, ErrInvalidConfig
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return WechatPayConfigVO{}, err
	}
	saved, err := s.store.SavePayConfig(ctx, tenantID, WechatPayProvider, string(raw), req.Enabled)
	if err != nil {
		return WechatPayConfigVO{}, err
	}
	s.resolver.Invalidate(tenantID)
	return s.wechatPayConfigVO(ctx, tenantID, saved)
}

// GetWechatPayConfig 查询租户微信支付配置（机密字段只回显是否已配置）。
func (s *Service) GetWechatPayConfig(ctx context.Context, tenantID uint64) (WechatPayConfigVO, error) {
	saved, err := s.store.FindPayConfig(ctx, tenantID, WechatPayProvider)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return s.wechatPayConfigVO(ctx, tenantID, IntegrationConfig{})
		}
		return WechatPayConfigVO{}, err
	}
	return s.wechatPayConfigVO(ctx, tenantID, saved)
}

// WechatPayEnabled C 端查询租户是否启用微信支付。
func (s *Service) WechatPayEnabled(ctx context.Context, tenantID uint64) bool {
	saved, err := s.store.FindPayConfig(ctx, tenantID, WechatPayProvider)
	if err != nil || !saved.Enabled {
		return false
	}
	stored := wechatPayStoredConfig{}
	if err := json.Unmarshal([]byte(saved.ConfigJSON), &stored); err != nil {
		return false
	}
	return stored.MchID != "" && stored.APIKeyV3 != "" && stored.PrivateKeyPEM != ""
}

// ProfitSharingRequired 判断租户订单是否需要分账标记：
// 抽成启用 + 商户配置了自己的商户号（商户层收款才分账，平台代收不分）。
func (s *Service) ProfitSharingRequired(ctx context.Context, tenantID uint64) bool {
	cfg, err := s.store.GetCommission(ctx, tenantID)
	if err != nil || !cfg.Enabled {
		return false
	}
	return s.resolver.MerchantMchID(ctx, tenantID) != ""
}

// wechatPayConfigVO 组装配置响应视图。
func (s *Service) wechatPayConfigVO(ctx context.Context, tenantID uint64, saved IntegrationConfig) (WechatPayConfigVO, error) {
	stored := wechatPayStoredConfig{}
	if saved.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(saved.ConfigJSON), &stored); err != nil {
			return WechatPayConfigVO{}, err
		}
	}
	vo := WechatPayConfigVO{
		TenantID:     tenantID,
		MchID:        stored.MchID,
		CertSerialNo: stored.CertSerialNo,
		PublicKeyID:  stored.PublicKeyID,
		Enabled:      saved.Enabled,
		HasAPIKeyV3:  stored.APIKeyV3 != "",
		HasPrivKey:   stored.PrivateKeyPEM != "",
		HasPubKey:    stored.PublicKeyPEM != "",
		NotifyURL:    s.resolver.NotifyURL(tenantID),
		UpdatedAt:    saved.UpdatedAt,
	}
	if app, err := s.store.FindEnabledWechatAppByTenant(ctx, tenantID); err == nil {
		vo.AppID = app.AppID
	}
	return vo, nil
}

// ---------- 小程序绑定 ----------

// SaveWechatApp 保存租户小程序绑定（appid 幂等，secret 留空保留旧值）。
func (s *Service) SaveWechatApp(ctx context.Context, tenantID uint64, req SaveWechatAppRequest) (WechatAppVO, error) {
	req.AppID = strings.TrimSpace(req.AppID)
	if req.AppID == "" {
		return WechatAppVO{}, ErrAppIDRequired
	}
	app, err := s.store.SaveWechatApp(ctx, tenantID, req.AppID, strings.TrimSpace(req.AppSecret), req.Enabled)
	if err != nil {
		return WechatAppVO{}, err
	}
	return wechatAppVO(app), nil
}

// ListWechatApps 列出租户小程序绑定。
func (s *Service) ListWechatApps(ctx context.Context, tenantID uint64) ([]WechatAppVO, error) {
	apps, err := s.store.ListWechatApps(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	list := make([]WechatAppVO, 0, len(apps))
	for _, app := range apps {
		list = append(list, wechatAppVO(app))
	}
	return list, nil
}

// wechatAppVO 组装小程序视图。
func wechatAppVO(app TenantWechatApp) WechatAppVO {
	return WechatAppVO{
		ID:        app.ID,
		TenantID:  app.TenantID,
		AppID:     app.AppID,
		Enabled:   app.Enabled,
		HasSecret: app.AppSecret != "",
		CreatedAt: app.CreatedAt,
	}
}

// ---------- 抽成配置 ----------

// SaveCommission 保存租户抽成配置（平台端）。
func (s *Service) SaveCommission(ctx context.Context, tenantID uint64, req SaveCommissionRequest) (CommissionVO, error) {
	if tenantID == 0 {
		return CommissionVO{}, errors.New("租户 ID 不合法")
	}
	if req.RateBP < 0 || req.RateBP > 3000 {
		return CommissionVO{}, ErrRateInvalid
	}
	if req.MinCommissionCent < 0 {
		return CommissionVO{}, ErrMinInvalid
	}
	cfg, err := s.store.SaveCommission(ctx, TenantCommission{
		TenantID:          tenantID,
		RateBP:            req.RateBP,
		MinCommissionCent: req.MinCommissionCent,
		Enabled:           req.Enabled,
	})
	if err != nil {
		return CommissionVO{}, err
	}
	return commissionVO(cfg), nil
}

// GetCommission 查询租户抽成配置（未设置返回零值视图）。
func (s *Service) GetCommission(ctx context.Context, tenantID uint64) (CommissionVO, error) {
	cfg, err := s.store.GetCommission(ctx, tenantID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CommissionVO{TenantID: tenantID}, nil
		}
		return CommissionVO{}, err
	}
	return commissionVO(cfg), nil
}

// commissionVO 组装抽成配置视图。
func commissionVO(cfg TenantCommission) CommissionVO {
	return CommissionVO{
		TenantID:          cfg.TenantID,
		RateBP:            cfg.RateBP,
		RatePercent:       formatRateBP(cfg.RateBP),
		MinCommissionCent: cfg.MinCommissionCent,
		Enabled:           cfg.Enabled,
	}
}

// formatRateBP 基点转百分比展示（500 → "5.00"）。
func formatRateBP(bp int64) string {
	return strconv.FormatInt(bp/100, 10) + "." + fmt.Sprintf("%02d", bp%100)
}

// ---------- 分账记录 ----------

// ListPSOrders 分页查询分账记录（tenantID=0 全平台；商户端强制传租户 ID）。
func (s *Service) ListPSOrders(ctx context.Context, tenantID uint64, status string, page, size int) ([]PSOrderVO, int64, error) {
	list, total, err := s.store.ListPSOrders(ctx, tenantID, strings.TrimSpace(status), page, size)
	if err != nil {
		return nil, 0, err
	}
	vos := make([]PSOrderVO, 0, len(list))
	for _, ps := range list {
		vos = append(vos, PSOrderVO{
			ProfitSharingOrder: ps,
			RatePercent:        formatRateBP(ps.RateBP),
		})
	}
	return vos, total, nil
}

// RetryPSOrder 手动补发分账（平台端）。
func (s *Service) RetryPSOrder(ctx context.Context, psID uint64) (PSOrderVO, error) {
	ps, err := s.executor.Retry(ctx, psID)
	if err != nil {
		return PSOrderVO{}, err
	}
	return PSOrderVO{
		ProfitSharingOrder: ps,
		RatePercent:        formatRateBP(ps.RateBP),
	}, nil
}
