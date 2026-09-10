// Package cache 提供 Redis 缓存读写。
//
// 后端选择（按优先级）：
//  1. Redka 本地持久化缓存：调用 InitLocal 启用（dev 模式替代 Redis，数据落 SQLite，重启不丢）；
//  2. Redis：调用 nova-lib/redis 的 Init 初始化全局单例后自动启用；
//  3. 进程内存兜底：默认开启（仅 KV，数据不跨进程、重启即失，首次触发时输出 Warn）。
//
// 队列类操作（List*）不支持进程内存兜底：无后端时返回 ErrUnavailable。
// 需要严格拒绝静默降级的服务（如任务队列、会话）可在 bootstrap 调用
// DisableMemoryFallback，此后无后端时所有读写返回 ErrUnavailable。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	// 纯 Go 的 "sqlite" 数据库驱动（database/sql），供 Redka 使用；
	// 与 identity 包的 glebarez/sqlite（GORM 驱动）同源，同一驱动仅注册一次。
	_ "github.com/glebarez/go-sqlite"
	"github.com/nalgeon/redka"
	goredis "github.com/redis/go-redis/v9"

	"github.com/xinpaiyun/nova-lib/logging"
	"github.com/xinpaiyun/nova-lib/redis"
)

var localDB *redka.DB
var memoryStore sync.Map
var memoryFallbackEnabled = true

// memoryFallbackWarnOnce 保证内存兜底告警只输出一次，避免热路径刷屏。
var memoryFallbackWarnOnce sync.Once

// listUnavailableWarnOnce 保证 List 操作后端缺失告警只输出一次。
var listUnavailableWarnOnce sync.Once

// warnMemoryFallback 首次触发内存兜底时告警：
// Redis 未初始化意味着数据不跨进程、重启即失，必须让该状态在日志中可见。
func warnMemoryFallback() {
	memoryFallbackWarnOnce.Do(func() {
		logging.Warn("redis client not initialized, cache falls back to in-process memory: data will not survive restart, call redis.Init() at bootstrap")
	})
}

// warnListUnavailable List 操作无可用后端时告警一次。
func warnListUnavailable() {
	listUnavailableWarnOnce.Do(func() {
		logging.Warn("list operation has no cache backend (neither redka local cache nor redis): call cache.InitLocal() or redis.Init() at bootstrap")
	})
}

// clientGetter 便于测试注入自定义客户端获取逻辑。
var clientGetter = redis.Client

// ErrMiss 表示缓存未命中（各后端的 miss 统一归一化为该错误）。
var ErrMiss = errors.New("cache miss")

// ErrUnavailable 表示无可用缓存后端（Redis 未初始化且未启用本地缓存），
// 且进程内存兜底已被 DisableMemoryFallback 关闭或该操作不支持内存兜底。
var ErrUnavailable = errors.New("cache backend unavailable: redis not initialized")

// InitLocal 启用 Redka 本地持久化缓存（dev 模式替代 Redis），数据写入本地 SQLite 文件。
// 使用纯 Go 的 "sqlite" 驱动，CGO_ENABLED=0 交叉编译可用；初始化失败返回错误，
// 由调用方决定终止进程（生产）还是降级继续（开发）。
func InitLocal(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create redka cache directory: %w", err)
		}
	}
	db, err := redka.Open(path, &redka.Options{DriverName: "sqlite"})
	if err != nil {
		return fmt.Errorf("open redka cache: %w", err)
	}
	localDB = db
	logging.Info("redka local cache initialized", "path", path)
	return nil
}

// CloseLocal 关闭 Redka 本地缓存连接。
func CloseLocal() error {
	if localDB == nil {
		return nil
	}
	err := localDB.Close()
	localDB = nil
	if err != nil {
		logging.Warn("redka local cache close failed", "error", err)
		return err
	}
	logging.Info("redka local cache closed")
	return nil
}

// BackendReady 返回是否有持久化后端可用（Redka 本地缓存或 Redis）。
func BackendReady() bool {
	return localDB != nil || clientGetter() != nil
}

// DisableMemoryFallback 关闭进程内存兜底：无可用后端时 KV 读写返回 ErrUnavailable，
// 而不是静默写进程内存。需要拒绝静默降级的服务（任务队列、会话等）在 bootstrap 调用。
func DisableMemoryFallback() {
	memoryFallbackEnabled = false
}

type memoryItem struct {
	value     string
	expiresAt time.Time
}

// Set 写入缓存；后端优先级为 Redka 本地 > Redis > 进程内存。
func Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if localDB != nil {
		if ttl > 0 {
			return localDB.Str().SetExpire(key, value, ttl)
		}
		return localDB.Str().Set(key, value)
	}
	if client := clientGetter(); client != nil {
		return client.Set(ctx, key, value, ttl).Err()
	}
	if !memoryFallbackEnabled {
		return ErrUnavailable
	}
	warnMemoryFallback()
	memoryStore.Store(key, memoryItem{value: value, expiresAt: time.Now().Add(ttl)})
	return nil
}

// Get 读取缓存；未命中统一返回 ErrMiss，Redis 连接故障等真实错误原样透传。
func Get(ctx context.Context, key string) (string, error) {
	if localDB != nil {
		value, err := localDB.Str().Get(key)
		if err != nil {
			if errors.Is(err, redka.ErrNotFound) {
				return "", ErrMiss
			}
			return "", err
		}
		return value.String(), nil
	}
	if client := clientGetter(); client != nil {
		value, err := client.Get(ctx, key).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				return "", ErrMiss
			}
			return "", err
		}
		return value, nil
	}
	if !memoryFallbackEnabled {
		return "", ErrUnavailable
	}
	warnMemoryFallback()
	raw, ok := memoryStore.Load(key)
	if !ok {
		return "", ErrMiss
	}
	item, ok := raw.(memoryItem)
	if !ok || time.Now().After(item.expiresAt) {
		memoryStore.Delete(key)
		return "", ErrMiss
	}
	return item.value, nil
}

