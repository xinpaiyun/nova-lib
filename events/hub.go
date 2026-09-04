// Package events 提供基于内存的发布/订阅 SSE Hub，
// 按 tenantID:userID 维度管理订阅者，支持进程内实时事件推送。
package events

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Event 表示推送到前端的 SSE 事件。
type Event struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	OccurredAt string `json:"occurred_at"`
	Data       any    `json:"data"`
}

type subscriber struct {
	channel chan Event
}

// Hub 管理按 tenantID:userID 分组的订阅者，支持多通道并发推送。
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]map[*subscriber]struct{}
}

// NewHub 创建内存事件订阅中心。
func NewHub() *Hub {
	return &Hub{subscribers: make(map[string]map[*subscriber]struct{})}
}

// NewEvent 创建带当前时间戳的 SSE 事件。
func NewEvent(eventType string, data any) Event {
	now := time.Now()
	return Event{
		ID:         fmt.Sprintf("%d", now.UnixNano()),
		Type:       eventType,
		OccurredAt: now.Format(time.RFC3339),
		Data:       data,
	}
}

// Subscribe 注册当前用户订阅，返回事件通道和取消订阅函数。
func (h *Hub) Subscribe(tenantID, userID uint64) (<-chan Event, func()) {
	sub := &subscriber{channel: make(chan Event, 16)}
	key := subscriberKey(tenantID, userID)
	h.mu.Lock()
	if h.subscribers[key] == nil {
		h.subscribers[key] = make(map[*subscriber]struct{})
	}
	h.subscribers[key][sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return sub.channel, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subscribers[key], sub)
			if len(h.subscribers[key]) == 0 {
				delete(h.subscribers, key)
			}
			close(sub.channel)
			h.mu.Unlock()
		})
	}
}

// Publish 将事件推送到指定用户的全部订阅通道。
func (h *Hub) Publish(tenantID, userID uint64, event Event) {
	key := subscriberKey(tenantID, userID)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for sub := range h.subscribers[key] {
		select {
		case sub.channel <- event:
		default:
		}
	}
}

// Encode 将事件序列化为 SSE 文本格式。
func Encode(event Event) ([]byte, error) {
	payload, err := json.Marshal(event.Data)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload)), nil
}

func subscriberKey(tenantID, userID uint64) string {
	return fmt.Sprintf("%d:%d", tenantID, userID)
}
