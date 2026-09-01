package computing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	sinceDeep        map[string]int    // endpoint -> cheap checks since the last engine probe
	deepStarted      map[string]bool   // endpoint -> has ever been engine-probed
	deepFails        map[string]int    // modelID -> consecutive failed engine probes
	// healthLog is a short rolling record of each model's health, so the UI can
	// show whether a model has been steady or flapping rather than only its
	// state at this instant. Kept in memory and capped: this answers "has this
	// been stable lately", which is a question about the last hour, not a
	// permanent record worth a schema migration.
	healthLog      map[string][]ModelHealth
	deepRotation   map[string]int // endpoint -> which of its models is probed next
	config         HealthCheckConfig
	httpClient     *http.Client
	deepClient     *http.Client
	stopCh         chan struct{}
	running        bool
	onStatusChange func(modelID string, oldHealth, newHealth ModelHealth)
	// recordRequest, when set, receives every engine probe. Probes are real
	// completions against the backend, so they consume the same capacity as
	// routed work — an operator watching GPU load or a metered upstream needs
	// to see them. A callback rather than the metrics object keeps this
	// testable without standing up the whole service.
	recordRequest func(RequestMetric)
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
		deepRotation:     make(map[string]int),
		deepStarted:      make(map[string]bool),
		deepFails:        make(map[string]int),
		healthLog:        make(map[string][]ModelHealth),
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

// SetRequestRecorder sets the sink for engine-probe requests.
func (h *ModelHealthChecker) SetRequestRecorder(fn func(RequestMetric)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordRequest = fn
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

	endpoint := h.endpoints[modelID]
	delete(h.endpoints, modelID)
	// The deep-check maps are keyed by endpoint, so they outlive the models
	// that created them. Drop an endpoint once nothing points at it any more,
	// or they grow without bound as models.json is edited over a long uptime.
	if endpoint != "" {
		stillUsed := false
		for _, ep := range h.endpoints {
			if ep == endpoint {
				stillUsed = true
				break
			}
		}
		if !stillUsed {
			delete(h.sinceDeep, endpoint)
			delete(h.deepRotation, endpoint)
			delete(h.deepStarted, endpoint)
		}
	}
	delete(h.apiKeys, modelID)
	delete(h.statuses, modelID)
	delete(h.localNames, modelID)
	delete(h.categories, modelID)
	delete(h.deepFails, modelID)
	delete(h.healthLog, modelID)
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

			// One model per endpoint per cycle, and only once the cheap probe
			// has answered. Serial with respect to this endpoint: a shared
			// proxy never sees more than one engine probe in flight from us.
			deepID, deepErr := "", error(nil)
			if err == nil {
				if deepID = h.pickDeepCheckModel(ep, info.modelIDs); deepID != "" {
					deepErr = h.deepCheckModel(deepID, ep)
				}
			}

			for _, modelID := range info.modelIDs {
				// Exactly one result per model per cycle: the endpoint's cheap
				// probe, except for the one model that was also engine-probed,
				// whose deeper failure supersedes a passing cheap probe.
				result := err
				if modelID == deepID && result == nil {
					result = deepErr
				}
				h.applyProbeResult(modelID, ep, result)
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
		// A forced check probes this model now. It deliberately does not go
		// through pickDeepCheckModel: that state is keyed by endpoint, and
		// asking it about a single model would reset the endpoint's rotation to
		// the first model and zero its interval counter — so repeatedly forcing
		// one model on a shared proxy would stop every other model on that
		// proxy from ever being engine-probed.
		if h.deepCheckEnabled() && h.chatCompatible(modelID) {
			err = h.deepCheckModel(modelID, endpoint)
		}
	}
	h.applyProbeResult(modelID, endpoint, err)
	h.recordDetectedContext(modelID, contexts)
}