// GetDel 原子读取并删除缓存，适用于短信验证码等一次性令牌。
// 错误语义与 Get 一致：未命中返回 ErrMiss，真实故障原样透传。
// Redka 后端为「读取后删除」两步实现，非严格原子。
func GetDel(ctx context.Context, key string) (string, error) {
	if localDB != nil {
		value, err := Get(ctx, key)
		if err != nil {
			return "", err
		}
		if _, err := localDB.Key().Delete(key); err != nil {
			return "", err
		}
		return value, nil
	}
	if client := clientGetter(); client != nil {
		value, err := client.GetDel(ctx, key).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				return "", ErrMiss
			}
			return "", err
		}
		return value, nil
	}
	if !memoryFallbackEnabled {
		return "", ErrUnavailable
	}
	warnMemoryFallback()
	raw, ok := memoryStore.LoadAndDelete(key)
	if !ok {
		return "", ErrMiss
	}
	item, ok := raw.(memoryItem)
	if !ok || time.Now().After(item.expiresAt) {
		return "", ErrMiss
	}
	return item.value, nil
}

// Del 删除缓存。
func Del(ctx context.Context, key string) error {
	if localDB != nil {
		_, err := localDB.Key().Delete(key)
		return err
	}
	if client := clientGetter(); client != nil {
		return client.Del(ctx, key).Err()
	}
	if !memoryFallbackEnabled {
		return ErrUnavailable
	}
	warnMemoryFallback()
	memoryStore.Delete(key)
	return nil
}

// Flush 清空进程内存回退缓存；Redis 与 Redka 模式下为空操作，避免误清持久化数据。
func Flush() {
	memoryStore.Range(func(key, _ any) bool {
		memoryStore.Delete(key)
		return true
	})
}

// SetJSON 将对象序列化为 JSON 后写入缓存。
func SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return Set(ctx, key, string(data), ttl)
}

// GetJSON 读取缓存并反序列化到 out；未命中返回 (false, nil)。
// Redis 未初始化、连接故障等真实错误会透传，供上层区分「未命中」与「服务故障」。
func GetJSON(ctx context.Context, key string, out any) (bool, error) {
	raw, err := Get(ctx, key)
	if err != nil {
		if errors.Is(err, ErrMiss) {
			return false, nil
		}
		return false, err
	}
	if raw == "" {
		return false, nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		_ = Del(ctx, key)
		return false, err
	}
	return true, nil
}

// ListPushBack 向列表尾部追加元素，用于轻量任务队列。
// 仅支持 Redka 本地与 Redis 后端，无后端时返回 ErrUnavailable（不入进程内存）。
func ListPushBack(ctx context.Context, key string, values ...string) error {
	if len(values) == 0 {
		return nil
	}
	if localDB != nil {
		for _, value := range values {
			if _, err := localDB.List().PushBack(key, value); err != nil {
				return err
			}
		}
		return nil
	}
	if client := clientGetter(); client != nil {
		args := make([]any, 0, len(values))
		for _, value := range values {
			args = append(args, value)
		}
		return client.RPush(ctx, key, args...).Err()
	}
	warnListUnavailable()
	return ErrUnavailable
}

// ListPopFront 从列表头部弹出一个元素；列表为空返回 ErrMiss。
func ListPopFront(ctx context.Context, key string) (string, error) {
	if localDB != nil {
		value, err := localDB.List().PopFront(key)
		if err != nil {
			if errors.Is(err, redka.ErrNotFound) {
				return "", ErrMiss
			}
			return "", err
		}
		return value.String(), nil
	}
	if client := clientGetter(); client != nil {
		value, err := client.LPop(ctx, key).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				return "", ErrMiss
			}
			return "", err
		}
		return value, nil
	}
	warnListUnavailable()
	return "", ErrUnavailable
}

// ListLen 返回列表长度。
func ListLen(ctx context.Context, key string) (int64, error) {
	if localDB != nil {
		n, err := localDB.List().Len(key)
		if err != nil {
			return 0, err
		}
		return int64(n), nil
	}
	if client := clientGetter(); client != nil {
		return client.LLen(ctx, key).Result()
	}
	warnListUnavailable()
	return 0, ErrUnavailable
}

// ListPopFrontN 从列表头部一次性弹出至多 count 个元素；列表为空返回 ErrMiss，
// 部分弹出时返回已弹出的元素与 nil 错误。
func ListPopFrontN(ctx context.Context, key string, count int) ([]string, error) {
	if count <= 0 {
		return []string{}, nil
	}
	if localDB != nil {
		values := make([]string, 0, count)
		for i := 0; i < count; i++ {
			value, err := localDB.List().PopFront(key)
			if err != nil {
				if errors.Is(err, redka.ErrNotFound) {
					if len(values) == 0 {
						return nil, ErrMiss
					}
					return values, nil
				}
				return nil, err
			}
			values = append(values, value.String())
		}
		return values, nil
	}
	if client := clientGetter(); client != nil {
		values, err := client.LPopCount(ctx, key, count).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				return nil, ErrMiss
			}
			return nil, err
		}
		return values, nil
	}
	warnListUnavailable()
	return nil, ErrUnavailable
}
