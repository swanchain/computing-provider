package computing

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// InferenceMetrics tracks metrics for the inference service
type InferenceMetrics struct {
	mu sync.RWMutex

	// Connection metrics
	ConnectionState    string    `json:"connection_state"`
	LastConnectedAt    time.Time `json:"last_connected_at,omitempty"`
	LastDisconnectedAt time.Time `json:"last_disconnected_at,omitempty"`
	ReconnectCount     int64     `json:"reconnect_count"`

	// Request metrics (aggregated)
	TotalRequests     int64   `json:"total_requests"`
	SuccessfulReqs    int64   `json:"successful_requests"`
	FailedReqs        int64   `json:"failed_requests"`
	StreamingReqs     int64   `json:"streaming_requests"`
	AvgLatencyMs      float64 `json:"avg_latency_ms"`
	P50LatencyMs      float64 `json:"p50_latency_ms"`
	P95LatencyMs      float64 `json:"p95_latency_ms"`
	P99LatencyMs      float64 `json:"p99_latency_ms"`
	TotalTokensIn     int64   `json:"total_tokens_in"`
	TotalTokensOut    int64   `json:"total_tokens_out"`
	TokensPerSecond   float64 `json:"tokens_per_second"`
	ActiveRequests    int64   `json:"active_requests"`
	RequestsPerMinute float64 `json:"requests_per_minute"`

	// Per-model metrics
	ModelMetrics map[string]*ModelMetrics `json:"model_metrics"`

	// GPU metrics
	GPUMetrics []GPUMetrics `json:"gpu_metrics"`

	// System metrics
	CPUUsagePercent    float64 `json:"cpu_usage_percent"`
	MemoryUsagePercent float64 `json:"memory_usage_percent"`
	MemoryUsedGB       float64 `json:"memory_used_gb"`
	MemoryTotalGB      float64 `json:"memory_total_gb"`

	// Time tracking for rate calculations
	startTime        time.Time
	latencies        []float64 // Recent latencies for percentile calculation
	maxLatencySample int       // Max samples to keep for percentile calculation
}

// ModelMetrics tracks metrics for a specific model
type ModelMetrics struct {
	ModelName       string  `json:"model_name"`
	TotalRequests   int64   `json:"total_requests"`
	SuccessfulReqs  int64   `json:"successful_requests"`
	FailedReqs      int64   `json:"failed_requests"`
	AvgLatencyMs    float64 `json:"avg_latency_ms"`
	TotalTokensIn   int64   `json:"total_tokens_in"`
	TotalTokensOut  int64   `json:"total_tokens_out"`
	TokensPerSecond float64 `json:"tokens_per_second"`
	ActiveRequests  int64   `json:"active_requests"`

	// Internal tracking
	latencySum float64
}

// GPUMetrics tracks metrics for a single GPU
type GPUMetrics struct {
	Index            int     `json:"index"`
	Name             string  `json:"name"`
	UUID             string  `json:"uuid,omitempty"`
	UtilizationPct   float64 `json:"utilization_percent"`
	MemoryUsedMB     float64 `json:"memory_used_mb"`
	MemoryTotalMB    float64 `json:"memory_total_mb"`
	MemoryUsagePct   float64 `json:"memory_usage_percent"`
	TemperatureC     float64 `json:"temperature_c"`
	PowerDrawW       float64 `json:"power_draw_w"`
	PowerLimitW      float64 `json:"power_limit_w"`
	FanSpeedPct      float64 `json:"fan_speed_percent,omitempty"`
	ComputeProcesses int     `json:"compute_processes"`
}

// RequestMetric represents a single request's metrics
type RequestMetric struct {
	RequestID   string    `json:"request_id"`
	Model       string    `json:"model"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time,omitempty"`
	LatencyMs   float64   `json:"latency_ms"`
	TokensIn    int       `json:"tokens_in"`
	TokensOut   int       `json:"tokens_out"`
	Streaming   bool      `json:"streaming"`
	Success     bool      `json:"success"`
	ErrorReason string    `json:"error_reason,omitempty"`
}

