package database

import (
	"gorm.io/gorm"
)

// SetDB 临时替换全局数据库连接，仅供测试注入使用；返回恢复函数，
// 可在测试结束时（t.Cleanup）或中途恢复原连接。
func SetDB(conn *gorm.DB) (restore func()) {
	prev := db
	db = conn
	return func() { db = prev }
}
