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

// Execute runs a function with retry logic
func (rp *RetryPolicy) Execute(ctx context.Context, operation func() error) error {
	return rp.ExecuteWithResult(ctx, func() (interface{}, error) {
		return nil, operation()
	})
}

// ExecuteWithResult runs a function that returns a result with retry logic
func (rp *RetryPolicy) ExecuteWithResult(ctx context.Context, operation func() (interface{}, error)) error {
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

		_, err := operation()
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

	// Check for context deadline exceeded (not retryable)
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check for context cancelled (not retryable)
	if errors.Is(err, context.Canceled) {
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

// BackoffCalculator provides various backoff strategies
type BackoffCalculator struct {
	strategy     BackoffStrategy
	initialDelay time.Duration
	maxDelay     time.Duration
	multiplier   float64
	jitterFactor float64
	rng          *rand.Rand
	mu           sync.Mutex
}

// BackoffStrategy defines the backoff calculation method
type BackoffStrategy int

const (
	BackoffExponential BackoffStrategy = iota
	BackoffLinear
	BackoffConstant
	BackoffFibonacci
)

// NewBackoffCalculator creates a new backoff calculator
func NewBackoffCalculator(strategy BackoffStrategy, initialDelay, maxDelay time.Duration) *BackoffCalculator {
	return &BackoffCalculator{
		strategy:     strategy,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
		multiplier:   2.0,
		jitterFactor: 0.2,
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// SetMultiplier sets the backoff multiplier (for exponential)
func (bc *BackoffCalculator) SetMultiplier(m float64) *BackoffCalculator {
	bc.multiplier = m
	return bc
}

// SetJitter sets the jitter factor
func (bc *BackoffCalculator) SetJitter(j float64) *BackoffCalculator {
	bc.jitterFactor = j
	return bc
}

// Calculate returns the delay for a given attempt
func (bc *BackoffCalculator) Calculate(attempt int) time.Duration {
	var delay float64

	switch bc.strategy {
	case BackoffExponential:
		delay = float64(bc.initialDelay) * math.Pow(bc.multiplier, float64(attempt))
	case BackoffLinear:
		delay = float64(bc.initialDelay) * float64(attempt+1)
	case BackoffConstant:
		delay = float64(bc.initialDelay)
	case BackoffFibonacci:
		delay = float64(bc.initialDelay) * float64(fibonacci(attempt+1))
	}

	// Cap at max
	if delay > float64(bc.maxDelay) {
		delay = float64(bc.maxDelay)
	}

	// Add jitter
	if bc.jitterFactor > 0 {
		bc.mu.Lock()
		jitter := (bc.rng.Float64()*2 - 1) * bc.jitterFactor * delay
		bc.mu.Unlock()
		delay += jitter
		if delay < 0 {
			delay = float64(bc.initialDelay)
		}
	}

	return time.Duration(delay)
}

// fibonacci returns the nth fibonacci number
func fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	a, b := 0, 1
	for i := 2; i <= n; i++ {
		a, b = b, a+b
	}
	return b
}

// RetryableOperation wraps an operation with retry capability
type RetryableOperation struct {
	policy    *RetryPolicy
	operation func(ctx context.Context) error
	onRetry   func(attempt int, err error)
}

// NewRetryableOperation creates a new retryable operation
func NewRetryableOperation(policy *RetryPolicy, op func(ctx context.Context) error) *RetryableOperation {
	return &RetryableOperation{
		policy:    policy,
		operation: op,
	}
}

// OnRetry sets a callback for retry events
func (ro *RetryableOperation) OnRetry(callback func(attempt int, err error)) *RetryableOperation {
	ro.onRetry = callback
	return ro
}

// Run executes the operation with retries
func (ro *RetryableOperation) Run(ctx context.Context) error {
	attempt := 0
	var lastErr error

	for attempt <= ro.policy.config.MaxRetries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := ro.operation(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		if !ro.policy.IsRetryable(err) {
			return err
		}

		if attempt >= ro.policy.config.MaxRetries {
			break
		}

		if ro.onRetry != nil {
			ro.onRetry(attempt, err)
		}

		delay := ro.policy.CalculateDelay(attempt)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		attempt++
	}

	return lastErr
}

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	mu              sync.Mutex
	state           CircuitState
	failureCount    int
	successCount    int
	failureThreshold int
	successThreshold int
	timeout         time.Duration
	lastFailure     time.Time
	onStateChange   func(from, to CircuitState)
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(failureThreshold, successThreshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            CircuitClosed,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
	}
}

// SetStateChangeCallback sets a callback for state changes
func (cb *CircuitBreaker) SetStateChangeCallback(callback func(from, to CircuitState)) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.onStateChange = callback
}

// Allow checks if a request should be allowed
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailure) > cb.timeout {
			cb.setState(CircuitHalfOpen)
			return true
		}
		return false
	case CircuitHalfOpen:
		return true
	}
	return false
}

// RecordSuccess records a successful operation
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0

	if cb.state == CircuitHalfOpen {
		cb.successCount++
		if cb.successCount >= cb.successThreshold {
			cb.setState(CircuitClosed)
		}
	}
}

// RecordFailure records a failed operation
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	cb.lastFailure = time.Now()

	if cb.state == CircuitHalfOpen {
		cb.setState(CircuitOpen)
		return
	}

	if cb.state == CircuitClosed && cb.failureCount >= cb.failureThreshold {
		cb.setState(CircuitOpen)
	}
}

// setState changes the circuit state
func (cb *CircuitBreaker) setState(newState CircuitState) {
	oldState := cb.state
	cb.state = newState
	cb.successCount = 0

	if cb.onStateChange != nil && oldState != newState {
		go cb.onStateChange(oldState, newState)
	}

	logs.GetLogger().Infof("Circuit breaker state changed: %s -> %s", oldState, newState)
}

// GetState returns the current circuit state
func (cb *CircuitBreaker) GetState() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.setState(CircuitClosed)
	cb.failureCount = 0
	cb.successCount = 0
}
