package aiobs

import (
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := MigrateAICallRecords(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// TestMySQLStorePersist 验证异步落库：Close 冲刷后记录完整入库，含计费字段。
func TestMySQLStorePersist(t *testing.T) {
	resetTestState(t)
	db := newTestDB(t)
	store := NewMySQLStore(db, WithBufferSize(8), WithBatchSize(2), WithFlushInterval(time.Hour))
	Register(store)

	Observe(Record{Scenario: "s1", Kind: KindText, Model: "qwen-plus", Outcome: OutcomeSuccess,
		PromptTokens: 1_000_000, CompletionTokens: 1_000_000, ElapsedMs: 120, StartedAt: time.Now()})
	Observe(Record{Scenario: "s1", Kind: KindText, Model: "unknown-model", Outcome: OutcomeFailed,
		Error: "timeout", PromptTokens: 500, CompletionTokens: 100, ElapsedMs: 60, StartedAt: time.Now()})
	// 打满缓冲触发丢弃计数。
	Observe(Record{Scenario: "s2", Kind: KindOCR, Outcome: OutcomeSuccess, PromptTokens: 1, StartedAt: time.Now()})
	for i := 0; i < 20; i++ {
		Observe(Record{Scenario: "flood", Kind: KindText, Outcome: OutcomeSuccess, StartedAt: time.Now()})
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if store.Written() == 0 {
		t.Fatal("written = 0，应有记录落库")
	}
	if store.Dropped() == 0 {
		t.Fatal("dropped = 0，缓冲 8 而投递 23 条，应有丢弃")
	}

	var records []AICallRecord
	if err := db.Where("scenario = ?", "s1").Order("id asc").Find(&records).Error; err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("s1 records = %d, want 2", len(records))
	}
	success, failed := records[0], records[1]
	if !success.Priced || success.Currency != CurrencyCNY {
		t.Fatalf("success pricing: priced=%v currency=%q", success.Priced, success.Currency)
	}
	// qwen-plus 0.8/2 元每 1M：输入+输出各 1M → 2.8 元。
	if success.Cost < 2.799 || success.Cost > 2.801 {
		t.Fatalf("success cost = %v, want 2.8", success.Cost)
	}
	if failed.Priced {
		t.Fatal("未收录模型不应计费")
	}
	if failed.Cost != 0 || failed.Error != "timeout" {
		t.Fatalf("failed record: cost=%v error=%q", failed.Cost, failed.Error)
	}
}

// TestSetModelPrices 验证业务方覆盖价生效（如自定义部署名）。
func TestSetModelPrices(t *testing.T) {
	SetModelPrices(map[string]ModelPrice{"qwen3.7-flash": {PromptPer1M: 0.5, CompletionPer1M: 1.5, Currency: CurrencyCNY}})
	defer func() {
		priceMu.Lock()
		overridPrices = nil
		priceMu.Unlock()
	}()

	record := Record{Model: "qwen3.7-flash", PromptTokens: 100_000, CompletionTokens: 100_000}
	applyPricing(&record)
	if !record.Priced || record.Currency != CurrencyCNY {
		t.Fatalf("priced=%v currency=%q", record.Priced, record.Currency)
	}
	if record.Cost < 0.199 || record.Cost > 0.201 {
		t.Fatalf("cost = %v, want 0.2", record.Cost)
	}

	// 未显式 Cost 时按目录计算；显式 Cost 则保留调用方赋值。
	explicit := Record{Model: "qwen3.7-flash", PromptTokens: 100_000, CompletionTokens: 100_000, Cost: 9.9}
	applyPricing(&explicit)
	if explicit.Cost != 9.9 {
		t.Fatalf("explicit cost = %v, want 9.9", explicit.Cost)
	}
}

// TestLookupModelPrice 验证内置目录查询。
func TestLookupModelPrice(t *testing.T) {
	if price, ok := LookupModelPrice("gpt-4o"); !ok || price.Currency != CurrencyUSD || price.PromptPer1M != 2.5 {
		t.Fatalf("gpt-4o price = %+v ok=%v", price, ok)
	}
	if _, ok := LookupModelPrice("no-such-model"); ok {
		t.Fatal("未收录模型应返回 false")
	}
}

// TestConcurrentObserve 并发投递不 panic、计数一致。
func TestConcurrentObserve(t *testing.T) {
	resetTestState(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Observe(Record{Scenario: "concurrent", Kind: KindText, Outcome: OutcomeSuccess, ElapsedMs: 1})
			}
		}()
	}
	wg.Wait()
	snapshot := Snapshot()
	if snapshot["totalCalls"].(uint64) != 800 {
		t.Fatalf("totalCalls = %v, want 800", snapshot["totalCalls"])
	}
}
