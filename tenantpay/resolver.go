package tenantpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/wechat"
)

// ErrPayNotConfigured 表示租户微信支付未配置或未启用。
var ErrPayNotConfigured = errors.New("微信支付未配置或未启用")

// wechatPayStoredConfig 定义 integration_configs.config_json 的存储结构。
// APIKeyV3 与 PrivateKeyPEM 为 AES-GCM 密文；其余字段明文（公钥非机密）。
type wechatPayStoredConfig struct {
	MchID         string `json:"mchId"`
	APIKeyV3      string `json:"apiKeyV3"`
	CertSerialNo  string `json:"certSerialNo"`
	PrivateKeyPEM string `json:"privateKeyPem"`
	PublicKeyPEM  string `json:"publicKeyPem"`
	PublicKeyID   string `json:"publicKeyId"`
}

// cachedClient 带 TTL 的支付客户端缓存条目。
type cachedClient struct {
	client   *wechat.PayClient
	expireAt time.Time
}

// Resolver 按租户解析微信支付客户端。
// 商户层（租户自己的商户号）优先；OpenFallback 开启且商户未配置时回退平台层（tenant_id=0）。
// 客户端带 TTL 缓存，避免每次支付都写临时证书文件。
type Resolver struct {
	store     *Store
	masterKey string
	// NotifyURLTemplate 回调地址模板，%d 会被替换为租户 ID（为空则使用配置内无租户回退）。
	NotifyURLTemplate string
	// OpenFallback 商户未配置时是否回退平台层（tenant_id=0）代收。
	OpenFallback bool
	ttl          time.Duration
	mu           sync.Mutex
	cache        map[uint64]cachedClient
}

// NewResolver 创建租户支付客户端解析器。
// masterKey 用于解密租户支付配置中的机密字段；ttl 为客户端缓存时长（<=0 取 5 分钟）。
func NewResolver(store *Store, masterKey, notifyURLTemplate string, openFallback bool, ttl time.Duration) *Resolver {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Resolver{
		store:             store,
		masterKey:         strings.TrimSpace(masterKey),
		NotifyURLTemplate: notifyURLTemplate,
		OpenFallback:      openFallback,
		ttl:               ttl,
		cache:             make(map[uint64]cachedClient),
	}
}

// NotifyURL 组装租户支付回调地址。
func (r *Resolver) NotifyURL(tenantID uint64) string {
	if r.NotifyURLTemplate == "" {
		return ""
	}
	return fmt.Sprintf(r.NotifyURLTemplate, tenantID)
}

// Resolve 返回租户可用的微信支付客户端（商户层优先，可选回退平台层）。
func (r *Resolver) Resolve(ctx context.Context, tenantID uint64) (*wechat.PayClient, error) {
	r.mu.Lock()
	if cached, ok := r.cache[tenantID]; ok && time.Now().Before(cached.expireAt) {
		r.mu.Unlock()
		return cached.client, nil
	}
	r.mu.Unlock()

	client, err := r.build(ctx, tenantID)
	if err != nil {
		if errors.Is(err, ErrPayNotConfigured) && r.OpenFallback && tenantID != 0 {
			// 回退平台层代收（tenant_id=0）；平台层配置缺失则如实报错。
			client, err = r.build(ctx, 0)
		}
		if err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	r.cache[tenantID] = cachedClient{client: client, expireAt: time.Now().Add(r.ttl)}
	r.mu.Unlock()
	return client, nil
}

// Invalidate 清除租户客户端缓存（配置更新后调用，使新配置即时生效）。
func (r *Resolver) Invalidate(tenantID uint64) {
	r.mu.Lock()
	delete(r.cache, tenantID)
	r.mu.Unlock()
}

// build 读取租户配置 → 解密机密字段 → 写临时 PEM → 构建支付客户端。
func (r *Resolver) build(ctx context.Context, tenantID uint64) (*wechat.PayClient, error) {
	saved, err := r.store.FindPayConfig(ctx, tenantID, WechatPayProvider)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrPayNotConfigured
		}
		return nil, err
	}
	if !saved.Enabled {
		return nil, ErrPayNotConfigured
	}
	stored := wechatPayStoredConfig{}
	if err := json.Unmarshal([]byte(saved.ConfigJSON), &stored); err != nil {
		return nil, err
	}
	apiKeyV3, err := Decrypt(r.masterKey, stored.APIKeyV3)
	if err != nil {
		return nil, fmt.Errorf("解密 apiv3 密钥失败: %w", err)
	}
	privateKeyPEM, err := Decrypt(r.masterKey, stored.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("解密商户私钥失败: %w", err)
	}
	if apiKeyV3 == "" || privateKeyPEM == "" || stored.MchID == "" || stored.CertSerialNo == "" ||
		stored.PublicKeyPEM == "" || stored.PublicKeyID == "" {
		return nil, ErrPayNotConfigured
	}
	privPath, privCleanup, err := writeTempPEM("tpay-priv", privateKeyPEM)
	if err != nil {
		return nil, err
	}
	defer privCleanup()
	pubPath, pubCleanup, err := writeTempPEM("tpay-pub", stored.PublicKeyPEM)
	if err != nil {
		return nil, err
	}
	defer pubCleanup()
	client := wechat.NewPayClient(config.WechatConfig{
		MchID:         stored.MchID,
		APIKeyV3:      apiKeyV3,
		CertPath:      privPath,
		CertSerialNo:  stored.CertSerialNo,
		PublicKeyPath: pubPath,
		PublicKeyID:   stored.PublicKeyID,
		NotifyURL:     r.NotifyURL(tenantID),
	})
	if !client.Enabled() {
		return nil, errors.New("微信支付客户端初始化失败，请检查证书/公钥配置")
	}
	return client, nil
}

// writeTempPEM 将 PEM 内容写入临时文件，返回路径与清理函数。
func writeTempPEM(prefix, content string) (string, func(), error) {
	f, err := os.CreateTemp("", prefix+"-*.pem")
	if err != nil {
		return "", nil, err
	}
	path := f.Name()
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(path)
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", nil, err
	}
	return path, func() { os.Remove(path) }, nil
}

// MerchantMchID 返回租户收款商户号（未配置返回空串），供分账判断订单归属层级。
func (r *Resolver) MerchantMchID(ctx context.Context, tenantID uint64) string {
	saved, err := r.store.FindPayConfig(ctx, tenantID, WechatPayProvider)
	if err != nil || !saved.Enabled {
		return ""
	}
	stored := wechatPayStoredConfig{}
	if err := json.Unmarshal([]byte(saved.ConfigJSON), &stored); err != nil {
		return ""
	}
	return stored.MchID
}
