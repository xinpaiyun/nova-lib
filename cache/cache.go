// Package cache 提供 Redis 缓存读写，Redis 未启用时自动回退到进程内存缓存。
package cache

import (
	"context"
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
