// Package config 定义 nova-lib 各能力包共享的配置结构。
//
// 各项目在自身的 config.yaml 加载逻辑中直接嵌入这些结构体，
// 从而保证数据库、Redis、JWT、短信、微信等基础配置格式全公司统一。
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// DatabaseConfig 定义 GORM 数据库连接配置。
type DatabaseConfig struct {
	Driver                 string `yaml:"driver"`
	Host                   string `yaml:"host"`
	Port                   int    `yaml:"port"`
	User                   string `yaml:"user"`
	Password               string `yaml:"password"`
	Name                   string `yaml:"name"`
	Charset                string `yaml:"charset"`
	ParseTime              bool   `yaml:"parse_time"`
	Loc                    string `yaml:"loc"`
	SQLitePath             string `yaml:"sqlite_path"`
	MaxOpenConns           int    `yaml:"max_open_conns"`
	MaxIdleConns           int    `yaml:"max_idle_conns"`
	ConnMaxLifetimeMinutes int    `yaml:"conn_max_lifetime_minutes"`
}

// DSN 按分离配置字段组装 MySQL DSN；loc 含特殊字符时做 URL 编码。
func (d DatabaseConfig) DSN() string {
	charset := strings.TrimSpace(d.Charset)
	if charset == "" {
		charset = "utf8mb4"
	}
	loc := strings.TrimSpace(d.Loc)
	if loc == "" {
		loc = "Local"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%t&loc=%s",
		d.User, d.Password, d.Host, d.Port, d.Name, charset, d.ParseTime, url.PathEscape(loc))
}

// RedisConfig 定义 Redis 连接配置。
type RedisConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// JWTConfig 定义 JWT 认证配置。
type JWTConfig struct {
	Secret     string `yaml:"secret"`
	ExpireHour int    `yaml:"expire_hour"`
}

// TokenTTL 返回 JWT 有效期。
func (c JWTConfig) TokenTTL() time.Duration {
	return time.Duration(c.ExpireHour) * time.Hour
}

// CORSConfig 定义跨域访问控制配置。
type CORSConfig struct {
	AllowedOrigins   []string `yaml:"allowed_origins"`
	AllowCredentials bool     `yaml:"allow_credentials"`
}

// RateLimitConfig 定义 API 请求限流配置。
type RateLimitConfig struct {
	Enabled          bool `yaml:"enabled"`
	RequestsPerMin   int  `yaml:"requests_per_min"`
	TrustForwardedIP bool `yaml:"trust_forwarded_ip"`
}

// SecurityHeadersConfig 定义 HTTP 安全响应头配置。
type SecurityHeadersConfig struct {
	Enabled bool `yaml:"enabled"`
}

// TenancyConfig 定义 SaaS、多租户和用户租户隔离业务形态。
type TenancyConfig struct {
	SaaSEnabled                bool `yaml:"saas_enabled"`
	MultiTenantEnabled         bool `yaml:"multi_tenant_enabled"`
	UserTenantIsolationEnabled bool `yaml:"user_tenant_isolation_enabled"`
}

// StorageConfig 定义本地文件存储或 S3 兼容对象存储配置。
type StorageConfig struct {
	Driver          string `yaml:"driver"`
	LocalDir        string `yaml:"local_dir"`
	LocalBaseURL    string `yaml:"local_base_url"`
	MaxUploadBytes  int64  `yaml:"max_upload_bytes"`
	Endpoint        string `yaml:"endpoint"`
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	BucketName      string `yaml:"bucket_name"`
	Region          string `yaml:"region"`
	UseSSL          bool   `yaml:"use_ssl"`
	ForcePathStyle  bool   `yaml:"force_path_style"`
	PublicBaseURL   string `yaml:"public_base_url"`
}

// SMSConfig 定义短信验证码服务配置。
type SMSConfig struct {
	Enabled         bool   `yaml:"enabled"`
	Provider        string `yaml:"provider"`
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	SignName        string `yaml:"sign_name"`
	TemplateCode    string `yaml:"template_code"`
}

// WechatConfig 定义微信小程序登录与微信支付 V3 配置。
type WechatConfig struct {
	AppID         string `yaml:"app_id"`
	AppSecret     string `yaml:"app_secret"`
	MchID         string `yaml:"mch_id"`
	APIKeyV3      string `yaml:"api_key_v3"`
	APIBaseURL    string `yaml:"api_base_url"`
	CertPath      string `yaml:"cert_path"`
	CertSerialNo  string `yaml:"cert_serial_no"`
	PublicKeyPath string `yaml:"public_key_path"`
	PublicKeyID   string `yaml:"public_key_id"`
	NotifyURL     string `yaml:"notify_url"`
}

