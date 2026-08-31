package computing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
	"time"
)

// deepCheckConfig probes on every check so a test does not have to tick ten
// times to exercise the path.
func deepCheckConfig() HealthCheckConfig {
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	c.DeepCheckTimeout = 2 * time.Second
	c.UnhealthyThreshold = 1
	return c
}

// backend serves a static /v1/models list and lets the test decide what
// completions do — the exact shape of the failure this issue is about.
func backend(t *testing.T, completions http.HandlerFunc) (*httptest.Server, *int64) {
	t.Helper()
	var calls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{"id": "m", "max_model_len": 4096}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		completions(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

// Issue #70 case 1: the vLLM engine is dead but the FastAPI process still
// serves its static model list. The cheap probe cannot see this.
func TestDeadEngineBehindLiveHTTPServerIsCaught(t *testing.T) {
	srv, calls := backend(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"EngineDeadError"}`, http.StatusInternalServerError)
	})

	h := NewModelHealthChecker(deepCheckConfig())
	h.RegisterModel("m", srv.URL, "", "", "text-generation")
	h.checkModel("m")

	if *calls == 0 {
		t.Fatal("engine was never probed")
	}
	st, _ := h.GetModelStatus("m")
	if st.Health == ModelHealthHealthy {
		t.Error("a dead engine behind a live /v1/models must not read healthy")
	}
	if st.LastDeepError == "" {
		t.Error("the engine failure should be recorded")
	}
}

// Issue #70 case 2: a proxy lists models fine but has no credentials, so every
// completion is a 503.
func TestProxyWithoutCredentialsIsCaught(t *testing.T) {
	srv, _ := backend(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"auth_unavailable: no auth available"}`, http.StatusServiceUnavailable)
	})

	h := NewModelHealthChecker(deepCheckConfig())
	h.RegisterModel("m", srv.URL, "", "", "text-generation")
	h.checkModel("m")

	st, _ := h.GetModelStatus("m")
	if st.Health == ModelHealthHealthy {
		t.Error("a backend returning 503 on every completion must not read healthy")
	}
	if st.LastDeepError == "" || !strings.Contains(st.LastDeepError, "engine error") {
		t.Errorf("failure should be described as an engine error, got %q", st.LastDeepError)
	}
}

func TestHealthyBackendStaysHealthy(t *testing.T) {
	srv, calls := backend(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]string{"content": "p"}}},
		})
	})

	h := NewModelHealthChecker(deepCheckConfig())
	h.RegisterModel("m", srv.URL, "", "", "text-generation")
	h.checkModel("m")

	if *calls != 1 {
		t.Fatalf("expected exactly one engine probe, got %d", *calls)
	}
	st, _ := h.GetModelStatus("m")
	if st.Health != ModelHealthHealthy {
		t.Errorf("health = %v, want healthy", st.Health)
	}
	if st.LastDeepSuccess.IsZero() {
		t.Error("a successful engine probe should be recorded")
	}
}

// A chat template that rejects "ping" is not a broken backend. Counting a 400
// against health would take a working model out of routing permanently — the
// "model-specific quirks" risk called out in the issue.
func TestClientSideRejectionDoesNotMarkUnhealthy(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		srv, _ := backend(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"template rejected the prompt"}`, code)
		})
		h := NewModelHealthChecker(deepCheckConfig())
		h.RegisterModel("m", srv.URL, "", "", "text-generation")
		h.checkModel("m")

		if st, _ := h.GetModelStatus("m"); st.Health != ModelHealthHealthy {
			t.Errorf("HTTP %d should not affect health, got %v", code, st.Health)
		}
	}
}

// Embedding and image models answer 404 or 400 at /v1/chat/completions.
// Probing them would report a permanent false failure.
func TestNonChatModelsAreNotProbed(t *testing.T) {
	srv, calls := backend(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("an embedding model must not be probed with a chat completion")
	})

	h := NewModelHealthChecker(deepCheckConfig())
	h.RegisterModel("e", srv.URL, "", "", "embeddings")
	h.checkAllModels()

	if *calls != 0 {
		t.Errorf("engine probed %d times, want 0", *calls)
	}
	if st, _ := h.GetModelStatus("e"); !st.DeepCheckSkipped || st.Health != ModelHealthHealthy {
		t.Errorf("skipped=%v health=%v, want skipped and healthy", st.DeepCheckSkipped, st.Health)
	}
}

// The whole point of the cadence knob is that the completion does not run on
// every tick.
func TestDeepCheckHonoursCadence(t *testing.T) {
	srv, calls := backend(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{}}})
	})

	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 5
	h := NewModelHealthChecker(c)
	h.RegisterModel("m", srv.URL, "", "", "")

	for i := 0; i < 10; i++ {
		h.checkAllModels()
	}
	// The first check always probes — a newly registered backend is the
	// likeliest to be misconfigured — then every 5th after it, so checks 1 and
	// 6 fire within a run of ten.
	if got := atomic.LoadInt64(calls); got != 2 {
		t.Errorf("engine probed %d times over 10 checks with DeepCheckEvery=5, want 2 (checks 1 and 6)", got)
	}
}

func TestDeepCheckCanBeDisabled(t *testing.T) {
	srv, calls := backend(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("probe ran while disabled")
	})
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 0
	h := NewModelHealthChecker(c)
	h.RegisterModel("m", srv.URL, "", "", "")
	for i := 0; i < 5; i++ {
		h.checkAllModels()
	}
	if *calls != 0 {
		t.Errorf("probed %d times, want 0", *calls)
	}
}

// Ollama knows the model by its own tag, not the marketplace ID; probing with
// the wrong name yields a 404 that looks like a dead backend.
func TestDeepProbeUsesTheBackendLocalName(t *testing.T) {
	got := make(chan string, 1)
	srv, _ := backend(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		got <- body.Model
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{}}})
	})

	h := NewModelHealthChecker(deepCheckConfig())
	h.RegisterModel("Qwen/Qwen2.5-7B-Instruct", srv.URL, "", "qwen2.5:7b", "")
	h.checkModel("Qwen/Qwen2.5-7B-Instruct")

	select {
	case m := <-got:
		if m != "qwen2.5:7b" {
			t.Errorf("probed model %q, want the backend-local name", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no probe arrived")
	}
}

// An unreachable backend and a broken engine both mean "cannot serve", but the
// operator fixes them differently, so the message must say which.
func TestFailureDescriptionsAreDistinct(t *testing.T) {
	cases := map[int]string{
		0:   "no response from backend",
		401: "rejected credentials",
		404: "not served at this endpoint",
		500: "engine error",
	}
	for code, want := range cases {
		got := describeProbeFailure(probeResultFor(code))
		if !strings.Contains(got, want) {
			t.Errorf("HTTP %d described as %q, want it to mention %q", code, got, want)
		}
	}
}

func probeResultFor(code int) selfcheck.ProbeResult {
	return selfcheck.ProbeResult{StatusCode: code, Error: "boom"}
}
