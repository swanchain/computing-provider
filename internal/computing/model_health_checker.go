package computing

import (
	"bytes"
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
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
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

	// Engine-probe results are tracked separately from the cheap probe so an
	// operator can tell "the engine cannot serve" from "the HTTP server is
	// unreachable". The two have different causes and different fixes, and
	// collapsing them into LastError loses the distinction that makes the
	// alert actionable.
	LastDeepCheck    time.Time `json:"last_deep_check,omitempty"`
	LastDeepSuccess  time.Time `json:"last_deep_success,omitempty"`
	LastDeepError    string    `json:"last_deep_error,omitempty"`
	DeepCheckSkipped bool      `json:"deep_check_skipped,omitempty"` // Not a chat model
}

// HealthCheckConfig configures the health checker behavior
type HealthCheckConfig struct {
	Interval           time.Duration // How often to check health
	Timeout            time.Duration // Timeout for each health check
	UnhealthyThreshold int           // Consecutive failures before marking unhealthy
	HealthyThreshold   int           // Consecutive successes to recover from unhealthy
	CircuitOpenTime    time.Duration // How long to keep circuit open before retrying

	// DeepCheckEvery runs a real one-token completion on every Nth check.
	// GET /v1/models is served from a static registry on most backends, so it
	// keeps returning 200 after the inference engine behind it has died — the
	// model reads healthy while failing every request. 0 disables the engine
	// probe and restores the cheap-probe-only behaviour.
	DeepCheckEvery int
	// DeepCheckTimeout bounds that completion. Deliberately longer than
	// Timeout: a cold or loaded engine can take seconds to return its first
	// token, and timing that out would mark a working backend unhealthy.
	DeepCheckTimeout time.Duration
}

// DefaultHealthCheckConfig returns default health check configuration
func DefaultHealthCheckConfig() HealthCheckConfig {
	return HealthCheckConfig{
		Interval:           30 * time.Second,
		Timeout:            10 * time.Second,
		UnhealthyThreshold: 3,
		HealthyThreshold:   2,
		CircuitOpenTime:    60 * time.Second,
		// Every 10th check at a 30s interval is one token per model every five
		// minutes: negligible for a local backend, and a bounded window in
		// which a dead engine can go unnoticed.
		DeepCheckEvery:   10,
		DeepCheckTimeout: 30 * time.Second,
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
	categories       map[string]string // modelID -> models.json category, to skip non-chat models
	sinceDeep        map[string]int    // modelID -> cheap checks since the last engine probe
	config           HealthCheckConfig
	httpClient       *http.Client
	deepClient       *http.Client
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
		categories:       make(map[string]string),
		sinceDeep:        make(map[string]int),
		config:           config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				// Probes run once per model per interval — tens of seconds
				// apart — so a pooled connection buys nothing and costs
				// correctness. uvicorn, which serves vLLM and SGLang, closes
				// idle connections after 5s by default; the client would keep
				// reusing one and get "connection reset by peer" on the next
				// probe. That reads as an unhealthy backend when the backend is
				// fine, and the model flaps out of routing.
				DisableKeepAlives: true,
			},
		},
		// A separate client: the engine probe needs a longer timeout than the
		// cheap probe and must not inherit one tuned for a static listing.
		deepClient: &http.Client{
			Timeout: deepTimeoutOrDefault(config),
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
				DisableKeepAlives: true,
			},
		},
		stopCh: make(chan struct{}),
	}
}

