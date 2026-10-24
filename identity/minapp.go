package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/wechat"
	"gorm.io/gorm"
)

// ErrAppNotBound 表示请求的小程序 appid 未接入或已停用。
var ErrAppNotBound = errors.New("小程序未接入或已停用")

// WechatAppCredentials 小程序登录凭据（按 appid 定位）。
type WechatAppCredentials struct {
	// TenantID 凭据归属租户，登录成功即会话租户（多商户各租户独立小程序）。
	TenantID uint64
	AppID    string
	AppSecret string
}

// WechatAppSource 按 appid 解析小程序登录凭据（项目注入凭据源：tenantpay 加密表/
// integration_configs/本地配置表）。小程序未接入时返回包装 ErrAppNotBound 的错误。
type WechatAppSource func(ctx context.Context, appID string) (*WechatAppCredentials, error)

// MiniAppFlow 小程序微信登录编排壳：把各项目重复的
// “appid 定位凭据 → 构造 LoginService → code 登录（可选手机号授权）→ 查 openid →
// Issuer 签发会话”编排沉淀为一次调用；角色解析经 Issuer 的 RoleResolver 承载，
// 建号扩展经 LoginServiceOption 透传，登录响应 VO 形状留在各项目自行组装。
type MiniAppFlow struct {
	db       *gorm.DB
	source   WechatAppSource
	issuer   *Issuer
	loginOpts []LoginServiceOption
	appType  string
}

// MiniAppFlowOption 配置小程序登录编排壳。
type MiniAppFlowOption func(*MiniAppFlow)

// WithMiniAppServiceOptions 透传 LoginService 选项（ExtensionHook/WechatPhoneNotifier 等）。
func WithMiniAppServiceOptions(opts ...LoginServiceOption) MiniAppFlowOption {
	return func(f *MiniAppFlow) { f.loginOpts = append(f.loginOpts, opts...) }
}

// WithMiniAppAppType 指定会话的应用类型维度（默认 "miniapp"）。
func WithMiniAppAppType(appType string) MiniAppFlowOption {
	return func(f *MiniAppFlow) { f.appType = appType }
}

// NewMiniAppFlow 创建小程序登录编排壳。
func NewMiniAppFlow(db *gorm.DB, source WechatAppSource, issuer *Issuer, options ...MiniAppFlowOption) *MiniAppFlow {
	f := &MiniAppFlow{db: db, source: source, issuer: issuer, appType: "miniapp"}
	for _, o := range options {
		o(f)
	}
	return f
}

// MiniAppLoginInput 小程序登录入参。
type MiniAppLoginInput struct {
	AppID string // 小程序 appid（定位凭据）
	Code  string // wx.login code（凭据缺失且带 dev: 前缀时走 identity 开发兜底）
	// PhoneCode 可选手机号授权 code，非空时走 WechatPhoneLogin（登录同时绑定手机号）；
	// 该路径必须凭据可用，无 dev 兜底。
	PhoneCode string
}

// MiniAppLoginResult 一次小程序登录的完整结果（项目自行映射响应 VO）。
type MiniAppLoginResult struct {
	Token     string
	ExpiresAt time.Time
	SessionID string
	TenantID  uint64
	RoleCode  string
	AppType   string
	OpenID    string
	User      *User
}

// Login 执行小程序登录编排：凭据解析 → code 登录 → openid 关联 → 会话签发。
func (f *MiniAppFlow) Login(ctx context.Context, in MiniAppLoginInput) (*MiniAppLoginResult, error) {
	if f.source == nil {
		return nil, errors.New("小程序凭据源未配置")
	}
	if f.issuer == nil {
		return nil, errors.New("会话签发器未配置")
	}
	appID := strings.TrimSpace(in.AppID)
	code := strings.TrimSpace(in.Code)
	if appID == "" || code == "" {
		return nil, errors.New("缺少小程序登录凭证")
	}
	// dev 兜底语义由 client 启用状态与 code 前缀共同决定（identity 内部处理）：
	// 凭据未接入（source 返回 ErrAppNotBound）且 code 带 dev: 前缀时放行建开发号，
	// 会话租户记 0；凭据已绑定但 secret 未配置时 client 未启用，同样走 dev 兜底，
	// 会话租户保持凭据归属租户；凭据完整时 dev: code 照常调微信（开发前缀会被拒绝）。
	cred, err := f.source(ctx, appID)
	if err != nil && (!errors.Is(err, ErrAppNotBound) || !strings.HasPrefix(code, "dev:")) {
		return nil, err
	}
	if cred == nil {
		cred = &WechatAppCredentials{}
	}
	// 按请求 appid 构造微信客户端（各小程序独立 appid/secret，openid 天然按 appid 隔离）；
	// 凭据为空对象时 client 为 nil，走 identity 的 dev 兜底。
	client := wechat.NewClient(config.WechatConfig{AppID: cred.AppID, AppSecret: cred.AppSecret})
	opts := make([]LoginServiceOption, 0, len(f.loginOpts)+1)
	opts = append(opts, f.loginOpts...)
	opts = append(opts, WithWechatClientResolver(func(ctx context.Context) *wechat.Client {
		return client
	}))
	login := NewLoginService(f.db, opts...)
	var user *User
	if strings.TrimSpace(in.PhoneCode) != "" {
		user, err = login.WechatPhoneLogin(ctx, code, in.PhoneCode)
	} else {
		user, err = login.WechatCodeLogin(ctx, code)
	}
	if err != nil {
		return nil, err
	}
	openID, err := f.latestOpenID(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	result, err := f.issuer.Issue(ctx, user, cred.TenantID, "", WithOpenID(openID), WithAppType(f.appType))
	if err != nil {
		return nil, err
	}
	return &MiniAppLoginResult{
		Token:     result.Token,
		ExpiresAt: result.ExpiresAt,
		SessionID: result.SessionID,
		TenantID:  result.TenantID,
		RoleCode:  result.RoleCode,
		AppType:   result.AppType,
		OpenID:    openID,
		User:      user,
	}, nil
}

// latestOpenID 返回用户最近一次绑定的微信 openid（未绑定为空串；查询异常返回错误）。
func (f *MiniAppFlow) latestOpenID(ctx context.Context, userID uint64) (string, error) {
	var oauth UserOAuth
	err := f.db.WithContext(ctx).
		Where("user_id = ? AND platform = ?", userID, PlatformWechat).
		Order("id DESC").First(&oauth).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return oauth.OpenID, nil
}