// MailConfig 定义 SMTP 邮件服务配置。
type MailConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
}

// OCRConfig 定义阿里云 OCR 配置。
type OCRConfig struct {
	Enabled         bool   `yaml:"enabled"`
	Endpoint        string `yaml:"endpoint"`
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
}

// AIConfig 定义 OpenAI 兼容模型服务配置。
type AIConfig struct {
	Enabled     bool   `yaml:"enabled"`
	BaseURL     string `yaml:"base_url"`
	APIKey      string `yaml:"api_key"`
	Model       string `yaml:"model"`
	TextModel   string `yaml:"text_model"`
	VisionModel string `yaml:"vision_model"`
	TimeoutSec  int    `yaml:"timeout_sec"`
}

// AlipayConfig 定义支付宝小程序开放平台参数。
type AlipayConfig struct {
	AppID          string `yaml:"app_id"`
	PrivateKey     string `yaml:"private_key"`
	PrivateKeyPath string `yaml:"private_key_path"`
	PublicKey      string `yaml:"public_key"`
	PublicKeyPath  string `yaml:"public_key_path"`
	EncryptKey     string `yaml:"encrypt_key"`
	GatewayURL     string `yaml:"gateway_url"`
	NotifyURL      string `yaml:"notify_url"`
}

// TencentMapConfig 定义腾讯位置服务 WebService API 配置。
type TencentMapConfig struct {
	WebServiceKey string `yaml:"webservice_key"`
	WebServiceSK  string `yaml:"webservice_sk"`
}

// ShengwangConfig 定义声网 RTC 与实时语音转写配置。
type ShengwangConfig struct {
	Enabled               bool   `yaml:"enabled"`
	AppID                 string `yaml:"app_id"`
	AppCertificate        string `yaml:"app_certificate"`
	CustomerKey           string `yaml:"customer_key"`
	CustomerSecret        string `yaml:"customer_secret"`
	Region                string `yaml:"region"`
	APIBaseURL            string `yaml:"api_base_url"`
	SDKDomain             string `yaml:"sdk_domain"`
	WebSDKVersion         string `yaml:"web_sdk_version"`
	JoinLinkSecret        string `yaml:"join_link_secret"`
	TokenExpireSeconds    int    `yaml:"token_expire_seconds"`
	RoomCloseDelaySeconds int    `yaml:"room_close_delay_seconds"`
	STTEnabled            bool   `yaml:"stt_enabled"`
	STTLanguage           string `yaml:"stt_language"`
	STTMaxIdleSeconds     int    `yaml:"stt_max_idle_seconds"`
}

// AllowsWildcard 返回 CORS 配置是否允许任意来源。
func (c CORSConfig) AllowsWildcard() bool {
	if len(c.AllowedOrigins) == 0 {
		return true
	}
	for _, origin := range c.AllowedOrigins {
		if strings.TrimSpace(origin) == "*" {
			return true
		}
	}
	return false
}

// IsLocal 返回当前是否使用本地文件存储。
func (c StorageConfig) IsLocal() bool {
	return strings.TrimSpace(c.Driver) == "" || strings.EqualFold(c.Driver, "local")
}

// UploadLimitBytes 返回单文件上传大小限制。
func (c StorageConfig) UploadLimitBytes() int64 {
	if c.MaxUploadBytes <= 0 {
		return 20 * 1024 * 1024
	}
	return c.MaxUploadBytes
}

// HasPayConfig 返回是否已完整配置微信支付 V3 参数。
func (c WechatConfig) HasPayConfig() bool {
	return strings.TrimSpace(c.AppID) != "" &&
		strings.TrimSpace(c.MchID) != "" &&
		strings.TrimSpace(c.APIKeyV3) != "" &&
		strings.TrimSpace(c.CertPath) != "" &&
		strings.TrimSpace(c.CertSerialNo) != "" &&
		strings.TrimSpace(c.PublicKeyPath) != "" &&
		strings.TrimSpace(c.PublicKeyID) != "" &&
		strings.TrimSpace(c.NotifyURL) != ""
}

// BootstrapConfig 定义首次启动时的默认租户和管理员配置。
type BootstrapConfig struct {
	AutoCreateAdmin  bool   `yaml:"auto_create_admin"`
	TenantName       string `yaml:"tenant_name"`
	AdminPhone       string `yaml:"admin_phone"`
	AdminEmail       string `yaml:"admin_email"`
	AdminPassword    string `yaml:"admin_password"`
	AdminDisplayName string `yaml:"admin_display_name"`
}