// engineFailure marks a failure the engine probe has already confirmed across
// its own consecutive-failure threshold. It must not be fed back into
// ConsecutiveFails: that counter is shared with the cheap probe, which passes
// between engine probes and clears it, so the model would never reach the
// unhealthy threshold no matter how many engine probes failed. By the time this
// is returned the backend has failed repeatedly and the state is not in doubt.
type engineFailure struct{ err error }

func (e engineFailure) Error() string { return e.err.Error() }

// pickDeepCheckModel decides whether this endpoint is due an engine probe and,
// if so, which of its models to probe.
//
// One model per endpoint per cycle, rotating. A deep probe cannot be shared the
// way the cheap /v1/models probe is — each model needs its own completion under
// its own name — so probing every model at once sends a burst of N simultaneous
// requests to a single host. Against a shared proxy fronting a metered upstream
// that is what earns "server_is_overloaded", and the burst is self-inflicted:
// the models are not independent backends, they are one server.
//
// Rotating instead means a proxy with six models sees one request per interval
// rather than six, at the cost of each individual model being checked six times
// less often. That is the same trade the cheap probe already makes by
// deduplicating per endpoint, and a dedicated single-model backend is unaffected.
func (h *ModelHealthChecker) pickDeepCheckModel(endpoint string, modelIDs []string) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.config.DeepCheckEvery <= 0 {
		return ""
	}

	// Only models that can answer a chat completion are candidates. Sorted so
	// the rotation is stable: ranging a map would pick a different order every
	// cycle and some models would be probed far more often than others.
	candidates := make([]string, 0, len(modelIDs))
	for _, id := range modelIDs {
		if selfcheck.ChatCompatible(h.categories[id]) {
			candidates = append(candidates, id)
			continue
		}
		// Recorded rather than silently ignored, so the status API can answer
		// "why is this model never engine-probed?" — the alternative is an
		// operator concluding the check is broken.
		if st := h.statuses[id]; st != nil {
			st.DeepCheckSkipped = true
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)

	h.sinceDeep[endpoint]++
	first := !h.deepStarted[endpoint]
	if !first && h.sinceDeep[endpoint] < h.config.DeepCheckEvery {
		return ""
	}
	h.sinceDeep[endpoint] = 0
	h.deepStarted[endpoint] = true

	i := h.deepRotation[endpoint] % len(candidates)
	h.deepRotation[endpoint] = (h.deepRotation[endpoint] + 1) % len(candidates)
	return candidates[i]
}

// deepCheckEnabled reports whether engine probing is switched on at all.
func (h *ModelHealthChecker) deepCheckEnabled() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config.DeepCheckEvery > 0
}

// chatCompatible reports whether this model can answer a chat completion.
func (h *ModelHealthChecker) chatCompatible(modelID string) bool {
	h.mu.RLock()
	category := h.categories[modelID]
	h.mu.RUnlock()
	return selfcheck.ChatCompatible(category)
}

