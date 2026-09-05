package identity

import "golang.org/x/crypto/bcrypt"

// bcryptDefaultCost 与各项目既有实现保持一致，保证历史哈希可继续校验。
const bcryptDefaultCost = bcrypt.DefaultCost

// HashPassword 使用 bcrypt 散列明文密码。
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptDefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// IsBcryptHash 判断给定密码串是否为 bcrypt 哈希（$2a$/$2b$/$2y$/$2x$/$2$ 前缀）。
func IsBcryptHash(encoded string) bool {
	if len(encoded) < 4 {
		return false
	}
	if encoded[0] != '$' {
		return false
	}
	// "$2a$..." → [0]='$',[1]='2',[2]='a',[3]='$'；兼容旧版 "$2$..."。
	if encoded[1] == '2' && encoded[3] == '$' {
		return true
	}
	if encoded[1] == 'a' || encoded[1] == 'b' || encoded[1] == 'y' || encoded[1] == 'x' {
		return encoded[3] == '$'
	}
	return false
}

// VerifyPassword 校验明文密码与历史存储哈希：
// - bcrypt 哈希走标准比对；
// - 非 bcrypt（历史明文存储）做等值比对，兼容早期项目。
func VerifyPassword(password, stored string) bool {
	if IsBcryptHash(stored) {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil
	}
	return stored != "" && stored == password
}