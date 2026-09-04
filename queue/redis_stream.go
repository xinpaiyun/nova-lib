package queue

import (
	"context"
	"errors"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	defaultStream         = "app:jobs"
	defaultGroup          = "app-workers"
	defaultPendingMinIdle = time.Minute
	defaultReadBlock      = 2 * time.Second
	defaultJobIDField     = "job_id"
)

// RedisStreamQueue 使用 Redis Streams 管理通用任务队列。
type RedisStreamQueue struct {
	client         goredis.UniversalClient
	stream         string
	group          string
	jobIDField     string
	pendingMinIdle time.Duration
}

// NewRedisStreamQueue 创建 Redis Streams 任务队列。
// stream 和 group 为空时使用默认值，jobIDField 为空时使用 "job_id"。
func NewRedisStreamQueue(client goredis.UniversalClient, stream string, group string, jobIDField string) *RedisStreamQueue {
	stream = strings.TrimSpace(stream)
	if stream == "" {
		stream = defaultStream
	}
	group = strings.TrimSpace(group)
	if group == "" {
		group = defaultGroup
	}
	jobIDField = strings.TrimSpace(jobIDField)
	if jobIDField == "" {
		jobIDField = defaultJobIDField
	}
	return &RedisStreamQueue{
		client:         client,
		stream:         stream,
		group:          group,
		jobIDField:     jobIDField,
		pendingMinIdle: defaultPendingMinIdle,
	}
}

// EnsureConsumerGroup 确保消费组存在。
func (q *RedisStreamQueue) EnsureConsumerGroup(ctx context.Context) error {
	if q == nil || q.client == nil {
		return errors.New("redis stream queue is not initialized")
	}
	err := q.client.XGroupCreateMkStream(ctx, q.stream, q.group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

// Enqueue 将任务 ID 投递到 Redis Stream。
func (q *RedisStreamQueue) Enqueue(ctx context.Context, jobID string) error {
	if q == nil || q.client == nil {
		return errors.New("redis stream queue is not initialized")
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return errors.New("job id is empty")
	}
	return q.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: q.stream,
		Values: map[string]any{q.jobIDField: jobID},
	}).Err()
}

// Receive 读取一条任务消息，优先认领长时间未确认的 pending 消息。
func (q *RedisStreamQueue) Receive(ctx context.Context, consumer string, block time.Duration) (Message, error) {
	if q == nil || q.client == nil {
		return Message{}, errors.New("redis stream queue is not initialized")
	}
	consumer = strings.TrimSpace(consumer)
	if consumer == "" {
		consumer = "worker"
	}
	if message, err := q.claimPending(ctx, consumer); err == nil {
		return message, nil
	} else if !errors.Is(err, ErrEmpty) {
		return Message{}, err
	}
	if block <= 0 {
		block = defaultReadBlock
	}
	streams, err := q.client.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group:    q.group,
		Consumer: consumer,
		Streams:  []string{q.stream, ">"},
		Count:    1,
		Block:    block,
	}).Result()
	if errors.Is(err, goredis.Nil) {
		return Message{}, ErrEmpty
	}
	if err != nil {
		return Message{}, err
	}
	return firstMessage(streams, q.jobIDField)
}

// Ack 确认指定消息已处理。
func (q *RedisStreamQueue) Ack(ctx context.Context, messageID string) error {
	if q == nil || q.client == nil {
		return errors.New("redis stream queue is not initialized")
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	return q.client.XAck(ctx, q.stream, q.group, messageID).Err()
}

// claimPending 认领长时间未确认的 pending 消息，避免 worker 异常退出后任务卡住。
func (q *RedisStreamQueue) claimPending(ctx context.Context, consumer string) (Message, error) {
	messages, _, err := q.client.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: consumer,
		MinIdle:  q.pendingMinIdle,
		Start:    "0-0",
		Count:    1,
	}).Result()
	if errors.Is(err, goredis.Nil) {
		return Message{}, ErrEmpty
	}
	if err != nil {
		return Message{}, err
	}
	if len(messages) == 0 {
		return Message{}, ErrEmpty
	}
	return parseMessage(messages[0], q.jobIDField)
}

func firstMessage(streams []goredis.XStream, jobIDField string) (Message, error) {
	for _, stream := range streams {
		for _, message := range stream.Messages {
			return parseMessage(message, jobIDField)
		}
	}
	return Message{}, ErrEmpty
}

func parseMessage(message goredis.XMessage, jobIDField string) (Message, error) {
	jobID := strings.TrimSpace(toString(message.Values[jobIDField]))
	if jobID == "" {
		return Message{}, errors.New("redis stream message missing job id")
	}
	return Message{ID: message.ID, JobID: jobID}, nil
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}
