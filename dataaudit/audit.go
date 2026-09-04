// Package dataaudit 提供租户级数据操作审计日志能力。
package dataaudit

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/xinpaiyun/nova-lib/database"
)

const (
	// ActionView 查看数据。
	ActionView = "view"
	// ActionCreate 新增数据。
	ActionCreate = "create"
	// ActionUpdate 修改数据。
	ActionUpdate = "update"
	// ActionDelete 删除数据。
	ActionDelete = "delete"
)

// DataAuditLog 记录用户对敏感业务数据的查看、新增、修改和删除操作。
type DataAuditLog struct {
	ID           uint      `json:"id" gorm:"primarykey"`
	CreatedAt    time.Time `json:"created_at" gorm:"index:idx_audit_tenant_created,priority:2;index:idx_audit_tenant_user_created,priority:3"`
	UpdatedAt    time.Time `json:"updated_at"`
	TenantID     uint64    `json:"tenant_id" gorm:"not null;index:idx_audit_tenant_created,priority:1;index:idx_audit_tenant_user_created,priority:1"`
	UserID       uint64    `json:"user_id" gorm:"not null;index:idx_audit_tenant_user_created,priority:2"`
	ObjectType   string    `json:"object_type" gorm:"not null;size:50;index:idx_audit_tenant_object,priority:1"`
	ObjectID     uint64    `json:"object_id" gorm:"not null;default:0;index:idx_audit_tenant_object,priority:2"`
	ObjectName   string    `json:"object_name" gorm:"size:180"`
	Action       string    `json:"action" gorm:"not null;size:50;index:idx_audit_tenant_action,priority:1"`
	MetadataJSON string    `json:"metadata_json" gorm:"type:text"`
	IPAddress    string    `json:"ip_address" gorm:"size:64"`
	UserAgent    string    `json:"user_agent" gorm:"size:500"`
}

// TableName 返回数据审计日志表名。
func (DataAuditLog) TableName() string {
	return "data_audit_logs"
}

// RecordReq 定义数据审计日志写入参数。
type RecordReq struct {
	TenantID     uint64
	UserID       uint64
	ObjectType   string
	ObjectID     uint64
	ObjectName   string
	Action       string
	Metadata     map[string]any
	IPAddress    string
	UserAgent    string
}

// Record 写入数据审计日志，关键参数缺失时静默忽略。
func Record(req RecordReq) error {
	if req.TenantID == 0 || req.UserID == 0 || req.ObjectID == 0 {
		return nil
	}
	action := strings.TrimSpace(req.Action)
	objectType := strings.TrimSpace(req.ObjectType)
	if action == "" || objectType == "" {
		return nil
	}
	metadataJSON := ""
	if len(req.Metadata) > 0 {
		raw, err := json.Marshal(req.Metadata)
		if err != nil {
			return err
		}
		metadataJSON = string(raw)
	}
	return database.DB().Create(&DataAuditLog{
		TenantID:     req.TenantID,
		UserID:       req.UserID,
		ObjectType:   objectType,
		ObjectID:     req.ObjectID,
		ObjectName:   strings.TrimSpace(req.ObjectName),
		Action:       action,
		MetadataJSON: metadataJSON,
		IPAddress:    strings.TrimSpace(req.IPAddress),
		UserAgent:    strings.TrimSpace(req.UserAgent),
	}).Error
}
