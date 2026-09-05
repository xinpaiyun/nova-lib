package opaudit

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/test/assert"
	"gorm.io/gorm"
)

// TestRecordWithDBGuard 验证关键参数缺失与空 DB 时的静默跳过。
func TestRecordWithDBGuard(t *testing.T) {
	if err := RecordWithDB(context.Background(), nil, Event{ActorUserID: 1, Action: "create"}); err != nil {
		t.Fatalf("nil db should skip silently, got %v", err)
	}
	db := &gorm.DB{Error: errors.New("unused")}
	if err := RecordWithDB(context.Background(), db, Event{Action: "create"}); err != nil {
		t.Fatalf("zero actor should skip silently, got %v", err)
	}
	if err := RecordWithDB(context.Background(), db, Event{ActorUserID: 1}); err != nil {
		t.Fatalf("empty action should skip silently, got %v", err)
	}
}

// TestFromRequest 验证请求侧字段补齐与 nil 保护。
func TestFromRequest(t *testing.T) {
	if got := FromRequest(nil, Event{Action: "create"}); got.Action != "create" {
		t.Fatalf("nil ctx should return event unchanged")
	}
	ctx := app.NewContext(1)
	ctx.Request.Header.Set("User-Agent", "test-agent")
	event := FromRequest(ctx, Event{Action: "create", TenantID: 3})
	assert.DeepEqual(t, "test-agent", event.UserAgent)
	assert.DeepEqual(t, uint64(3), event.TenantID)
}

// TestTableName 验证审计日志表名契约。
func TestTableName(t *testing.T) {
	if got := (Log{}).TableName(); got != "audit_logs" {
		t.Fatalf("TableName() = %q, want audit_logs", got)
	}
}
