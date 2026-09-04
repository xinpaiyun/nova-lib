package event

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestWorkerDispatchesToIdempotentMetricSubscriber(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:event_worker?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(ModelTypes()...); err != nil {
		t.Fatalf("migrate event models failed: %v", err)
	}
	repo := NewRepository(db)
	svc := NewService(repo)
	publisher := NewLocalPublisher(svc)
	if err := publisher.Subscribe("*", "event.metrics", func(ctx context.Context, event OutboxEvent) error {
		return repo.RecordEventTypeMetric(ctx, event)
	}); err != nil {
		t.Fatalf("subscribe metrics failed: %v", err)
	}
	event, err := svc.Append(context.Background(), AppendInput{
		EventType: "resource.reservation_created.v1", Producer: "resource",
		TenantID: 1001, AggregateType: "resource_reservation", AggregateID: 2001,
		PrivacyLevel: "tenant_private", Payload: map[string]any{"resourceId": 3001},
	})
	if err != nil {
		t.Fatalf("append event failed: %v", err)
	}
	worker := NewWorker(svc, publisher, 10*time.Millisecond, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)
	t.Cleanup(worker.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var stored OutboxEvent
		if err := db.First(&stored, event.ID).Error; err == nil && stored.Status == OutboxStatusPublished {
			var metric EventTypeMetric
			if err := db.Where("event_type = ?", event.EventType).First(&metric).Error; err != nil {
				t.Fatalf("event metric not found: %v", err)
			}
			if metric.ProcessedCount != 1 || metric.LastEventID != event.EventID {
				t.Fatalf("event metric = %+v, want one processed event", metric)
			}
			worker.Stop()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not publish event before deadline")
}

func TestBacklogRequiresAlert(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		metrics BacklogMetrics
		want    bool
	}{
		{name: "empty", metrics: BacklogMetrics{}, want: false},
		{name: "recent pending", metrics: BacklogMetrics{
			Pending: 1, OldestAt: timePointer(now.Add(-time.Minute)),
		}, want: false},
		{name: "old pending", metrics: BacklogMetrics{
			Pending: 1, OldestAt: timePointer(now.Add(-oldestAlertThreshold)),
		}, want: true},
		{name: "failed", metrics: BacklogMetrics{Failed: 1}, want: true},
		{name: "dead letter", metrics: BacklogMetrics{DeadLetter: 1}, want: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := backlogRequiresAlert(testCase.metrics, now); got != testCase.want {
				t.Fatalf("backlogRequiresAlert() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
