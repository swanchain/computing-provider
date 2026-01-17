package computing

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// Priority levels for request queue
type RequestPriority int

const (
	PriorityLow    RequestPriority = 0
	PriorityNormal RequestPriority = 1
	PriorityHigh   RequestPriority = 2
)

// QueuedRequest represents a request waiting in the queue
type QueuedRequest struct {
	ID          string
	ModelID     string
	Priority    RequestPriority
	Payload     json.RawMessage
	EnqueueTime time.Time
	Deadline    time.Time
	ResultChan  chan *QueueResult
	ctx         context.Context
	cancelFunc  context.CancelFunc
	index       int // heap index
}

// QueueResult contains the result of a queued request
type QueueResult struct {
	Response json.RawMessage
	Error    error
}

// QueueConfig configures the request queue behavior
type QueueConfig struct {
	MaxQueueSize      int           // Maximum total queue size
	MaxPerModelQueue  int           // Maximum queue size per model
	DefaultTimeout    time.Duration // Default request timeout
	DrainTimeout      time.Duration // Timeout for draining on shutdown
	EnablePriority    bool          // Enable priority-based queuing
}

// DefaultQueueConfig returns sensible defaults
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		MaxQueueSize:     1000,
		MaxPerModelQueue: 100,
		DefaultTimeout:   2 * time.Minute,
		DrainTimeout:     30 * time.Second,
		EnablePriority:   true,
	}
}

// QueueMetrics tracks queue statistics
type QueueMetrics struct {
	TotalEnqueued    int64   `json:"total_enqueued"`
	TotalDequeued    int64   `json:"total_dequeued"`
	TotalRejected    int64   `json:"total_rejected"`
	TotalTimedOut    int64   `json:"total_timed_out"`
	TotalCancelled   int64   `json:"total_cancelled"`
	CurrentDepth     int64   `json:"current_depth"`
	AvgWaitTimeMs    float64 `json:"avg_wait_time_ms"`
	MaxWaitTimeMs    float64 `json:"max_wait_time_ms"`
	PerModelDepth    map[string]int64 `json:"per_model_depth"`
}

// RequestQueue manages incoming inference requests with priority and backpressure
type RequestQueue struct {
	mu             sync.Mutex
	config         QueueConfig
	heap           *requestHeap
	perModelCount  map[string]int
	metrics        *queueMetricsCollector
	stopCh         chan struct{}
	drainCh        chan struct{}
	running        bool
	processFunc    func(*QueuedRequest) // Function to process dequeued requests
}

// requestHeap implements heap.Interface for priority queue
type requestHeap []*QueuedRequest

func (h requestHeap) Len() int { return len(h) }

func (h requestHeap) Less(i, j int) bool {
	// Higher priority first
	if h[i].Priority != h[j].Priority {
		return h[i].Priority > h[j].Priority
	}
	// Earlier deadline first (FIFO within same priority)
	return h[i].EnqueueTime.Before(h[j].EnqueueTime)
}

func (h requestHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *requestHeap) Push(x interface{}) {
	n := len(*h)
	item := x.(*QueuedRequest)
	item.index = n
	*h = append(*h, item)
}

func (h *requestHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	item.index = -1
	*h = old[0 : n-1]
	return item
}

// queueMetricsCollector tracks queue metrics
type queueMetricsCollector struct {
	totalEnqueued  int64
	totalDequeued  int64
	totalRejected  int64
	totalTimedOut  int64
	totalCancelled int64
	totalWaitTime  int64 // in milliseconds
	maxWaitTime    int64 // in milliseconds
	waitTimeCount  int64
}

// NewRequestQueue creates a new request queue
func NewRequestQueue(config QueueConfig) *RequestQueue {
	h := &requestHeap{}
	heap.Init(h)

	return &RequestQueue{
		config:        config,
		heap:          h,
		perModelCount: make(map[string]int),
		metrics:       &queueMetricsCollector{},
		stopCh:        make(chan struct{}),
		drainCh:       make(chan struct{}),
	}
}

