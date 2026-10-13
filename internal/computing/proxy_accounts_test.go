package computing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Each proxy instance is reported with its role for this node and its
// accounts: plan, usage windows, cooldowns and the stored token's expiry.
// A model server answering 404 is not a proxy and is left out.
func TestProxyAccountsReport(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "codex-a@example.com-plus.json")
	if err := os.WriteFile(cred, []byte(`{"email":"a@example.com","type":"codex","expired":"2026-10-14T23:44:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files" || r.Header.Get("Authorization") != "Bearer mk" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"files":[{"account":"a@example.com","provider":"codex","status":"active","path":%q,
			"success":7,"failed":1,
			"quota":{"observed_at":"2026-10-11T17:40:00Z","signals":{"X-Codex-Plan-Type":"plus",
				"X-Codex-Primary-Window-Minutes":"300","X-Codex-Primary-Used-Percent":"12","X-Codex-Primary-Reset-At":"1791745747",
				"X-Codex-Secondary-Window-Minutes":"10080","X-Codex-Secondary-Used-Percent":"33",
				"X-Codex-Secondary-Reset-At":"1792296247"}},
			"cooldowns":[{"model_key":"gpt-5.6-sol","reason":"usage_limit_reached","retry_at":"2026-10-11T19:00:00Z"}]}]}`, cred)
	}))
	defer proxy.Close()
	llama := httptest.NewServer(http.NotFoundHandler())
	defer llama.Close()

	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["openai/sol"] = ModelMapping{Endpoint: proxy.URL, LocalEndpoint: "http://127.0.0.1:1"}
	s.modelMappings["org/llama"] = ModelMapping{Endpoint: llama.URL}
	s.modelMappings["anthropic/private"] = ModelMapping{Endpoint: "http://127.0.0.1:1", LocalEndpoint: "http://127.0.0.1:1", LocalOnly: true}
	s.usageSampler = NewBackendUsageSampler(nil)
	s.usageSampler.queueStart["http://127.0.0.1:1"] = time.Now() // read as a proxy before it went down

	if r := s.ProxyAccounts(context.Background()); len(r.Instances) != 0 || r.Note == "" {
		t.Fatalf("without a management key: %+v, want a note and nothing else", r)
	}
	s.usageSampler.managementKey = "mk"
	r := s.ProxyAccounts(context.Background())

	var ext *ProxyInstance
	for i := range r.Instances {
		switch r.Instances[i].Endpoint {
		case proxy.URL:
			ext = &r.Instances[i]
		case llama.URL:
			t.Errorf("a model server answering 404 was reported as a proxy")
		case "http://127.0.0.1:1":
			if r.Instances[i].Error == "" || len(r.Instances[i].Roles) != 1 || r.Instances[i].Roles[0] != "local" {
				t.Errorf("an unreachable local proxy should be listed with its error: %+v", r.Instances[i])
			}
		}
	}
	if ext == nil || len(ext.Accounts) != 1 {
		t.Fatalf("proxy instance missing: %+v", r)
	}
	if len(ext.Roles) != 1 || ext.Roles[0] != "hub" {
		t.Errorf("roles = %v, want hub", ext.Roles)
	}
	a := ext.Accounts[0]
	if a.Account != "a@example.com" || a.Plan != "plus" || a.Success != 7 || a.Failed != 1 {
		t.Errorf("account = %+v", a)
	}
	if len(a.Windows) != 2 || a.Windows[0].Label != "5h" || a.Windows[0].UsedPercent != 12 ||
		a.Windows[1].Label != "weekly" || a.Windows[1].UsedPercent != 33 || a.Windows[1].ResetAt == nil {
		t.Errorf("windows = %+v", a.Windows)
	}
	if len(a.Cooldowns) != 1 || a.Cooldowns[0].Model != "gpt-5.6-sol" {
		t.Errorf("cooldowns = %+v", a.Cooldowns)
	}
	if a.Expires == nil || !a.Expires.Equal(time.Date(2026, 10, 14, 23, 44, 0, 0, time.UTC)) {
		t.Errorf("expires = %v, want the credential file's", a.Expires)
	}
}

// A Claude login's file name carries an ID and the account, not a plan; that
// must not be shown as one.
func TestProxyAccountsNoPlanFromAccountName(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "claude-bce0ac99-b@example.com.json")
	if err := os.WriteFile(cred, []byte(`{"email":"b@example.com","type":"claude","expired":"2026-10-12T01:53:34Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"files":[{"account":"b@example.com","provider":"claude","status":"active","path":%q,"quota":{"signals":{}}}]}`, cred)
	}))
	defer proxy.Close()
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["anthropic/m"] = ModelMapping{Endpoint: proxy.URL}
	s.usageSampler = NewBackendUsageSampler(nil)
	s.usageSampler.managementKey = "mk"
	r := s.ProxyAccounts(context.Background())
	if len(r.Instances) != 1 || len(r.Instances[0].Accounts) != 1 {
		t.Fatalf("report = %+v", r)
	}
	if p := r.Instances[0].Accounts[0].Plan; p != "" {
		t.Errorf("plan = %q, want none rather than the file name", p)
	}
}

