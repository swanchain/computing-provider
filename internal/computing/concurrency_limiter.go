package computing

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// ConcurrencyConfig configures the concurrency limiter
type ConcurrencyConfig struct {
	GlobalMaxConcurrent  int           // Maximum concurrent requests globally
	DefaultModelMax      int           // Default max concurrent per model
	AcquireTimeout       time.Duration // Timeout for acquiring a slot
	EnableGPUAwareness   bool          // Adjust limits based on GPU memory
	GPUMemoryBufferMB    int           // Buffer to keep free in GPU memory
}

// DefaultConcurrencyConfig returns sensible defaults
func DefaultConcurrencyConfig() ConcurrencyConfig {
	return ConcurrencyConfig{
		GlobalMaxConcurrent: 50,
		DefaultModelMax:     10,
		AcquireTimeout:      30 * time.Second,
		EnableGPUAwareness:  true,
		GPUMemoryBufferMB:   1024, // Keep 1GB buffer
	}
}

// ConcurrencyMetrics tracks concurrency statistics
type ConcurrencyMetrics struct {
	GlobalActive      int64            `json:"global_active"`
	GlobalMax         int              `json:"global_max"`
	TotalAcquired     int64            `json:"total_acquired"`
	TotalReleased     int64            `json:"total_released"`
	TotalRejected     int64            `json:"total_rejected"`
	TotalTimeouts     int64            `json:"total_timeouts"`
	PerModelActive    map[string]int64 `json:"per_model_active"`
	PerModelMax       map[string]int   `json:"per_model_max"`
	AvgHoldTimeMs     float64          `json:"avg_hold_time_ms"`
}

// Semaphore implements a counting semaphore
type Semaphore struct {
	mu       sync.Mutex
	cond     *sync.Cond
	current  int
	max      int
	acquired int64
	released int64
	rejected int64
	timeouts int64
	totalHoldTime int64
	holdCount     int64
}

// NewSemaphore creates a new semaphore
func NewSemaphore(max int) *Semaphore {
	s := &Semaphore{
		max: max,
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Acquire tries to acquire a slot, blocking until available or timeout
func (s *Semaphore) Acquire(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)

	s.mu.Lock()
	defer s.mu.Unlock()

	for s.current >= s.max {
		// Check timeout
		remaining := time.Until(deadline)
		if remaining <= 0 {
			s.timeouts++
			return false
		}

		// Wait with timeout
		done := make(chan struct{})
		go func() {
			time.Sleep(remaining)
			s.cond.Broadcast()
			close(done)
		}()

		s.cond.Wait()

		select {
		case <-done:
			// Timeout occurred during wait
			if s.current >= s.max {
				s.timeouts++
				return false
			}
		default:
			// Woken up by release
		}
	}

	s.current++
	s.acquired++
	return true
}

// TryAcquire attempts to acquire without blocking
func (s *Semaphore) TryAcquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current >= s.max {
		s.rejected++
		return false
	}

	s.current++
	s.acquired++
	return true
}

// Release releases a slot
func (s *Semaphore) Release(holdTime time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current > 0 {
		s.current--
		s.released++
		s.totalHoldTime += holdTime.Milliseconds()
		s.holdCount++
		s.cond.Signal()
	}
}

// SetMax updates the maximum concurrent slots
func (s *Semaphore) SetMax(max int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.max = max
	// Wake up waiters in case max increased
	s.cond.Broadcast()
}

// GetStats returns current semaphore stats
func (s *Semaphore) GetStats() (current, max int, acquired, released, rejected, timeouts int64, avgHoldTime float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	avgHold := float64(0)
	if s.holdCount > 0 {
		avgHold = float64(s.totalHoldTime) / float64(s.holdCount)
	}
	return s.current, s.max, s.acquired, s.released, s.rejected, s.timeouts, avgHold
}

// ConcurrencyLimiter manages concurrent request limits
type ConcurrencyLimiter struct {
	mu              sync.RWMutex
	config          ConcurrencyConfig
	globalSem       *Semaphore
	modelSems       map[string]*Semaphore
	modelGPUMemory  map[string]int // GPU memory requirement per model
	gpuCollector    *GPUMetricsCollector
	stopCh          chan struct{}
	running         bool
}

// NewConcurrencyLimiter creates a new concurrency limiter
func NewConcurrencyLimiter(config ConcurrencyConfig, gpuCollector *GPUMetricsCollector) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		config:         config,
		globalSem:      NewSemaphore(config.GlobalMaxConcurrent),
		modelSems:      make(map[string]*Semaphore),
		modelGPUMemory: make(map[string]int),
		gpuCollector:   gpuCollector,
		stopCh:         make(chan struct{}),
	}
}

