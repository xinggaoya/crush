package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics collector for application performance and usage metrics
type Metrics struct {
	// Request metrics
	RequestCount    atomic.Int64
	RequestDuration atomic.Int64 // nanoseconds
	ErrorCount      atomic.Int64

	// Connection metrics
	ActiveConnections atomic.Int64
	LSPConnections    atomic.Int64
	MCPConnections    atomic.Int64

	// Cache metrics
	CacheHits   atomic.Int64
	CacheMisses atomic.Int64

	// Database metrics
	DBQueries  atomic.Int64
	DBErrors   atomic.Int64
	DBDuration atomic.Int64 // nanoseconds

	// Memory metrics
	MemoryUsage atomic.Int64 // bytes

	// Custom metrics
	customMetrics sync.Map // map[string]*atomic.Int64

	startTime time.Time
}

// NewMetrics creates a new metrics collector
func NewMetrics() *Metrics {
	return &Metrics{
		startTime: time.Now(),
	}
}

// RecordRequest records a request
func (m *Metrics) RecordRequest(duration time.Duration, success bool) {
	m.RequestCount.Add(1)
	m.RequestDuration.Add(duration.Nanoseconds())
	if !success {
		m.ErrorCount.Add(1)
	}
}

// RecordDBQuery records a database query
func (m *Metrics) RecordDBQuery(duration time.Duration, success bool) {
	m.DBQueries.Add(1)
	m.DBDuration.Add(duration.Nanoseconds())
	if !success {
		m.DBErrors.Add(1)
	}
}

// RecordCacheHit records a cache hit
func (m *Metrics) RecordCacheHit() {
	m.CacheHits.Add(1)
}

// RecordCacheMiss records a cache miss
func (m *Metrics) RecordCacheMiss() {
	m.CacheMisses.Add(1)
}

// IncrementCustomMetric increments a custom metric
func (m *Metrics) IncrementCustomMetric(name string) {
	if val, ok := m.customMetrics.Load(name); ok {
		if counter, ok := val.(*atomic.Int64); ok {
			counter.Add(1)
		}
	} else {
		counter := &atomic.Int64{}
		counter.Add(1)
		m.customMetrics.Store(name, counter)
	}
}

// GetSnapshot returns a snapshot of current metrics
func (m *Metrics) GetSnapshot() map[string]interface{} {
	uptime := time.Since(m.startTime)

	snapshot := map[string]interface{}{
		"uptime_seconds":     uptime.Seconds(),
		"request_count":      m.RequestCount.Load(),
		"error_count":        m.ErrorCount.Load(),
		"active_connections": m.ActiveConnections.Load(),
		"lsp_connections":    m.LSPConnections.Load(),
		"mcp_connections":    m.MCPConnections.Load(),
		"cache_hits":         m.CacheHits.Load(),
		"cache_misses":       m.CacheMisses.Load(),
		"db_queries":         m.DBQueries.Load(),
		"db_errors":          m.DBErrors.Load(),
		"memory_usage_bytes": m.MemoryUsage.Load(),
	}

	// Calculate averages
	if reqCount := m.RequestCount.Load(); reqCount > 0 {
		snapshot["avg_request_duration_ms"] = float64(m.RequestDuration.Load()) / float64(reqCount) / 1e6
	}

	if dbCount := m.DBQueries.Load(); dbCount > 0 {
		snapshot["avg_db_duration_ms"] = float64(m.DBDuration.Load()) / float64(dbCount) / 1e6
	}

	// Calculate cache hit rate
	if hits := m.CacheHits.Load(); hits > 0 {
		misses := m.CacheMisses.Load()
		total := hits + misses
		if total > 0 {
			snapshot["cache_hit_rate"] = float64(hits) / float64(total)
		}
	}

	// Add custom metrics
	m.customMetrics.Range(func(key, value interface{}) bool {
		if counter, ok := value.(*atomic.Int64); ok {
			snapshot[key.(string)] = counter.Load()
		}
		return true
	})

	return snapshot
}

// Reset resets all metrics
func (m *Metrics) Reset() {
	m.RequestCount.Store(0)
	m.RequestDuration.Store(0)
	m.ErrorCount.Store(0)
	m.ActiveConnections.Store(0)
	m.LSPConnections.Store(0)
	m.MCPConnections.Store(0)
	m.CacheHits.Store(0)
	m.CacheMisses.Store(0)
	m.DBQueries.Store(0)
	m.DBErrors.Store(0)
	m.DBDuration.Store(0)
	m.MemoryUsage.Store(0)

	m.customMetrics.Range(func(key, value interface{}) bool {
		m.customMetrics.Delete(key)
		return true
	})

	m.startTime = time.Now()
}

// MetricsCollector interface for different metric backends
type MetricsCollector interface {
	Collect(ctx context.Context, metrics *Metrics) error
}

// PrometheusCollector implements Prometheus metrics collection
type PrometheusCollector struct {
	namespace string
	subsystem string
}

// NewPrometheusCollector creates a new Prometheus collector
func NewPrometheusCollector(namespace, subsystem string) *PrometheusCollector {
	return &PrometheusCollector{
		namespace: namespace,
		subsystem: subsystem,
	}
}

// Collect implements MetricsCollector
func (p *PrometheusCollector) Collect(ctx context.Context, metrics *Metrics) error {
	// Implementation would use Prometheus client library
	// This is a placeholder for the actual implementation
	return nil
}
