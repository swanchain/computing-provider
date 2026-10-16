package computing

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// recordingTransport fails every request and remembers where it was going,
// so a test can assert that nothing left the machine.
type recordingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.urls = append(rt.urls, r.URL.String()+" auth="+r.Header.Get("Authorization"))
	rt.mu.Unlock()
	return nil, errors.New("no network in tests")
}

// The CLIProxyAPI management key controls the proxy and its accounts. It was
// sent to any endpoint whose /metrics answered 404 — a third-party API among
// them — on the guess that it might be a proxy. It must never leave this
// machine, and a remote endpoint is not sampled at all.
func TestManagementKeyStaysOnThisMachine(t *testing.T) {
	const remote = "https://api.example.com"
	rt := &recordingTransport{}
	b := NewBackendUsageSampler(func() map[string]endpointInfo {
		return map[string]endpointInfo{remote: {Models: []string{"org/embed"}, APIKey: "model-key"}}
	})
	b.client = &http.Client{Transport: rt}
	b.managementKey = "mgmt-secret"

	b.sampleAll()
	if err := b.management(context.Background(), remote+"/v0/management/auth-files", &struct{}{}); err == nil {
		t.Error("management() accepted a remote URL")
	}

	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["org/embed"] = ModelMapping{Endpoint: remote, LocalOnly: true}
	s.modelMappings["org/hub"] = ModelMapping{Endpoint: remote + ":8443"}
	s.usageSampler = b
	if r := s.ProxyAccounts(context.Background()); len(r.Instances) != 0 {
		t.Errorf("remote endpoints listed as proxies: %+v", r.Instances)
	}

	if len(rt.urls) != 0 {
		t.Fatalf("requests left the machine: %v", rt.urls)
	}
	st := b.Status()
	if len(st) != 1 || st[0].Measured || !strings.Contains(st[0].Reason, "not on this machine") {
		t.Errorf("status = %+v, want the remote endpoint listed as not measured", st)
	}
}

func TestOnThisMachine(t *testing.T) {
	for ep, want := range map[string]bool{
		"http://127.0.0.1:8318":       true,
		"http://localhost:30000":      true,
		"http://[::1]:8080":           true,
		"https://api.example.org":     false,
		"http://10.0.0.5:8317":        false,
		"http://127.0.0.1.example.io": false,
		"not a url":                   false,
	} {
		if got := onThisMachine(ep); got != want {
			t.Errorf("onThisMachine(%q) = %v, want %v", ep, got, want)
		}
	}
}

// Only models registered with Swan Inference are health-checked. A local-only
// model is ready once configured; making it a hub model starts its checks,
// and making a hub model local-only stops them.
func TestLocalOnlyModelsAreNotHealthChecked(t *testing.T) {
	dir := t.TempDir()
	write := func(json string) {
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"org/private": {"endpoint": "http://127.0.0.1:1", "local_only": true},
	        "org/public":  {"endpoint": "http://127.0.0.1:2"}}`)
	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	r := NewModelRegistry(dir, h)
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	checked := func(id string) bool {
		h.mu.RLock()
		defer h.mu.RUnlock()
		_, ok := h.endpoints[id]
		return ok
	}
	if checked("org/private") {
		t.Error("a local-only model is health-checked")
	}
	if !checked("org/public") {
		t.Error("a hub model lost its health check")
	}
	if m, _ := r.GetModel("org/private"); m.State != ModelStateReady {
		t.Errorf("local-only model state = %s, want ready without a health check", m.State)
	}

	write(`{"org/private": {"endpoint": "http://127.0.0.1:1"},
	        "org/public":  {"endpoint": "http://127.0.0.1:2", "local_only": true}}`)
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	if !checked("org/private") || checked("org/public") {
		t.Errorf("after swapping local_only: private checked=%v public checked=%v, want true/false", checked("org/private"), checked("org/public"))
	}
	if m, _ := r.GetModel("org/private"); m.State != ModelStateLoading {
		t.Errorf("a model made public is %s, want loading until a check passes", m.State)
	}

	// A check already in flight when a model became local-only changes nothing.
	r.onHealthStatusChange("org/public", ModelHealthHealthy, ModelHealthUnhealthy)
	if m, _ := r.GetModel("org/public"); m.State != ModelStateReady {
		t.Errorf("a stale health result moved a local-only model to %s", m.State)
	}
}
