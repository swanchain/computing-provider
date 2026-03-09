package computing

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// ModelHealth represents the health state of a model endpoint
type ModelHealth int

const (
	ModelHealthUnknown ModelHealth = iota
	ModelHealthHealthy
	ModelHealthDegraded
	ModelHealthUnhealthy
)

func (h ModelHealth) String() string {
	switch h {
	case ModelHealthHealthy:
		return "healthy"
	case ModelHealthDegraded:
		return "degraded"
	case ModelHealthUnhealthy:
		return "unhealthy"
	default:
		return "unknown"
	}
}

// ModelStatus tracks the health status of a single model
type ModelStatus struct {
	ModelID         string      `json:"model_id"`
	Endpoint        string      `json:"endpoint"`
	Health          ModelHealth `json:"health"`
	HealthString    string      `json:"health_string"`
	LastCheck       time.Time   `json:"last_check"`
	LastSuccess     time.Time   `json:"last_success"`
	LastError       string      `json:"last_error,omitempty"`
	LatencyMs       float64     `json:"latency_ms"`
	AvgLatencyMs    float64     `json:"avg_latency_ms"`
	ConsecutiveFails int        `json:"consecutive_fails"`
	TotalChecks     int64       `json:"total_checks"`
	TotalSuccesses  int64       `json:"total_successes"`
	TotalFailures   int64       `json:"total_failures"`
	CircuitOpen     bool        `json:"circuit_open"`
}

// HealthCheckConfig configures the health checker behavior
type HealthCheckConfig struct {
	Interval           time.Duration // How often to check health
	Timeout            time.Duration // Timeout for each health check
	UnhealthyThreshold int           // Consecutive failures before marking unhealthy
	HealthyThreshold   int           // Consecutive successes to recover from unhealthy
	CircuitOpenTime    time.Duration // How long to keep circuit open before retrying
}

// DefaultHealthCheckConfig returns default health check configuration
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Interval:           30 * time.Second,
		Timeout:            10 * time.Second,
		UnhealthyThreshold: 3,
		HealthyThreshold:   2,
		CircuitOpenTime:    60 * time.Second,
	}
}

// ModelHealthChecker performs periodic health checks on model endpoints
type ModelHealthChecker struct {
	mu            sync.RWMutex
	statuses      map[string]*ModelStatus
	endpoints     map[string]string // modelID -> endpoint
	apiKeys       map[string]string // modelID -> API key for authenticated endpoints
	config        HealthCheckConfig
	httpClient    *http.Client
	stopCh        chan struct{}
	running       bool
	onStatusChange func(modelID string, oldHealth, newHealth ModelHealth)
}

// NewModelHealthChecker creates a new health checker
func NewModelHealthChecker(config HealthCheckConfig) *ModelHealthChecker {
	return &ModelHealthChecker{
		statuses:  make(map[string]*ModelStatus),
		endpoints: make(map[string]string),
		apiKeys:   make(map[string]string),
		config:    config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
		stopCh: make(chan struct{}),
	}
}

// SetStatusChangeCallback sets a callback for health status changes
func (h *ModelHealthChecker) SetStatusChangeCallback(cb func(modelID string, oldHealth, newHealth ModelHealth)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onStatusChange = cb
}

// RegisterModel adds a model to health checking
func (h *ModelHealthChecker) RegisterModel(modelID, endpoint, apiKey string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.endpoints[modelID] = endpoint
	if apiKey != "" {
		h.apiKeys[modelID] = apiKey
	} else {
		delete(h.apiKeys, modelID)
	}
	if _, exists := h.statuses[modelID]; !exists {
		h.statuses[modelID] = &ModelStatus{
			ModelID:      modelID,
			Endpoint:     endpoint,
			Health:       ModelHealthUnknown,
			HealthString: ModelHealthUnknown.String(),
		}
	} else {
		h.statuses[modelID].Endpoint = endpoint
	}
	logs.GetLogger().Infof("Registered model %s for health checking at %s", modelID, endpoint)
}

// UnregisterModel removes a model from health checking
func (h *ModelHealthChecker) UnregisterModel(modelID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.endpoints, modelID)
	delete(h.apiKeys, modelID)
	delete(h.statuses, modelID)
	logs.GetLogger().Infof("Unregistered model %s from health checking", modelID)
}

// Start begins periodic health checking
func (h *ModelHealthChecker) Start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	h.stopCh = make(chan struct{})
	h.mu.Unlock()

	// Run initial health check immediately
	h.checkAllModels()

	go h.runHealthCheckLoop()
	logs.GetLogger().Info("Model health checker started")
}

// Stop stops the health checker
func (h *ModelHealthChecker) Stop() {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	close(h.stopCh)
	h.mu.Unlock()

	logs.GetLogger().Info("Model health checker stopped")
}

// runHealthCheckLoop runs the periodic health check loop
func (h *ModelHealthChecker) runHealthCheckLoop() {
	ticker := time.NewTicker(h.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.checkAllModels()
		}
	}
}

// checkAllModels checks health of all registered models
func (h *ModelHealthChecker) checkAllModels() {
	h.mu.RLock()
	modelIDs := make([]string, 0, len(h.endpoints))
	for modelID := range h.endpoints {
		modelIDs = append(modelIDs, modelID)
	}
	h.mu.RUnlock()

	// Check models concurrently
	var wg sync.WaitGroup
	for _, modelID := range modelIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			h.checkModel(id)
		}(modelID)
	}
	wg.Wait()
}