// Start begins the concurrency limiter
func (cl *ConcurrencyLimiter) Start() {
	cl.mu.Lock()
	if cl.running {
		cl.mu.Unlock()
		return
	}
	cl.running = true
	cl.stopCh = make(chan struct{})
	cl.mu.Unlock()

	if cl.config.EnableGPUAwareness && cl.gpuCollector != nil {
		go cl.gpuAwarenessLoop()
	}

	logs.GetLogger().Info("Concurrency limiter started")
}

// Stop stops the concurrency limiter
func (cl *ConcurrencyLimiter) Stop() {
	cl.mu.Lock()
	if !cl.running {
		cl.mu.Unlock()
		return
	}
	cl.running = false
	close(cl.stopCh)
	cl.mu.Unlock()

	logs.GetLogger().Info("Concurrency limiter stopped")
}

// RegisterModel sets up concurrency limits for a model
func (cl *ConcurrencyLimiter) RegisterModel(modelID string, maxConcurrent int, gpuMemoryMB int) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	cl.modelSems[modelID] = NewSemaphore(maxConcurrent)
	cl.modelGPUMemory[modelID] = gpuMemoryMB

	logs.GetLogger().Infof("Registered model %s: max concurrent %d, GPU memory %d MB",
		modelID, maxConcurrent, gpuMemoryMB)
}

// UnregisterModel removes concurrency limits for a model
func (cl *ConcurrencyLimiter) UnregisterModel(modelID string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	delete(cl.modelSems, modelID)
	delete(cl.modelGPUMemory, modelID)
}

// Acquire acquires slots for a request (both global and model-specific)
func (cl *ConcurrencyLimiter) Acquire(ctx context.Context, modelID string) (*ConcurrencyToken, error) {
	timeout := cl.config.AcquireTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}

	// Try to acquire global slot first
	if !cl.globalSem.Acquire(timeout) {
		return nil, ErrGlobalConcurrencyLimit
	}

	// Get or create model semaphore
	cl.mu.RLock()
	modelSem, exists := cl.modelSems[modelID]
	cl.mu.RUnlock()

	if !exists {
		// Create default semaphore for unknown model
		cl.mu.Lock()
		modelSem, exists = cl.modelSems[modelID]
		if !exists {
			modelSem = NewSemaphore(cl.config.DefaultModelMax)
			cl.modelSems[modelID] = modelSem
		}
		cl.mu.Unlock()
	}

	// Try to acquire model-specific slot
	remainingTimeout := timeout - time.Millisecond // Small buffer
	if remainingTimeout <= 0 {
		remainingTimeout = time.Millisecond
	}

	if !modelSem.Acquire(remainingTimeout) {
		// Release global slot since we couldn't get model slot
		cl.globalSem.Release(0)
		return nil, ErrModelConcurrencyLimit
	}

	return &ConcurrencyToken{
		modelID:    modelID,
		limiter:    cl,
		acquiredAt: time.Now(),
	}, nil
}

// TryAcquire attempts to acquire without blocking
func (cl *ConcurrencyLimiter) TryAcquire(modelID string) (*ConcurrencyToken, error) {
	// Try global first
	if !cl.globalSem.TryAcquire() {
		return nil, ErrGlobalConcurrencyLimit
	}

	// Get model semaphore
	cl.mu.RLock()
	modelSem, exists := cl.modelSems[modelID]
	cl.mu.RUnlock()

	if !exists {
		cl.mu.Lock()
		modelSem, exists = cl.modelSems[modelID]
		if !exists {
			modelSem = NewSemaphore(cl.config.DefaultModelMax)
			cl.modelSems[modelID] = modelSem
		}
		cl.mu.Unlock()
	}

	// Try model-specific
	if !modelSem.TryAcquire() {
		cl.globalSem.Release(0)
		return nil, ErrModelConcurrencyLimit
	}

	return &ConcurrencyToken{
		modelID:    modelID,
		limiter:    cl,
		acquiredAt: time.Now(),
	}, nil
}

// ConcurrencyToken represents an acquired concurrency slot
type ConcurrencyToken struct {
	modelID    string
	limiter    *ConcurrencyLimiter
	acquiredAt time.Time
	released   int32 // atomic flag to prevent double release
}

// Release releases the concurrency slots
func (ct *ConcurrencyToken) Release() {
	if !atomic.CompareAndSwapInt32(&ct.released, 0, 1) {
		return // Already released
	}

	holdTime := time.Since(ct.acquiredAt)

	ct.limiter.mu.RLock()
	modelSem := ct.limiter.modelSems[ct.modelID]
	ct.limiter.mu.RUnlock()

	if modelSem != nil {
		modelSem.Release(holdTime)
	}
	ct.limiter.globalSem.Release(holdTime)
}

