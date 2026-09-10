// Package redis 管理全局 Redis 客户端连接。
package redis

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/xinpaiyun/nova-lib/config"
	"github.com/xinpaiyun/nova-lib/logging"
)

var client *redis.Client

// Init 初始化全局 Redis 客户端并验证连接。
// 注意：Enabled=false 时不会创建客户端（全局保持 nil），
// cache 将静默回退到进程内存，而依赖 Redis 的功能（如 auth 会话存储）会直接报错；
// 需要会话等持久语义的服务必须保证 Enabled=true 并在启动阶段调用本函数。
func Init(cfg config.RedisConfig) error {
	if !cfg.Enabled {
		// 配置遗漏的关键信号：全局客户端保持 nil，下游 cache 静默回退进程内存、
		// auth 会话 fail fast，必须在启动日志中可见，避免重启掉线类问题无迹可循。
		logging.Warn("redis disabled in config, global client not initialized: cache falls back to in-process memory, session storage will fail fast")
		return nil
	}
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

// Client 返回已初始化的 Redis 客户端；未启用时返回 nil。
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