// SetProcessFunc sets the function to process dequeued requests
func (q *RequestQueue) SetProcessFunc(f func(*QueuedRequest)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.processFunc = f
}

// Start begins the queue processor
func (q *RequestQueue) Start() {
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.stopCh = make(chan struct{})
	q.drainCh = make(chan struct{})
	q.mu.Unlock()

	go q.processLoop()
	go q.timeoutChecker()

	logs.GetLogger().Info("Request queue started")
}

// Stop gracefully stops the queue, draining pending requests
func (q *RequestQueue) Stop() {
	q.mu.Lock()
	if !q.running {
		q.mu.Unlock()
		return
	}
	q.running = false
	close(q.stopCh)
	q.mu.Unlock()

	// Wait for drain or timeout
	select {
	case <-q.drainCh:
		logs.GetLogger().Info("Request queue drained successfully")
	case <-time.After(q.config.DrainTimeout):
		logs.GetLogger().Warn("Request queue drain timeout, some requests may be lost")
	}

	logs.GetLogger().Info("Request queue stopped")
}

// Enqueue adds a request to the queue
func (q *RequestQueue) Enqueue(req *QueuedRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.running {
		return ErrQueueStopped
	}

	// Check global queue limit
	if q.heap.Len() >= q.config.MaxQueueSize {
		atomic.AddInt64(&q.metrics.totalRejected, 1)
		return ErrQueueFull
	}

	// Check per-model queue limit
	if q.perModelCount[req.ModelID] >= q.config.MaxPerModelQueue {
		atomic.AddInt64(&q.metrics.totalRejected, 1)
		return ErrModelQueueFull
	}

	// Set defaults
	if req.EnqueueTime.IsZero() {
		req.EnqueueTime = time.Now()
	}
	if req.Deadline.IsZero() {
		req.Deadline = time.Now().Add(q.config.DefaultTimeout)
	}
	if req.ResultChan == nil {
		req.ResultChan = make(chan *QueueResult, 1)
	}

	// Create cancellable context
	ctx, cancel := context.WithDeadline(context.Background(), req.Deadline)
	req.ctx = ctx
	req.cancelFunc = cancel

	// Add to heap
	heap.Push(q.heap, req)
	q.perModelCount[req.ModelID]++

	atomic.AddInt64(&q.metrics.totalEnqueued, 1)

	return nil
}

// EnqueueWithTimeout enqueues a request and waits for the result
func (q *RequestQueue) EnqueueWithTimeout(req *QueuedRequest, timeout time.Duration) (*QueueResult, error) {
	if timeout > 0 {
		req.Deadline = time.Now().Add(timeout)
	}
	req.ResultChan = make(chan *QueueResult, 1)

	if err := q.Enqueue(req); err != nil {
		return nil, err
	}

	select {
	case result := <-req.ResultChan:
		return result, nil
	case <-time.After(timeout):
		req.cancelFunc()
		return nil, ErrRequestTimeout
	}
}

// Dequeue removes and returns the highest priority request
func (q *RequestQueue) Dequeue() *QueuedRequest {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.heap.Len() == 0 {
		return nil
	}

	req := heap.Pop(q.heap).(*QueuedRequest)
	q.perModelCount[req.ModelID]--
	if q.perModelCount[req.ModelID] == 0 {
		delete(q.perModelCount, req.ModelID)
	}

	// Track wait time
	waitTime := time.Since(req.EnqueueTime).Milliseconds()
	atomic.AddInt64(&q.metrics.totalWaitTime, waitTime)
	atomic.AddInt64(&q.metrics.waitTimeCount, 1)
	atomic.AddInt64(&q.metrics.totalDequeued, 1)

	// Update max wait time
	for {
		old := atomic.LoadInt64(&q.metrics.maxWaitTime)
		if waitTime <= old {
			break
		}
		if atomic.CompareAndSwapInt64(&q.metrics.maxWaitTime, old, waitTime) {
			break
		}
	}

	return req
}

