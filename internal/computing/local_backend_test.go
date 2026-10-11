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

	if _, err := s.handleInference(context.Background(), InferencePayload{ModelID: "org/m",
		Request: json.RawMessage(`{"model":"org/m","messages":[{"role":"user","content":"hi"}]}`)}); err != nil {
		t.Fatal(err)
	}
	if hubHits != 1 || localHits != 1 {
		t.Errorf("hub request reached hub=%d local=%d, want it on the endpoint", hubHits, localHits)
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
