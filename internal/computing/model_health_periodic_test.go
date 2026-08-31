package computing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sharedProxy models the real shape that caused this bug: one server fronting
// several model names, which is what a CLIProxy or LiteLLM deployment is.
// It records every completion and the peak number in flight at once.
type sharedProxy struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []string
	inFlt  int
	peak   int
	status int
}

func newSharedProxy(t *testing.T, status int) *sharedProxy {
	t.Helper()
	p := &sharedProxy{status: status}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		p.mu.Lock()
		p.calls = append(p.calls, body.Model)
		p.inFlt++
		if p.inFlt > p.peak {
			p.peak = p.inFlt
		}
		p.mu.Unlock()

		time.Sleep(15 * time.Millisecond) // widen the window for overlap
		p.mu.Lock()
		p.inFlt--
		p.mu.Unlock()

		if p.status >= 300 {
			http.Error(w, `{"error":{"code":"server_is_overloaded"}}`, p.status)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{}}})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *sharedProxy) seen() ([]string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...), p.peak
}

// The regression this fixes: the engine probe was wired into checkModel, but
// the 30s loop calls checkAllModels, which never calls checkModel. The feature
// shipped and never ran. Any test for it must drive the periodic path.
func TestEngineProbeRunsFromThePeriodicPath(t *testing.T) {
	p := newSharedProxy(t, 200)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	h.RegisterModel("m", p.srv.URL, "", "", "text-generation")

	h.checkAllModels()

	if calls, _ := p.seen(); len(calls) == 0 {
		t.Fatal("checkAllModels never ran an engine probe — the loop does not reach it")
	}
}

// Six models behind one proxy must not produce six simultaneous completions.
// That burst is what earns "server_is_overloaded" from a metered upstream.
func TestSharedEndpointIsProbedOneModelAtATime(t *testing.T) {
	p := newSharedProxy(t, 200)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	models := []string{"openai/a", "openai/b", "openai/c", "openai/d", "openai/e", "openai/f"}
	for _, m := range models {
		h.RegisterModel(m, p.srv.URL, "", "", "text-generation")
	}

	h.checkAllModels()

	calls, peak := p.seen()
	if len(calls) != 1 {
		t.Errorf("one cycle produced %d completions against a shared proxy, want 1: %v", len(calls), calls)
	}
	if peak > 1 {
		t.Errorf("peak concurrent engine probes to one host = %d, want 1", peak)
	}
}

// Over successive cycles every model on a shared endpoint gets its turn, so
// none is permanently unchecked.
func TestSharedEndpointRotatesAcrossModels(t *testing.T) {
	p := newSharedProxy(t, 200)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	models := []string{"openai/a", "openai/b", "openai/c"}
	for _, m := range models {
		h.RegisterModel(m, p.srv.URL, "", "", "text-generation")
	}

	for i := 0; i < len(models); i++ {
		h.checkAllModels()
	}

	calls, _ := p.seen()
	seen := map[string]bool{}
	for _, c := range calls {
		seen[c] = true
	}
	if len(seen) != len(models) {
		t.Errorf("after %d cycles the probes covered %v, want all of %v", len(models), calls, models)
	}
}

// A dedicated single-model backend must keep the intended cadence — the
// rotation must not slow down the common case.
func TestDedicatedEndpointKeepsItsCadence(t *testing.T) {
	p := newSharedProxy(t, 200)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 3
	h := NewModelHealthChecker(c)
	h.RegisterModel("solo", p.srv.URL, "", "", "text-generation")

	for i := 0; i < 9; i++ {
		h.checkAllModels()
	}
	// First cycle always probes, then every 3rd: cycles 1, 4, 7.
	if calls, _ := p.seen(); len(calls) != 3 {
		t.Errorf("%d probes over 9 cycles at DeepCheckEvery=3, want 3: %v", len(calls), calls)
	}
}

// A failing engine probe must mark only the model actually probed, not every
// model that happens to share the endpoint.
func TestDeepFailureMarksOnlyTheProbedModel(t *testing.T) {
	p := newSharedProxy(t, http.StatusInternalServerError)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	c.UnhealthyThreshold = 1
	h := NewModelHealthChecker(c)
	for _, m := range []string{"openai/a", "openai/b", "openai/c"} {
		h.RegisterModel(m, p.srv.URL, "", "", "text-generation")
	}

	h.checkAllModels()

	calls, _ := p.seen()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one probe, got %v", calls)
	}
	probed := calls[0]
	unhealthy := 0
	for _, m := range []string{"openai/a", "openai/b", "openai/c"} {
		st, _ := h.GetModelStatus(m)
		if st.Health != ModelHealthHealthy {
			unhealthy++
			if m != probed {
				t.Errorf("%s was marked unhealthy but %s was the one probed", m, probed)
			}
		}
	}
	if unhealthy != 1 {
		t.Errorf("%d models marked unhealthy, want only the probed one", unhealthy)
	}
}