// processLoop continuously processes queued requests
func (q *RequestQueue) processLoop() {
	for {
		select {
		case <-q.stopCh:
			// Drain remaining requests
			q.drainQueue()
			close(q.drainCh)
			return
		default:
			req := q.Dequeue()
			if req == nil {
				// No requests, wait a bit
				time.Sleep(10 * time.Millisecond)
				continue
			}

			// Check if request is still valid
			if req.ctx.Err() != nil {
				atomic.AddInt64(&q.metrics.totalCancelled, 1)
				req.ResultChan <- &QueueResult{Error: ErrRequestCancelled}
				continue
			}

			// Process the request
			if q.processFunc != nil {
				go q.processFunc(req)
			}
		}
	}
}

// drainQueue processes remaining requests during shutdown
func (q *RequestQueue) drainQueue() {
	for {
		req := q.Dequeue()
		if req == nil {
			return
		}

		// Cancel remaining requests on shutdown
		req.ResultChan <- &QueueResult{Error: ErrQueueShutdown}
	}
}

// timeoutChecker periodically removes timed-out requests
func (q *RequestQueue) timeoutChecker() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.removeTimedOut()
		}
	}
}

// removeTimedOut removes requests that have exceeded their deadline
func (q *RequestQueue) removeTimedOut() {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	var toRemove []*QueuedRequest

	// Find timed out requests
	for _, req := range *q.heap {
		if now.After(req.Deadline) {
			toRemove = append(toRemove, req)
		}
	}

	// Remove them
	for _, req := range toRemove {
		if req.index >= 0 {
			heap.Remove(q.heap, req.index)
			q.perModelCount[req.ModelID]--
			if q.perModelCount[req.ModelID] == 0 {
				delete(q.perModelCount, req.ModelID)
			}
			atomic.AddInt64(&q.metrics.totalTimedOut, 1)
			req.ResultChan <- &QueueResult{Error: ErrRequestTimeout}
		}
	}
}

// GetMetrics returns current queue metrics
func (q *RequestQueue) GetMetrics() QueueMetrics {
	q.mu.Lock()
	perModelDepth := make(map[string]int64, len(q.perModelCount))
	for model, count := range q.perModelCount {
		perModelDepth[model] = int64(count)
	}
	currentDepth := int64(q.heap.Len())
	q.mu.Unlock()

	totalWaitTime := atomic.LoadInt64(&q.metrics.totalWaitTime)
	waitTimeCount := atomic.LoadInt64(&q.metrics.waitTimeCount)
	var avgWaitTime float64
	if waitTimeCount > 0 {
		avgWaitTime = float64(totalWaitTime) / float64(waitTimeCount)
	}

	return QueueMetrics{
		TotalEnqueued:  atomic.LoadInt64(&q.metrics.totalEnqueued),
		TotalDequeued:  atomic.LoadInt64(&q.metrics.totalDequeued),
		TotalRejected:  atomic.LoadInt64(&q.metrics.totalRejected),
		TotalTimedOut:  atomic.LoadInt64(&q.metrics.totalTimedOut),
		TotalCancelled: atomic.LoadInt64(&q.metrics.totalCancelled),
		CurrentDepth:   currentDepth,
		AvgWaitTimeMs:  avgWaitTime,
		MaxWaitTimeMs:  float64(atomic.LoadInt64(&q.metrics.maxWaitTime)),
		PerModelDepth:  perModelDepth,
	}
}

// GetDepth returns the current queue depth
func (q *RequestQueue) GetDepth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.heap.Len()
}

// GetModelDepth returns queue depth for a specific model
func (q *RequestQueue) GetModelDepth(modelID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.perModelCount[modelID]
}

// IsAccepting returns whether the queue can accept new requests
func (q *RequestQueue) IsAccepting() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running && q.heap.Len() < q.config.MaxQueueSize
}

// Queue errors
var (
	ErrQueueFull        = errors.New("queue is full")
	ErrModelQueueFull   = errors.New("model queue is full")
	ErrQueueStopped     = errors.New("queue is stopped")
	ErrQueueShutdown    = errors.New("queue is shutting down")
	ErrRequestTimeout   = errors.New("request timed out")
	ErrRequestCancelled = errors.New("request was cancelled")
)
