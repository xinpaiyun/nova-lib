package identity

import (
	"context"
	"errors"
	"strings"

	"github.com/xinpaiyun/nova-lib/sms"
	"github.com/xinpaiyun/nova-lib/wechat"
	"gorm.io/gorm"
)

// 身份域业务错误。
var (
	ErrNotFound      = errors.New("用户不存在")
	ErrCredential    = errors.New("账号或密码错误")
	ErrDisabled      = errors.New("账号已停用")
	ErrCodeInvalid   = errors.New("验证码无效或已过期")
	ErrWeakPassword  = errors.New("密码至少 6 位")
	ErrBindConflict  = errors.New("该身份已绑定其他账号")
	ErrUserExists    = errors.New("用户已存在")
	ErrWechatMissing = errors.New("微信小程序未配置")
)

// RoleResolver 根据用户解析入会话的角色码（授权语义由项目注入）。
type RoleResolver func(ctx context.Context, user *User) (string, error)

// ExtensionHook 在身份创建/绑定事务内执行项目扩展逻辑（写扩展表、绑角色/成员等）。
// 返回 error 会回滚整个事务。
type ExtensionHook func(ctx context.Context, tx *gorm.DB, userID uint64) error

// WechatClientResolver 按上下文（如 tenantID）返回对应微信客户端；返回 nil 时表示未配置微信。
type WechatClientResolver func(ctx context.Context) *wechat.Client

// WechatPhoneNotifier 在微信绑定成功后把用户手机号落库（部分项目授权后补全手机号）。
// 未配置时跳过。
type WechatPhoneNotifier func(ctx context.Context, tx *gorm.DB, userID uint64, phone string) error

// LoginService 提供统一登录/注册编排，建号与绑定均在事务内完成。
type LoginService struct {
	db     *gorm.DB
	hook   ExtensionHook
	phone  WechatPhoneNotifier
	wechat WechatClientResolver
}

// LoginServiceOption 配置 LoginService。
type LoginServiceOption func(*LoginService)

// WithExtensionHook 注入建号/绑定后的扩展回调。
func WithExtensionHook(hook ExtensionHook) LoginServiceOption {
	return func(s *LoginService) { s.hook = hook }
}

// WithWechatClientResolver 注入微信客户端解析回调。
func WithWechatClientResolver(resolver WechatClientResolver) LoginServiceOption {
	return func(s *LoginService) { s.wechat = resolver }
}

// WithWechatPhoneNotifier 注入微信绑定后手机号落库回调。
func WithWechatPhoneNotifier(notifier WechatPhoneNotifier) LoginServiceOption {
	return func(s *LoginService) { s.phone = notifier }
}

// NewLoginService 创建统一登录服务。
func NewLoginService(db *gorm.DB, options ...LoginServiceOption) *LoginService {
	s := &LoginService{db: db}
	for _, o := range options {
		o(s)
	}
	return s
}

