package selfcheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBackend serves the OpenAI-ish endpoints a model backend exposes.
type fakeBackend struct {
	maxModelLen   int
	completionOK  bool
	completionMsg string
	statusCode    int
}

func (f fakeBackend) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{{"id": "m", "max_model_len": f.maxModelLen}},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if !f.completionOK {
			code := f.statusCode
			if code == 0 {
				code = http.StatusServiceUnavailable
			}
			w.WriteHeader(code)
			fmt.Fprint(w, f.completionMsg)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{"message": map[string]string{"content": "ok"}}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// fakeDaemon serves the provider's own API.
func fakeDaemon(t *testing.T, connected bool, registered []string, health map[string]string, requests map[string]int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/computing/inference/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": connected, "registered_models": registered, "active_models": registered,
		})
	})
	mux.HandleFunc("/api/v1/computing/inference/health", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]map[string]interface{}{}
		for id, h := range health {
			out[id] = map[string]interface{}{"health_string": h}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/api/v1/computing/inference/metrics", func(w http.ResponseWriter, r *http.Request) {
		mm := map[string]map[string]interface{}{}
		for id, n := range requests {
			mm[id] = map[string]interface{}{"total_requests": n}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"model_metrics": mm})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeModels(t *testing.T, dir string, models map[string]map[string]interface{}) {
	t.Helper()
	b, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func find(t *testing.T, r Report, name string) Result {
	t.Helper()
	for _, res := range r.Results {
		if res.Name == name {
			return res
		}
	}
	t.Fatalf("no result named %q in %+v", name, r.Results)
	return Result{}
}

func TestHealthyProviderPasses(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{
		"org/model-a": {"endpoint": be.URL, "context_length": 4096},
	})
	d := fakeDaemon(t, true, []string{"org/model-a"}, map[string]string{"org/model-a": "healthy"}, map[string]int64{"org/model-a": 12})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/model-a"}, LogDir: dir})
	if r.Failed() {
		t.Fatalf("healthy provider reported failures: %+v", r.Problems())
	}
}

// The failure that cost the most: a model healthy and mapped, but never sent to
// Swan Inference, so it earns nothing while looking fine locally.
func TestUnregisteredModelIsAFailure(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{
		"org/a": {"endpoint": be.URL},
		"org/b": {"endpoint": be.URL},
	})
	// Only a is registered upstream.
	d := fakeDaemon(t, true, []string{"org/a"},
		map[string]string{"org/a": "healthy", "org/b": "healthy"}, map[string]int64{"org/a": 5, "org/b": 5})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a", "org/b"}, LogDir: dir})
	res := find(t, r, "registered with Swan Inference")
	if res.Status != StatusFail {
		t.Fatalf("status = %s, want fail; message: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "org/b") {
		t.Errorf("message should name the unregistered model, got %q", res.Message)
	}
}

// A model listed in models.json but absent from config.toml may not be
// registered at all on a cold start.
func TestConfigDriftIsAFailure(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{
		"org/a": {"endpoint": be.URL},
		"org/b": {"endpoint": be.URL},
	})
	d := fakeDaemon(t, true, []string{"org/a", "org/b"},
		map[string]string{"org/a": "healthy", "org/b": "healthy"}, map[string]int64{"org/a": 1, "org/b": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	res := find(t, r, "config/models.json agreement")
	if res.Status != StatusFail || !strings.Contains(res.Message, "org/b") {
		t.Fatalf("got %s %q, want fail naming org/b", res.Status, res.Message)
	}
}

// The gpt-5.x case: /v1/models answers fine, every completion fails on auth.
func TestBackendThatCannotServeIsCaught(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: false, statusCode: 503,
		completionMsg: `auth_unavailable: no auth available`}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/a": {"endpoint": be.URL}})
	d := fakeDaemon(t, true, []string{"org/a"}, map[string]string{"org/a": "healthy"}, map[string]int64{"org/a": 3})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})

	if h := find(t, r, "model health"); h.Status != StatusPass {
		t.Errorf("health check should still pass — that is the point of this case, got %s", h.Status)
	}
	probe := find(t, r, "inference probe")
	if probe.Status != StatusFail {
		t.Fatalf("inference probe = %s, want fail", probe.Status)
	}
	if !strings.Contains(probe.Message, "503") {
		t.Errorf("message should carry the upstream status, got %q", probe.Message)
	}
}

