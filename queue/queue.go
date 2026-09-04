package queue

import (
	"context"
	"errors"
	"time"
)

// Queue 定义异步任务队列的通用消费端口。
type Queue interface {
	EnsureConsumerGroup(ctx context.Context) error
	Enqueue(ctx context.Context, jobID string) error
	Receive(ctx context.Context, consumer string, block time.Duration) (Message, error)
	Ack(ctx context.Context, messageID string) error
}

// Message 表示队列中的一条任务消息。
type Message struct {
	ID    string
	JobID string
}

// Handler 处理单个任务，返回 nil 表示处理成功。
type Handler func(ctx context.Context, jobID string) error

// ErrEmpty 表示当前队列没有可消费任务。
var ErrEmpty = errors.New("job queue empty")