// gpuAwarenessLoop adjusts limits based on GPU memory
func (cl *ConcurrencyLimiter) gpuAwarenessLoop() {
	defer func() {
		if err := recover(); err != nil {
			logs.GetLogger().Errorf("[concurrency_limiter:gpuAwarenessLoop] panic recovered: %v\n%s", err, debug.Stack())
		}
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-cl.stopCh:
			return
		case <-ticker.C:
			cl.adjustLimitsBasedOnGPU()
		}
	}
}

// adjustLimitsBasedOnGPU adjusts concurrency limits based on available GPU memory
func (cl *ConcurrencyLimiter) adjustLimitsBasedOnGPU() {
	if cl.gpuCollector == nil {
		return
	}

	metrics := cl.gpuCollector.CollectGPUMetrics()
	if len(metrics) == 0 {
		return
	}

	// Calculate total available GPU memory
	var totalAvailable float64
	for _, gpu := range metrics {
		available := gpu.MemoryTotalMB - gpu.MemoryUsedMB - float64(cl.config.GPUMemoryBufferMB)
		if available > 0 {
			totalAvailable += available
		}
	}

	cl.mu.Lock()
	defer cl.mu.Unlock()

	// Adjust per-model limits based on GPU memory requirements
	for modelID, gpuMem := range cl.modelGPUMemory {
		if gpuMem <= 0 {
			continue
		}

		sem := cl.modelSems[modelID]
		if sem == nil {
			continue
		}

		// Calculate how many instances can fit
		maxInstances := int(totalAvailable / float64(gpuMem))
		if maxInstances < 1 {
			maxInstances = 1 // Always allow at least 1
		}
		if maxInstances > cl.config.DefaultModelMax*2 {
			maxInstances = cl.config.DefaultModelMax * 2 // Cap at 2x default
		}

		current, oldMax, _, _, _, _, _ := sem.GetStats()
		if maxInstances != oldMax {
			sem.SetMax(maxInstances)
			logs.GetLogger().Infof("Adjusted model %s concurrency limit: %d -> %d (current: %d, available GPU: %.0f MB)",
				modelID, oldMax, maxInstances, current, totalAvailable)
		}
	}
}

// GetMetrics returns concurrency metrics
func (cl *ConcurrencyLimiter) GetMetrics() ConcurrencyMetrics {
	cl.mu.RLock()
	defer cl.mu.RUnlock()

	globalCurrent, globalMax, globalAcq, globalRel, globalRej, globalTO, avgHold := cl.globalSem.GetStats()

	perModelActive := make(map[string]int64)
	perModelMax := make(map[string]int)

	for modelID, sem := range cl.modelSems {
		current, max, _, _, _, _, _ := sem.GetStats()
		perModelActive[modelID] = int64(current)
		perModelMax[modelID] = max
	}

	return ConcurrencyMetrics{
		GlobalActive:   int64(globalCurrent),
		GlobalMax:      globalMax,
		TotalAcquired:  globalAcq,
		TotalReleased:  globalRel,
		TotalRejected:  globalRej,
		TotalTimeouts:  globalTO,
		PerModelActive: perModelActive,
		PerModelMax:    perModelMax,
		AvgHoldTimeMs:  avgHold,
	}
}

// GetModelConcurrency returns current and max concurrency for a model
func (cl *ConcurrencyLimiter) GetModelConcurrency(modelID string) (current, max int) {
	cl.mu.RLock()
	sem := cl.modelSems[modelID]
	cl.mu.RUnlock()

	if sem == nil {
		return 0, cl.config.DefaultModelMax
	}

	current, max, _, _, _, _, _ = sem.GetStats()
	return
}

// SetGlobalMax updates the global maximum concurrent requests
func (cl *ConcurrencyLimiter) SetGlobalMax(max int) {
	cl.globalSem.SetMax(max)
	logs.GetLogger().Infof("Updated global concurrency limit to %d", max)
}

// SetModelMax updates the maximum concurrent requests for a model
func (cl *ConcurrencyLimiter) SetModelMax(modelID string, max int) {
	cl.mu.Lock()
	sem, exists := cl.modelSems[modelID]
	if !exists {
		sem = NewSemaphore(max)
		cl.modelSems[modelID] = sem
	}
	cl.mu.Unlock()

	sem.SetMax(max)
	logs.GetLogger().Infof("Updated model %s concurrency limit to %d", modelID, max)
}

// Concurrency errors
var (
	ErrGlobalConcurrencyLimit = errors.New("global concurrency limit reached")
	ErrModelConcurrencyLimit  = errors.New("model concurrency limit reached")
)
