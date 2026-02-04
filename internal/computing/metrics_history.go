package computing

import (
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/internal/db"
	"gorm.io/gorm"
)

// MetricsHistoryEntity represents a historical metrics data point in the database
type MetricsHistoryEntity struct {
	ID                uint      `gorm:"primaryKey;autoIncrement"`
	Timestamp         time.Time `gorm:"index;not null"`
	TotalRequests     int64     `json:"total_requests"`
	SuccessfulReqs    int64     `json:"successful_requests"`
	FailedReqs        int64     `json:"failed_requests"`
	SuccessRate       float64   `json:"success_rate"`
	AvgLatencyMs      float64   `json:"avg_latency_ms"`
	P50LatencyMs      float64   `json:"p50_latency_ms"`
	P95LatencyMs      float64   `json:"p95_latency_ms"`
	P99LatencyMs      float64   `json:"p99_latency_ms"`
	TokensPerSecond   float64   `json:"tokens_per_second"`
	RequestsPerMinute float64   `json:"requests_per_minute"`
	ActiveRequests    int64     `json:"active_requests"`
	TotalTokensIn     int64     `json:"total_tokens_in"`
	TotalTokensOut    int64     `json:"total_tokens_out"`
}

func (MetricsHistoryEntity) TableName() string {
	return "metrics_history"
}

// HistoricalDataPoint represents an aggregated data point for API responses
type HistoricalDataPoint struct {
	Timestamp         time.Time `json:"timestamp"`
	TotalRequests     int64     `json:"total_requests"`
	SuccessRate       float64   `json:"success_rate"`
	AvgLatencyMs      float64   `json:"avg_latency_ms"`
	P99LatencyMs      float64   `json:"p99_latency_ms"`
	TokensPerSecond   float64   `json:"tokens_per_second"`
	RequestsPerMinute float64   `json:"requests_per_minute"`
}

// MetricsHistory manages historical metrics storage and retrieval
type MetricsHistory struct {
	mu             sync.RWMutex
	recordInterval time.Duration
	retentionDays  int
	stopChan       chan struct{}
	running        bool
}

// NewMetricsHistory creates a new MetricsHistory instance
func NewMetricsHistory() *MetricsHistory {
	return &MetricsHistory{
		recordInterval: 1 * time.Minute, // Record every minute
		retentionDays:  7,               // Keep 7 days of data
		stopChan:       make(chan struct{}),
	}
}

// Start begins the metrics recording goroutine
func (h *MetricsHistory) Start(metricsProvider func() *InferenceMetrics) error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return nil
	}
	h.running = true
	h.mu.Unlock()

	// Auto-migrate the table
	if err := h.migrate(); err != nil {
		return err
	}

	go h.recordLoop(metricsProvider)
	go h.pruneLoop()

	logs.GetLogger().Info("Metrics history recorder started")
	return nil
}

// Stop stops the metrics recording goroutine
func (h *MetricsHistory) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return
	}

	close(h.stopChan)
	h.running = false
	logs.GetLogger().Info("Metrics history recorder stopped")
}

// migrate creates the metrics_history table if it doesn't exist
func (h *MetricsHistory) migrate() error {
	database := db.NewDbService()
	if database == nil {
		return nil // DB not initialized yet
	}
	return database.AutoMigrate(&MetricsHistoryEntity{})
}

// recordLoop periodically records metrics snapshots
func (h *MetricsHistory) recordLoop(metricsProvider func() *InferenceMetrics) {
	ticker := time.NewTicker(h.recordInterval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopChan:
			return
		case <-ticker.C:
			h.recordSnapshot(metricsProvider)
		}
	}
}

// recordSnapshot saves a metrics snapshot to the database
func (h *MetricsHistory) recordSnapshot(metricsProvider func() *InferenceMetrics) {
	metrics := metricsProvider()
	if metrics == nil {
		return
	}

	database := db.NewDbService()
	if database == nil {
		return
	}

	snapshot := metrics.GetSnapshot()

	// Calculate success rate
	var successRate float64
	total := snapshot.SuccessfulReqs + snapshot.FailedReqs
	if total > 0 {
		successRate = float64(snapshot.SuccessfulReqs) / float64(total) * 100
	}

	entry := MetricsHistoryEntity{
		Timestamp:         time.Now(),
		TotalRequests:     snapshot.TotalRequests,
		SuccessfulReqs:    snapshot.SuccessfulReqs,
		FailedReqs:        snapshot.FailedReqs,
		SuccessRate:       successRate,
		AvgLatencyMs:      snapshot.AvgLatencyMs,
		P50LatencyMs:      snapshot.P50LatencyMs,
		P95LatencyMs:      snapshot.P95LatencyMs,
		P99LatencyMs:      snapshot.P99LatencyMs,
		TokensPerSecond:   snapshot.TokensPerSecond,
		RequestsPerMinute: snapshot.RequestsPerMinute,
		ActiveRequests:    snapshot.ActiveRequests,
		TotalTokensIn:     snapshot.TotalTokensIn,
		TotalTokensOut:    snapshot.TotalTokensOut,
	}

	if err := database.Create(&entry).Error; err != nil {
		logs.GetLogger().Warnf("Failed to record metrics history: %v", err)
	}
}

