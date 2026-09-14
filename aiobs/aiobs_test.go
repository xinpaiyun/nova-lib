package aiobs

import (
	"sync"
	"testing"
	"time"
)

// resetTestState 清理全局指标与附加观察者，保证用例互不干扰。
func resetTestState(t *testing.T) {
	t.Helper()
	mu.Lock()
	extra = nil
	mu.Unlock()
	scenarioStat = sync.Map{}
	totalCalls.Store(0)
	failedCalls.Store(0)
	t.Cleanup(func() {
		mu.Lock()
		extra = nil
		mu.Unlock()
		scenarioStat = sync.Map{}
		totalCalls.Store(0)
		failedCalls.Store(0)
	})
}

// TestObserveDefaults 验证空字段归一化与指标累计。
func TestObserveDefaults(t *testing.T) {
	resetTestState(t)
	Observe(Record{Kind: KindText, Model: "m1", ElapsedMs: 120, PromptTokens: 10, CompletionTokens: 20, StartedAt: time.Now()})
	Observe(Record{Scenario: "s1", Kind: KindText, Model: "m1", Outcome: OutcomeSuccess, ElapsedMs: 80, PromptTokens: 5, CompletionTokens: 7})

	snapshot := Snapshot()
	if snapshot["totalCalls"].(uint64) != 2 {
		t.Fatalf("totalCalls = %v, want 2", snapshot["totalCalls"])
	}
	if snapshot["failedCalls"].(uint64) != 1 {
		t.Fatalf("failedCalls = %v, want 1（Outcome 空缺省为 failed）", snapshot["failedCalls"])
	}
	scenarios := snapshot["scenarios"].(map[string]any)
	unspecified := scenarios["unspecified"].(map[string]any)
	if unspecified["failures"].(uint64) != 1 {
		t.Fatalf("unspecified failures = %v, want 1", unspecified["failures"])
	}
	s1 := scenarios["s1"].(map[string]any)
	if s1["calls"].(uint64) != 1 || s1["completionTokens"].(uint64) != 7 {
		t.Fatalf("s1 stat = %v", s1)
	}
}

// TestRegisterObservers 验证附加观察者按注册顺序收到记录，且 panic 被隔离。
func TestRegisterObservers(t *testing.T) {
	resetTestState(t)
	var muProtect sync.Mutex
	var received []Record
	Register(ObserverFunc(func(Record) { panic("observer boom") }))
	Register(ObserverFunc(func(record Record) {
		muProtect.Lock()
		defer muProtect.Unlock()
		received = append(received, record)
	}))

	Observe(Record{Scenario: "s1", Kind: KindOCR, Outcome: OutcomeFailed, Error: "boom", ElapsedMs: 10})
	if len(received) != 1 {
		t.Fatalf("panic 隔离失败：后续观察者未收到记录，received=%d", len(received))
	}
	if received[0].Scenario != "s1" || received[0].Error != "boom" {
		t.Fatalf("record = %+v", received[0])
	}
	if failedCalls.Load() != 1 {
		t.Fatalf("failedCalls = %d, want 1", failedCalls.Load())
	}
}

// TestRegisterNil 验证 nil 观察者被忽略。
func TestRegisterNil(t *testing.T) {
	resetTestState(t)
	Register(nil)
	Register(nil)
	Observe(Record{Scenario: "s1", Outcome: OutcomeSuccess})
	if totalCalls.Load() != 1 {
		t.Fatalf("totalCalls = %d, want 1", totalCalls.Load())
	}
}

// TestObserverFunc 适配器 smoke。
func TestObserverFunc(t *testing.T) {
	called := false
	ObserverFunc(func(Record) { called = true }).Observe(Record{})
	if !called {
		t.Fatal("ObserverFunc 未被调用")
	}
}
