// Package identity 提供全公司统一的用户身份域：用户主表、微信/第三方绑定子表、
// 密码散列、统一登录服务（密码/短信/微信 code2session）、服务端会话签发与超管初始化。
//
// identity 是编排层，只依赖 nova-lib 的 auth/sms/wechat/config/database 基础能力，
// 不改写这些包的既有职责。各项目通过回调（RoleResolver/extensionHook/WechatClientResolver/
// SuperAdminScope.BindPlatformRole）注入自身业务语义。
package identity

import "time"

// Status 定义 users 表 status 字段约定：1 正常，0 停用。
const (
	StatusDisabled int8 = 0
	StatusEnabled  int8 = 1
)

// User 是全公司统一的用户主表模型，物理表名保持 `users`。
//
// 各项目强业务字段（如角色、会员、团队、商户关联）不在本表，而放在与该用户的
// 1:1 扩展表（user_profiles / user_security_profiles 等）中，避免通用表被业务污染。
type User struct {
	ID           uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Username     string     `gorm:"column:username;size:80;index" json:"username"`
	Phone        string     `gorm:"column:phone;size:20;index" json:"phone"`
	Email        string     `gorm:"column:email;size:120;index" json:"email"`
	PasswordHash string     `gorm:"column:password_hash;size:255;not null" json:"-"`
	Nickname     string     `gorm:"column:nickname;size:80" json:"nickname"`
	AvatarURL    string     `gorm:"column:avatar_url;size:500" json:"avatarUrl"`
	Status       int8       `gorm:"column:status;not null;default:1;index" json:"status"`
	LastLoginAt  *time.Time `gorm:"column:last_login_at;index" json:"lastLoginAt"`
	CreatedAt    time.Time  `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt    time.Time  `gorm:"column:updated_at" json:"updatedAt"`
	DeletedAt    *time.Time `gorm:"column:deleted_at;index" json:"-"`
}

// TableName 返回用户表名。
func (User) TableName() string { return "users" }

// UserOAuth 是用户第三方（含微信）身份绑定子表，物理表名 `user_oauths`。
// 支持一个用户绑定多个平台/openid（同一开放平台下 unionid 可关联多个小程序 openid）。
type UserOAuth struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    uint64    `gorm:"column:user_id;not null;index" json:"userId"`
	Platform  string    `gorm:"column:platform;size:32;not null" json:"platform"`
	OpenID    string    `gorm:"column:open_id;size:64;not null" json:"openId"`
	UnionID   string    `gorm:"column:union_id;size:64;index" json:"unionId"`
	CreatedAt time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName 返回第三方绑定表名。
func (UserOAuth) TableName() string { return "user_oauths" }

// platform 常量，用于 UserOAuth.Platform。
const (
	PlatformWechat = "wechat"
	PlatformAlipay = "alipay"
)

// ModelTypes 返回身份域需要自动迁移的数据模型。
func ModelTypes() []any {
	return []any{&User{}, &UserOAuth{}}
}