package computing

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	ModelID          string      `json:"model_id"`
	Endpoint         string      `json:"endpoint"`
	Health           ModelHealth `json:"health"`
	HealthString     string      `json:"health_string"`
	LastCheck        time.Time   `json:"last_check"`
	LastSuccess      time.Time   `json:"last_success"`
	LastError        string      `json:"last_error,omitempty"`
	LatencyMs        float64     `json:"latency_ms"`
	AvgLatencyMs     float64     `json:"avg_latency_ms"`
	ConsecutiveFails int         `json:"consecutive_fails"`
	TotalChecks      int64       `json:"total_checks"`
	TotalSuccesses   int64       `json:"total_successes"`
	TotalFailures    int64       `json:"total_failures"`
	CircuitOpen      bool        `json:"circuit_open"`
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
	mu               sync.RWMutex
	statuses         map[string]*ModelStatus
	endpoints        map[string]string // modelID -> endpoint
	apiKeys          map[string]string // modelID -> API key for authenticated endpoints
	localNames       map[string]string // modelID -> backend-local model name (e.g. Ollama tag)
	detectedContexts map[string]int    // modelID -> context window detected from the backend (/v1/models max_model_len)
	contextProbed    map[string]bool   // modelID -> a probe has completed, whether or not it yielded a window
	config           HealthCheckConfig
	httpClient       *http.Client
	stopCh           chan struct{}
	running          bool
	onStatusChange   func(modelID string, oldHealth, newHealth ModelHealth)
}

