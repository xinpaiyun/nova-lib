package event

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultMaxAttempts = 8
	defaultBatchSize   = 100
	lockTimeout        = 5 * time.Minute
	maxErrorLength     = 2000
)

// AppendInput 定义标准领域事件写入参数。
type AppendInput struct {
	EventType     string
	SchemaVersion int
	Producer      string
	TenantID      uint64
	SubjectType   string
	SubjectID     uint64
	AggregateType string
	AggregateID   uint64
	PrivacyLevel  string
	TraceID       string
	Payload       any
	OccurredAt    time.Time
	MaxAttempts   int
}

// Publisher 定义 Outbox 至少一次投递端口。
type Publisher interface {
	Publish(context.Context, OutboxEvent) error
}

// DispatchResult 汇总单批 Outbox 投递结果。
type DispatchResult struct {
	Claimed    int
	Published  int
	Retrying   int
	DeadLetter int
}

// Service 封装 Outbox、重试、幂等消费和积压观测。
type Service struct {
	repo *Repository
	now  func() time.Time
}

// NewService 创建事件中心服务。
func NewService(repo *Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Append 将领域事件信封写入当前业务事务。
func (s *Service) Append(ctx context.Context, input AppendInput) (*OutboxEvent, error) {
	if err := validateAppendInput(input); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input.Payload)
	if err != nil {
		return nil, fmt.Errorf("事件 Payload 无法序列化: %w", err)
	}
	occurredAt := input.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	schemaVersion := input.SchemaVersion
	if schemaVersion <= 0 {
		schemaVersion = 1
	}
	maxAttempts := input.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	event := &OutboxEvent{
		EventID: newIdentifier("evt"), EventType: strings.TrimSpace(input.EventType),
		SchemaVersion: schemaVersion, Producer: strings.TrimSpace(input.Producer),
		TenantID:      input.TenantID,
		SubjectType:   strings.TrimSpace(input.SubjectType), SubjectID: input.SubjectID,
		AggregateType: strings.TrimSpace(input.AggregateType), AggregateID: input.AggregateID,
		PrivacyLevel:  strings.TrimSpace(input.PrivacyLevel),
		TraceID:       strings.TrimSpace(input.TraceID),
		Payload: string(payload), Status: OutboxStatusPending,
		MaxAttempts: maxAttempts, AvailableAt: occurredAt, OccurredAt: occurredAt,
	}
	if err := s.repo.Append(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

// DispatchBatch 认领并至少一次投递一批 Outbox 事件。
func (s *Service) DispatchBatch(ctx context.Context, publisher Publisher, batchSize int) (DispatchResult, error) {
	if publisher == nil {
		return DispatchResult{}, errors.New("事件发布器不能为空")
	}
	if batchSize <= 0 || batchSize > defaultBatchSize {
		batchSize = defaultBatchSize
	}
	now := s.now()
	lockToken := newIdentifier("lock")
	events, err := s.repo.ClaimBatch(ctx, now, now.Add(-lockTimeout), batchSize, lockToken)
	if err != nil {
		return DispatchResult{}, err
	}
	result := DispatchResult{Claimed: len(events)}
	for index := range events {
		event := events[index]
		if err := publisher.Publish(ctx, event); err != nil {
			status := OutboxStatusFailed
			availableAt := now.Add(retryDelay(event.Attempts))
			if event.Attempts >= event.MaxAttempts {
				status = OutboxStatusDeadLetter
				availableAt = now
				result.DeadLetter++
			} else {
				result.Retrying++
			}
			if markErr := s.repo.MarkFailed(
				ctx, event.ID, lockToken, status, availableAt,
				truncateError(err.Error()), now,
			); markErr != nil {
				return result, markErr
			}
			continue
		}
		if err := s.repo.MarkPublished(ctx, event.ID, lockToken, now); err != nil {
			return result, err
		}
		result.Published++
	}
	return result, nil
}

// ConsumeOnce 在事务中幂等执行指定消费者。
func (s *Service) ConsumeOnce(
	ctx context.Context,
	consumerCode string,
	eventID string,
	callback func(context.Context) error,
) (bool, error) {
	consumerCode = strings.TrimSpace(consumerCode)
	eventID = strings.TrimSpace(eventID)
	if consumerCode == "" || eventID == "" || callback == nil {
		return false, errors.New("消费者、事件和处理函数不能为空")
	}
	now := s.now()
	processed, err := s.repo.ConsumeOnce(ctx, consumerCode, eventID, now, callback)
	if err == nil {
		return processed, nil
	}
	if markErr := s.repo.MarkConsumptionFailed(
		ctx, consumerCode, eventID, truncateError(err.Error()), now,
	); markErr != nil {
		return false, fmt.Errorf("消费失败: %v; 记录失败状态: %w", err, markErr)
	}
	return false, err
}

// Backlog 返回 Outbox 当前积压指标。
func (s *Service) Backlog(ctx context.Context) (BacklogMetrics, error) {
	return s.repo.Backlog(ctx)
}

// validateAppendInput 校验事件类型、生产者、聚合和隐私级别。
func validateAppendInput(input AppendInput) error {
	switch {
	case strings.TrimSpace(input.EventType) == "":
		return errors.New("事件类型不能为空")
	case strings.TrimSpace(input.Producer) == "":
		return errors.New("事件生产者不能为空")
	case strings.TrimSpace(input.AggregateType) == "" || input.AggregateID == 0:
		return errors.New("事件聚合不能为空")
	case strings.TrimSpace(input.PrivacyLevel) == "":
		return errors.New("事件隐私级别不能为空")
	default:
		return nil
	}
}

// retryDelay 按投递次数计算有上限的指数退避时间。
func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 {
		attempt = 10
	}
	delay := time.Second * time.Duration(1<<(attempt-1))
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// truncateError 截断可能包含上游长响应的错误文本。
func truncateError(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxErrorLength {
		return string(runes)
	}
	return string(runes[:maxErrorLength])
}

// newIdentifier 生成不依赖外部服务的随机事件或锁标识。
func newIdentifier(prefix string) string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buffer)
}