// Each model must get exactly one health result per cycle, or TotalChecks
// double-counts for whichever model was engine-probed.
func TestOneResultPerModelPerCycle(t *testing.T) {
	p := newSharedProxy(t, 200)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	for _, m := range []string{"openai/a", "openai/b"} {
		h.RegisterModel(m, p.srv.URL, "", "", "text-generation")
	}

	h.checkAllModels()
	h.checkAllModels()

	for _, m := range []string{"openai/a", "openai/b"} {
		st, _ := h.GetModelStatus(m)
		if st.TotalChecks != 2 {
			t.Errorf("%s TotalChecks = %d after 2 cycles, want 2", m, st.TotalChecks)
		}
	}
}

var _ = atomic.LoadInt64

// The deep-check maps are keyed by endpoint rather than model, so they must be
// dropped when the last model on an endpoint goes away — otherwise editing
// models.json over a long uptime leaks entries.
func TestEndpointBookkeepingIsReleased(t *testing.T) {
	p := newSharedProxy(t, 200)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	h.RegisterModel("a", p.srv.URL, "", "", "text-generation")
	h.RegisterModel("b", p.srv.URL, "", "", "text-generation")
	h.checkAllModels()

	h.UnregisterModel("a")
	h.mu.RLock()
	stillThere := len(h.sinceDeep)
	h.mu.RUnlock()
	if stillThere == 0 {
		t.Fatal("endpoint state dropped while another model still uses it")
	}

	h.UnregisterModel("b")
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.sinceDeep) != 0 || len(h.deepRotation) != 0 || len(h.deepStarted) != 0 {
		t.Errorf("endpoint state leaked: sinceDeep=%d rotation=%d started=%d",
			len(h.sinceDeep), len(h.deepRotation), len(h.deepStarted))
	}
}

// The engine probe must not only detect a dead engine but take it out of
// routing. It previously did the first and not the second: the deep failure fed
// into ConsecutiveFails, which the passing cheap probes between deep probes
// reset, so the model stayed healthy indefinitely.
func TestDeadEngineEventuallyGoesUnhealthy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"EngineDeadError"}`, 500)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	h.RegisterModel("m", srv.URL, "", "", "text-generation")
	for i := 0; i < 60; i++ {
		h.checkAllModels()
	}
	st, _ := h.GetModelStatus("m")
	t.Logf("after 60 cycles: health=%v deepErr=%q routable=%v", st.Health, st.LastDeepError, h.IsModelHealthy("m"))
	if h.IsModelHealthy("m") {
		t.Error("a dead engine is still taking routed traffic")
	}
}

func TestEngineRecoveryRestoresHealth(t *testing.T) {
	dead := true
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if dead {
			http.Error(w, `{"error":"EngineDeadError"}`, 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	h.RegisterModel("m", srv.URL, "", "", "text-generation")
	for i := 0; i < 40; i++ {
		h.checkAllModels()
	}
	if h.IsModelHealthy("m") {
		t.Fatal("should be unhealthy while the engine is dead")
	}
	dead = false
	for i := 0; i < 20; i++ {
		h.checkAllModels()
	}
	if !h.IsModelHealthy("m") {
		t.Error("engine recovered but the model is still held unhealthy")
	}
}

// ForceCheck must not disturb the endpoint's rotation. It is keyed by endpoint,
// so asking it about one model pinned a shared proxy to its first model and
// starved the rest.
func TestForceCheckDoesNotStarveOtherModelsOnTheEndpoint(t *testing.T) {
	p := newSharedProxy(t, 200)
	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	for _, m := range []string{"openai/a", "openai/b", "openai/c"} {
		h.RegisterModel(m, p.srv.URL, "", "", "text-generation")
	}

	for i := 0; i < 3; i++ {
		h.checkModel("openai/a") // as ForceCheck does
		h.checkAllModels()
	}

	calls, _ := p.seen()
	seen := map[string]bool{}
	for _, c := range calls {
		seen[c] = true
	}
	if len(seen) < 3 {
		t.Errorf("forcing one model starved the others; probes covered %v", calls)
	}
}

// Each model must be probed with its own credentials. The endpoint group's key
// comes from whichever model won map iteration, so a proxy with per-model keys
// would authenticate as the wrong one and 401 a working model.
func TestDeepProbeUsesTheModelsOwnApiKey(t *testing.T) {
	got := make(chan string, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Model string `json:"model"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		got <- b.Model + "|" + r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := DefaultHealthCheckConfig()
	c.DeepCheckEvery = 1
	h := NewModelHealthChecker(c)
	h.RegisterModel("model-a", srv.URL, "key-a", "", "text-generation")
	h.RegisterModel("model-b", srv.URL, "key-b", "", "text-generation")

	for i := 0; i < 2; i++ {
		h.checkAllModels()
	}
	close(got)
	for v := range got {
		parts := strings.SplitN(v, "|", 2)
		want := "Bearer key-" + strings.TrimPrefix(parts[0], "model-")
		if parts[1] != want {
			t.Errorf("%s probed with %q, want %q", parts[0], parts[1], want)
		}
	}
}
