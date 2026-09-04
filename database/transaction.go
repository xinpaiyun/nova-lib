package database

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrStorageDisabled 表示数据库连接未初始化或已禁用。
var ErrStorageDisabled = errors.New("数据库未初始化或已禁用")

type transactionContextKey struct{}

// ContextWithTransaction 将当前 GORM 事务绑定到调用链上下文，
// 供同事务内后续数据库操作复用。
func ContextWithTransaction(ctx context.Context, tx *gorm.DB) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, transactionContextKey{}, tx)
}

// ResolveTransaction 优先返回上下文绑定的事务连接，无事务时回退到默认连接。
func ResolveTransaction(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if ctx != nil {
		if tx, ok := ctx.Value(transactionContextKey{}).(*gorm.DB); ok && tx != nil {
			return tx
		}
	}
	return fallback
}
