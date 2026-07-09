package computing

import (
	"math/rand"
	"sync"
	"time"
)

// RetryConfig configures retry behavior
type RetryConfig struct {
	MaxRetries        int           // Maximum number of retry attempts
	InitialDelay      time.Duration // Initial delay before first retry
	MaxDelay          time.Duration // Maximum delay between retries
	Multiplier        float64       // Delay multiplier for exponential backoff
	JitterFactor      float64       // Random jitter factor (0-1)
	RetryableErrors   []string      // Error substrings that are retryable
	NonRetryableErrors []string     // Error substrings that should not be retried
}

// DefaultRetryConfig returns sensible defaults
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
		JitterFactor: 0.2,
		RetryableErrors: []string{
			"connection refused",
			"connection reset",
			"timeout",
			"temporary failure",
			"service unavailable",
			"503",
			"502",
			"504",
			"EOF",
			"broken pipe",
		},
		NonRetryableErrors: []string{
			"authentication",
			"unauthorized",
			"forbidden",
			"not found",
			"invalid",
			"bad request",
			"400",
			"401",
			"403",
			"404",
		},
	}
}

// RetryMetrics tracks retry statistics
type RetryMetrics struct {
	TotalAttempts       int64   `json:"total_attempts"`
	TotalRetries        int64   `json:"total_retries"`
	TotalSuccesses      int64   `json:"total_successes"`
	TotalFailures       int64   `json:"total_failures"`
	TotalNonRetryable   int64   `json:"total_non_retryable"`
	AvgRetriesPerRequest float64 `json:"avg_retries_per_request"`
	RetrySuccessRate    float64 `json:"retry_success_rate"`
}

// RetryPolicy implements retry logic with exponential backoff and jitter
type RetryPolicy struct {
	mu      sync.RWMutex
	config  RetryConfig
	metrics *retryMetricsCollector
	rng     *rand.Rand
}

type retryMetricsCollector struct {
	totalAttempts     int64
	totalRetries      int64
	totalSuccesses    int64
	totalFailures     int64
	totalNonRetryable int64
	mu                sync.Mutex
}

// NewRetryPolicy creates a new retry policy
func NewRetryPolicy(config RetryConfig) *RetryPolicy {
	return &RetryPolicy{
		config:  config,
		metrics: &retryMetricsCollector{},
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// GetMetrics returns retry metrics
func (rp *RetryPolicy) GetMetrics() RetryMetrics {
	rp.metrics.mu.Lock()
	defer rp.metrics.mu.Unlock()

	var avgRetries float64
	if rp.metrics.totalAttempts > 0 {
		avgRetries = float64(rp.metrics.totalRetries) / float64(rp.metrics.totalAttempts)
	}

	var successRate float64
	if rp.metrics.totalRetries > 0 {
		successRate = float64(rp.metrics.totalSuccesses) / float64(rp.metrics.totalRetries)
	}

	return RetryMetrics{
		TotalAttempts:        rp.metrics.totalAttempts,
		TotalRetries:         rp.metrics.totalRetries,
		TotalSuccesses:       rp.metrics.totalSuccesses,
		TotalFailures:        rp.metrics.totalFailures,
		TotalNonRetryable:    rp.metrics.totalNonRetryable,
		AvgRetriesPerRequest: avgRetries,
		RetrySuccessRate:     successRate,
	}
}
