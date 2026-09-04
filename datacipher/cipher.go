// Package datacipher 提供敏感业务数据的 AES-GCM 加密存储、HMAC-SHA256 检索哈希和字段脱敏能力。
package datacipher

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const encryptedPrefix = "enc:v1:"

var manager *dataSecurityManager

type dataSecurityManager struct {
	encryptKey []byte
	hashKey    []byte
}

// Configure 初始化数据加密与检索哈希能力。
// encryptKey 与 hashKey 为空时从环境变量读取，仍为空时使用 fallbackSecret 派生。
func Configure(encryptKey, hashKey, fallbackSecret string) {
	encSource := firstNonEmpty(os.Getenv("APP_DATA_ENCRYPT_KEY"), encryptKey, fallbackSecret)
	hashSource := firstNonEmpty(os.Getenv("APP_DATA_HASH_KEY"), hashKey, fallbackSecret)
	manager = &dataSecurityManager{
		encryptKey: deriveKey(encSource, "encrypt"),
		hashKey:    deriveKey(hashSource, "hash"),
	}
}

// EncryptString 加密字符串，空值直接返回。
func EncryptString(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	if IsEncrypted(value) {
		return value, nil
	}
	if manager == nil {
		return value, nil
	}
	block, err := aes.NewCipher(manager.encryptKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	cipherText := gcm.Seal(nonce, nonce, []byte(value), nil)
	return encryptedPrefix + base64.RawURLEncoding.EncodeToString(cipherText), nil
}

// DecryptString 解密字符串，兼容历史明文。
func DecryptString(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	if !IsEncrypted(value) || manager == nil {
		return value, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, encryptedPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(manager.encryptKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("密文格式错误")
	}
	plain, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// IsEncrypted 判断字符串是否为当前版本密文。
func IsEncrypted(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), encryptedPrefix)
}

// LookupHash 生成查询使用的稳定 HMAC-SHA256 哈希。
func LookupHash(value string) string {
	normalized := normalizeLookup(value)
	if normalized == "" {
		return ""
	}
	if manager == nil {
		sum := sha256.Sum256([]byte(normalized))
		return hex.EncodeToString(sum[:])
	}
	mac := hmac.New(sha256.New, manager.hashKey)
	_, _ = mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}

// MaskName 脱敏个人姓名。
func MaskName(value string) string {
	runes := []rune(strings.TrimSpace(value))
	switch len(runes) {
	case 0:
		return ""
	case 1:
		return "*"
	case 2:
		return string(runes[0]) + "*"
	default:
		return string(runes[0]) + strings.Repeat("*", len(runes)-2) + string(runes[len(runes)-1])
	}
}

// MaskPhone 脱敏手机号或联系电话。
func MaskPhone(value string) string {
	return maskMiddle(value, 3, 4)
}

// MaskIDCard 脱敏身份证号。
func MaskIDCard(value string) string {
	return maskMiddle(value, 4, 4)
}

// MaskAddress 脱敏地址，保留前缀便于识别。
func MaskAddress(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= 6 {
		return string(runes[:min(len(runes), 2)]) + "***"
	}
	return string(runes[:6]) + "****"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func deriveKey(value, purpose string) []byte {
	sum := sha256.Sum256([]byte(purpose + ":" + value))
	key := make([]byte, len(sum))
	copy(key, sum[:])
	return key
}

func normalizeLookup(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func maskMiddle(value string, prefix, suffix int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	length := utf8.RuneCountInString(trimmed)
	if length <= prefix+suffix {
		if length <= 2 {
			return strings.Repeat("*", length)
		}
		runes := []rune(trimmed)
		return string(runes[0]) + strings.Repeat("*", length-2) + string(runes[length-1])
	}
	runes := []rune(trimmed)
	return string(runes[:prefix]) + strings.Repeat("*", length-prefix-suffix) + string(runes[length-suffix:])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
