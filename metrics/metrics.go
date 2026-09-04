// Package metrics 提供进程内 HTTP 请求指标记录与快照输出。
package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

var startedAt = time.Now()
var totalRequests uint64
var failedRequests uint64
var totalLatencyMs uint64
var routeCounters sync.Map

// RecordRequest 记录一次 HTTP 请求指标。
func RecordRequest(method string, path string, status int, latency time.Duration) {
	atomic.AddUint64(&totalRequests, 1)
	atomic.AddUint64(&totalLatencyMs, uint64(latency.Milliseconds()))
	if status >= 500 {
		atomic.AddUint64(&failedRequests, 1)
	}
	key := method + " " + path
	value, _ := routeCounters.LoadOrStore(key, new(uint64))
	atomic.AddUint64(value.(*uint64), 1)
}

// Snapshot 返回当前进程内运行指标快照。
func Snapshot() map[string]any {
	total := atomic.LoadUint64(&totalRequests)
	failed := atomic.LoadUint64(&failedRequests)
	latency := atomic.LoadUint64(&totalLatencyMs)
	avgLatency := float64(0)
	if total > 0 {
		avgLatency = float64(latency) / float64(total)
	}
	routes := map[string]uint64{}
	routeCounters.Range(func(key any, value any) bool {
		routes[key.(string)] = atomic.LoadUint64(value.(*uint64))
		return true
	})
	return map[string]any{
		"startedAt":        startedAt,
		"uptimeSeconds":    int64(time.Since(startedAt).Seconds()),
		"totalRequests":    total,
		"failedRequests":   failed,
		"averageLatencyMs": avgLatency,
		"routes":           routes,
	}
}
