// Package cache 提供 Redis 缓存读写，Redis 未启用时自动回退到进程内存缓存。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xinpaiyun/nova-lib/logging"
	"github.com/xinpaiyun/nova-lib/redis"
)

var memoryStore sync.Map
var memoryFallbackEnabled = true

// memoryFallbackWarnOnce 保证内存兜底告警只输出一次，避免热路径刷屏。
var memoryFallbackWarnOnce sync.Once

// warnMemoryFallback 首次触发内存兜底时告警：
// Redis 未初始化意味着数据不跨进程、重启即失，必须让该状态在日志中可见。
func warnMemoryFallback() {
	memoryFallbackWarnOnce.Do(func() {
		logging.Warn("redis client not initialized, cache falls back to in-process memory: data will not survive restart, call redis.Init() at bootstrap")
	})
}

// clientGetter 便于测试注入自定义客户端获取逻辑。
var clientGetter = redis.Client

// DisableMemoryFallback 关闭进程内存回退缓存；Redis 未启用时所有读写按 miss 处理。
// 供测试包在 TestMain 中调用，避免跨测试的内存状态污染。
func DisableMemoryFallback() {
	memoryFallbackEnabled = false
}

type memoryItem struct {
	value     string
	expiresAt time.Time
}

// ErrMiss 表示缓存未命中（Redis miss 与内存回退统一归一化为该错误）。
var ErrMiss = errors.New("cache miss")

// Set 写入缓存，Redis 未启用时回退到进程内存缓存。
func Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if client := clientGetter(); client != nil {
		return client.Set(ctx, key, value, ttl).Err()
	}
	if !memoryFallbackEnabled {
		return nil
	}
	warnMemoryFallback()
	memoryStore.Store(key, memoryItem{value: value, expiresAt: time.Now().Add(ttl)})
	return nil
}

// Get 读取缓存，Redis 未启用时读取进程内存缓存。
// 未命中统一返回 ErrMiss；Redis 连接故障等真实错误原样透传，与未命中可区分。
func Get(ctx context.Context, key string) (string, error) {
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
		return "", ErrMiss
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
func GetDel(ctx context.Context, key string) (string, error) {
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
		return "", ErrMiss
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

// Del 删除缓存，Redis 未启用时删除进程内存缓存。
func Del(ctx context.Context, key string) error {
	if client := clientGetter(); client != nil {
		return client.Del(ctx, key).Err()
	}
	if !memoryFallbackEnabled {
		return nil
	}
	warnMemoryFallback()
	memoryStore.Delete(key)
	return nil
}

// Flush 清空进程内存回退缓存；Redis 模式下为空操作，避免误清远端数据。
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
// Redis 未初始化、连接故障等真实错误会透传，供上层区分「未登录」与「服务故障」。
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
