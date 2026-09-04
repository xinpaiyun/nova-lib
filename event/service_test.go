package event

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xinpaiyun/nova-lib/database"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type publisherFunc func(context.Context, OutboxEvent) error

func (f publisherFunc) Publish(ctx context.Context, event OutboxEvent) error {
	return f(ctx, event)
}

type eventProjection struct {
	ID      uint64 `gorm:"primaryKey"`
	EventID string `gorm:"size:64;uniqueIndex"`
}

func TestAppendParticipatesInBusinessTransaction(t *testing.T) {
	svc, db := newEventTestService(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		ctx := database.ContextWithTransaction(context.Background(), tx)
		if _, err := svc.Append(ctx, testAppendInput()); err != nil {
			return err
		}
		return errors.New("rollback business")
	})
	if err == nil {
		t.Fatal("business transaction error = nil")
	}
	var count int64
	if err := db.Model(&OutboxEvent{}).Count(&count).Error; err != nil {
		t.Fatalf("count outbox failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("outbox count after rollback = %d, want 0", count)
	}
}

func TestDispatchBatchRetriesAndMovesToDeadLetter(t *testing.T) {
	svc, db := newEventTestService(t)
	now := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	input := testAppendInput()
	input.MaxAttempts = 2
	event, err := svc.Append(context.Background(), input)
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}
	failing := publisherFunc(func(context.Context, OutboxEvent) error {
		return errors.New("publisher unavailable")
	})
	first, err := svc.DispatchBatch(context.Background(), failing, 10)
	if err != nil {
		t.Fatalf("first dispatch failed: %v", err)
	}
	if first.Retrying != 1 || first.DeadLetter != 0 {
		t.Fatalf("first dispatch = %+v, want retry", first)
	}
	now = now.Add(2 * time.Second)
	second, err := svc.DispatchBatch(context.Background(), failing, 10)
	if err != nil {
		t.Fatalf("second dispatch failed: %v", err)
	}
	if second.DeadLetter != 1 {
		t.Fatalf("second dispatch = %+v, want dead letter", second)
	}
	var stored OutboxEvent
	if err := db.First(&stored, event.ID).Error; err != nil {
		t.Fatalf("find outbox failed: %v", err)
	}
	if stored.Status != OutboxStatusDeadLetter || stored.Attempts != 2 || stored.LastError == "" {
		t.Fatalf("dead letter event = %+v", stored)
	}
}

func TestConsumeOnceIsTransactionalAndIdempotent(t *testing.T) {
	svc, db := newEventTestService(t)
	callback := func(ctx context.Context) error {
		return database.ResolveTransaction(ctx, db).WithContext(ctx).
			Create(&eventProjection{EventID: "evt_consume"}).Error
	}
	processed, err := svc.ConsumeOnce(context.Background(), "timeline", "evt_consume", callback)
	if err != nil || !processed {
		t.Fatalf("first consume processed/error = %v/%v", processed, err)
	}
	processed, err = svc.ConsumeOnce(context.Background(), "timeline", "evt_consume", callback)
	if err != nil || processed {
		t.Fatalf("second consume processed/error = %v/%v, want false/nil", processed, err)
	}
	var count int64
	if err := db.Model(&eventProjection{}).Count(&count).Error; err != nil {
		t.Fatalf("count projection failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("projection count = %d, want 1", count)
	}
}

func TestConsumeFailureRollsBackProjectionAndRecordsFailure(t *testing.T) {
	svc, db := newEventTestService(t)
	processed, err := svc.ConsumeOnce(context.Background(), "search", "evt_failure", func(ctx context.Context) error {
		if err := database.ResolveTransaction(ctx, db).WithContext(ctx).
			Create(&eventProjection{EventID: "evt_failure"}).Error; err != nil {
			return err
		}
		return errors.New("projection failed")
	})
	if err == nil || processed {
		t.Fatalf("failed consume processed/error = %v/%v", processed, err)
	}
	var projectionCount int64
	if err := db.Model(&eventProjection{}).Where("event_id = ?", "evt_failure").Count(&projectionCount).Error; err != nil {
		t.Fatalf("count failed projection failed: %v", err)
	}
	if projectionCount != 0 {
		t.Fatalf("failed projection count = %d, want 0", projectionCount)
	}
	var consumption EventConsumption
	if err := db.Where("consumer_code = ? AND event_id = ?", "search", "evt_failure").
		First(&consumption).Error; err != nil {
		t.Fatalf("failed consumption record not found: %v", err)
	}
	if consumption.Status != ConsumptionStatusFailed || consumption.LastError == "" {
		t.Fatalf("failed consumption = %+v", consumption)
	}
}

func TestLocalPublisherRejectsEventsWithoutSubscribers(t *testing.T) {
	svc, _ := newEventTestService(t)
	publisher := NewLocalPublisher(svc)
	err := publisher.Publish(context.Background(), OutboxEvent{
		EventID: "evt_without_subscriber", EventType: "unknown.event.v1",
	})
	if !errors.Is(err, ErrNoSubscriber) {
		t.Fatalf("publish without subscriber err = %v, want ErrNoSubscriber", err)
	}
}

func TestBacklogReturnsCounts(t *testing.T) {
	svc, _ := newEventTestService(t)
	now := time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	if _, err := svc.Append(context.Background(), testAppendInput()); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	metrics, err := svc.Backlog(context.Background())
	if err != nil {
		t.Fatalf("backlog failed: %v", err)
	}
	if metrics.Pending != 1 || metrics.Processing != 0 {
		t.Fatalf("backlog = %+v, want pending 1", metrics)
	}
}

func newEventTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	models := append(ModelTypes(), &eventProjection{})
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate event models failed: %v", err)
	}
	return NewService(NewRepository(db)), db
}

func testAppendInput() AppendInput {
	return AppendInput{
		EventType: "membership.upgraded.v1", Producer: "membership",
		TenantID: 1001,
		SubjectType: "user", SubjectID: 3001,
		AggregateType: "membership", AggregateID: 4001,
		PrivacyLevel: "tenant_private",
		Payload:      map[string]any{"tier": "gold"},
	}
}
