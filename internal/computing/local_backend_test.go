package computing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A model with a local_endpoint is served to the local gateway from that
// backend, with its own key, while hub requests keep using its endpoint.
func TestLocalGatewayUsesLocalEndpoint(t *testing.T) {
	var hubHits, localHits int
	var localAuth string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubHits++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hub"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer hub.Close()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localHits++
		localAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"local"}}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`)
	}))
	defer local.Close()

	s := NewInferenceService("test-node", t.TempDir())
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	s.registry = NewModelRegistry("", nil)
	s.registry.models["org/m"] = &RegisteredModel{ID: "org/m", Endpoint: hub.URL, APIKey: "hub-key",
		LocalEndpoint: local.URL, LocalAPIKey: "local-key", LocalModel: "m", Enabled: true, State: ModelStateReady}
	s.modelMappings["org/m"] = ModelMapping{Endpoint: hub.URL, APIKey: "hub-key", LocalEndpoint: local.URL, LocalAPIKey: "local-key", LocalModel: "m"}

	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()
	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"org/m","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if localHits != 1 || hubHits != 0 || !strings.Contains(string(body), "local") {
		t.Fatalf("gateway request reached hub=%d local=%d: %s", hubHits, localHits, body)
	}
	if localAuth != "Bearer local-key" {
		t.Errorf("local backend got %q, want its own key", localAuth)
	}
	history := s.client.Metrics().GetRequestHistory(1, "org/m")
	if len(history) != 1 || !history[0].RouteRecorded || history[0].BackendEndpoint != local.URL || history[0].BackendModel != "m" || history[0].BackendAttempts != 1 {
		t.Fatalf("local route receipt = %+v", history)
	}

	ctx, route := withRequestRoute(context.Background())
	if _, err := s.handleInference(ctx, InferencePayload{ModelID: "org/m",
		Request: json.RawMessage(`{"model":"org/m","messages":[{"role":"user","content":"hi"}]}`)}); err != nil {
		t.Fatal(err)
	}
	if hubHits != 1 || localHits != 1 {
		t.Errorf("hub request reached hub=%d local=%d, want it on the endpoint", hubHits, localHits)
	}
	receipt := RequestMetric{}
	route.apply(&receipt)
	if receipt.BackendEndpoint != hub.URL || receipt.BackendModel != "m" || receipt.BackendAttempts != 1 {
		t.Errorf("hub route receipt = %+v", receipt)
	}

	// A disabled model stays refused on the local backend too.
	if err := s.registry.DisableModel("org/m"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, mse := s.resolveGatewayEndpoint("org/m"); mse == nil || mse.StatusCode != 503 {
		t.Errorf("disabled model through the gateway: %+v, want 503", mse)
	}
}

// The local backend's reachability is reported per model, so a down local
// backend does not hide behind the hub endpoint's health.
func TestLocalBackendHealthIsReported(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer up.Close()
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()

	s := NewInferenceService("test-node", t.TempDir())
	s.registry = NewModelRegistry("", nil)
	s.registry.models["a"] = &RegisteredModel{ID: "a", Endpoint: "http://hub", LocalEndpoint: up.URL, LocalAPIKey: "k"}
	s.registry.models["b"] = &RegisteredModel{ID: "b", Endpoint: "http://hub", LocalEndpoint: down.URL}
	s.registry.models["c"] = &RegisteredModel{ID: "c", Endpoint: "http://hub"}
	s.localChecker = newLocalBackendChecker(s.localBackends)
	s.localChecker.checkAll()

	got := map[string]*RegisteredModel{}
	for _, m := range s.GetAllModels() {
		got[m.ID] = m
	}
	if got["a"].LocalHealth != "healthy" {
		t.Errorf("a = %q (%s), want healthy", got["a"].LocalHealth, got["a"].LocalError)
	}
	if got["b"].LocalHealth != "unreachable" || !strings.Contains(got["b"].LocalError, "unreachable") {
		t.Errorf("b = %q (%s), want unreachable", got["b"].LocalHealth, got["b"].LocalError)
	}
	if got["c"].LocalHealth != "" {
		t.Errorf("c has no local endpoint but reports %q", got["c"].LocalHealth)
	}
	raw, _ := json.Marshal(got["a"])
	if strings.Contains(string(raw), `"k"`) {
		t.Errorf("the local API key leaked into the status JSON: %s", raw)
	}
}

// With two backends for a model, each counter is compared only against the
// recorded work that went to it: local requests to the local backend, hub
// work to the endpoint.
func TestUsageSeriesSplitsDirectUsageByBackend(t *testing.T) {
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	hubEp, localEp := "http://ext", "http://int"
	mappings := map[string]ModelMapping{"openai/sol": {Endpoint: hubEp, LocalEndpoint: localEp}}
	samples := map[string][]BackendUsageSample{
		perModelKey(hubEp, "openai/sol"): {
			{Time: from.Add(-30 * time.Minute)},
			{Time: from.Add(30 * time.Minute), Prompt: 1000, Generated: 50},
		},
		perModelKey(localEp, "openai/sol"): {
			{Time: from.Add(-30 * time.Minute)},
			{Time: from.Add(30 * time.Minute), Prompt: 700, Generated: 20},
		},
	}
	records := []usageRecord{
		{Model: "openai/sol", Source: "hub", StartTime: from.Add(5 * time.Minute), TokensIn: 1000, TokensOut: 50},
		{Model: "openai/sol", Source: "local", StartTime: from.Add(6 * time.Minute), TokensIn: 500, TokensOut: 20},
	}
	models := map[string][]string{
		perModelKey(hubEp, "openai/sol"):   {"openai/sol"},
		perModelKey(localEp, "openai/sol"): {"openai/sol"},
	}
	s := buildUsageSeries(records, samples, models, usageClassOf(mappings), from, from.Add(time.Hour), time.Hour, "24h")
	if d := s.Totals[SourceDirect]; d.TokensIn != 200 || d.TokensOut != 0 {
		t.Errorf("direct = %+v, want 200 in / 0 out: only the internal proxy saw unrecorded work", d)
	}
}

// A local-only model is served by the local gateway and nothing else: it is
// left out of every registration, a hub or warmup request for it is refused,
// and the health checker never spends a completion on it.
func TestLocalOnlyModelNeverReachesTheHub(t *testing.T) {
	var backendHits int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHits++
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer backend.Close()

	s := NewInferenceService("test-node", t.TempDir())
	s.registry = NewModelRegistry("", nil)
	ready := func(id string, localOnly bool) *RegisteredModel {
		return &RegisteredModel{ID: id, Endpoint: backend.URL, Enabled: true, LocalOnly: localOnly,
			State: ModelStateReady, Health: ModelHealthHealthy}
	}
	s.registry.models["org/public"] = ready("org/public", false)
	s.registry.models["anthropic/private"] = ready("anthropic/private", true)
	s.modelMappings["org/public"] = ModelMapping{Endpoint: backend.URL}
	s.modelMappings["anthropic/private"] = ModelMapping{Endpoint: backend.URL, LocalOnly: true}
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}

	// The hot-reload path re-registers whatever is ready.
	s.updateClientModels()
	if got := s.client.ModelList(); len(got) != 1 || got[0] != "org/public" {
		t.Errorf("registered %v, want only org/public", got)
	}
	// The startup path sends config.toml's list, which may name it too.
	s.client.SetModelList([]string{"org/public", "anthropic/private"})
	s.client.SetModelList(s.withoutLocalOnly(s.client.ModelList()))
	if got := s.client.ModelList(); len(got) != 1 {
		t.Errorf("startup list %v still carries the local-only model", got)
	}

	req := json.RawMessage(`{"model":"anthropic/private","messages":[{"role":"user","content":"hi"}]}`)
	if _, err := s.handleInference(context.Background(), InferencePayload{ModelID: "anthropic/private", Request: req}); err == nil {
		t.Error("a hub request for a local-only model was served")
	}
	if res := s.handleStreamingInference(context.Background(), "r1", InferencePayload{ModelID: "anthropic/private", Request: req, Stream: true},
		func([]byte, bool) error { return nil }); res == nil || res.Error == nil {
		t.Error("a streaming hub request for a local-only model was served")
	}
	if _, err := s.handleWarmup(WarmupPayload{ModelID: "anthropic/private"}); err == nil {
		t.Error("a warmup for a local-only model was sent")
	}
	if backendHits != 0 {
		t.Fatalf("the backend was called %d times by hub-side paths", backendHits)
	}

	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()
	resp, err := http.Post(gw.URL+"/v1/chat/completions", "application/json", strings.NewReader(string(req)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || backendHits != 1 {
		t.Errorf("local gateway: status %d, backend hits %d; want it served locally", resp.StatusCode, backendHits)
	}

	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	h.RegisterModel("anthropic/private", backend.URL, "", "", "text-generation")
	h.SetProbeExempt("anthropic/private", true)
	if h.chatCompatible("anthropic/private") {
		t.Error("a local-only model would be sent deep-check completions")
	}
	// The periodic rotation picks its own candidates; it must skip it too.
	for i := 0; i < 3*DefaultHealthCheckConfig().DeepCheckEvery; i++ {
		if id := h.pickDeepCheckModel(backend.URL, []string{"anthropic/private"}); id != "" {
			t.Fatalf("rotation picked %s for a deep check", id)
		}
	}
}