// checkModel performs a health check on a single model
func (h *ModelHealthChecker) checkModel(modelID string) {
	h.mu.RLock()
	endpoint, exists := h.endpoints[modelID]
	apiKey := h.apiKeys[modelID]
	status := h.statuses[modelID]
	circuitOpen := status != nil && status.CircuitOpen
	circuitOpenedAt := time.Time{}
	if status != nil {
		circuitOpenedAt = status.LastCheck
	}
	h.mu.RUnlock()

	if !exists {
		return
	}

	// Check if circuit is open and should stay open
	if circuitOpen {
		if time.Since(circuitOpenedAt) < h.config.CircuitOpenTime {
			// Circuit still open, skip this check
			return
		}
		// Circuit timeout expired, allow a test request (half-open)
		logs.GetLogger().Infof("Circuit half-open for model %s, attempting health check", modelID)
	}

	// Perform health check
	start := time.Now()
	err := h.probeEndpoint(endpoint, apiKey)
	latency := time.Since(start)

	h.mu.Lock()
	defer h.mu.Unlock()

	if status == nil {
		status = &ModelStatus{
			ModelID:  modelID,
			Endpoint: endpoint,
		}
		h.statuses[modelID] = status
	}

	oldHealth := status.Health
	status.LastCheck = time.Now()
	status.TotalChecks++
	status.LatencyMs = float64(latency.Milliseconds())

	// Update average latency using exponential moving average
	if status.AvgLatencyMs == 0 {
		status.AvgLatencyMs = status.LatencyMs
	} else {
		alpha := 0.3 // Smoothing factor
		status.AvgLatencyMs = alpha*status.LatencyMs + (1-alpha)*status.AvgLatencyMs
	}

	if err != nil {
		status.TotalFailures++
		status.ConsecutiveFails++
		status.LastError = err.Error()

		// Determine new health status
		if status.ConsecutiveFails >= h.config.UnhealthyThreshold {
			status.Health = ModelHealthUnhealthy
			status.CircuitOpen = true
		} else if status.Health == ModelHealthHealthy {
			status.Health = ModelHealthDegraded
		}

		logs.GetLogger().Warnf("Health check failed for model %s: %v (consecutive: %d)",
			modelID, err, status.ConsecutiveFails)
	} else {
		status.TotalSuccesses++
		status.LastSuccess = time.Now()
		status.LastError = ""

		// Recovery logic
		if status.Health == ModelHealthUnhealthy {
			// In recovery phase, need consecutive successes
			if status.ConsecutiveFails == 0 {
				// This means we had a success before, track consecutive successes
				status.Health = ModelHealthDegraded
			}
		}

		status.ConsecutiveFails = 0
		status.CircuitOpen = false

		if status.Health != ModelHealthHealthy {
			status.Health = ModelHealthHealthy
		}
	}

	status.HealthString = status.Health.String()

	// Notify callback if health changed
	if oldHealth != status.Health && h.onStatusChange != nil {
		go h.onStatusChange(modelID, oldHealth, status.Health)
	}
}

// probeEndpoint performs the actual health check request
func (h *ModelHealthChecker) probeEndpoint(endpoint, apiKey string) error {
	// Use a short timeout for /health since some proxies (e.g., LiteLLM) have slow health endpoints.
	// This prevents a hanging /health from consuming the entire timeout budget.
	healthTimeout := h.config.Timeout / 2
	if healthTimeout < 3*time.Second {
		healthTimeout = 3 * time.Second
	}

	// Try /health endpoint first (common health check endpoint)
	healthCtx, healthCancel := context.WithTimeout(context.Background(), healthTimeout)
	defer healthCancel()

	healthURL := endpoint + "/health"
	req, err := http.NewRequestWithContext(healthCtx, "GET", healthURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		resp.Body.Close()
		return nil
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Try /v1/models endpoint (OpenAI-compatible) with its own timeout
	modelsCtx, modelsCancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer modelsCancel()

	modelsURL := endpoint + "/v1/models"
	req, err = http.NewRequestWithContext(modelsCtx, "GET", modelsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err = h.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("endpoint unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("endpoint returned status %d", resp.StatusCode)
	}

	return nil
}

// GetModelStatus returns the health status of a specific model
func (h *ModelHealthChecker) GetModelStatus(modelID string) (*ModelStatus, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status, exists := h.statuses[modelID]
	if !exists {
		return nil, false
	}

	// Return a copy to prevent race conditions
	statusCopy := *status
	return &statusCopy, true
}

// GetAllStatuses returns health status of all models
func (h *ModelHealthChecker) GetAllStatuses() map[string]*ModelStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]*ModelStatus, len(h.statuses))
	for id, status := range h.statuses {
		statusCopy := *status
		result[id] = &statusCopy
	}
	return result
}

// GetHealthyModels returns list of healthy model IDs
func (h *ModelHealthChecker) GetHealthyModels() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var healthy []string
	for id, status := range h.statuses {
		if status.Health == ModelHealthHealthy || status.Health == ModelHealthDegraded {
			healthy = append(healthy, id)
		}
	}
	return healthy
}

// IsModelHealthy returns whether a specific model is healthy enough to serve requests
func (h *ModelHealthChecker) IsModelHealthy(modelID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	status, exists := h.statuses[modelID]
	if !exists {
		return false
	}

	// Allow healthy and degraded models to serve requests
	return status.Health == ModelHealthHealthy || status.Health == ModelHealthDegraded
}

// ForceCheck triggers an immediate health check for a model
func (h *ModelHealthChecker) ForceCheck(modelID string) {
	h.mu.RLock()
	_, exists := h.endpoints[modelID]
	h.mu.RUnlock()

	if exists {
		h.checkModel(modelID)
	}
}

// GetStatusJSON returns all statuses as JSON
func (h *ModelHealthChecker) GetStatusJSON() ([]byte, error) {
	statuses := h.GetAllStatuses()
	return json.MarshalIndent(statuses, "", "  ")
}
