package event

import (
	"context"
	"errors"
	"time"

	"github.com/xinpaiyun/nova-lib/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BacklogMetrics 定义 Outbox 投递积压观测值。
type BacklogMetrics struct {
	Pending    int64
	Processing int64
	Failed     int64
	DeadLetter int64
	OldestAt   *time.Time
}

// Repository 封装 Outbox 投递和消费幂等数据访问。
type Repository struct {
	db *gorm.DB
}

// NewRepository 创建事件中心仓储。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// Append 在当前业务事务中写入 Outbox 事件。
func (r *Repository) Append(ctx context.Context, event *OutboxEvent) error {
	if r.db == nil {
		return database.ErrStorageDisabled
	}
	return database.ResolveTransaction(ctx, r.db).WithContext(ctx).Create(event).Error
}

// ClaimBatch 锁定一批可投递或锁超时的事件并分配投递令牌。
// SKIP LOCKED 在 MySQL/PostgreSQL 下有效，SQLite 驱动会忽略该子句，由 CAS 保证并发安全。
func (r *Repository) ClaimBatch(
	ctx context.Context,
	now time.Time,
	staleBefore time.Time,
	limit int,
	lockToken string,
) ([]OutboxEvent, error) {
	if r.db == nil {
		return nil, database.ErrStorageDisabled
	}
	var claimed []OutboxEvent
	err := database.ResolveTransaction(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []OutboxEvent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("attempts < max_attempts").
			Where(
				"((status IN ? AND available_at <= ?) OR (status = ? AND locked_at < ?))",
				[]string{OutboxStatusPending, OutboxStatusFailed},
				now,
				OutboxStatusProcessing,
				staleBefore,
			).
			Order("available_at ASC, id ASC").
			Limit(limit).
			Find(&rows).Error; err != nil {
			return err
		}
		for index := range rows {
			result := tx.Model(&OutboxEvent{}).
				Where("id = ? AND attempts = ?", rows[index].ID, rows[index].Attempts).
				Updates(map[string]any{
					"status":    OutboxStatusProcessing,
					"attempts":  gorm.Expr("attempts + 1"),
					"locked_at": now, "lock_token": lockToken,
					"updated_at": now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				continue
			}
			rows[index].Status = OutboxStatusProcessing
			rows[index].Attempts++
			rows[index].LockedAt = &now
			rows[index].LockToken = lockToken
			claimed = append(claimed, rows[index])
		}
		return nil
	})
	return claimed, err
}

// MarkPublished 按锁令牌确认事件投递成功。
func (r *Repository) MarkPublished(ctx context.Context, id uint64, lockToken string, publishedAt time.Time) error {
	if r.db == nil {
		return database.ErrStorageDisabled
	}
	result := database.ResolveTransaction(ctx, r.db).WithContext(ctx).
		Model(&OutboxEvent{}).
		Where("id = ? AND status = ? AND lock_token = ?", id, OutboxStatusProcessing, lockToken).
		Updates(map[string]any{
			"status": OutboxStatusPublished, "published_at": publishedAt,
			"locked_at": nil, "lock_token": "", "last_error": "", "updated_at": publishedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("Outbox 事件锁已失效")
	}
	return nil
}

// MarkFailed 记录投递错误并安排重试或转入死信。
func (r *Repository) MarkFailed(
	ctx context.Context,
	id uint64,
	lockToken string,
	status string,
	availableAt time.Time,
	lastError string,
	updatedAt time.Time,
) error {
	if r.db == nil {
		return database.ErrStorageDisabled
	}
	result := database.ResolveTransaction(ctx, r.db).WithContext(ctx).
		Model(&OutboxEvent{}).
		Where("id = ? AND status = ? AND lock_token = ?", id, OutboxStatusProcessing, lockToken).
		Updates(map[string]any{
			"status": status, "available_at": availableAt, "last_error": lastError,
			"locked_at": nil, "lock_token": "", "updated_at": updatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("Outbox 事件锁已失效")
	}
	return nil
}

// ConsumeOnce 在同一事务中执行消费者并记录幂等完成状态。
func (r *Repository) ConsumeOnce(
	ctx context.Context,
	consumerCode string,
	eventID string,
	now time.Time,
	callback func(context.Context) error,
) (bool, error) {
	if r.db == nil {
		return false, database.ErrStorageDisabled
	}
	processed := false
	err := database.ResolveTransaction(ctx, r.db).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var consumption EventConsumption
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("consumer_code = ? AND event_id = ?", consumerCode, eventID).
			First(&consumption).Error
		if err == nil && consumption.Status == ConsumptionStatusCompleted {
			return nil
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			consumption = EventConsumption{
				ConsumerCode: consumerCode, EventID: eventID,
				Status: ConsumptionStatusProcessing, Attempts: 1, StartedAt: now,
			}
			if err := tx.Create(&consumption).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if err := tx.Model(&EventConsumption{}).
				Where("id = ?", consumption.ID).
				Updates(map[string]any{
					"status":     ConsumptionStatusProcessing,
					"attempts":   gorm.Expr("attempts + 1"),
					"started_at": now, "last_error": "", "updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
		txCtx := database.ContextWithTransaction(ctx, tx)
		if err := callback(txCtx); err != nil {
			return err
		}
		completedAt := now
		if err := tx.Model(&EventConsumption{}).
			Where("id = ?", consumption.ID).
			Updates(map[string]any{
				"status":       ConsumptionStatusCompleted,
				"completed_at": completedAt, "last_error": "", "updated_at": completedAt,
			}).Error; err != nil {
			return err
		}
		processed = true
		return nil
	})
	return processed, err
}

// MarkConsumptionFailed 在消费者事务回滚后独立记录失败和尝试次数。
func (r *Repository) MarkConsumptionFailed(ctx context.Context, consumerCode string, eventID string, lastError string, now time.Time) error {
	if r.db == nil {
		return database.ErrStorageDisabled
	}
	entity := EventConsumption{
		ConsumerCode: consumerCode, EventID: eventID,
		Status: ConsumptionStatusFailed, Attempts: 1,
		LastError: lastError, StartedAt: now,
	}
	return database.ResolveTransaction(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "consumer_code"}, {Name: "event_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"status":     ConsumptionStatusFailed,
			"attempts":   gorm.Expr("attempts + 1"),
			"last_error": lastError, "updated_at": now,
		}),
	}).Create(&entity).Error
}

// Backlog 返回待投递、处理中、失败和死信事件指标。
func (r *Repository) Backlog(ctx context.Context) (BacklogMetrics, error) {
	if r.db == nil {
		return BacklogMetrics{}, database.ErrStorageDisabled
	}
	db := database.ResolveTransaction(ctx, r.db).WithContext(ctx)
	metrics := BacklogMetrics{}
	for status, target := range map[string]*int64{
		OutboxStatusPending:    &metrics.Pending,
		OutboxStatusProcessing: &metrics.Processing,
		OutboxStatusFailed:     &metrics.Failed,
		OutboxStatusDeadLetter: &metrics.DeadLetter,
	} {
		if err := db.Model(&OutboxEvent{}).Where("status = ?", status).Count(target).Error; err != nil {
			return BacklogMetrics{}, err
		}
	}
	var oldest OutboxEvent
	err := db.Where("status IN ?", []string{
		OutboxStatusPending, OutboxStatusProcessing, OutboxStatusFailed,
	}).Order("occurred_at ASC").First(&oldest).Error
	if err == nil {
		metrics.OldestAt = &oldest.OccurredAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return BacklogMetrics{}, err
	}
	return metrics, nil
}

// RecordEventTypeMetric 在消费者事务中累计事件类型投递观测。
func (r *Repository) RecordEventTypeMetric(ctx context.Context, event OutboxEvent) error {
	if r.db == nil {
		return database.ErrStorageDisabled
	}
	metric := EventTypeMetric{
		EventType: event.EventType, ProcessedCount: 1,
		LastEventID: event.EventID, LastTenantID: event.TenantID,
		LastOccurredAt: event.OccurredAt,
	}
	return database.ResolveTransaction(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "event_type"}},
		DoUpdates: clause.Assignments(map[string]any{
			"processed_count": gorm.Expr("processed_count + 1"),
			"last_event_id":   event.EventID, "last_tenant_id": event.TenantID,
			"last_occurred_at": event.OccurredAt, "updated_at": time.Now(),
		}),
	}).Create(&metric).Error
}
