package identity

import (
	"context"
	"time"

	"github.com/xinpaiyun/nova-lib/auth"
	"github.com/xinpaiyun/nova-lib/config"
)

// sessionOpts 承载会话签发所需的可选项（AppType/OpenID 等业务身份维度）。
type sessionOpts struct {
	appType string
	openID  string
}

// Option 配置会话签发选项。
type Option func(*sessionOpts)

// WithAppType 指定会话的应用类型维度（如 xingxueji 三态 user/store/admin）。
func WithAppType(appType string) Option {
	return func(o *sessionOpts) { o.appType = appType }
}

// WithOpenID 指定会话关联的微信/第三方 openid。
func WithOpenID(openID string) Option {
	return func(o *sessionOpts) { o.openID = openID }
}

// Issuer 负责会话签发，聚合 nova-lib auth 的随机 Token + Redis Session。
type Issuer struct {
	cfg        config.JWTConfig
	resolver   RoleResolver
	sessionTTL time.Duration
}

// IssuerOption 配置 Issuer。
type IssuerOption func(*Issuer)

// WithRoleResolver 注入从用户解析入会话角色码的回调。
func WithRoleResolver(resolver RoleResolver) IssuerOption {
	return func(i *Issuer) { i.resolver = resolver }
}

// WithSessionTTL 覆盖会话 TTL（默认取 cfg.TokenTTL）。
func WithSessionTTL(ttl time.Duration) IssuerOption {
	return func(i *Issuer) {
		if ttl > 0 {
			i.sessionTTL = ttl
		}
	}
}

// NewIssuer 创建统一会话签发器。
func NewIssuer(cfg config.JWTConfig, options ...IssuerOption) *Issuer {
	i := &Issuer{cfg: cfg}
	for _, o := range options {
		o(i)
	}
	return i
}

// IssueResult 是一次登录签发的完整结果。
type IssueResult struct {
	Token     string
	ExpiresAt time.Time
	SessionID string
	TenantID  uint64
	RoleCode  string
	AppType   string
	OpenID    string
}

// Issue 为指定用户签发服务端会话并写入 Redis。
// roleCode 为空时若配置了 RoleResolver 则回退解析；tenantID 由调用方按业务传入。
func (i *Issuer) Issue(ctx context.Context, user *User, tenantID uint64, roleCode string, options ...Option) (*IssueResult, error) {
	return i.issue(ctx, user, tenantID, roleCode, options...)
}

func (i *Issuer) issue(ctx context.Context, user *User, tenantID uint64, roleCode string, options ...Option) (*IssueResult, error) {
	opts := sessionOpts{}
	for _, o := range options {
		o(&opts)
	}
	if roleCode == "" && i.resolver != nil {
		code, err := i.resolver(ctx, user)
		if err != nil {
			return nil, err
		}
		roleCode = code
	}
	token, err := auth.GenerateToken()
	if err != nil {
		return nil, err
	}
	ttl := i.sessionTTL
	if ttl == 0 {
		ttl = i.cfg.TokenTTL()
	}
	now := time.Now()
	session := auth.Session{
		UserID:    user.ID,
		TenantID:  tenantID,
		RoleCode:  roleCode,
		AppType:   opts.appType,
		OpenID:    opts.openID,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	sessionID := auth.TokenHash(token)
	if err := auth.StoreSession(ctx, token, session); err != nil {
		return nil, err
	}
	return &IssueResult{
		Token:     token,
		ExpiresAt: session.ExpiresAt,
		SessionID: sessionID,
		TenantID:  tenantID,
		RoleCode:  roleCode,
		AppType:   opts.appType,
		OpenID:    opts.openID,
	}, nil
}

// Revoke 注销指定访问 Token 的服务端会话。
func (i *Issuer) Revoke(ctx context.Context, token string) error {
	return auth.RevokeSession(ctx, token)
}

// Resolve 解析当前访问 Token 对应的会话声明。validator 非空时额外执行业务校验。
func (i *Issuer) Resolve(ctx context.Context, token string) (*auth.Claims, error) {
	claims, err := auth.ResolveClaims(ctx, token)
	if err != nil {
		return nil, err
	}
	return claims, nil
}