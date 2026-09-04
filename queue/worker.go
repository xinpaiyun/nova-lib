package queue

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"
)

const (
	defaultWorkerBlock = 2 * time.Second
)

// Worker 持续从队列认领并执行任务。
type Worker struct {
	queue    Queue
	handler  Handler
	consumer string
	block    time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	wait      sync.WaitGroup
}

// NewWorker 创建任务消费 Worker。
// consumer 为空时使用 hostname-pid 作为消费者标识。
func NewWorker(queue Queue, handler Handler, consumer string, block time.Duration) *Worker {
	if consumer == "" {
		consumer = consumerName()
	}
	if block <= 0 {
		block = defaultWorkerBlock
	}
	return &Worker{queue: queue, handler: handler, consumer: consumer, block: block}
}

// Start 启动单个可重复安全调用的消费循环。
func (w *Worker) Start(parent context.Context) {
	if w == nil || w.queue == nil || w.handler == nil {
		return
	}
	w.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		w.cancel = cancel
		w.wait.Add(1)
		go w.run(ctx)
	})
}

// Stop 停止 Worker 并等待当前任务处理结束。
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

func (w *Worker) run(ctx context.Context) {
	defer w.wait.Done()
	if err := w.queue.EnsureConsumerGroup(ctx); err != nil {
		slog.Error("queue worker init consumer group failed", "error", err)
		return
	}
	slog.Info("queue worker ready", "consumer", w.consumer)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			message, err := w.queue.Receive(ctx, w.consumer, w.block)
			if errors.Is(err, ErrEmpty) {
				continue
			}
			if err != nil {
				slog.Error("queue worker receive failed", "error", err)
				time.Sleep(time.Second)
				continue
			}
			if err := w.handler(ctx, message.JobID); err != nil {
				slog.Error("queue worker process failed", "job_id", message.JobID, "error", err)
				continue
			}
			if err := w.queue.Ack(ctx, message.ID); err != nil {
				slog.Error("queue worker ack failed", "message_id", message.ID, "error", err)
			}
		}
	}
}

func consumerName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "worker"
	}
	return hostname + "-" + string(rune(os.Getpid()))
}