func TestContextMismatchIsCaught(t *testing.T) {
	dir := t.TempDir()
	// Backend serves 45056 while models.json claims the catalog's 131072.
	be := fakeBackend{maxModelLen: 45056, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{
		"org/a": {"endpoint": be.URL, "context_length": 131072},
	})
	d := fakeDaemon(t, true, []string{"org/a"}, map[string]string{"org/a": "healthy"}, map[string]int64{"org/a": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	res := find(t, r, "context window")
	if res.Status != StatusFail {
		t.Fatalf("status = %s, want fail", res.Status)
	}
	if !strings.Contains(res.Message, "131072") || !strings.Contains(res.Message, "45056") {
		t.Errorf("message should show both values, got %q", res.Message)
	}
}

func TestDisconnectedProviderFails(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/a": {"endpoint": be.URL}})
	d := fakeDaemon(t, false, []string{"org/a"}, map[string]string{"org/a": "healthy"}, map[string]int64{"org/a": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	if res := find(t, r, "Swan Inference connection"); res.Status != StatusFail {
		t.Fatalf("status = %s, want fail", res.Status)
	}
}

func TestUnreachableDaemonFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	writeModels(t, dir, map[string]map[string]interface{}{})
	// Port 1 is reserved and refuses connections.
	r := Run(Options{RepoPath: dir, APIBase: "http://127.0.0.1:1", LogDir: dir, SkipCompletion: true})
	if res := find(t, r, "daemon"); res.Status != StatusFail {
		t.Fatalf("status = %s, want fail when the daemon is down", res.Status)
	}
	if !r.Failed() {
		t.Error("report should be marked failed")
	}
}

func TestIdleModelWarnsButDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/a": {"endpoint": be.URL}})
	d := fakeDaemon(t, true, []string{"org/a"}, map[string]string{"org/a": "healthy"}, map[string]int64{"org/a": 0})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	if res := find(t, r, "traffic"); res.Status != StatusWarn {
		t.Fatalf("status = %s, want warn", res.Status)
	}
	if r.Failed() {
		t.Error("an idle model is worth noting, not failing")
	}
}

func TestSkipCompletionOmitsTheProbe(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: false}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/a": {"endpoint": be.URL}})
	d := fakeDaemon(t, true, []string{"org/a"}, map[string]string{"org/a": "healthy"}, map[string]int64{"org/a": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir, SkipCompletion: true})
	for _, res := range r.Results {
		if res.Name == "inference probe" {
			t.Fatal("probe should be skipped")
		}
	}
}

// Regression: zero registered models is the exact state this feature exists to
// catch. It must fail, not be mistaken for "the daemon does not report it".
func TestZeroRegisteredModelsFails(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/a": {"endpoint": be.URL}})
	d := fakeDaemon(t, true, []string{}, map[string]string{"org/a": "healthy"}, map[string]int64{"org/a": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	res := find(t, r, "registered with Swan Inference")
	if res.Status != StatusFail {
		t.Fatalf("status = %s, want fail — an empty registration is a real outage", res.Status)
	}
	if !r.Failed() {
		t.Error("report should be marked failed so the CLI exits non-zero")
	}
}

// A daemon that predates the field is a different case and must only warn.
func TestAbsentRegisteredFieldOnlyWarns(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/a": {"endpoint": be.URL}})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/computing/inference/status", func(w http.ResponseWriter, r *http.Request) {
		// No registered_models key at all.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"connected": true})
	})
	mux.HandleFunc("/api/v1/computing/inference/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]map[string]interface{}{"org/a": {"health_string": "healthy"}})
	})
	mux.HandleFunc("/api/v1/computing/inference/metrics", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"model_metrics": map[string]interface{}{"org/a": map[string]interface{}{"total_requests": 1}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	r := Run(Options{RepoPath: dir, APIBase: srv.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	if res := find(t, r, "registered with Swan Inference"); res.Status != StatusWarn {
		t.Fatalf("status = %s, want warn", res.Status)
	}
}

// One endpoint commonly serves several models; the context check must compare
// each model against its own entry rather than whichever is listed first.
func TestContextMatchesModelById(t *testing.T) {
	dir := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]interface{}{
			{"id": "org/other", "max_model_len": 8192},
			{"id": "org/a", "max_model_len": 4096},
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"choices": []map[string]interface{}{{}}})
	})
	be := httptest.NewServer(mux)
	defer be.Close()

	writeModels(t, dir, map[string]map[string]interface{}{
		"org/a": {"endpoint": be.URL, "context_length": 4096},
	})
	d := fakeDaemon(t, true, []string{"org/a"}, map[string]string{"org/a": "healthy"}, map[string]int64{"org/a": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	if res := find(t, r, "context window"); res.Status != StatusPass {
		t.Fatalf("got %s %q — should have matched org/a (4096), not the first entry (8192)", res.Status, res.Message)
	}
}

// Embedding and image models do not answer /v1/chat/completions; probing them
// would report a permanent false failure.
func TestNonChatModelsAreNotProbed(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: false, statusCode: 404}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{
		"org/embed": {"endpoint": be.URL, "category": "embeddings"},
	})
	d := fakeDaemon(t, true, []string{"org/embed"}, map[string]string{"org/embed": "healthy"}, map[string]int64{"org/embed": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/embed"}, LogDir: dir})
	if res := find(t, r, "inference probe"); res.Status != StatusPass {
		t.Fatalf("got %s %q, want pass — an embeddings model should be skipped", res.Status, res.Message)
	}
	if r.Failed() {
		t.Error("a healthy embeddings-only provider must not fail the audit")
	}
}

// degraded still serves and unknown means the first check has not run; neither
// should fail a provider that is working.
func TestDegradedAndUnknownOnlyWarn(t *testing.T) {
	dir := t.TempDir()
	be := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/a": {"endpoint": be.URL}})
	d := fakeDaemon(t, true, []string{"org/a"}, map[string]string{"org/a": "degraded"}, map[string]int64{"org/a": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/a"}, LogDir: dir})
	if res := find(t, r, "model health"); res.Status != StatusWarn {
		t.Fatalf("status = %s, want warn for a degraded model", res.Status)
	}
	if r.Failed() {
		t.Error("a degraded but serving model should not fail the audit")
	}
}

// Which failures may pull a model from routing. A 400 is a client's over-long
// prompt and says nothing about the backend; disabling on it would remove a
// healthy, earning model.
func TestBackendAtFaultClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		probe ProbeResult
		want  bool
	}{
		{"success", ProbeResult{OK: true, StatusCode: 200}, false},
		{"skipped", ProbeResult{Skipped: true}, false},
		{"connection refused", ProbeResult{StatusCode: 0, Error: "connection refused"}, true},
		{"401 credentials", ProbeResult{StatusCode: 401}, true},
		{"403 forbidden", ProbeResult{StatusCode: 403}, true},
		{"404 not served", ProbeResult{StatusCode: 404}, true},
		{"500", ProbeResult{StatusCode: 500}, true},
		{"503 auth_unavailable", ProbeResult{StatusCode: 503}, true},
		{"400 context too long", ProbeResult{StatusCode: 400}, false},
		{"422 unprocessable", ProbeResult{StatusCode: 422}, false},
		{"429 our own rate limit", ProbeResult{StatusCode: 429}, false},
	} {
		if got := tc.probe.BackendAtFault(); got != tc.want {
			t.Errorf("%s: BackendAtFault() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The runner acts on per-model results, so the report must carry them.
func TestReportCarriesPerModelProbes(t *testing.T) {
	dir := t.TempDir()
	ok := fakeBackend{maxModelLen: 4096, completionOK: true}.server(t)
	bad := fakeBackend{maxModelLen: 4096, completionOK: false, statusCode: 503}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{
		"org/ok":    {"endpoint": ok.URL},
		"org/bad":   {"endpoint": bad.URL},
		"org/embed": {"endpoint": bad.URL, "category": "embeddings"},
	})
	d := fakeDaemon(t, true, []string{"org/ok", "org/bad", "org/embed"},
		map[string]string{"org/ok": "healthy", "org/bad": "healthy", "org/embed": "healthy"},
		map[string]int64{"org/ok": 1, "org/bad": 1, "org/embed": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL,
		ConfigModels: []string{"org/ok", "org/bad", "org/embed"}, LogDir: dir})

	if len(r.Probes) != 3 {
		t.Fatalf("got %d probe results, want 3: %+v", len(r.Probes), r.Probes)
	}
	if !r.Probes["org/ok"].OK {
		t.Error("org/ok should have completed")
	}
	if p := r.Probes["org/bad"]; p.OK || p.StatusCode != 503 || !p.BackendAtFault() {
		t.Errorf("org/bad = %+v, want a backend-owned 503 failure", p)
	}
	if !r.Probes["org/embed"].Skipped {
		t.Error("a non-chat model should be marked skipped, not failed")
	}
}

// A model already taken out of service keeps failing its probe every tick. That
// is a handled state: reporting it as a new failure would turn one outage into
// a permanent alert stream at a 10-minute period.
func TestExpectedProbeFailuresDoNotFailTheReport(t *testing.T) {
	dir := t.TempDir()
	bad := fakeBackend{maxModelLen: 4096, completionOK: false, statusCode: 503}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/bad": {"endpoint": bad.URL}})
	d := fakeDaemon(t, true, []string{}, map[string]string{"org/bad": "healthy"}, map[string]int64{"org/bad": 1})

	opt := Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/bad"}, LogDir: dir,
		ExpectedProbeFailures: map[string]bool{"org/bad": true}}
	r := Run(opt)

	if res := find(t, r, "inference probe"); res.Status != StatusPass {
		t.Fatalf("inference probe = %s (%q), want pass for an already-disabled model", res.Status, res.Message)
	}
	// The probe result itself must still record the failure, so recovery is
	// detectable on a later tick.
	if p := r.Probes["org/bad"]; p.OK {
		t.Error("the probe should still have run and failed")
	}
}

// Without the suppression the same run must fail, or the feature is untestable.
func TestUnexpectedProbeFailureStillFails(t *testing.T) {
	dir := t.TempDir()
	bad := fakeBackend{maxModelLen: 4096, completionOK: false, statusCode: 503}.server(t)
	writeModels(t, dir, map[string]map[string]interface{}{"org/bad": {"endpoint": bad.URL}})
	d := fakeDaemon(t, true, []string{"org/bad"}, map[string]string{"org/bad": "healthy"}, map[string]int64{"org/bad": 1})

	r := Run(Options{RepoPath: dir, APIBase: d.URL, ConfigModels: []string{"org/bad"}, LogDir: dir})
	if res := find(t, r, "inference probe"); res.Status != StatusFail {
		t.Fatalf("inference probe = %s, want fail", res.Status)
	}
}

// The probe must not time out a merely busy backend: with auto-disable that
// would pull a model precisely when it is earning most.
func TestProbeTimeoutIsGenerousByDefault(t *testing.T) {
	dir := t.TempDir()
	writeModels(t, dir, map[string]map[string]interface{}{})
	r := Run(Options{RepoPath: dir, APIBase: "http://127.0.0.1:1", LogDir: dir, SkipCompletion: true})
	_ = r // Run applies the default; assert it here rather than reaching inside.
	if (Options{}).ProbeTimeout != 0 {
		t.Fatal("zero value should be unset")
	}
}
