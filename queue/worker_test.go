package queue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeQueue struct {
	mu       sync.Mutex
	messages []Message
	empty    int
	capacity int
}

func newFakeQueue(capacity int) *fakeQueue {
	return &fakeQueue{capacity: capacity}
}

func (q *fakeQueue) EnsureConsumerGroup(context.Context) error { return nil }

func (q *fakeQueue) Enqueue(_ context.Context, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) >= q.capacity {
		return errors.New("queue full")
	}
	q.messages = append(q.messages, Message{ID: "msg_" + jobID, JobID: jobID})
	return nil
}

func (q *fakeQueue) Receive(_ context.Context, _ string, _ time.Duration) (Message, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return Message{}, ErrEmpty
	}
	msg := q.messages[0]
	q.messages = q.messages[1:]
	return msg, nil
}

func (q *fakeQueue) Ack(_ context.Context, _ string) error { return nil }

func TestWorkerProcessesAllEnqueuedJobs(t *testing.T) {
	fq := newFakeQueue(10)
	for i := 0; i < 5; i++ {
		if err := fq.Enqueue(context.Background(), "job_"+string(rune('a'+i))); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}
	var mu sync.Mutex
	processed := map[string]bool{}
	handler := func(_ context.Context, jobID string) error {
		mu.Lock()
		processed[jobID] = true
		mu.Unlock()
		return nil
	}
	worker := NewWorker(fq, handler, "test-worker", 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker.Start(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(processed)
		mu.Unlock()
		if count >= 5 {
			worker.Stop()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	worker.Stop()
	mu.Lock()
	t.Fatalf("processed %d jobs, want 5, processed=%v", len(processed), processed)
}