// pruneLoop periodically removes old data
func (h *MetricsHistory) pruneLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Initial prune
	h.pruneOldData()

	for {
		select {
		case <-h.stopChan:
			return
		case <-ticker.C:
			h.pruneOldData()
		}
	}
}

// pruneOldData removes data older than retention period
func (h *MetricsHistory) pruneOldData() {
	database := db.NewDbService()
	if database == nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -h.retentionDays)
	result := database.Where("timestamp < ?", cutoff).Delete(&MetricsHistoryEntity{})
	if result.Error != nil {
		logs.GetLogger().Warnf("Failed to prune old metrics: %v", result.Error)
	} else if result.RowsAffected > 0 {
		logs.GetLogger().Debugf("Pruned %d old metrics history entries", result.RowsAffected)
	}
}

// GetHistory retrieves historical metrics for the given duration with the specified resolution
func (h *MetricsHistory) GetHistory(duration time.Duration, resolution time.Duration) ([]HistoricalDataPoint, error) {
	database := db.NewDbService()
	if database == nil {
		return nil, nil
	}

	startTime := time.Now().Add(-duration)
	var entries []MetricsHistoryEntity

	err := database.Where("timestamp > ?", startTime).
		Order("timestamp ASC").
		Find(&entries).Error
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return []HistoricalDataPoint{}, nil
	}

	// Aggregate data points based on resolution
	result := h.aggregateByResolution(entries, resolution)
	return result, nil
}

// aggregateByResolution groups entries by resolution intervals and averages the values
func (h *MetricsHistory) aggregateByResolution(entries []MetricsHistoryEntity, resolution time.Duration) []HistoricalDataPoint {
	if len(entries) == 0 {
		return []HistoricalDataPoint{}
	}

	// If resolution is less than or equal to record interval, return raw data
	if resolution <= h.recordInterval {
		result := make([]HistoricalDataPoint, len(entries))
		for i, e := range entries {
			result[i] = HistoricalDataPoint{
				Timestamp:         e.Timestamp,
				TotalRequests:     e.TotalRequests,
				SuccessRate:       e.SuccessRate,
				AvgLatencyMs:      e.AvgLatencyMs,
				P99LatencyMs:      e.P99LatencyMs,
				TokensPerSecond:   e.TokensPerSecond,
				RequestsPerMinute: e.RequestsPerMinute,
			}
		}
		return result
	}

	// Group and aggregate
	buckets := make(map[int64][]MetricsHistoryEntity)
	for _, e := range entries {
		bucketKey := e.Timestamp.Truncate(resolution).Unix()
		buckets[bucketKey] = append(buckets[bucketKey], e)
	}

	// Sort bucket keys and create result
	var sortedKeys []int64
	for k := range buckets {
		sortedKeys = append(sortedKeys, k)
	}
	sortInt64s(sortedKeys)

	result := make([]HistoricalDataPoint, 0, len(sortedKeys))
	for _, key := range sortedKeys {
		bucket := buckets[key]
		if len(bucket) == 0 {
			continue
		}

		// Average the values in this bucket
		var sumSuccessRate, sumAvgLatency, sumP99Latency, sumTokensPerSec, sumReqPerMin float64
		var maxTotalReqs int64
		for _, e := range bucket {
			sumSuccessRate += e.SuccessRate
			sumAvgLatency += e.AvgLatencyMs
			sumP99Latency += e.P99LatencyMs
			sumTokensPerSec += e.TokensPerSecond
			sumReqPerMin += e.RequestsPerMinute
			if e.TotalRequests > maxTotalReqs {
				maxTotalReqs = e.TotalRequests
			}
		}

		count := float64(len(bucket))
		result = append(result, HistoricalDataPoint{
			Timestamp:         time.Unix(key, 0),
			TotalRequests:     maxTotalReqs,
			SuccessRate:       sumSuccessRate / count,
			AvgLatencyMs:      sumAvgLatency / count,
			P99LatencyMs:      sumP99Latency / count,
			TokensPerSecond:   sumTokensPerSec / count,
			RequestsPerMinute: sumReqPerMin / count,
		})
	}

	return result
}

// sortInt64s sorts a slice of int64 in ascending order
func sortInt64s(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// GetRecentDataPoints returns the most recent N data points (for quick access)
func (h *MetricsHistory) GetRecentDataPoints(count int) ([]HistoricalDataPoint, error) {
	database := db.NewDbService()
	if database == nil {
		return nil, nil
	}

	var entries []MetricsHistoryEntity
	err := database.Order("timestamp DESC").Limit(count).Find(&entries).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	// Reverse to get chronological order
	result := make([]HistoricalDataPoint, len(entries))
	for i, e := range entries {
		result[len(entries)-1-i] = HistoricalDataPoint{
			Timestamp:         e.Timestamp,
			TotalRequests:     e.TotalRequests,
			SuccessRate:       e.SuccessRate,
			AvgLatencyMs:      e.AvgLatencyMs,
			P99LatencyMs:      e.P99LatencyMs,
			TokensPerSecond:   e.TokensPerSecond,
			RequestsPerMinute: e.RequestsPerMinute,
		}
	}

	return result, nil
}
