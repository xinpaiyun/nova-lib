package logging

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// GormLogger 将 GORM 日志统一输出到 hlog（即 JSON 结构化日志流）。
type GormLogger struct {
	logLevel      gormlogger.LogLevel
	slowThreshold time.Duration
}

// NewGormLogger 创建 GORM 日志适配器。
func NewGormLogger() gormlogger.Interface {
	return &GormLogger{
		logLevel:      gormlogger.Info,
		slowThreshold: 200 * time.Millisecond,
	}
}

// LogMode 返回指定级别的 GORM 日志器副本。
func (l *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	return &GormLogger{
		logLevel:      level,
		slowThreshold: l.slowThreshold,
	}
}

// Info 输出 GORM info 日志。
func (l *GormLogger) Info(ctx context.Context, msg string, args ...any) {
	if l.logLevel < gormlogger.Info {
		return
	}
	hlog.CtxInfof(ctx, msg, args...)
}

// Warn 输出 GORM warn 日志。
func (l *GormLogger) Warn(ctx context.Context, msg string, args ...any) {
	if l.logLevel < gormlogger.Warn {
		return
	}
	hlog.CtxWarnf(ctx, msg, args...)
}

// Error 输出 GORM error 日志。
func (l *GormLogger) Error(ctx context.Context, msg string, args ...any) {
	if l.logLevel < gormlogger.Error {
		return
	}
	hlog.CtxErrorf(ctx, msg, args...)
}

// Trace 输出 SQL 执行、慢查询与数据库异常日志。
func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.logLevel == gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	elapsedMs := float64(elapsed.Microseconds()) / 1000

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		hlog.CtxErrorf(ctx, "gorm query failed, elapsed_ms=%.2f, rows=%d, sql=%s, err=%v", elapsedMs, rows, sql, err)
	case l.slowThreshold > 0 && elapsed > l.slowThreshold && l.logLevel >= gormlogger.Warn:
		hlog.CtxWarnf(ctx, "gorm slow query, elapsed_ms=%.2f, rows=%d, sql=%s", elapsedMs, rows, sql)
	case l.logLevel >= gormlogger.Info:
		hlog.CtxDebugf(ctx, "gorm query executed, elapsed_ms=%.2f, rows=%d, sql=%s", elapsedMs, rows, sql)
	}
}
