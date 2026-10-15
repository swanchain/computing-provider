package computing

import (
	"testing"
	"time"
)

// Traffic is grouped per source, model and backend; rows that recorded no
// backend are put on the one the model is configured to use; and a
// configured backend with no traffic is still listed, with its state.
func TestRoutesGroupsTrafficAndListsIdleBackends(t *testing.T) {
	store := withStore(t, 7, 1000)
	now := time.Now().UTC()
	rec := func(id, model string, source RequestSource, backend string, ok bool, errReason string) {
		store.Record(RequestMetric{
			RequestID: id, Model: model, Source: source, StartTime: now.Add(-time.Minute), EndTime: now,
			LatencyMs: 100, TokensIn: 10, TokensOut: 5, Success: ok, ErrorReason: errReason,
			RouteRecorded: backend != "", BackendEndpoint: backend,
		})
	}
	rec("a", "openai/sol", SourceHub, "http://localhost:8317", true, "")
	rec("b", "openai/sol", SourceHub, "http://localhost:8317", false, "upstream 502")
	rec("c", "openai/sol", SourceLocal, "http://127.0.0.1:8318", true, "")
	rec("d", "openai/sol", SourceHealth, "", true, "")                                    // probe: no backend recorded
	rec("e", "anthropic/private", SourceHub, "", false, "model server returned HTTP 404") // refused: local-only
	store.Record(RequestMetric{RequestID: "f", Model: "openai/sol", Source: SourceHub, StartTime: now.Add(-time.Minute),
		Success: false, ErrorReason: "rate limited", RouteRecorded: true}) // rejected before forwarding
	store.Record(RequestMetric{RequestID: "old", Model: "openai/sol", Source: SourceHub,
		StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-2 * time.Hour), Success: true})
	flush(t, store)

	s := NewInferenceService("test-node", t.TempDir())
	s.registry = NewModelRegistry("", nil)
	s.registry.models["openai/sol"] = &RegisteredModel{ID: "openai/sol", Enabled: true, Health: ModelHealthHealthy,
		Endpoint: "http://localhost:8317/", LocalEndpoint: "http://127.0.0.1:8318"}
	s.registry.models["org/moved"] = &RegisteredModel{ID: "org/moved", Enabled: true, Health: ModelHealthUnhealthy,
		Endpoint: "http://localhost:30003"}
	s.registry.models["anthropic/private"] = &RegisteredModel{ID: "anthropic/private", Enabled: true, LocalOnly: true,
		Endpoint: "http://127.0.0.1:8318"}
	s.registry.models["org/off"] = &RegisteredModel{ID: "org/off", Enabled: false, Endpoint: "http://localhost:30001"}

	m := s.Routes("15m")
	links := map[[3]string]RouteLink{}
	for _, l := range m.Links {
		links[[3]string{l.Source, l.Model, l.Backend}] = l
	}
	if hub := links[[3]string{"hub", "openai/sol", "http://localhost:8317"}]; hub.Requests != 2 || hub.Failures != 1 || hub.LastError != "upstream 502" || hub.LastAt.IsZero() {
		t.Errorf("hub link = %+v, want 2 requests, 1 failure with its error, and a time (outside-window row excluded)", hub)
	}
	if l := links[[3]string{"local", "openai/sol", "http://127.0.0.1:8318"}]; l.Requests != 1 {
		t.Errorf("local link = %+v", l)
	}
	if p := links[[3]string{"health", "openai/sol", "http://localhost:8317"}]; p.Requests != 1 {
		t.Errorf("probe with no recorded backend should be on the configured endpoint: %+v", m.Links)
	}
	if r := links[[3]string{"hub", "anthropic/private", ""}]; r.Requests != 1 {
		t.Errorf("a refused hub request for a local-only model was attributed to a backend: %+v", m.Links)
	}
	if r := links[[3]string{"hub", "openai/sol", ""}]; r.Requests != 1 {
		t.Errorf("a request recorded as not forwarded was attributed to a backend: %+v", m.Links)
	}
	if len(m.Links) != 5 {
		t.Errorf("links = %+v, want 5", m.Links)
	}

	health := map[string]string{}
	for _, b := range m.Backends {
		health[b.Endpoint] = b.Health
	}
	if health["http://localhost:8317"] != "up" || health["http://localhost:30003"] != "down" {
		t.Errorf("backends = %+v, want :8317 up and the idle :30003 listed down", m.Backends)
	}
	if _, ok := health["http://localhost:30001"]; ok {
		t.Errorf("a disabled model's idle backend was listed: %+v", m.Backends)
	}
}

// Direct usage is derived per counter: a model served by two proxies gets a
// link to each that did unrecorded work, recorded work is subtracted, and a
// counter with no unrecorded output makes no link.
func TestDirectRouteLinksPerBackend(t *testing.T) {
	bucket := 5 * time.Minute
	from := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	at := func(m int) time.Time { return from.Add(time.Duration(m) * time.Minute) }
	hub, local := perModelKey("http://localhost:8317", "openai/sol"), perModelKey("http://127.0.0.1:8318", "openai/sol")
	in := usageData{
		samples: map[string][]BackendUsageSample{
			hub:   {{Endpoint: hub, Time: at(-5), Generated: 100}, {Endpoint: hub, Time: at(10), Generated: 150}},
			local: {{Endpoint: local, Time: at(-5), Generated: 0}, {Endpoint: local, Time: at(20), Prompt: 900, Generated: 400}},
			"http://localhost:30000": {{Endpoint: "http://localhost:30000", Time: at(-5)}, {Endpoint: "http://localhost:30000", Time: at(30), Generated: 0}},
		},
		endpointModels: map[string][]string{hub: {"openai/sol"}, local: {"openai/sol"}, "http://localhost:30000": {"org/glm"}},
		// The hub instance's 50 generated tokens are all the node's own work.
		records: []usageRecord{{Model: "openai/sol", Source: string(SourceHub), StartTime: at(7), TokensOut: 50}},
	}
	classOf := usageClassOf(map[string]ModelMapping{"openai/sol": {Endpoint: "http://localhost:8317", LocalEndpoint: "http://127.0.0.1:8318"}})
	links := directRouteLinks(in, classOf, from, to, bucket)
	if len(links) != 1 {
		t.Fatalf("links = %+v, want only the local proxy's unrecorded work", links)
	}
	l := links[0]
	if l.Source != SourceDirect || l.Backend != "http://127.0.0.1:8318" || l.Model != "openai/sol" || l.TokensOut != 400 || l.TokensIn != 900 || l.Requests != 0 {
		t.Errorf("link = %+v", l)
	}
	if !l.LastAt.Equal(at(25)) {
		t.Errorf("last = %v, want the end of the bucket the work was counted in", l.LastAt)
	}

	m := buildRouteMap(nil, nil, nil, append(links, RouteLink{Source: SourceDirect, Model: "x/y", Backend: "http://localhost:9999", TokensOut: 1}))
	if len(m.Links) != 2 || len(m.Backends) != 2 || m.Backends[1].Roles[0] != "direct" {
		t.Errorf("direct links should be kept and their unconfigured backends listed as direct: %+v", m)
	}
}