// deepTimeoutOrDefault stops a caller-built config that predates
// DeepCheckTimeout from producing a client with no timeout at all, which would
// leak a goroutine per probe against a backend that never answers.
func deepTimeoutOrDefault(c HealthCheckConfig) time.Duration {
	if c.DeepCheckTimeout > 0 {
		return c.DeepCheckTimeout
	}
	return 30 * time.Second
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
func (h *ModelHealthChecker) RegisterModel(modelID, endpoint, apiKey, localModel, category string) {
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
	// Outside the branch above: the category decides whether this model can be
	// probed at all, and most models have no local_model override. Storing it
	// only for Ollama-style entries left every other embedding model looking
	// chat-compatible.
	h.categories[modelID] = category
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
	delete(h.categories, modelID)
	delete(h.sinceDeep, modelID)
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
	if err == nil {
		// Only worth asking whether the engine can serve once the HTTP server
		// has answered; if the cheap probe already failed, the model is going
		// to be marked down anyway and a completion would just add latency.
		err = h.maybeDeepCheck(modelID, endpoint, apiKey)
	}
	h.applyProbeResult(modelID, endpoint, err)
	h.recordDetectedContext(modelID, contexts)
}

// maybeDeepCheck runs a real one-token completion every DeepCheckEvery-th call
// and returns an error only when the backend is genuinely at fault.
//
// This is the check that catches the failure GET /v1/models cannot see: a vLLM
// engine that has died behind a FastAPI process still serving its static model
// list, or a proxy backend answering completions with 503 auth_unavailable
// while listing models normally.
func (h *ModelHealthChecker) maybeDeepCheck(modelID, endpoint, apiKey string) error {
	h.mu.Lock()
	every := h.config.DeepCheckEvery
	if every <= 0 {
		h.mu.Unlock()
		return nil
	}
	category := h.categories[modelID]
	served := h.localNames[modelID]
	// Count first so the very first check of a newly registered model runs the
	// probe: that is when a misconfigured backend is most likely, and waiting
	// DeepCheckEvery intervals to find out wastes the whole window.
	h.sinceDeep[modelID]++
	due := h.sinceDeep[modelID] >= every || h.statuses[modelID] == nil || h.statuses[modelID].LastDeepCheck.IsZero()
	if due {
		h.sinceDeep[modelID] = 0
	}
	h.mu.Unlock()

	if !due {
		return nil
	}

	if !selfcheck.ChatCompatible(category) {
		h.recordDeepResult(modelID, selfcheck.ProbeResult{Skipped: true})
		return nil
	}
	if served == "" {
		served = modelID
	}

	result := h.deepProbe(endpoint, apiKey, served)
	h.recordDeepResult(modelID, result)

	if !result.BackendAtFault() {
		// A 400/422 is the backend rejecting this particular prompt — a chat
		// template that will not accept "ping", most often — and says nothing
		// about whether it can serve real traffic. A 429 is load, not failure.
		if !result.OK && !result.Skipped {
			logs.GetLogger().Debugf("Engine probe for %s returned HTTP %d (%s); not counted against health",
				modelID, result.StatusCode, result.Error)
		}
		return nil
	}
	return fmt.Errorf("engine probe failed: %s", describeProbeFailure(result))
}

// describeProbeFailure says which kind of failure this was, because "cannot
// serve" has several causes an operator fixes differently.
func describeProbeFailure(r selfcheck.ProbeResult) string {
	switch {
	case r.StatusCode == 0:
		return fmt.Sprintf("no response from backend (%s)", r.Error)
	case r.StatusCode == 401 || r.StatusCode == 403:
		return fmt.Sprintf("backend rejected credentials (HTTP %d: %s)", r.StatusCode, r.Error)
	case r.StatusCode == 404:
		return fmt.Sprintf("model not served at this endpoint (HTTP 404: %s)", r.Error)
	case r.StatusCode >= 500:
		return fmt.Sprintf("engine error (HTTP %d: %s)", r.StatusCode, r.Error)
	default:
		return fmt.Sprintf("HTTP %d: %s", r.StatusCode, r.Error)
	}
}

// deepProbe asks the backend for a single token.
func (h *ModelHealthChecker) deepProbe(endpoint, apiKey, servedName string) selfcheck.ProbeResult {
	body, err := json.Marshal(map[string]interface{}{
		"model": servedName,
		// A one-word user turn: an empty prompt is rejected outright by some
		// chat templates, and a system-only message by others.
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	})
	if err != nil {
		return selfcheck.ProbeResult{Error: err.Error()}
	}

	url := strings.TrimRight(endpoint, "/") + "/v1/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return selfcheck.ProbeResult{Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := h.deepClient.Do(req)
	if err != nil {
		// No status code: the request never reached the backend at all.
		return selfcheck.ProbeResult{Error: err.Error()}
	}
	defer resp.Body.Close()

	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	if resp.StatusCode >= 300 {
		return selfcheck.ProbeResult{StatusCode: resp.StatusCode, Error: strings.TrimSpace(string(snippet))}
	}
	return selfcheck.ProbeResult{OK: true, StatusCode: resp.StatusCode}
}

// recordDeepResult stores the engine-probe outcome for the status API.
func (h *ModelHealthChecker) recordDeepResult(modelID string, r selfcheck.ProbeResult) {
	h.mu.Lock()
	defer h.mu.Unlock()

	status := h.statuses[modelID]
	if status == nil {
		return // applyProbeResult creates it moments later; nothing to attach to yet.
	}
	status.DeepCheckSkipped = r.Skipped
	if r.Skipped {
		return
	}
	status.LastDeepCheck = time.Now()
	if r.OK {
		status.LastDeepSuccess = status.LastDeepCheck
		status.LastDeepError = ""
		return
	}
	status.LastDeepError = describeProbeFailure(r)
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
