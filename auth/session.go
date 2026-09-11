package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/xinpaiyun/nova-lib/cache"
	"github.com/xinpaiyun/nova-lib/logging"
	"github.com/xinpaiyun/nova-lib/redis"
)

const sessionCachePrefix = "auth:session:"

// ErrSessionInvalid 表示会话不存在或已失效（业务语义错误，可用 errors.Is 判断），
// 上层可安全映射为 401；Redis 故障等基础设施错误不属于该类别，应按 500 处理。
var ErrSessionInvalid = errors.New("登录状态已失效")

// ErrRedisNotInitialized 表示全局 Redis 客户端尚未初始化。
// 会话是持久性数据，默认存储拒绝静默降级为进程内存（否则服务重启即全员掉线）：
// 必须先调用 redis.Init(cfg)（github.com/xinpaiyun/nova-lib/redis）完成初始化。
var ErrRedisNotInitialized = errors.New("redis 客户端未初始化：请先调用 redis.Init() 初始化全局 Redis 客户端，会话不允许降级写入进程内存")

// Session 抽象服务端保存的 Redis 登录会话，是访问身份的唯一可信来源。
type Session struct {
	TokenHash string    `json:"tokenHash"`
	UserID    uint64    `json:"userId"`
	TenantID  uint64    `json:"tenantId"`
	RoleCode  string    `json:"roleCode"`
	AppType   string    `json:"appType"`
	OpenID    string    `json:"openId"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// SessionStore 抽象会话存储，可注入自管存储或测试替身。
type SessionStore interface {
	SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error
	GetJSON(ctx context.Context, key string, out any) (bool, error)
	Del(ctx context.Context, key string) error
}

// sessionStore 为包级默认存储：基于 nova-lib/cache 的 Redis 缓存，
// Redis 客户端未初始化时直接报错，绝不静默降级为进程内存。
var sessionStore SessionStore = redisSessionStore{}

// SetCache 注入自定义会话存储；传 nil 恢复默认 Redis 存储。
// 默认存储要求先调用 redis.Init() 初始化全局 Redis 客户端，
// 否则 StoreSession / ResolveSession 会返回 ErrRedisNotInitialized。
func SetCache(store SessionStore) {
	if store == nil {
		sessionStore = redisSessionStore{}
		return
	}
	sessionStore = store
}

// warnRedisNotInitialized 记录高可见度告警后返回统一错误，
// 便于从日志直接定位「启动时遗漏 redis.Init()」这类配置问题。
func warnRedisNotInitialized() error {
	logging.Warn("session storage rejected: redis client not initialized, call redis.Init() at bootstrap")
	return ErrRedisNotInitialized
}

// redisSessionStore 是默认会话存储，所有操作都要求 Redis 客户端已初始化。
type redisSessionStore struct{}

func (redisSessionStore) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	if redis.Client() == nil {
		return warnRedisNotInitialized()
	}
	return cache.SetJSON(ctx, key, value, ttl)
}

func (redisSessionStore) GetJSON(ctx context.Context, key string, out any) (bool, error) {
	if redis.Client() == nil {
		return false, warnRedisNotInitialized()
	}
	return cache.GetJSON(ctx, key, out)
}

func (redisSessionStore) Del(ctx context.Context, key string) error {
	if redis.Client() == nil {
		return warnRedisNotInitialized()
	}
	return cache.Del(ctx, key)
}

// cacheSessionStore 是跟随 nova-lib/cache 当前后端的会话存储：
// Redka 本地缓存（InitLocal，dev 模式）或 Redis（redis.Init），数据不落进程内存；
// 后端未初始化时读写显式失败（ErrUnavailable），不会静默降级。
type cacheSessionStore struct{}

// NewCacheSessionStore 创建跟随 cache 后端的会话存储。
// dev 模式（无 Redis、走 Redka 本地持久化）用它替代默认的 redisSessionStore，
// 使会话在 air 热重载后依然有效；生产模式走 Redis，行为与默认存储一致。
func NewCacheSessionStore() SessionStore { return cacheSessionStore{} }

func (cacheSessionStore) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	return cache.SetJSON(ctx, key, value, ttl)
}

func (cacheSessionStore) GetJSON(ctx context.Context, key string, out any) (bool, error) {
	return cache.GetJSON(ctx, key, out)
}

func (cacheSessionStore) Del(ctx context.Context, key string) error {
	return cache.Del(ctx, key)
}

// MemoryStore 是基于进程内存的 SessionStore 实现，仅供测试或本地调试使用：
// 数据不跨进程共享、重启即丢失，生产环境必须使用默认 Redis 存储。
type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]memorySessionItem
}

type memorySessionItem struct {
	data      string
	expiresAt time.Time
}

// NewMemoryStore 创建进程内存会话存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]memorySessionItem)}
}

// SetJSON 序列化后写入内存，ttl 到期后读取视为未命中。
func (m *MemoryStore) SetJSON(_ context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = memorySessionItem{data: string(data), expiresAt: time.Now().Add(ttl)}
	return nil
}

// GetJSON 从内存读取并反序列化；未命中或已过期返回 (false, nil)。
func (m *MemoryStore) GetJSON(_ context.Context, key string, out any) (bool, error) {
	m.mu.RLock()
	item, ok := m.data[key]
	m.mu.RUnlock()
	if !ok || time.Now().After(item.expiresAt) {
		return false, nil
	}
	return true, json.Unmarshal([]byte(item.data), out)
}

// Del 删除内存中的会话。
func (m *MemoryStore) Del(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
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

// StoreSession 将服务端会话写入 Redis，TTL 取会话剩余有效期。
// 前置条件：全局 Redis 客户端已通过 redis.Init() 初始化；
// 未初始化时返回 ErrRedisNotInitialized，不会静默写入进程内存。
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
	if err := sessionStore.SetJSON(ctx, sessionCacheKey(session.TokenHash), session, time.Until(session.ExpiresAt)); err != nil {
		return err
	}
	logging.Info("session stored",
		"user_id", session.UserID,
		"tenant_id", session.TenantID,
		"expires_at", session.ExpiresAt.Unix(),
	)
	return nil
}

// ResolveSession 根据客户端 Token 解析服务端会话。
// 不查询数据库：账号停用等状态由登录时校验，封禁可通过删除会话键即时生效。
// 会话不存在或已过期返回 ErrSessionInvalid（可安全映射为 401）；
// Redis 未初始化、连接故障等基础设施错误原样透传，上层应按 500 处理，
// 而不是伪装成「登录状态已失效」掩盖真实故障。
func ResolveSession(ctx context.Context, token string) (*Session, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("token 不能为空")
	}
	var session Session
	tokenHash := TokenHash(token)
	ok, err := sessionStore.GetJSON(ctx, sessionCacheKey(tokenHash), &session)
	if err != nil {
		return nil, err
	}
	if !ok {
		// miss 属于热路径常态（过期后的重复请求），只打 Debug 避免刷屏；
		// 401 语义由上层中间件按需记录。
		logging.Debug("session miss", "token_hash", tokenHash[:8])
		return nil, ErrSessionInvalid
	}
	if !session.ExpiresAt.After(time.Now()) {
		return nil, fmt.Errorf("%w: 会话已过期", ErrSessionInvalid)
	}
	if session.UserID == 0 {
		return nil, fmt.Errorf("%w: 会话数据异常", ErrSessionInvalid)
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
	tokenHash := TokenHash(token)
	if err := sessionStore.Del(ctx, sessionCacheKey(tokenHash)); err != nil {
		return err
	}
	logging.Info("session revoked", "token_hash", tokenHash[:8])
	return nil
}

// ClaimsFromSession 将服务端会话转换为统一的身份声明。
func ClaimsFromSession(session Session) *Claims {
	return &Claims{
		UserID:   session.UserID,
		TenantID: session.TenantID,
		RoleCode: session.RoleCode,
		AppType:  session.AppType,
		OpenID:   session.OpenID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(session.CreatedAt),
			ExpiresAt: jwt.NewNumericDate(session.ExpiresAt),
		},
	}
}

func sessionCacheKey(tokenHash string) string {
	return sessionCachePrefix + tokenHash
}
