// Package tenantpay - 机密字段加密：AES-256-GCM（平台主密钥模式）。
// 密文格式与宿主项目 shared/crypto 保持兼容（enc.v1: 前缀），同一主密钥可互通加解密。
package tenantpay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrInvalidMasterKey 表示主密钥格式不合法（需 32 字节 base64/hex/原文）。
var ErrInvalidMasterKey = errors.New("支付主密钥格式不合法，需 32 字节")

// ErrCiphertextInvalid 表示密文格式不合法或主密钥不匹配。
var ErrCiphertextInvalid = errors.New("密文解析失败")

// cipherPrefix 标识 AES-GCM 加密负载，便于将来轮换算法。
const cipherPrefix = "enc.v1:"

// Encrypt 使用主密钥加密明文，返回 base64 密文（带算法前缀）。明文为空时原样返回。
func Encrypt(masterKey, plaintext string) (string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return "", nil
	}
	key, err := parseMasterKey(masterKey)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return cipherPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 产生的密文。非本工具前缀的值原样返回（兼容明文历史数据）。
func Decrypt(masterKey, ciphertext string) (string, error) {
	ciphertext = strings.TrimSpace(ciphertext)
	if ciphertext == "" {
		return "", nil
	}
	encoded, ok := strings.CutPrefix(ciphertext, cipherPrefix)
	if !ok {
		return ciphertext, nil
	}
	key, err := parseMasterKey(masterKey)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrCiphertextInvalid
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", ErrCiphertextInvalid
	}
	plaintext, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrCiphertextInvalid, err)
	}
	return string(plaintext), nil
}

// newGCM 构建 AES-256-GCM 实例。
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// parseMasterKey 解析主密钥：优先 32 字节 base64，否则按原文补齐/截断为 32 字节（开发便利）。
func parseMasterKey(masterKey string) ([]byte, error) {
	masterKey = strings.TrimSpace(masterKey)
	if masterKey == "" {
		return nil, ErrInvalidMasterKey
	}
	if decoded, err := base64.StdEncoding.DecodeString(masterKey); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	if len(masterKey) >= 32 {
		return []byte(masterKey[:32]), nil
	}
	padded := make([]byte, 32)
	copy(padded, masterKey)
	return padded, nil
}
