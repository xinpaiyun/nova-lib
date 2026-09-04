package event

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	// ErrNoSubscriber 表示事件没有任何已注册消费者，发布器不得确认成功。
	ErrNoSubscriber = errors.New("事件没有已注册消费者")
)

// EventHandler 定义模块化单体内部事件消费者。
type EventHandler func(context.Context, OutboxEvent) error

type subscription struct {
	eventType    string
	consumerCode string
	handler      EventHandler
}

// LocalPublisher 将 Outbox 事件投递给进程内幂等消费者。
type LocalPublisher struct {
	service       *Service
	mu            sync.RWMutex
	subscriptions []subscription
	keys          map[string]bool
}

// NewLocalPublisher 创建进程内事件发布器。
func NewLocalPublisher(service *Service) *LocalPublisher {
	return &LocalPublisher{service: service, keys: map[string]bool{}}
}

// Subscribe 注册指定事件类型或通配符消费者。
func (p *LocalPublisher) Subscribe(eventType string, consumerCode string, handler EventHandler) error {
	eventType = strings.TrimSpace(eventType)
	consumerCode = strings.TrimSpace(consumerCode)
	if p == nil || p.service == nil || eventType == "" || consumerCode == "" || handler == nil {
		return errors.New("事件订阅参数不能为空")
	}
	key := eventType + "\x00" + consumerCode
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys[key] {
		return fmt.Errorf("事件消费者重复注册: %s/%s", eventType, consumerCode)
	}
	p.keys[key] = true
	p.subscriptions = append(p.subscriptions, subscription{
		eventType: eventType, consumerCode: consumerCode, handler: handler,
	})
	return nil
}

// Publish 将事件至少一次投递给所有匹配消费者。
func (p *LocalPublisher) Publish(ctx context.Context, event OutboxEvent) error {
	if p == nil || p.service == nil {
		return errors.New("本地事件发布器未初始化")
	}
	p.mu.RLock()
	matched := make([]subscription, 0, len(p.subscriptions))
	for _, item := range p.subscriptions {
		if item.eventType == "*" || item.eventType == event.EventType {
			matched = append(matched, item)
		}
	}
	p.mu.RUnlock()
	if len(matched) == 0 {
		return fmt.Errorf("%w: %s", ErrNoSubscriber, event.EventType)
	}
	for _, item := range matched {
		if _, err := p.service.ConsumeOnce(ctx, item.consumerCode, event.EventID, func(txCtx context.Context) error {
			return item.handler(txCtx, event)
		}); err != nil {
			return fmt.Errorf("消费者 %s 处理 %s 失败: %w", item.consumerCode, event.EventID, err)
		}
	}
	return nil
}
