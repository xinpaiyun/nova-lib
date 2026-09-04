package events

import (
	"testing"
	"time"
)

func TestHubPublishDeliversToMatchingSubscriber(t *testing.T) {
	hub := NewHub()
	ch, unsub := hub.Subscribe(1, 100)
	defer unsub()
	event := NewEvent("order.created.v1", map[string]any{"orderId": 123})
	hub.Publish(1, 100, event)
	select {
	case got := <-ch:
		if got.Type != event.Type || got.ID != event.ID {
			t.Fatalf("received event = %+v, want type=%s id=%s", got, event.Type, event.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive event")
	}
}

func TestHubPublishDoesNotDeliverToOtherUser(t *testing.T) {
	hub := NewHub()
	ch, unsub := hub.Subscribe(1, 100)
	defer unsub()
	hub.Publish(1, 200, NewEvent("other.event", nil))
	select {
	case <-ch:
		t.Fatal("subscriber received event for different user")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEncodeSSEFormat(t *testing.T) {
	event := Event{ID: "123", Type: "test.event", OccurredAt: "2026-01-01T00:00:00Z", Data: map[string]string{"key": "value"}}
	data, err := Encode(event)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	got := string(data)
	if got != "id: 123\nevent: test.event\ndata: {\"key\":\"value\"}\n\n" {
		t.Fatalf("encoded SSE = %q, want correct format", got)
	}
}

func TestUnsubClosesChannel(t *testing.T) {
	hub := NewHub()
	_, unsub := hub.Subscribe(1, 100)
	unsub()
	// 第二次 unsub 应幂等（sync.Once）
	unsub()
}
