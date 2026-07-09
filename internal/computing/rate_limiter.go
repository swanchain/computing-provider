package computing

import (
	"runtime/debug"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// RateLimiterConfig configures the rate limiter
type RateLimiterConfig struct {
	// Token bucket settings
	TokensPerSecond float64       // Rate of token replenishment
	BurstSize       int           // Maximum burst capacity

	// Adaptive rate limiting
	EnableAdaptive      bool    // Enable GPU-aware rate limiting
	GPUThresholdHigh    float64 // GPU utilization above which to reduce rate
	GPUThresholdLow     float64 // GPU utilization below which to increase rate
	AdaptiveMinRate     float64 // Minimum tokens per second when adapting
	AdaptiveMaxRate     float64 // Maximum tokens per second when adapting
	AdaptiveAdjustment  float64 // Rate adjustment factor per interval
}

// DefaultRateLimiterConfig returns sensible defaults
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		TokensPerSecond:    100,   // 100 requests per second
		BurstSize:          50,    // Allow burst of 50 requests
		EnableAdaptive:     true,
		GPUThresholdHigh:   80.0,  // Reduce rate above 80% GPU utilization
		GPUThresholdLow:    50.0,  // Increase rate below 50% GPU utilization
		AdaptiveMinRate:    10.0,  // Minimum 10 requests/second
		AdaptiveMaxRate:    500.0, // Maximum 500 requests/second
		AdaptiveAdjustment: 0.1,   // 10% adjustment per interval
	}
}

// RateLimiterMetrics tracks rate limiter statistics
type RateLimiterMetrics struct {
	TotalAllowed    int64   `json:"total_allowed"`
	TotalThrottled  int64   `json:"total_throttled"`
	CurrentRate     float64 `json:"current_rate"`
	CurrentTokens   float64 `json:"current_tokens"`
	BurstSize       int     `json:"burst_size"`
	AdaptiveEnabled bool    `json:"adaptive_enabled"`
}

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	mu            sync.Mutex
	tokens        float64
	maxTokens     float64
	refillRate    float64 // tokens per second
	lastRefill    time.Time
	totalAllowed  int64
	totalThrottled int64
}

// NewTokenBucket creates a new token bucket
func NewTokenBucket(tokensPerSecond float64, burstSize int) *TokenBucket {
	return &TokenBucket{
		tokens:     float64(burstSize),
		maxTokens:  float64(burstSize),
		refillRate: tokensPerSecond,
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed and consumes a token
func (tb *TokenBucket) Allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= 1 {
		tb.tokens--
		tb.totalAllowed++
		return true
	}

	tb.totalThrottled++
	return false
}

// refill adds tokens based on elapsed time
func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	tb.lastRefill = now

	tb.tokens += elapsed * tb.refillRate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}
}

// SetRate updates the token refill rate
func (tb *TokenBucket) SetRate(tokensPerSecond float64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refillRate = tokensPerSecond
}

// GetStats returns current bucket stats
func (tb *TokenBucket) GetStats() (tokens float64, rate float64, allowed, throttled int64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.refill()
	return tb.tokens, tb.refillRate, tb.totalAllowed, tb.totalThrottled
}

