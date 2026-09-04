// Package password 提供登录密码的基础校验。
package password

import (
	"fmt"
	"strings"
)

// Validate 校验用户登录密码是否填写，适用于注册、改密、后台建号和邀请接受。
func Validate(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("请输入密码")
	}
	return nil
}
