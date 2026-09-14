package aiobs

import (
	"context"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// AICallRecord AI 调用记录的 MySQL 实体，表名 ai_call_records。
type AICallRecord struct {
	ID               uint64    `gorm:"primaryKey" json:"id"`
	Scenario         string    `gorm:"size:64;index:idx_scenario_time,priority:1" json:"scenario"`
	Kind             string    `gorm:"size:16" json:"kind"`
	Model            string    `gorm:"size:128;index" json:"model"`
	Outcome          string    `gorm:"size:16;index" json:"outcome"`
	Error            string    `gorm:"size:512" json:"error"`
	ElapsedMs        int64     `json:"elapsedMs"`
	PromptTokens     int       `json:"promptTokens"`
	CompletionTokens int       `json:"completionTokens"`
	TotalTokens      int       `json:"totalTokens"`
	Cost             float64   `gorm:"type:decimal(12,6)" json:"cost"`
	Currency         string    `gorm:"size:8" json:"currency"`
	Priced           bool      `json:"priced"`
	StartedAt        time.Time `gorm:"index:idx_scenario_time,priority:2" json:"startedAt"`
	CreatedAt        time.Time `json:"createdAt"`
}

// TableName 指定表名。
func (AICallRecord) TableName() string { return "ai_call_records" }

// MigrateAICallRecords 建 AI 调用记录表（幂等）。
func MigrateAICallRecords(db *gorm.DB) error {
	return db.AutoMigrate(&AICallRecord{})
}

// MySQLStore 异步批量把调用记录写入 MySQL：非阻塞投递、攒批落库、缓冲满丢弃计数，避免拖慢业务调用。
type MySQLStore struct {
	db      *gorm.DB
	ch      chan AICallRecord
	done    chan struct{}
	closeCh chan struct{}
	dropped atomic.Uint64
	written atomic.Uint64
}

// StoreOption store 配置项。
type StoreOption func(*storeConfig)

type storeConfig struct {
	bufferSize    int
	batchSize     int
	flushInterval time.Duration
}

// WithBufferSize 设置投递缓冲长度（默认 2048）。
func WithBufferSize(size int) StoreOption {
	return func(c *storeConfig) { c.bufferSize = size }
}

// WithBatchSize 设置单批写入条数（默认 100）。
func WithBatchSize(size int) StoreOption {
	return func(c *storeConfig) { c.batchSize = size }
}

// WithFlushInterval 设置攒批冲刷间隔（默认 1s）。
func WithFlushInterval(d time.Duration) StoreOption {
	return func(c *storeConfig) { c.flushInterval = d }
}

// NewMySQLStore 创建并启动异步落库 store；不再使用时调用 Close 冲刷剩余记录。
// 启动前建议先执行 MigrateAICallRecords 建表。
func NewMySQLStore(db *gorm.DB, opts ...StoreOption) *MySQLStore {
	config := storeConfig{bufferSize: 2048, batchSize: 100, flushInterval: time.Second}
	for _, opt := range opts {
		opt(&config)
	}
	store := &MySQLStore{
		db:      db.WithContext(context.Background()).Session(&gorm.Session{Logger: db.Logger.LogMode(logger.Warn)}),
		ch:      make(chan AICallRecord, config.bufferSize),
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
	}
	go store.loop(config.batchSize, config.flushInterval)
	return store
}

// Observe 实现 Observer：非阻塞投递，缓冲满丢弃并计数（观测绝不拖垮业务）。
func (s *MySQLStore) Observe(record Record) {
	entity := AICallRecord{
		Scenario:         record.Scenario,
		Kind:             record.Kind,
		Model:            record.Model,
		Outcome:          record.Outcome,
		Error:            record.Error,
		ElapsedMs:        record.ElapsedMs,
		PromptTokens:     record.PromptTokens,
		CompletionTokens: record.CompletionTokens,
		TotalTokens:      record.TotalTokens,
		Cost:             record.Cost,
		Currency:         record.Currency,
		Priced:           record.Priced,
		StartedAt:        record.StartedAt,
		CreatedAt:        time.Now(),
	}
	if entity.StartedAt.IsZero() {
		entity.StartedAt = entity.CreatedAt
	}
	select {
	case s.ch <- entity:
	default:
		s.dropped.Add(1)
	}
}

// Dropped 返回因缓冲满被丢弃的记录数。
func (s *MySQLStore) Dropped() uint64 { return s.dropped.Load() }

// Written 返回已成功落库的记录数。
func (s *MySQLStore) Written() uint64 { return s.written.Load() }

// Close 停止接收新记录并冲刷缓冲内剩余记录，返回落库结果。
func (s *MySQLStore) Close() error {
	select {
	case <-s.closeCh:
		// 已关闭
	default:
		close(s.closeCh)
	}
	<-s.done
	return nil
}

// loop 后台攒批写入循环。
func (s *MySQLStore) loop(batchSize int, flushInterval time.Duration) {
	defer close(s.done)
	batch := make([]AICallRecord, 0, batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.db.CreateInBatches(batch, batchSize).Error; err != nil {
			s.dropped.Add(uint64(len(batch)))
		} else {
			s.written.Add(uint64(len(batch)))
		}
		batch = batch[:0]
	}
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case entity, ok := <-s.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, entity)
			if len(batch) >= batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.closeCh:
			// 冲刷缓冲内剩余记录后退出。
			for {
				select {
				case entity := <-s.ch:
					batch = append(batch, entity)
				default:
					flush()
					return
				}
			}
		}
	}
}
