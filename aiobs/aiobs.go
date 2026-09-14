// Package aiobs 提供 AI 模型调用的统一观测能力：结构化调用记录 + 进程内指标。
//
// 每次模型调用产生一条 Record，通过 Observer 接口分发：
//   - 内置日志观察者：统一输出事件 ai_call_record（成功 Info / 失败 Warn）；
//   - 内置进程内指标：按 scenario 累计调用数、失败数、耗时与 token，Snapshot() 导出快照；
//   - 业务方可 Register 附加观察者（如推送远端存储、对接 OTel），panic 被隔离不影响调用方。
//
// 模型客户端（如 openai 包）自动埋点，调用方仅需在请求里声明 Scenario 标识业务场景。
package aiobs

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// 调用类型。
const (
	KindText   = "text"   // 纯文本补全
	KindVision = "vision" // 图片理解
	KindOCR    = "ocr"    // 图片文字识别
)

// 调用结果。
const (
	OutcomeSuccess = "success" // 成功返回内容
	OutcomeFailed  = "failed"  // 调用出错
	OutcomeEmpty   = "empty"   // 上游返回空 choices
)

// Record 描述一次模型调用的观测记录。
type Record struct {
	Scenario         string    // 业务场景标识（调用方声明，空则归为 unspecified）
	Kind             string    // 调用类型：text/vision/ocr
	Model            string    // 实际使用的模型
	Outcome          string    // success/failed/empty
	Error            string    // 失败原因，成功为空
	ElapsedMs        int64     // 调用耗时（毫秒）
	PromptTokens     int       // 输入 token
	CompletionTokens int       // 输出 token
	TotalTokens      int       // 总 token
	Cost             float64   // 成本金额（按价格目录计算，币种见 Currency；调用方可显式赋值覆盖）
	Currency         string    // 成本币种：USD/CNY，随命中价格目录而定
	Priced           bool      // 是否命中价格目录完成计费
	StartedAt        time.Time // 调用开始时间
}

// Observer 接收一条调用记录。实现须自行保证并发安全，且不应长时间阻塞调用方。
type Observer interface {
	Observe(Record)
}

// ObserverFunc 函数式观察者适配器。
type ObserverFunc func(Record)

// Observe 实现 Observer 接口。
func (f ObserverFunc) Observe(record Record) { f(record) }

var (
	mu           sync.RWMutex
	extra        []Observer
	scenarioStat sync.Map // scenario -> *stat
	totalCalls   atomic.Uint64
	failedCalls  atomic.Uint64
)

// stat 单个场景的累计指标。
type stat struct {
	calls            atomic.Uint64
	failures         atomic.Uint64
	elapsedMs        atomic.Uint64
	promptTokens     atomic.Uint64
	completionTokens atomic.Uint64
}

// Register 注册附加观察者（在内置日志与指标之外追加）。传入 nil 被忽略。
func Register(observer Observer) {
	if observer == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	extra = append(extra, observer)
}

// Observe 分发一条记录：补齐计费字段 → 累计进程内指标 → 内置日志 → 附加观察者。
// 空字段做归一化（Outcome/Scenario 缺省），观察者 panic 被隔离。
func Observe(record Record) {
	if record.Outcome == "" {
		record.Outcome = OutcomeFailed
	}
	if record.Scenario == "" {
		record.Scenario = "unspecified"
	}
	applyPricing(&record)
	accumulate(record)
	logRecord(record)
	mu.RLock()
	observers := append([]Observer(nil), extra...)
	mu.RUnlock()
	for _, observer := range observers {
		observeSafely(observer, record)
	}
}

// observeSafely 隔离观察者 panic，保证观测自身不影响业务调用。
func observeSafely(observer Observer, record Record) {
	defer func() { _ = recover() }()
	observer.Observe(record)
}

// accumulate 累计进程内指标；非 success 一律计为失败。
func accumulate(record Record) {
	value, _ := scenarioStat.LoadOrStore(record.Scenario, new(stat))
	item := value.(*stat)
	item.calls.Add(1)
	item.elapsedMs.Add(uint64(record.ElapsedMs))
	item.promptTokens.Add(uint64(record.PromptTokens))
	item.completionTokens.Add(uint64(record.CompletionTokens))
	if record.Outcome != OutcomeSuccess {
		item.failures.Add(1)
		failedCalls.Add(1)
	}
	totalCalls.Add(1)
}

// logRecord 内置日志观察者，统一事件名 ai_call_record。
func logRecord(record Record) {
	attrs := []any{
		"scenario", record.Scenario, "kind", record.Kind, "model", record.Model,
		"outcome", record.Outcome, "elapsed_ms", record.ElapsedMs,
		"prompt_tokens", record.PromptTokens, "completion_tokens", record.CompletionTokens,
	}
	if record.Priced {
		attrs = append(attrs, "cost", record.Cost, "currency", record.Currency)
	}
	if record.Outcome == OutcomeSuccess {
		slog.Info("ai_call_record", attrs...)
		return
	}
	slog.Warn("ai_call_record", append(attrs, "error", record.Error)...)
}

// Snapshot 返回进程内指标快照，供运维端点透出。
func Snapshot() map[string]any {
	scenarios := map[string]any{}
	scenarioStat.Range(func(key, value any) bool {
		item := value.(*stat)
		scenarios[key.(string)] = map[string]any{
			"calls":            item.calls.Load(),
			"failures":         item.failures.Load(),
			"totalElapsedMs":   item.elapsedMs.Load(),
			"promptTokens":     item.promptTokens.Load(),
			"completionTokens": item.completionTokens.Load(),
		}
		return true
	})
	return map[string]any{
		"totalCalls":  totalCalls.Load(),
		"failedCalls": failedCalls.Load(),
		"scenarios":   scenarios,
	}
}
