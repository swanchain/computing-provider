package computing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
)

func probeBackend(t *testing.T, completionStatus int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if completionStatus >= 300 {
			http.Error(w, `{"error":"EngineDeadError"}`, completionStatus)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Probes are real completions and consume the same backend capacity as routed
// work. Leaving them out of the request history made that load invisible —
// which is how its volume came to be misjudged.
func TestEngineProbeIsRecordedAsHealth(t *testing.T) {
	srv := probeBackend(t, 200)
	var got []RequestMetric
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	h.SetRequestRecorder(func(m RequestMetric) { got = append(got, m) })
	h.RegisterModel("test/model", srv.URL, "", "", "text-generation")

	h.checkAllModels()

	if len(got) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(got))
	}
	if got[0].Source != SourceHealth {
		t.Errorf("source = %q, want %q", got[0].Source, SourceHealth)
	}
	if got[0].Model != "test/model" || !got[0].Success {
		t.Errorf("got %+v", got[0])
	}
}

// A failing probe must be recorded too, with the reason, or the history shows
// only the checks that passed.
func TestFailingEngineProbeIsRecorded(t *testing.T) {
	srv := probeBackend(t, 500)
	var got []RequestMetric
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	h.SetRequestRecorder(func(m RequestMetric) { got = append(got, m) })
	h.RegisterModel("test/model", srv.URL, "", "", "text-generation")

	h.checkAllModels()

	if len(got) != 1 || got[0].Success {
		t.Fatalf("want one failed record, got %+v", got)
	}
	if got[0].ErrorReason == "" {
		t.Error("a failed probe should record why")
	}
}

func TestSelfCheckProbesAreRecorded(t *testing.T) {
	var got []RequestMetric
	r := newSelfCheckRunner(nil, nil, func() conf.SelfCheck { return conf.SelfCheck{} }, nil)
	r.SetRequestRecorder(func(m RequestMetric) { got = append(got, m) })

	r.recordProbes(selfcheck.Report{Probes: map[string]selfcheck.ProbeResult{
		"a/model":     {OK: true, StatusCode: 200, LatencyMs: 42},
		"b/model":     {StatusCode: 503, Error: "auth_unavailable", LatencyMs: 12},
		"embed/model": {Skipped: true},
	}})

	if len(got) != 2 {
		t.Fatalf("recorded %d, want 2 — a skipped probe was never sent", len(got))
	}
	for _, m := range got {
		if m.Source != SourceSelfCheck {
			t.Errorf("%s source = %q, want %q", m.Model, m.Source, SourceSelfCheck)
		}
	}
}

// Hub traffic keeps its own label, or the column cannot separate the two.
func TestHubRequestsAreLabelled(t *testing.T) {
	m := NewInferenceMetrics()
	m.RecordRequest(RequestMetric{RequestID: "x", Model: "m", Source: SourceHub, Success: true})
	h := m.GetRequestHistory(10, "")
	if len(h) != 1 || h[0].Source != SourceHub {
		t.Errorf("got %+v, want one record with source %q", h, SourceHub)
	}
}
