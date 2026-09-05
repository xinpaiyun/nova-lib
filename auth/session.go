package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/xinpaiyun/nova-lib/cache"
)

const sessionCachePrefix = "auth:session:"

// Session 表示服务端保存的 Redis 登录会话，是访问身份的唯一可信来源。
type Session struct {
	TokenHash string    `json:"tokenHash"`
	UserID    uint64    `json:"userId"`
	TenantID  uint64    `json:"tenantId"`
	RoleCode  string    `json:"roleCode"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// GenerateToken 生成服务端会话使用的随机访问 Token（会话模式下无需 JWT 签名）。
func GenerateToken() (string, error) {
	token := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, token); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

// TokenHash 返回服务端存储和查询会话使用的 SHA-256 摘要。
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// StoreSession 将服务端会话写入缓存，TTL 取会话剩余有效期。
func StoreSession(ctx context.Context, token string, session Session) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("token 不能为空")
	}
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if !session.ExpiresAt.After(now) {
		return errors.New("token 过期时间无效")
	}
	if session.UserID == 0 {
		return errors.New("会话缺少用户身份")
	}
	session.TokenHash = TokenHash(token)
	return cache.SetJSON(ctx, sessionCacheKey(session.TokenHash), session, time.Until(session.ExpiresAt))
}

// ResolveSession 根据客户端 Token 解析服务端会话。
// 不查询数据库：账号停用等状态由登录时校验，封禁可通过删除会话键即时生效。
func ResolveSession(ctx context.Context, token string) (*Session, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("token 不能为空")
	}
	var session Session
	ok, err := cache.GetJSON(ctx, sessionCacheKey(TokenHash(token)), &session)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("登录状态已失效")
	}
	if !session.ExpiresAt.After(time.Now()) {
		return nil, errors.New("登录状态已过期")
	}
	if session.UserID == 0 {
		return nil, errors.New("登录状态异常")
	}
	return &session, nil
}

// ResolveClaims 解析会话并返回统一的身份声明，供中间件注入请求上下文。
func ResolveClaims(ctx context.Context, token string) (*Claims, error) {
	session, err := ResolveSession(ctx, token)
	if err != nil {
		return nil, err
	}
	return ClaimsFromSession(*session), nil
}

// RevokeSession 删除客户端 Token 对应的会话（登出或踢下线）。
func RevokeSession(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("token 不能为空")
	}
	return cache.Del(ctx, sessionCacheKey(TokenHash(token)))
}

// ClaimsFromSession 将服务端会话转换为统一的身份声明。
func ClaimsFromSession(session Session) *Claims {
	return &Claims{
		UserID:    session.UserID,
		TenantID:  session.TenantID,
		RoleCode:  session.RoleCode,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(session.CreatedAt),
			ExpiresAt: jwt.NewNumericDate(session.ExpiresAt),
		},
	}
}

func sessionCacheKey(tokenHash string) string {
	return sessionCachePrefix + tokenHash
}
