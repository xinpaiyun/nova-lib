// Package auth 提供 JWT 访问令牌签发与解析。
package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/xinpaiyun/nova-lib/config"
)

// Claims 定义 Nova API 访问令牌中的基础身份声明。
type Claims struct {
	UserID       uint64 `json:"userId"`
	TenantID     uint64 `json:"tenantId"`
	RoleCode     string `json:"roleCode"`
	SessionID    string `json:"sessionId"`
	TokenVersion int    `json:"tokenVersion"`
	jwt.RegisteredClaims
}

// IssueToken 签发包含用户、租户和角色信息的 JWT。
func IssueToken(cfg config.JWTConfig, userID uint64, tenantID uint64, roleCode string, sessionID string, tokenVersion int) (string, time.Time, error) {
	expiresAt := time.Now().Add(cfg.TokenTTL())
	claims := Claims{
		UserID:       userID,
		TenantID:     tenantID,
		RoleCode:     roleCode,
		SessionID:    sessionID,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(cfg.Secret))
	return signed, expiresAt, err
}

// ParseToken 解析并校验 JWT，返回 Nova 身份声明。
func ParseToken(cfg config.JWTConfig, tokenValue string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenValue, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unsupported signing method")
		}
		return []byte(cfg.Secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