// register 在事务内执行建号/绑定逻辑（统一入口，避免各调用点重复事务）。
func (s *LoginService) register(ctx context.Context, user *User, oauth *UserOAuth) (*User, error) {
	if user == nil {
		user = &User{}
	}
	// 无凭据时必须有第三方绑定，否则无法构成可识别账号。
	if oauth == nil && user.PasswordHash == "" && user.Phone == "" && user.Username == "" {
		return nil, errors.New("缺少账号标识")
	}
	var created *User
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Create(user).Error; err != nil {
			return err
		}
		created = user
		if oauth != nil {
			oauth.UserID = user.ID
			if err := tx.WithContext(ctx).Create(oauth).Error; err != nil {
				return err
			}
		}
		if s.hook != nil {
			if err := s.hook(ctx, tx, user.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// RegisterPassword 用用户名/手机号 + 密码创建账号（不含扩展 hook 外额外业务）。
func (s *LoginService) RegisterPassword(ctx context.Context, account string, password string, nickname string) (*User, error) {
	if len(strings.TrimSpace(password)) < 6 {
		return nil, ErrWeakPassword
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return nil, errors.New("账号不能为空")
	}
	user := &User{
		Username:     account,
		PasswordHash: hash,
		Nickname:     nickname,
		Status:       StatusEnabled,
	}
	if _, err := s.register(ctx, user, nil); err != nil {
		return nil, err
	}
	return user, nil
}

// PasswordLogin 用账号（用户名/手机号）+ 密码登录，返回用户（不含会话签发）。
func (s *LoginService) PasswordLogin(ctx context.Context, account string, password string) (*User, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return nil, ErrNotFound
	}
	var user User
	err := s.db.WithContext(ctx).
		Where("(username = ? AND username <> '') OR phone = ?", account, account).
		First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if user.Status != StatusEnabled {
		return nil, ErrDisabled
	}
	if !VerifyPassword(password, user.PasswordHash) {
		return nil, ErrCredential
	}
	return &user, nil
}

// SMSCodeLogin 用手机号 + 短信验证码登录；用户不存在时可选自动建号。
func (s *LoginService) SMSCodeLogin(ctx context.Context, phone string, code string) (*User, error) {
	if !sms.Enabled() {
		return nil, errors.New("短信服务未配置")
	}
	if !sms.VerifyCode(ctx, phone, code) {
		return nil, ErrCodeInvalid
	}
	return s.findOrCreateByPhone(ctx, phone)
}

// findOrCreateByPhone 按手机号查找，不存在则自动建号。
func (s *LoginService) findOrCreateByPhone(ctx context.Context, phone string) (*User, error) {
	phone = strings.TrimSpace(phone)
	var user User
	err := s.db.WithContext(ctx).Where("phone = ?", phone).First(&user).Error
	if err == nil {
		if user.Status != StatusEnabled {
			return nil, ErrDisabled
		}
		return &user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	created, err := s.register(ctx, &User{
		Phone:  phone,
		Status: StatusEnabled,
	}, nil)
	if err != nil {
		return nil, err
	}
	return created, nil
}

// WechatCodeLogin 用微信 code 登录，支持按 unionid 或 openid 匹配既有账号。
// openID 为空时按 dev: 前缀回退生成开发 openid（微信未启用场景）。
func (s *LoginService) WechatCodeLogin(ctx context.Context, loginCode string) (*User, error) {
	var client *wechat.Client
	if s.wechat != nil {
		client = s.wechat(ctx)
	}
	if client == nil || !client.Enabled() {
		if strings.HasPrefix(loginCode, "dev:") {
			return s.findOrCreateByWechat(ctx, "dev-"+strings.TrimPrefix(loginCode, "dev:"), "")
		}
		return nil, ErrWechatMissing
	}
	session, err := client.Session(ctx, loginCode)
	if err != nil {
		return nil, err
	}
	return s.findOrCreateByWechat(ctx, session.OpenID, session.UnionID)
}

// WechatPhoneLogin 微信 code 登录后，用手机号授权 code 再换取并绑定手机号。
func (s *LoginService) WechatPhoneLogin(ctx context.Context, loginCode string, phoneCode string) (*User, error) {
	client := s.wechat(ctx)
	if client == nil || !client.Enabled() {
		return nil, ErrWechatMissing
	}
	session, err := client.Session(ctx, loginCode)
	if err != nil {
		return nil, err
	}
	phone, err := client.FetchPhoneNumber(ctx, phoneCode)
	if err != nil {
		return nil, err
	}
	user, err := s.findOrCreateByWechat(ctx, session.OpenID, session.UnionID)
	if err != nil {
		return nil, err
	}
	if user.Phone == "" && phone != "" && s.phone != nil {
		if err := s.db.WithContext(ctx).Model(user).Update("phone", phone).Error; err != nil {
			return nil, err
		}
		user.Phone = phone
	}
	return user, nil
}

// findOrCreateByWechat 按 openid/unionid 查找用户并通过 oauth 表完成绑定。
func (s *LoginService) findOrCreateByWechat(ctx context.Context, openID string, unionID string) (*User, error) {
	openID = strings.TrimSpace(openID)
	if openID == "" {
		return nil, ErrWechatMissing
	}
	var user User
	// 1) 优先按 openid 精确匹配已绑定
	var oauth UserOAuth
	err := s.db.WithContext(ctx).
		Where("platform = ? AND open_id = ?", PlatformWechat, openID).
		First(&oauth).Error
	if err == nil {
		if err := s.db.WithContext(ctx).Where("id = ?", oauth.UserID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if user.Status != StatusEnabled {
			return nil, ErrDisabled
		}
		return &user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	// 2) 按 unionid 匹配既有账号绑定新 openid
	if unionID != "" {
		var existing UserOAuth
		err = s.db.WithContext(ctx).
			Where("platform = ? AND union_id = ?", PlatformWechat, unionID).
			First(&existing).Error
		if err == nil {
			if err := s.db.WithContext(ctx).Where("id = ?", existing.UserID).First(&user).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, ErrNotFound
				}
				return nil, err
			}
			// 绑定新 openid 到该账号
			if err := s.db.WithContext(ctx).Create(&UserOAuth{
				UserID:   user.ID,
				Platform: PlatformWechat,
				OpenID:   openID,
				UnionID:  unionID,
			}).Error; err != nil {
				return nil, err
			}
			if user.Status != StatusEnabled {
				return nil, ErrDisabled
			}
			return &user, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	// 3) 全新建号并绑定 openid
	created, err := s.register(ctx, &User{Status: StatusEnabled}, &UserOAuth{
		Platform: PlatformWechat,
		OpenID:   openID,
		UnionID:  unionID,
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// ChangePassword 校验旧密码并更新密码哈希（改密契约，newPassword 至少 6 位）。
func (s *LoginService) ChangePassword(ctx context.Context, userID uint64, oldPassword string, newPassword string) error {
	if len(strings.TrimSpace(newPassword)) < 6 {
		return ErrWeakPassword
	}
	var user User
	if err := s.db.WithContext(ctx).Where("id = ?", userID).First(&user).Error; err != nil {
		return err
	}
	if !VerifyPassword(oldPassword, user.PasswordHash) {
		return ErrCredential
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&user).Update("password_hash", hash).Error
}

// GetByID 按主键获取用户（含软删过滤）。
func (s *LoginService) GetByID(ctx context.Context, userID uint64) (*User, error) {
	var user User
	if err := s.db.WithContext(ctx).Where("id = ?", userID).First(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}