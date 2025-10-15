package metrics

import (
	"testing"
	"time"
)

func TestMetrics(t *testing.T) {
	m := NewMetrics()
	
	// Test request recording
	m.RecordRequest(100*time.Millisecond, true)
	m.RecordRequest(200*time.Millisecond, false)
	
	// Test DB query recording
	m.RecordDBQuery(50*time.Millisecond, true)
	m.RecordDBQuery(150*time.Millisecond, false)
	
	// Test cache operations
	m.RecordCacheHit()
	m.RecordCacheHit()
	m.RecordCacheMiss()
	
	// Test custom metric
	m.IncrementCustomMetric("test_metric")
	m.IncrementCustomMetric("test_metric")
	
	// Get snapshot
	snapshot := m.GetSnapshot()
	
	// Verify counts
	if snapshot["request_count"] != int64(2) {
		t.Errorf("Expected request count 2, got %v", snapshot["request_count"])
	}
	
	if snapshot["error_count"] != int64(1) {
		t.Errorf("Expected error count 1, got %v", snapshot["error_count"])
	}
	
	if snapshot["db_queries"] != int64(2) {
		t.Errorf("Expected DB queries 2, got %v", snapshot["db_queries"])
	}
	
	if snapshot["db_errors"] != int64(1) {
		t.Errorf("Expected DB errors 1, got %v", snapshot["db_errors"])
	}
	
	if snapshot["cache_hits"] != int64(2) {
		t.Errorf("Expected cache hits 2, got %v", snapshot["cache_hits"])
	}
	
	if snapshot["cache_misses"] != int64(1) {
		t.Errorf("Expected cache misses 1, got %v", snapshot["cache_misses"])
	}
	
	if snapshot["test_metric"] != int64(2) {
		t.Errorf("Expected test metric 2, got %v", snapshot["test_metric"])
	}
	
	// Verify averages
	if avgDuration, ok := snapshot["avg_request_duration_ms"].(float64); ok {
		expected := float64(100+200) / 2.0 // Average of 100ms and 200ms
		if avgDuration != expected {
			t.Errorf("Expected avg request duration %f, got %f", expected, avgDuration)
		}
	} else {
		t.Error("avg_request_duration_ms not found or not a float")
	}
	
	// Verify cache hit rate
	if hitRate, ok := snapshot["cache_hit_rate"].(float64); ok {
		expected := 2.0 / 3.0 // 2 hits out of 3 total
		if hitRate != expected {
			t.Errorf("Expected cache hit rate %f, got %f", expected, hitRate)
		}
	} else {
		t.Error("cache_hit_rate not found or not a float")
	}
	
	// Test reset
	m.Reset()
	snapshotAfterReset := m.GetSnapshot()
	
	if snapshotAfterReset["request_count"] != int64(0) {
		t.Error("Expected request count to be 0 after reset")
	}
}