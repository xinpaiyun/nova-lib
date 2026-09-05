// Package opaudit 提供管理后台关键操作审计日志能力（与 dataaudit 的数据审计互补）。
package opaudit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"gorm.io/gorm"

	"github.com/xinpaiyun/nova-lib/database"
	"github.com/xinpaiyun/nova-lib/logging"
)

const (
	// ResultSuccess 表示操作成功。
	ResultSuccess = "success"
	// ResultFailed 表示操作失败。
	ResultFailed = "failed"
)

// Log 定义管理后台关键操作审计日志。
type Log struct {
	ID           uint64    `json:"id" gorm:"primaryKey"`
	ActorUserID  uint64    `json:"actorUserId" gorm:"index;not null"`
	AppType      string    `json:"appType" gorm:"size:32;index"`
	TenantID     uint64    `json:"tenantId" gorm:"index"`
	ShopID       uint64    `json:"shopId" gorm:"index"`
	Action       string    `json:"action" gorm:"size:96;index;not null"`
	ResourceType string    `json:"resourceType" gorm:"size:64;index"`
	ResourceID   uint64    `json:"resourceId" gorm:"index"`
	Result       string    `json:"result" gorm:"size:32;index"`
	Message      string    `json:"message" gorm:"size:255"`
	Metadata     string    `json:"metadata" gorm:"size:2048"`
	RequestID    string    `json:"requestId" gorm:"size:64;index"`
	IP           string    `json:"ip" gorm:"size:64"`
	UserAgent    string    `json:"userAgent" gorm:"size:512"`
	CreatedAt    time.Time `json:"createdAt" gorm:"index"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// TableName 返回审计日志表名。
func (Log) TableName() string {
	return "audit_logs"
}

// ModelTypes 返回审计日志自动迁移模型。
func ModelTypes() []any {
	return []any{&Log{}}
}

// Event 描述一次关键操作审计事件。
type Event struct {
	ActorUserID  uint64
	AppType      string
	TenantID     uint64
	ShopID       uint64
	Action       string
	ResourceType string
	ResourceID   uint64
	Result       string
	Message      string
	Metadata     map[string]any
	RequestID    string
	IP           string
	UserAgent    string
}

// FromRequest 使用 Hertz 请求上下文补齐审计事件的请求侧字段。
func FromRequest(c *app.RequestContext, event Event) Event {
	if c == nil {
		return event
	}
	event.RequestID = logging.RequestID(c)
	event.IP = c.ClientIP()
	event.UserAgent = string(c.GetHeader("User-Agent"))
	return event
}

// Record 写入审计日志；无数据库模式下直接跳过。
func Record(ctx context.Context, event Event) error {
	if database.DB() == nil {
		return nil
	}
	return RecordWithDB(ctx, database.DB(), event)
}

// RecordWithDB 使用指定 DB 写入审计日志。
func RecordWithDB(ctx context.Context, db *gorm.DB, event Event) error {
	if db == nil || event.ActorUserID == 0 || event.Action == "" {
		return nil
	}
	metadata := ""
	if len(event.Metadata) > 0 {
		if raw, err := json.Marshal(event.Metadata); err == nil {
			metadata = string(raw)
		}
	}
	result := event.Result
	if result == "" {
		result = ResultSuccess
	}
	log := &Log{
		ActorUserID:  event.ActorUserID,
		AppType:      event.AppType,
		TenantID:     event.TenantID,
		ShopID:       event.ShopID,
		Action:       event.Action,
		ResourceType: event.ResourceType,
		ResourceID:   event.ResourceID,
		Result:       result,
		Message:      event.Message,
		Metadata:     metadata,
		RequestID:    event.RequestID,
		IP:           event.IP,
		UserAgent:    event.UserAgent,
	}
	return db.WithContext(ctx).Create(log).Error
}
