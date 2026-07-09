package computing

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
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

// Execute runs a function with retry logic (exponential backoff with jitter)
func (rp *RetryPolicy) Execute(ctx context.Context, operation func() error) error {
	var lastErr error
	attempt := 0

	rp.metrics.mu.Lock()
	rp.metrics.totalAttempts++
	rp.metrics.mu.Unlock()

	for attempt <= rp.config.MaxRetries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := operation()
		if err == nil {
			if attempt > 0 {
				rp.metrics.mu.Lock()
				rp.metrics.totalSuccesses++
				rp.metrics.mu.Unlock()
				logs.GetLogger().Debugf("Operation succeeded after %d retries", attempt)
			}
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !rp.IsRetryable(err) {
			rp.metrics.mu.Lock()
			rp.metrics.totalNonRetryable++
			rp.metrics.mu.Unlock()
			return err
		}

		// Don't retry if we've exhausted attempts
		if attempt >= rp.config.MaxRetries {
			break
		}

		// Calculate delay with exponential backoff and jitter
		delay := rp.CalculateDelay(attempt)

		logs.GetLogger().Debugf("Retrying operation (attempt %d/%d) after %v: %v",
			attempt+1, rp.config.MaxRetries, delay, err)

		rp.metrics.mu.Lock()
		rp.metrics.totalRetries++
		rp.metrics.mu.Unlock()

		// Wait before retry
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		attempt++
	}

	rp.metrics.mu.Lock()
	rp.metrics.totalFailures++
	rp.metrics.mu.Unlock()

	return lastErr
}

// IsRetryable determines if an error should be retried
func (rp *RetryPolicy) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Check non-retryable errors first
	for _, pattern := range rp.config.NonRetryableErrors {
		if strings.Contains(errStr, strings.ToLower(pattern)) {
			return false
		}
	}

	// Check for known retryable errors
	for _, pattern := range rp.config.RetryableErrors {
		if strings.Contains(errStr, strings.ToLower(pattern)) {
			return true
		}
	}

	// Check for network errors (usually retryable)
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Temporary() || netErr.Timeout()
	}

	// Context deadline exceeded / cancellation are not retryable
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}

	// Default: don't retry unknown errors
	return false
}

// CalculateDelay calculates the delay for a given attempt with jitter
func (rp *RetryPolicy) CalculateDelay(attempt int) time.Duration {
	// Exponential backoff
	delay := float64(rp.config.InitialDelay) * math.Pow(rp.config.Multiplier, float64(attempt))

	// Cap at max delay
	if delay > float64(rp.config.MaxDelay) {
		delay = float64(rp.config.MaxDelay)
	}

	// Add jitter
	if rp.config.JitterFactor > 0 {
		rp.mu.Lock()
		jitter := (rp.rng.Float64()*2 - 1) * rp.config.JitterFactor * delay
		rp.mu.Unlock()
		delay += jitter
	}

	// Ensure delay is positive
	if delay < 0 {
		delay = float64(rp.config.InitialDelay)
	}

	return time.Duration(delay)
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
