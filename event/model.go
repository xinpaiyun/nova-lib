// Package event 提供与业务事实同事务提交的标准事件 Outbox 模式，
// 支持进程内本地发布（LocalPublisher）和幂等消费。
package event

import "time"

const (
	// OutboxStatusPending 待投递。
	OutboxStatusPending = "pending"
	// OutboxStatusProcessing 认领中。
	OutboxStatusProcessing = "processing"
	// OutboxStatusPublished 已成功投递。
	OutboxStatusPublished = "published"
	// OutboxStatusFailed 投递失败，等待重试。
	OutboxStatusFailed = "failed"
	// OutboxStatusDeadLetter 重试耗尽，进入死信。
	OutboxStatusDeadLetter = "dead_letter"

	// ConsumptionStatusProcessing 消费者处理中。
	ConsumptionStatusProcessing = "processing"
	// ConsumptionStatusCompleted 消费者处理完成。
	ConsumptionStatusCompleted = "completed"
	// ConsumptionStatusFailed 消费者处理失败。
	ConsumptionStatusFailed = "failed"
)

// OutboxEvent 定义与业务事实同事务提交的标准事件信封。
type OutboxEvent struct {
	ID            uint64     `json:"id" gorm:"primaryKey"`
	EventID       string     `json:"eventId" gorm:"size:64;not null;uniqueIndex"`
	EventType     string     `json:"eventType" gorm:"size:96;not null;index"`
	SchemaVersion int        `json:"schemaVersion" gorm:"not null;default:1"`
	Producer      string     `json:"producer" gorm:"size:64;not null;index"`
	TenantID      uint64     `json:"tenantId" gorm:"index"`
	SubjectType   string     `json:"subjectType" gorm:"size:32;index"`
	SubjectID     uint64     `json:"subjectId" gorm:"index"`
	AggregateType string     `json:"aggregateType" gorm:"size:64;not null;index:idx_outbox_aggregate,priority:1"`
	AggregateID   uint64     `json:"aggregateId" gorm:"not null;index:idx_outbox_aggregate,priority:2"`
	PrivacyLevel  string     `json:"privacyLevel" gorm:"size:32;not null"`
	TraceID       string     `json:"traceId" gorm:"size:96;index"`
	Payload       string     `json:"payload" gorm:"type:longtext;not null"`
	Status        string     `json:"status" gorm:"size:20;not null;default:pending;index:idx_outbox_delivery,priority:1"`
	Attempts      int        `json:"attempts" gorm:"not null;default:0"`
	MaxAttempts   int        `json:"maxAttempts" gorm:"not null;default:8"`
	AvailableAt   time.Time  `json:"availableAt" gorm:"index:idx_outbox_delivery,priority:2"`
	LockedAt      *time.Time `json:"lockedAt" gorm:"index"`
	LockToken     string     `json:"lockToken" gorm:"size:64;index"`
	LastError     string     `json:"lastError" gorm:"size:2000"`
	OccurredAt    time.Time  `json:"occurredAt" gorm:"index"`
	PublishedAt   *time.Time `json:"publishedAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// EventConsumption 记录消费者对事件的幂等处理状态。
type EventConsumption struct {
	ID           uint64     `json:"id" gorm:"primaryKey"`
	ConsumerCode string     `json:"consumerCode" gorm:"size:96;not null;uniqueIndex:idx_event_consumption,priority:1"`
	EventID      string     `json:"eventId" gorm:"size:64;not null;uniqueIndex:idx_event_consumption,priority:2"`
	Status       string     `json:"status" gorm:"size:20;not null;default:processing;index"`
	Attempts     int        `json:"attempts" gorm:"not null;default:1"`
	LastError    string     `json:"lastError" gorm:"size:2000"`
	StartedAt    time.Time  `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// EventTypeMetric 定义事件类型的幂等消费观测投影。
type EventTypeMetric struct {
	ID             uint64    `json:"id" gorm:"primaryKey"`
	EventType      string    `json:"eventType" gorm:"size:96;not null;uniqueIndex"`
	ProcessedCount int64     `json:"processedCount" gorm:"not null;default:0"`
	LastEventID    string    `json:"lastEventId" gorm:"size:64"`
	LastTenantID   uint64    `json:"lastTenantId"`
	LastOccurredAt time.Time `json:"lastOccurredAt"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// TableName 返回 Outbox 表名。
func (OutboxEvent) TableName() string { return "outbox_events" }

// TableName 返回事件消费幂等表名。
func (EventConsumption) TableName() string { return "event_consumptions" }

// TableName 返回事件类型指标表名。
func (EventTypeMetric) TableName() string { return "event_type_metrics" }

// ModelTypes 返回事件中心自动迁移模型。
func ModelTypes() []any {
	return []any{&OutboxEvent{}, &EventConsumption{}, &EventTypeMetric{}}
}