// NewInferenceMetrics creates a new InferenceMetrics instance
func NewInferenceMetrics() *InferenceMetrics {
	return &InferenceMetrics{
		ConnectionState:  "disconnected",
		ModelMetrics:     make(map[string]*ModelMetrics),
		GPUMetrics:       make([]GPUMetrics, 0),
		startTime:        time.Now(),
		latencies:        make([]float64, 0, 1000),
		maxLatencySample: 1000,
	}
}

// RecordConnectionState updates the connection state
func (m *InferenceMetrics) RecordConnectionState(state string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ConnectionState = state
	if state == "connected" {
		m.LastConnectedAt = time.Now()
	} else if state == "disconnected" {
		m.LastDisconnectedAt = time.Now()
	}
}

// RecordReconnect increments the reconnect counter
func (m *InferenceMetrics) RecordReconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReconnectCount++
}

// RecordRequestStart records the start of a request
func (m *InferenceMetrics) RecordRequestStart(model string, streaming bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalRequests++
	m.ActiveRequests++
	if streaming {
		m.StreamingReqs++
	}

	// Update model-specific metrics
	mm, exists := m.ModelMetrics[model]
	if !exists {
		mm = &ModelMetrics{ModelName: model}
		m.ModelMetrics[model] = mm
	}
	mm.TotalRequests++
	mm.ActiveRequests++
}

// RecordRequestEnd records the completion of a request
func (m *InferenceMetrics) RecordRequestEnd(model string, latencyMs float64, tokensIn, tokensOut int, success bool, errorReason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.ActiveRequests--
	if m.ActiveRequests < 0 {
		m.ActiveRequests = 0
	}

	if success {
		m.SuccessfulReqs++
	} else {
		m.FailedReqs++
	}

	// Update tokens
	m.TotalTokensIn += int64(tokensIn)
	m.TotalTokensOut += int64(tokensOut)

	// Update latency tracking
	m.latencies = append(m.latencies, latencyMs)
	if len(m.latencies) > m.maxLatencySample {
		m.latencies = m.latencies[1:]
	}
	m.updateLatencyStats()

	// Calculate tokens per second
	elapsed := time.Since(m.startTime).Seconds()
	if elapsed > 0 {
		m.TokensPerSecond = float64(m.TotalTokensOut) / elapsed
		m.RequestsPerMinute = float64(m.TotalRequests) / (elapsed / 60)
	}

	// Update model-specific metrics
	if mm, exists := m.ModelMetrics[model]; exists {
		mm.ActiveRequests--
		if mm.ActiveRequests < 0 {
			mm.ActiveRequests = 0
		}
		if success {
			mm.SuccessfulReqs++
		} else {
			mm.FailedReqs++
		}
		mm.TotalTokensIn += int64(tokensIn)
		mm.TotalTokensOut += int64(tokensOut)
		mm.latencySum += latencyMs
		if mm.TotalRequests > 0 {
			mm.AvgLatencyMs = mm.latencySum / float64(mm.SuccessfulReqs+mm.FailedReqs)
		}
		if elapsed > 0 {
			mm.TokensPerSecond = float64(mm.TotalTokensOut) / elapsed
		}
	}
}

// updateLatencyStats calculates latency percentiles (must be called with lock held)
func (m *InferenceMetrics) updateLatencyStats() {
	if len(m.latencies) == 0 {
		return
	}

	// Calculate average
	var sum float64
	for _, l := range m.latencies {
		sum += l
	}
	m.AvgLatencyMs = sum / float64(len(m.latencies))

	// Sort latencies for percentile calculation
	sorted := make([]float64, len(m.latencies))
	copy(sorted, m.latencies)
	sortFloat64s(sorted)

	// Calculate percentiles
	m.P50LatencyMs = percentile(sorted, 50)
	m.P95LatencyMs = percentile(sorted, 95)
	m.P99LatencyMs = percentile(sorted, 99)
}

// UpdateGPUMetrics updates the GPU metrics
func (m *InferenceMetrics) UpdateGPUMetrics(gpuMetrics []GPUMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GPUMetrics = gpuMetrics
}

// UpdateSystemMetrics updates system-level metrics
func (m *InferenceMetrics) UpdateSystemMetrics(cpuPercent, memPercent, memUsedGB, memTotalGB float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CPUUsagePercent = cpuPercent
	m.MemoryUsagePercent = memPercent
	m.MemoryUsedGB = memUsedGB
	m.MemoryTotalGB = memTotalGB
}

