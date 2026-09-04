// Package cache 提供 Redis 缓存读写，Redis 未启用时自动回退到进程内存缓存。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/xinpaiyun/nova-lib/redis"
)

var memoryStore sync.Map

type memoryItem struct {
	value     string
	expiresAt time.Time
}

// ErrMiss 表示缓存未命中（内存回退模式下返回）。
var ErrMiss = errors.New("cache miss")

// Set 写入缓存，Redis 未启用时回退到进程内存缓存。
func Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if client := redis.Client(); client != nil {
		return client.Set(ctx, key, value, ttl).Err()
	}
	memoryStore.Store(key, memoryItem{value: value, expiresAt: time.Now().Add(ttl)})
	return nil
}

// Get 读取缓存，Redis 未启用时读取进程内存缓存。
func Get(ctx context.Context, key string) (string, error) {
	if client := redis.Client(); client != nil {
		return client.Get(ctx, key).Result()
	}
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
func GetDel(ctx context.Context, key string) (string, error) {
	if client := redis.Client(); client != nil {
		return client.GetDel(ctx, key).Result()
	}
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
	if client := redis.Client(); client != nil {
		return client.Del(ctx, key).Err()
	}
	memoryStore.Delete(key)
	return nil
}

// SetJSON 将对象序列化为 JSON 后写入缓存。
func SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return Set(ctx, key, string(data), ttl)
}

// GetJSON 读取缓存并反序列化到 out；未命中时返回 false。
func GetJSON(ctx context.Context, key string, out any) (bool, error) {
	raw, err := Get(ctx, key)
	if err != nil || raw == "" {
		return false, nil
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		_ = Del(ctx, key)
		return false, err
	}
	return true, nil
}
