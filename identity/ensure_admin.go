package identity

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// SuperAdminConfig 定义超管初始化的账号要素（对应 config.Bootstrap 超管节）。
type SuperAdminConfig struct {
	Enabled  bool
	Account  string // 超管登录账号（phone 或 username，由 AccountField 决定语义）
	Password string
	Nickname string
	Email    string
}

// AccountField 是超管定位字段常量。
const (
	AccountFieldPhone   = "phone"
	AccountFieldUsername = "username"
)

// SuperAdminScope 收敛各项目超管初始化的差异点。
type SuperAdminScope struct {
	Config       SuperAdminConfig
	AccountField string // AccountFieldPhone / AccountFieldUsername
	// WeakPassword 判定弱密码（默认 <6 位即拒绝）；nil 用默认。
	WeakPassword func(password string) bool
	// BindPlatformRole 在超管 upsert 成功后绑定平台角色（写扩展表/角色绑定表）。
	// nil 表示无需额外绑定。
	BindPlatformRole func(ctx context.Context, db *gorm.DB, user *User) error
	// DefaultNickname 未提供昵称时的兜底显示名。
	DefaultNickname string
}

// EnsureSuperAdmin 幂等地创建/更新默认超级管理员账号。
//
// 收敛 app/baoxian/xingxueji/chehuixing 各自独立的超管实现；
// 依赖统一 users 表（identity.User），超管角色绑定经 scope.BindPlatformRole 注入。
func EnsureSuperAdmin(ctx context.Context, db *gorm.DB, scope SuperAdminScope) error {
	if !scope.Config.Enabled {
		return nil
	}
	account := strings.TrimSpace(scope.Config.Account)
	if account == "" {
		return errors.New("超管账号不能为空")
	}
	field := scope.AccountField
	if field == "" {
		field = AccountFieldUsername
	}
	if field != AccountFieldPhone && field != AccountFieldUsername {
		return errors.New("超管定位字段不合法")
	}
	password := strings.TrimSpace(scope.Config.Password)
	weak := scope.WeakPassword
	if weak == nil {
		weak = func(p string) bool { return len(p) < 6 }
	}
	if weak(password) {
		return errors.New("超管密码过弱（至少 6 位）")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	nickname := strings.TrimSpace(scope.Config.Nickname)
	if nickname == "" {
		nickname = scope.DefaultNickname
	}
	if nickname == "" {
		nickname = "平台超级管理员"
	}

	var user User
	query := db.WithContext(ctx).Where(field+" = ?", account)
	err = query.First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		user = User{
			Status:       StatusEnabled,
			Nickname:     nickname,
			PasswordHash: hash,
		}
		if scope.Config.Email != "" {
			user.Email = scope.Config.Email
		}
		if field == AccountFieldPhone {
			user.Phone = account
		} else {
			user.Username = account
		}
		if err := db.WithContext(ctx).Create(&user).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		updates := map[string]any{
			"password_hash": hash,
			"status":        StatusEnabled,
		}
		if strings.TrimSpace(user.Nickname) == "" {
			updates["nickname"] = nickname
		}
		if scope.Config.Email != "" {
			updates["email"] = scope.Config.Email
		}
		if err := db.WithContext(ctx).Model(&user).Updates(updates).Error; err != nil {
			return err
		}
	}
	if scope.BindPlatformRole != nil {
		if err := scope.BindPlatformRole(ctx, db, &user); err != nil {
			return err
		}
	}
	return nil
}