// GetSnapshot returns a copy of the current metrics
func (m *InferenceMetrics) GetSnapshot() InferenceMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Create a copy
	snapshot := *m

	// Deep copy model metrics
	snapshot.ModelMetrics = make(map[string]*ModelMetrics)
	for k, v := range m.ModelMetrics {
		mm := *v
		snapshot.ModelMetrics[k] = &mm
	}

	// Deep copy GPU metrics
	snapshot.GPUMetrics = make([]GPUMetrics, len(m.GPUMetrics))
	copy(snapshot.GPUMetrics, m.GPUMetrics)

	// Don't copy internal tracking fields
	snapshot.latencies = nil

	return snapshot
}

// GetPrometheusMetrics returns metrics in Prometheus text format
func (m *InferenceMetrics) GetPrometheusMetrics() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sb strings.Builder

	// Connection metrics
	sb.WriteString("# HELP inference_connection_state Current connection state (1=connected, 0=disconnected)\n")
	sb.WriteString("# TYPE inference_connection_state gauge\n")
	if m.ConnectionState == "connected" {
		sb.WriteString("inference_connection_state 1\n")
	} else {
		sb.WriteString("inference_connection_state 0\n")
	}

	sb.WriteString("# HELP inference_reconnect_total Total number of reconnections\n")
	sb.WriteString("# TYPE inference_reconnect_total counter\n")
	sb.WriteString(fmt.Sprintf("inference_reconnect_total %d\n", m.ReconnectCount))

	// Request metrics
	sb.WriteString("# HELP inference_requests_total Total number of inference requests\n")
	sb.WriteString("# TYPE inference_requests_total counter\n")
	sb.WriteString(fmt.Sprintf("inference_requests_total %d\n", m.TotalRequests))

	sb.WriteString("# HELP inference_requests_successful_total Total successful requests\n")
	sb.WriteString("# TYPE inference_requests_successful_total counter\n")
	sb.WriteString(fmt.Sprintf("inference_requests_successful_total %d\n", m.SuccessfulReqs))

	sb.WriteString("# HELP inference_requests_failed_total Total failed requests\n")
	sb.WriteString("# TYPE inference_requests_failed_total counter\n")
	sb.WriteString(fmt.Sprintf("inference_requests_failed_total %d\n", m.FailedReqs))

	sb.WriteString("# HELP inference_requests_active Current number of active requests\n")
	sb.WriteString("# TYPE inference_requests_active gauge\n")
	sb.WriteString(fmt.Sprintf("inference_requests_active %d\n", m.ActiveRequests))

	// Latency metrics
	sb.WriteString("# HELP inference_latency_ms Request latency in milliseconds\n")
	sb.WriteString("# TYPE inference_latency_ms summary\n")
	sb.WriteString(fmt.Sprintf("inference_latency_ms{quantile=\"0.5\"} %.2f\n", m.P50LatencyMs))
	sb.WriteString(fmt.Sprintf("inference_latency_ms{quantile=\"0.95\"} %.2f\n", m.P95LatencyMs))
	sb.WriteString(fmt.Sprintf("inference_latency_ms{quantile=\"0.99\"} %.2f\n", m.P99LatencyMs))

	// Token metrics
	sb.WriteString("# HELP inference_tokens_in_total Total input tokens processed\n")
	sb.WriteString("# TYPE inference_tokens_in_total counter\n")
	sb.WriteString(fmt.Sprintf("inference_tokens_in_total %d\n", m.TotalTokensIn))

	sb.WriteString("# HELP inference_tokens_out_total Total output tokens generated\n")
	sb.WriteString("# TYPE inference_tokens_out_total counter\n")
	sb.WriteString(fmt.Sprintf("inference_tokens_out_total %d\n", m.TotalTokensOut))

	sb.WriteString("# HELP inference_tokens_per_second Current tokens per second throughput\n")
	sb.WriteString("# TYPE inference_tokens_per_second gauge\n")
	sb.WriteString(fmt.Sprintf("inference_tokens_per_second %.2f\n", m.TokensPerSecond))

	// GPU metrics
	for _, gpu := range m.GPUMetrics {
		labels := fmt.Sprintf("gpu=\"%d\",name=\"%s\"", gpu.Index, gpu.Name)

		sb.WriteString("# HELP inference_gpu_utilization_percent GPU utilization percentage\n")
		sb.WriteString("# TYPE inference_gpu_utilization_percent gauge\n")
		sb.WriteString(fmt.Sprintf("inference_gpu_utilization_percent{%s} %.2f\n", labels, gpu.UtilizationPct))

		sb.WriteString("# HELP inference_gpu_memory_used_mb GPU memory used in MB\n")
		sb.WriteString("# TYPE inference_gpu_memory_used_mb gauge\n")
		sb.WriteString(fmt.Sprintf("inference_gpu_memory_used_mb{%s} %.2f\n", labels, gpu.MemoryUsedMB))

		sb.WriteString("# HELP inference_gpu_memory_total_mb GPU total memory in MB\n")
		sb.WriteString("# TYPE inference_gpu_memory_total_mb gauge\n")
		sb.WriteString(fmt.Sprintf("inference_gpu_memory_total_mb{%s} %.2f\n", labels, gpu.MemoryTotalMB))

		sb.WriteString("# HELP inference_gpu_temperature_celsius GPU temperature in Celsius\n")
		sb.WriteString("# TYPE inference_gpu_temperature_celsius gauge\n")
		sb.WriteString(fmt.Sprintf("inference_gpu_temperature_celsius{%s} %.2f\n", labels, gpu.TemperatureC))

		sb.WriteString("# HELP inference_gpu_power_draw_watts GPU power draw in watts\n")
		sb.WriteString("# TYPE inference_gpu_power_draw_watts gauge\n")
		sb.WriteString(fmt.Sprintf("inference_gpu_power_draw_watts{%s} %.2f\n", labels, gpu.PowerDrawW))
	}

	// Per-model metrics
	for model, mm := range m.ModelMetrics {
		labels := fmt.Sprintf("model=\"%s\"", model)

		sb.WriteString(fmt.Sprintf("inference_model_requests_total{%s} %d\n", labels, mm.TotalRequests))
		sb.WriteString(fmt.Sprintf("inference_model_requests_active{%s} %d\n", labels, mm.ActiveRequests))
		sb.WriteString(fmt.Sprintf("inference_model_latency_avg_ms{%s} %.2f\n", labels, mm.AvgLatencyMs))
		sb.WriteString(fmt.Sprintf("inference_model_tokens_out_total{%s} %d\n", labels, mm.TotalTokensOut))
	}

	// System metrics
	sb.WriteString("# HELP inference_cpu_usage_percent CPU usage percentage\n")
	sb.WriteString("# TYPE inference_cpu_usage_percent gauge\n")
	sb.WriteString(fmt.Sprintf("inference_cpu_usage_percent %.2f\n", m.CPUUsagePercent))

	sb.WriteString("# HELP inference_memory_usage_percent Memory usage percentage\n")
	sb.WriteString("# TYPE inference_memory_usage_percent gauge\n")
	sb.WriteString(fmt.Sprintf("inference_memory_usage_percent %.2f\n", m.MemoryUsagePercent))

	return sb.String()
}

// Reset resets all metrics
func (m *InferenceMetrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalRequests = 0
	m.SuccessfulReqs = 0
	m.FailedReqs = 0
	m.StreamingReqs = 0
	m.AvgLatencyMs = 0
	m.P50LatencyMs = 0
	m.P95LatencyMs = 0
	m.P99LatencyMs = 0
	m.TotalTokensIn = 0
	m.TotalTokensOut = 0
	m.TokensPerSecond = 0
	m.ActiveRequests = 0
	m.RequestsPerMinute = 0
	m.ReconnectCount = 0
	m.ModelMetrics = make(map[string]*ModelMetrics)
	m.latencies = make([]float64, 0, m.maxLatencySample)
	m.startTime = time.Now()
}

// Helper functions

// sortFloat64s sorts a slice of float64 in ascending order
func sortFloat64s(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// percentile calculates the p-th percentile of a sorted slice
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	idx := (p / 100) * float64(len(sorted)-1)
	lower := int(idx)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	weight := idx - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