// NewModelHealthChecker creates a new health checker
func NewModelHealthChecker(config HealthCheckConfig) *ModelHealthChecker {
	return &ModelHealthChecker{
		statuses:         make(map[string]*ModelStatus),
		endpoints:        make(map[string]string),
		apiKeys:          make(map[string]string),
		localNames:       make(map[string]string),
		detectedContexts: make(map[string]int),
		contextProbed:    make(map[string]bool),
		config:           config,
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

// RegisterModel adds a model to health checking. localModel is the backend's
// own name for the model (e.g. an Ollama tag) used to match /v1/models entries
// for context detection; pass "" when it equals the model ID.
func (h *ModelHealthChecker) RegisterModel(modelID, endpoint, apiKey, localModel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.endpoints[modelID] = endpoint
	if apiKey != "" {
		h.apiKeys[modelID] = apiKey
	} else {
		delete(h.apiKeys, modelID)
	}
	if localModel != "" {
		h.localNames[modelID] = localModel
	} else {
		delete(h.localNames, modelID)
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
	delete(h.localNames, modelID)
	delete(h.detectedContexts, modelID)
	delete(h.contextProbed, modelID)
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
	// Group models by endpoint to avoid redundant probes when multiple models
	// share the same server (e.g., a LiteLLM proxy serving 65 models).
	type endpointInfo struct {
		apiKey   string
		modelIDs []string
	}
	endpointGroups := make(map[string]*endpointInfo)
	for modelID, endpoint := range h.endpoints {
		if group, exists := endpointGroups[endpoint]; exists {
			group.modelIDs = append(group.modelIDs, modelID)
		} else {
			endpointGroups[endpoint] = &endpointInfo{
				apiKey:   h.apiKeys[modelID],
				modelIDs: []string{modelID},
			}
		}
	}
	h.mu.RUnlock()

	// Probe each unique endpoint once, then apply results to all its models
	var wg sync.WaitGroup
	for endpoint, group := range endpointGroups {
		wg.Add(1)
		go func(ep string, info *endpointInfo) {
			defer wg.Done()
			contexts, err := h.probeEndpoint(ep, info.apiKey)
			for _, modelID := range info.modelIDs {
				h.applyProbeResult(modelID, ep, err)
				h.recordDetectedContext(modelID, contexts)
			}
		}(endpoint, group)
	}
	wg.Wait()
}

// recordDetectedContext stores the backend-reported context window for a model
// if the endpoint's /v1/models listing included one for it.
func (h *ModelHealthChecker) recordDetectedContext(modelID string, contexts map[string]int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Record the attempt even when the backend declares nothing: only then can
	// a caller distinguish "this backend has no window to report" from "the
	// first health check has not run yet".
	h.contextProbed[modelID] = true

	if len(contexts) == 0 {
		return
	}

	name := h.localNames[modelID]
	if name == "" {
		name = modelID
	}
	ctx, ok := contexts[name]
	if !ok {
		// Backends differ in ID casing; fall back to a case-insensitive match.
		for id, c := range contexts {
			if strings.EqualFold(id, name) {
				ctx, ok = c, true
				break
			}
		}
	}
	if !ok || ctx <= 0 {
		return
	}
	if h.detectedContexts[modelID] != ctx {
		logs.GetLogger().Infof("Detected context window for %s: %d tokens (backend max_model_len)", modelID, ctx)
		h.detectedContexts[modelID] = ctx
	}
}

// ContextProbed reports whether a health check has completed for this model,
// so an empty detected context can be read as "the backend declares none"
// rather than "not yet known".
func (h *ModelHealthChecker) ContextProbed(modelID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.contextProbed[modelID]
}

// GetDetectedContext returns the backend-reported context window for a model,
// or 0 if the backend does not expose one (e.g. Ollama).
func (h *ModelHealthChecker) GetDetectedContext(modelID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.detectedContexts[modelID]
}

// checkModel performs a health check on a single model (used by ForceCheck)
func (h *ModelHealthChecker) checkModel(modelID string) {
	h.mu.RLock()
	endpoint, exists := h.endpoints[modelID]
	apiKey := h.apiKeys[modelID]
	h.mu.RUnlock()

	if !exists {
		return
	}

	contexts, err := h.probeEndpoint(endpoint, apiKey)
	h.applyProbeResult(modelID, endpoint, err)
	h.recordDetectedContext(modelID, contexts)
}

// applyProbeResult updates a model's health status based on a probe result.
// This is called after probing an endpoint, allowing multiple models sharing
// the same endpoint to reuse a single probe result.
func (h *ModelHealthChecker) applyProbeResult(modelID, endpoint string, probeErr error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	status := h.statuses[modelID]
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

	if probeErr != nil {
		status.TotalFailures++
		status.ConsecutiveFails++
		status.LastError = probeErr.Error()

		// Determine new health status
		if status.ConsecutiveFails >= h.config.UnhealthyThreshold {
			status.Health = ModelHealthUnhealthy
			status.CircuitOpen = true
		} else if status.Health == ModelHealthHealthy {
			status.Health = ModelHealthDegraded
		}

		logs.GetLogger().Warnf("Health check failed for model %s: %v (consecutive: %d)",
			modelID, probeErr, status.ConsecutiveFails)
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

// probeEndpoint performs the actual health check request.
// Tries /v1/models first (lightweight, just lists models) then falls back to
// /health. This avoids triggering expensive deep health checks on proxies like
// LiteLLM, where GET /health sends a real inference request to every backend.
// On a successful /v1/models probe it also returns each listed model's
// max_model_len (vLLM/SGLang expose it; Ollama does not), keyed by the
// backend's model ID, for context-window reporting (#61).
func (h *ModelHealthChecker) probeEndpoint(endpoint, apiKey string) (map[string]int, error) {
	// Try /v1/models first — lightweight on all known serving engines
	modelsCtx, modelsCancel := context.WithTimeout(context.Background(), h.config.Timeout)
	defer modelsCancel()

	modelsURL := endpoint + "/v1/models"
	req, err := http.NewRequestWithContext(modelsCtx, "GET", modelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := h.httpClient.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		contexts := parseModelContexts(resp.Body)
		resp.Body.Close()
		return contexts, nil
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Fall back to /health (works for SGLang, vLLM, Ollama, etc.)
	healthTimeout := h.config.Timeout / 2
	if healthTimeout < 3*time.Second {
		healthTimeout = 3 * time.Second
	}
	healthCtx, healthCancel := context.WithTimeout(context.Background(), healthTimeout)
	defer healthCancel()

	healthURL := endpoint + "/health"
	req, err = http.NewRequestWithContext(healthCtx, "GET", healthURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err = h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("endpoint unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endpoint returned status %d", resp.StatusCode)
	}

	return nil, nil
}

// parseModelContexts extracts per-model context windows from a /v1/models
// response body. vLLM and SGLang report max_model_len per model; backends that
// don't (e.g. Ollama) simply yield no entries.
func parseModelContexts(body io.Reader) map[string]int {
	var listing struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&listing); err != nil {
		return nil
	}
	contexts := make(map[string]int)
	for _, m := range listing.Data {
		if m.MaxModelLen > 0 {
			contexts[m.ID] = m.MaxModelLen
		}
	}
	return contexts
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