// deepCheckModel runs a real one-token completion and reports an error only
// when the backend is genuinely at fault.
//
// This is the check that catches what GET /v1/models cannot see: a vLLM engine
// that has died behind a FastAPI process still serving its static model list,
// or a proxy answering completions with 503 while listing models normally.
func (h *ModelHealthChecker) deepCheckModel(modelID, endpoint string) error {
	h.mu.RLock()
	served := h.localNames[modelID]
	// This model's own key, not the endpoint group's. checkAllModels takes the
	// group key from whichever model came first in map iteration, so on a proxy
	// where models carry different virtual keys the probe would authenticate as
	// the wrong one, get 401, and mark a working model unhealthy — and which
	// model, nondeterministically.
	apiKey := h.apiKeys[modelID]
	h.mu.RUnlock()
	if served == "" {
		served = modelID
	}

	started := time.Now()
	result := h.deepProbe(endpoint, apiKey, served)
	elapsed := time.Since(started)
	h.recordDeepResult(modelID, result)

	h.mu.RLock()
	record := h.recordRequest
	h.mu.RUnlock()
	if record != nil {
		reason := ""
		if !result.OK {
			reason = describeProbeFailure(result)
		}
		record(RequestMetric{
			RequestID:   fmt.Sprintf("health-%s-%d", modelID, started.UnixNano()),
			Model:       modelID,
			StartTime:   started,
			EndTime:     started.Add(elapsed),
			LatencyMs:   float64(elapsed.Milliseconds()),
			Success:     result.OK,
			ErrorReason: reason,
			Source:      SourceHealth,
		})
	}

	h.mu.Lock()
	if result.BackendAtFault() {
		h.deepFails[modelID]++
	} else {
		delete(h.deepFails, modelID)
	}
	fails := h.deepFails[modelID]
	threshold := h.config.UnhealthyThreshold
	h.mu.Unlock()

	// Only report once the engine has failed UnhealthyThreshold probes in a
	// row. Reporting on the first would let one transient 502 from a shared
	// upstream pull a working model, and the count cannot live in
	// ConsecutiveFails: that is shared with the cheap probe, which passes
	// between deep probes and would clear it every time.
	if result.BackendAtFault() && fails < threshold {
		logs.GetLogger().Infof("Engine probe for %s failed %d/%d consecutive attempts: %s",
			modelID, fails, threshold, describeProbeFailure(result))
		return nil
	}

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
	return engineFailure{fmt.Errorf("engine probe failed %d times in a row: %s", fails, describeProbeFailure(result))}
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

		// A confirmed engine failure is authoritative on its own: the HTTP
		// server answering /v1/models says nothing about whether the engine
		// behind it can serve, which is the whole point of the deeper probe.
		var engine engineFailure
		if errors.As(probeErr, &engine) {
			status.Health = ModelHealthUnhealthy
			status.CircuitOpen = true
			status.HealthString = status.Health.String()
			logs.GetLogger().Warnf("Model %s marked unhealthy by the engine probe: %v", modelID, probeErr)
			if oldHealth != status.Health && h.onStatusChange != nil {
				go h.onStatusChange(modelID, oldHealth, status.Health)
			}
			return
		}

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

		// A passing cheap probe must not clear a confirmed engine failure. The
		// engine probe runs once every DeepCheckEvery cycles; the cheap probe
		// runs on all the others and keeps succeeding, because the HTTP server
		// is alive. Letting it restore health meant the model flipped back to
		// healthy on the very next cycle and kept taking traffic it could not
		// serve — which is the failure this whole check exists to catch. Only a
		// passing engine probe clears it.
		if h.deepFails[modelID] >= h.config.UnhealthyThreshold {
			status.ConsecutiveFails = 0
			status.Health = ModelHealthUnhealthy
			status.CircuitOpen = true
			status.HealthString = status.Health.String()
			if oldHealth != status.Health && h.onStatusChange != nil {
				go h.onStatusChange(modelID, oldHealth, status.Health)
			}
			return
		}

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
	h.appendHealthLog(modelID, status.Health)

	// Notify callback if health changed
	if oldHealth != status.Health && h.onStatusChange != nil {
		go h.onStatusChange(modelID, oldHealth, status.Health)
	}
}

// healthLogSize caps the rolling record. At the default 30s interval this is
// about an hour, which is the window in which "it keeps flapping" is a useful
// observation.
const healthLogSize = 120

// appendHealthLog records one sample. The caller holds h.mu.
func (h *ModelHealthChecker) appendHealthLog(modelID string, state ModelHealth) {
	log := append(h.healthLog[modelID], state)
	if len(log) > healthLogSize {
		log = log[len(log)-healthLogSize:]
	}
	h.healthLog[modelID] = log
}

// HealthLog returns the rolling health record for a model, oldest first.
func (h *ModelHealthChecker) HealthLog(modelID string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	log := h.healthLog[modelID]
	out := make([]string, 0, len(log))
	for _, st := range log {
		out = append(out, st.String())
	}
	return out
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