func TestSummarizeProxyTokens24hByModel(t *testing.T) {
	endpoint := "http://proxy_one:8318"
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	rows := []BackendUsageSample{
		{Endpoint: perModelKey(endpoint, "openai/sol"), Time: start, Prompt: 100, Generated: 10},
		{Endpoint: perModelKey(endpoint, "openai/sol"), Time: start.Add(time.Minute), Prompt: 150, Generated: 15},
		// A reset contributes the new counter value, not a negative delta.
		{Endpoint: perModelKey(endpoint, "openai/sol"), Time: start.Add(2 * time.Minute), Prompt: 4, Generated: 1},
		{Endpoint: perModelKey(endpoint, "anthropic/haiku"), Time: start, Prompt: 0, Generated: 0},
		{Endpoint: perModelKey(endpoint, "anthropic/haiku"), Time: start.Add(time.Minute), Prompt: 20, Generated: 3},
		{Endpoint: perModelKey(endpoint, "anthropic/opus"), Time: start, Prompt: 7, Generated: 2},
		// The SQL LIKE query can match a different endpoint when a URL has an underscore.
		{Endpoint: perModelKey("http://proxyXone:8318", "wrong"), Time: start, Prompt: 1000, Generated: 1000},
		{Endpoint: perModelKey("http://proxyXone:8318", "wrong"), Time: start.Add(time.Minute), Prompt: 2000, Generated: 2000},
	}
	in, out, measured, models := summarizeProxyTokens24h(endpoint, rows, nil)
	if !measured || in != 74 || out != 9 {
		t.Fatalf("totals = %d/%d measured=%v, want 74/9 measured", in, out, measured)
	}
	if len(models) != 3 || models[0].Model != "openai/sol" || models[0].TokensIn24h != 54 || models[0].TokensOut24h != 6 ||
		models[1].Model != "anthropic/haiku" || models[1].TokensIn24h != 20 || models[1].TokensOut24h != 3 ||
		models[2].Model != "anthropic/opus" || models[2].Measured24h {
		t.Fatalf("per-model usage = %+v", models)
	}
}

func TestSummarizeProxyTokens24hMergesServedNames(t *testing.T) {
	endpoint := "http://127.0.0.1:8318"
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	rows := []BackendUsageSample{
		// Recorded under the served name before the model was configured...
		{Endpoint: perModelKey(endpoint, "claude-haiku"), Time: start, Prompt: 0, Generated: 0},
		{Endpoint: perModelKey(endpoint, "claude-haiku"), Time: start.Add(time.Minute), Prompt: 30, Generated: 4},
		// ...and under the model ID afterwards: two counters, one model.
		{Endpoint: perModelKey(endpoint, "anthropic/claude-haiku"), Time: start.Add(2 * time.Minute), Prompt: 0, Generated: 0},
		{Endpoint: perModelKey(endpoint, "anthropic/claude-haiku"), Time: start.Add(3 * time.Minute), Prompt: 5, Generated: 1},
	}
	names := map[string]string{"anthropic/claude-haiku": "anthropic/claude-haiku", "claude-haiku": "anthropic/claude-haiku"}
	in, out, _, models := summarizeProxyTokens24h(endpoint, rows, names)
	if len(models) != 1 || models[0].Model != "anthropic/claude-haiku" || models[0].TokensIn24h != 35 || models[0].TokensOut24h != 5 || in != 35 || out != 5 {
		t.Fatalf("models = %+v, totals %d/%d; want one anthropic/claude-haiku row with 35/5", models, in, out)
	}
}

// A disabled model's server is usually stopped. Unreachable, it cannot be told
// apart from a proxy that is down, so it must not be listed as one.
func TestProxyAccountsSkipsDisabledModels(t *testing.T) {
	s := NewInferenceService("test-node", t.TempDir())
	s.registry = NewModelRegistry("", nil)
	s.registry.models["org/stopped"] = &RegisteredModel{ID: "org/stopped", Endpoint: "http://127.0.0.1:1", Enabled: false}
	s.registry.models["org/down"] = &RegisteredModel{ID: "org/down", Endpoint: "http://127.0.0.1:2", Enabled: true}
	s.modelMappings["org/stopped"] = ModelMapping{Endpoint: "http://127.0.0.1:1"}
	s.modelMappings["org/down"] = ModelMapping{Endpoint: "http://127.0.0.1:2"}
	s.usageSampler = NewBackendUsageSampler(nil)
	s.usageSampler.managementKey = "mk"
	s.usageSampler.queueStart["http://127.0.0.1:2"] = time.Now()

	r := s.ProxyAccounts(context.Background())
	var seen []string
	for _, inst := range r.Instances {
		seen = append(seen, inst.Endpoint)
	}
	if len(seen) != 1 || seen[0] != "http://127.0.0.1:2" {
		t.Fatalf("instances %v, want only the enabled model's endpoint (a proxy that is down still shows)", seen)
	}
}

// An enabled model whose server was stopped or moved is unreachable too. Only
// an endpoint already seen answering as a proxy is listed when unreachable;
// anything else would be shown as an empty proxy.
func TestProxyAccountsSkipsUnreachableEndpointsNeverSeenAsProxies(t *testing.T) {
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["org/moved"] = ModelMapping{Endpoint: "http://127.0.0.1:3"}
	s.modelMappings["openai/down"] = ModelMapping{Endpoint: "http://127.0.0.1:4"}
	s.usageSampler = NewBackendUsageSampler(nil)
	s.usageSampler.managementKey = "mk"
	s.usageSampler.queueStart["http://127.0.0.1:4"] = time.Now()

	r := s.ProxyAccounts(context.Background())
	var seen []string
	for _, inst := range r.Instances {
		seen = append(seen, inst.Endpoint)
	}
	if len(seen) != 1 || seen[0] != "http://127.0.0.1:4" {
		t.Fatalf("instances %v, want only the endpoint known to be a proxy", seen)
	}
}
