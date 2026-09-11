// Package redis 管理全局 Redis 客户端连接。
package redis

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/logging"
)

var client *redis.Client

// Init 初始化全局 Redis 客户端并验证连接。配置了 Redis（addr 非空）时由 bootstrap 调用；
// 未配置 Redis 的 dev 模式应改调 cache.InitLocal 启用 Redka 本地缓存。
func Init(cfg config.RedisConfig) error {
	created, err := NewClient(cfg)
	if err != nil {
		return err
	}
	client = created
	logging.Info("redis client initialized", "addr", cfg.Addr, "db", cfg.DB)
	return nil
}

// NewClient 按配置创建独立 Redis 客户端并验证连接，
// 供需要多实例或自管理生命周期的服务使用。
func NewClient(cfg config.RedisConfig) (*redis.Client, error) {
	instance := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := instance.Ping(context.Background()).Err(); err != nil {
		_ = instance.Close()
		return nil, err
	}
	return instance, nil
}

// Client 返回已初始化的 Redis 客户端；未初始化时返回 nil。
func Client() *redis.Client {
	return client
}

// Close 关闭 Redis 客户端连接。
func Close() error {
	if client == nil {
		return nil
	}
	err := client.Close()
	if err != nil {
		logging.Warn("redis client close failed", "error", err)
		return err
	}
	logging.Info("redis client closed")
	return nil
}
