package event

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultInterval      = 5 * time.Second
	defaultWorkerBatch   = 100
	defaultAlertInterval = time.Minute
	oldestAlertThreshold = 5 * time.Minute
)

// Worker 持续认领并投递 Outbox 事件。
type Worker struct {
	service   *Service
	publisher Publisher
	interval  time.Duration
	batchSize int

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	wait      sync.WaitGroup
	lastAlert time.Time
}

// NewWorker 创建 Outbox Dispatcher Worker。
func NewWorker(service *Service, publisher Publisher, interval time.Duration, batchSize int) *Worker {
	if interval <= 0 {
		interval = defaultInterval
	}
	if batchSize <= 0 {
		batchSize = defaultWorkerBatch
	}
	return &Worker{
		service: service, publisher: publisher, interval: interval, batchSize: batchSize,
	}
}

// Start 启动单个可重复安全调用的 Dispatcher 循环。
func (w *Worker) Start(parent context.Context) {
	if w == nil || w.service == nil || w.publisher == nil {
		return
	}
	w.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		w.cancel = cancel
		w.wait.Add(1)
		go w.run(ctx)
	})
}

// Stop 停止 Worker 并等待当前投递批次结束。
func (w *Worker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		w.wait.Wait()
	})
}

// run 按固定间隔投递，避免服务启动阶段被历史 Outbox backlog 阻塞。
func (w *Worker) run(ctx context.Context) {
	defer w.wait.Done()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.dispatchAvailable(ctx)
		}
	}
}

// dispatchAvailable 投递一批可用事件，避免单轮无限排空历史积压。
func (w *Worker) dispatchAvailable(ctx context.Context) {
	defer w.observeBacklog(ctx)
	if ctx.Err() != nil {
		return
	}
	result, err := w.service.DispatchBatch(ctx, w.publisher, w.batchSize)
	if err != nil {
		slog.Error("outbox dispatch failed", "error", err)
		return
	}
	if result.Claimed > 0 {
		slog.Info(
			"outbox batch dispatched",
			"claimed", result.Claimed,
			"published", result.Published,
			"retrying", result.Retrying,
			"dead_letter", result.DeadLetter,
		)
	}
}

// observeBacklog 对失败、死信或超时积压输出节流的生产告警日志。
func (w *Worker) observeBacklog(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	metrics, err := w.service.Backlog(ctx)
	if err != nil {
		slog.Error("outbox backlog observation failed", "error", err)
		return
	}
	now := time.Now()
	if !backlogRequiresAlert(metrics, now) {
		return
	}
	if !w.lastAlert.IsZero() && now.Sub(w.lastAlert) < defaultAlertInterval {
		return
	}
	w.lastAlert = now
	oldestAgeSeconds := int64(0)
	if metrics.OldestAt != nil && now.After(*metrics.OldestAt) {
		oldestAgeSeconds = int64(now.Sub(*metrics.OldestAt).Seconds())
	}
	slog.Warn(
		"outbox backlog alert",
		"pending", metrics.Pending,
		"processing", metrics.Processing,
		"failed", metrics.Failed,
		"dead_letter", metrics.DeadLetter,
		"oldest_age_seconds", oldestAgeSeconds,
	)
}

// backlogRequiresAlert 判断当前积压是否越过生产告警边界。
func backlogRequiresAlert(metrics BacklogMetrics, now time.Time) bool {
	if metrics.Failed > 0 || metrics.DeadLetter > 0 {
		return true
	}
	return metrics.OldestAt != nil && now.Sub(*metrics.OldestAt) >= oldestAlertThreshold
}