// RateLimiter provides rate limiting with optional adaptive adjustment
type RateLimiter struct {
	mu             sync.RWMutex
	config         RateLimiterConfig
	globalBucket   *TokenBucket
	modelBuckets   map[string]*TokenBucket
	gpuCollector   *GPUMetricsCollector
	stopCh         chan struct{}
	running        bool
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config RateLimiterConfig, gpuCollector *GPUMetricsCollector) *RateLimiter {
	return &RateLimiter{
		config:       config,
		globalBucket: NewTokenBucket(config.TokensPerSecond, config.BurstSize),
		modelBuckets: make(map[string]*TokenBucket),
		gpuCollector: gpuCollector,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the rate limiter (adaptive adjustment if enabled)
func (rl *RateLimiter) Start() {
	rl.mu.Lock()
	if rl.running {
		rl.mu.Unlock()
		return
	}
	rl.running = true
	rl.stopCh = make(chan struct{})
	rl.mu.Unlock()

	if rl.config.EnableAdaptive && rl.gpuCollector != nil {
		go rl.adaptiveLoop()
	}

	logs.GetLogger().Info("Rate limiter started")
}

// Stop stops the rate limiter
func (rl *RateLimiter) Stop() {
	rl.mu.Lock()
	if !rl.running {
		rl.mu.Unlock()
		return
	}
	rl.running = false
	close(rl.stopCh)
	rl.mu.Unlock()

	logs.GetLogger().Info("Rate limiter stopped")
}

// AllowModel checks if a request for a specific model is allowed
func (rl *RateLimiter) AllowModel(modelID string) bool {
	// Check global limit first
	if !rl.globalBucket.Allow() {
		return false
	}

	// Check model-specific limit
	rl.mu.RLock()
	bucket, exists := rl.modelBuckets[modelID]
	rl.mu.RUnlock()

	if !exists {
		// No model-specific limit, allow
		return true
	}

	return bucket.Allow()
}

// SetModelLimit sets a rate limit for a specific model
func (rl *RateLimiter) SetModelLimit(modelID string, tokensPerSecond float64, burstSize int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.modelBuckets[modelID] = NewTokenBucket(tokensPerSecond, burstSize)
	logs.GetLogger().Infof("Set rate limit for model %s: %.2f req/s, burst %d", modelID, tokensPerSecond, burstSize)
}

// adaptiveLoop adjusts rate based on GPU utilization
func (rl *RateLimiter) adaptiveLoop() {
	defer func() {
		if err := recover(); err != nil {
			logs.GetLogger().Errorf("[rate_limiter:adaptiveLoop] panic recovered: %v\n%s", err, debug.Stack())
		}
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.adjustRateBasedOnGPU()
		}
	}
}

// adjustRateBasedOnGPU adjusts the rate limit based on GPU utilization
func (rl *RateLimiter) adjustRateBasedOnGPU() {
	if rl.gpuCollector == nil {
		return
	}

	avgUtil, _ := rl.gpuCollector.GetAggregatedGPUMetrics()

	rl.mu.Lock()
	currentRate := rl.globalBucket.refillRate
	var newRate float64

	if avgUtil > rl.config.GPUThresholdHigh {
		// High utilization - reduce rate
		newRate = currentRate * (1 - rl.config.AdaptiveAdjustment)
		if newRate < rl.config.AdaptiveMinRate {
			newRate = rl.config.AdaptiveMinRate
		}
		logs.GetLogger().Debugf("High GPU utilization (%.1f%%), reducing rate: %.2f -> %.2f",
			avgUtil, currentRate, newRate)
	} else if avgUtil < rl.config.GPUThresholdLow {
		// Low utilization - increase rate
		newRate = currentRate * (1 + rl.config.AdaptiveAdjustment)
		if newRate > rl.config.AdaptiveMaxRate {
			newRate = rl.config.AdaptiveMaxRate
		}
		logs.GetLogger().Debugf("Low GPU utilization (%.1f%%), increasing rate: %.2f -> %.2f",
			avgUtil, currentRate, newRate)
	} else {
		newRate = currentRate
	}
	rl.mu.Unlock()

	if newRate != currentRate {
		rl.globalBucket.SetRate(newRate)
	}
}

// GetMetrics returns rate limiter metrics
func (rl *RateLimiter) GetMetrics() RateLimiterMetrics {
	tokens, rate, allowed, throttled := rl.globalBucket.GetStats()

	return RateLimiterMetrics{
		TotalAllowed:    allowed,
		TotalThrottled:  throttled,
		CurrentRate:     rate,
		CurrentTokens:   tokens,
		BurstSize:       rl.config.BurstSize,
		AdaptiveEnabled: rl.config.EnableAdaptive,
	}
}
