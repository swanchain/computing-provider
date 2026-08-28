// Package selfcheck audits a running provider for the problems that produce no
// error anywhere: drift between what the provider claims and what it can
// actually do.
//
// Health checks answer "is the backend responding". These checks answer a
// different question — "is this node actually able to earn". A model can be
// healthy and still be missing from the set registered with Swan Inference, or
// advertise a context window twice what it will accept, or answer /v1/models
// while every completion fails on expired credentials. None of that surfaces as
// a failure until a customer request is lost.
package selfcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Status is the outcome of a single check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Result is one audited condition.
type Result struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"` // What to do about it
}

// Report is the outcome of a full run.
type Report struct {
	StartedAt time.Time `json:"started_at"`
	Duration  string    `json:"duration"`
	Results   []Result  `json:"results"`
}

// Failed reports whether any check failed.
func (r Report) Failed() bool {
	for _, res := range r.Results {
		if res.Status == StatusFail {
			return true
		}
	}
	return false
}

// Problems returns only the results that need attention.
func (r Report) Problems() []Result {
	var out []Result
	for _, res := range r.Results {
		if res.Status != StatusPass {
			out = append(out, res)
		}
	}
	return out
}

// Summary renders a one-line count, for an alert message.
func (r Report) Summary() string {
	var pass, warn, fail int
	for _, res := range r.Results {
		switch res.Status {
		case StatusPass:
			pass++
		case StatusWarn:
			warn++
		case StatusFail:
			fail++
		}
	}
	return fmt.Sprintf("%d passed, %d warnings, %d failures", pass, warn, fail)
}

// Options configures a run.
type Options struct {
	RepoPath     string   // $CP_PATH
	APIBase      string   // e.g. http://localhost:8085
	ConfigModels []string // [Inference] Models from config.toml
	LogDir       string   // [Log] Dir
	// MinFreeGB is the free-space floor below which a volume warns.
	MinFreeGB float64
	// SkipCompletion omits the real-inference probe, which costs one token per
	// model. Useful in tests and on metered backends.
	SkipCompletion bool
	HTTPTimeout    time.Duration
}

// modelEntry mirrors the parts of models.json this audit needs.
type modelEntry struct {
	Endpoint      string `json:"endpoint"`
	APIKey        string `json:"api_key"`
	LocalModel    string `json:"local_model"`
	Category      string `json:"category"`
	ContextLength int    `json:"context_length"`
}

// servedName is the name the backend knows the model by, which differs from the
// marketplace ID for engines like Ollama.
func (m modelEntry) servedName(id string) string {
	if m.LocalModel != "" {
		return m.LocalModel
	}
	return id
}

// chatCompatible reports whether /v1/chat/completions is the right endpoint.
// models.json also carries image-generation, embeddings and audio models, which
// answer 404 or 400 there — probing them would report a permanent false failure.
func (m modelEntry) chatCompatible() bool {
	switch strings.ToLower(strings.TrimSpace(m.Category)) {
	case "", "text-generation", "llm", "chat":
		return true
	default:
		return false
	}
}

type checker struct {
	opt    Options
	client *http.Client
	res    []Result
}

func (c *checker) add(name string, status Status, msg, hint string) {
	c.res = append(c.res, Result{Name: name, Status: status, Message: msg, Hint: hint})
}

// Run executes every check and returns a report. It never returns an error:
// a check that cannot run reports itself as a failure so it stays visible.
func Run(opt Options) Report {
	if opt.HTTPTimeout == 0 {
		opt.HTTPTimeout = 15 * time.Second
	}
	if opt.MinFreeGB == 0 {
		opt.MinFreeGB = 10
	}
	c := &checker{opt: opt, client: &http.Client{Timeout: opt.HTTPTimeout}}
	start := time.Now()

	models := c.checkModelsConfig()
	status := c.checkDaemon()
	c.checkRegistration(models, status)
	c.checkHealth()
	c.checkContexts(models)
	if !opt.SkipCompletion {
		c.checkCompletions(models)
	}
	c.checkTraffic()
	c.checkDisk()

	return Report{StartedAt: start, Duration: time.Since(start).Round(time.Millisecond).String(), Results: c.res}
}

// checkModelsConfig compares models.json against config.toml's Models list.
// A model missing from either side is served but never advertised, or
// advertised but not served.
func (c *checker) checkModelsConfig() map[string]modelEntry {
	path := filepath.Join(c.opt.RepoPath, "models.json")
	data, err := os.ReadFile(path)
	if err != nil {
		c.add("models.json", StatusFail, fmt.Sprintf("cannot read %s: %v", path, err),
			"Create models.json mapping each model ID to its backend endpoint.")
		return nil
	}
	var models map[string]modelEntry
	if err := json.Unmarshal(data, &models); err != nil {
		c.add("models.json", StatusFail, fmt.Sprintf("invalid JSON in %s: %v", path, err), "Fix the syntax and the provider will hot-reload it.")
		return nil
	}
	c.add("models.json", StatusPass, fmt.Sprintf("%d models mapped", len(models)), "")

	cfg := make(map[string]bool, len(c.opt.ConfigModels))
	for _, m := range c.opt.ConfigModels {
		cfg[m] = true
	}
	var missingFromConfig, missingFromJSON []string
	for id := range models {
		if !cfg[id] {
			missingFromConfig = append(missingFromConfig, id)
		}
	}
	for id := range cfg {
		if _, ok := models[id]; !ok {
			missingFromJSON = append(missingFromJSON, id)
		}
	}
	sort.Strings(missingFromConfig)
	sort.Strings(missingFromJSON)

	switch {
	case len(missingFromConfig) > 0 && len(missingFromJSON) > 0:
		c.add("config/models.json agreement", StatusFail,
			fmt.Sprintf("in models.json but not config.toml: %s; in config.toml but not models.json: %s",
				strings.Join(missingFromConfig, ", "), strings.Join(missingFromJSON, ", ")),
			"Make [Inference] Models list exactly the keys of models.json.")
	case len(missingFromConfig) > 0:
		c.add("config/models.json agreement", StatusFail,
			fmt.Sprintf("served but not listed in config.toml: %s", strings.Join(missingFromConfig, ", ")),
			"Add them to [Inference] Models; on a cold start they may not be registered at all.")
	case len(missingFromJSON) > 0:
		c.add("config/models.json agreement", StatusWarn,
			fmt.Sprintf("listed in config.toml but absent from models.json: %s", strings.Join(missingFromJSON, ", ")),
			"Remove them from [Inference] Models, or add an endpoint mapping.")
	default:
		c.add("config/models.json agreement", StatusPass, "config.toml and models.json list the same models", "")
	}
	return models
}

type daemonStatus struct {
	Connected    bool     `json:"connected"`
	ActiveModels []string `json:"active_models"`
	// nil means the running provider predates this field, which is not the same
	// as an empty registration and must not be reported as one.
	RegisteredModels []string `json:"registered_models"`
}

func (c *checker) checkDaemon() *daemonStatus {
	var st daemonStatus
	if err := c.getJSON("/api/v1/computing/inference/status", &st); err != nil {
		c.add("daemon", StatusFail, fmt.Sprintf("provider API unreachable at %s: %v", c.opt.APIBase, err),
			"Is `computing-provider run` running, and is API.Port correct?")
		return nil
	}
	c.add("daemon", StatusPass, "provider API is responding", "")
	if st.Connected {
		c.add("Swan Inference connection", StatusPass, "connected", "")
	} else {
		c.add("Swan Inference connection", StatusFail, "not connected — no requests can be received",
			"Check [Inference] WebSocketURL and ApiKey, and the provider log for reconnect errors.")
	}
	return &st
}

// checkRegistration is the drift that costs the most and shows the least: a
// model can be present, enabled and healthy while never having been sent to
// Swan Inference, so it earns nothing and looks fine locally.
func (c *checker) checkRegistration(models map[string]modelEntry, st *daemonStatus) {
	if st == nil || models == nil {
		return
	}
	if st.RegisteredModels == nil {
		c.add("registered with Swan Inference", StatusWarn,
			"the running provider does not report its registered models",
			"Upgrade and restart the daemon to enable this check.")
		return
	}
	reg := make(map[string]bool, len(st.RegisteredModels))
	for _, m := range st.RegisteredModels {
		reg[m] = true
	}
	var missing []string
	for id := range models {
		if !reg[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		c.add("registered with Swan Inference", StatusFail,
			fmt.Sprintf("%d of %d models are not registered: %s", len(missing), len(models), strings.Join(missing, ", ")),
			"They will receive no traffic. Check they are healthy and listed in [Inference] Models, then restart.")
		return
	}
	c.add("registered with Swan Inference", StatusPass,
		fmt.Sprintf("all %d models registered", len(models)), "")
}

func (c *checker) checkHealth() {
	var health map[string]struct {
		HealthString string `json:"health_string"`
		CircuitOpen  bool   `json:"circuit_open"`
	}
	if err := c.getJSON("/api/v1/computing/inference/health", &health); err != nil {
		c.add("model health", StatusWarn, fmt.Sprintf("could not read health: %v", err), "")
		return
	}
	// Only "unhealthy" means the model is out of service. "degraded" still
	// serves requests, and "unknown" is the normal state before the first health
	// check completes, so neither should fail a run started right after boot.
	var unhealthy, other []string
	for id, h := range health {
		switch h.HealthString {
		case "healthy":
		case "unhealthy":
			unhealthy = append(unhealthy, id)
		default:
			other = append(other, fmt.Sprintf("%s (%s)", id, h.HealthString))
		}
	}
	sort.Strings(unhealthy)
	sort.Strings(other)
	if len(unhealthy) > 0 {
		msg := "unhealthy: " + strings.Join(unhealthy, ", ")
		if len(other) > 0 {
			msg += "; " + strings.Join(other, ", ")
		}
		c.add("model health", StatusFail, msg,
			"Check the backend is up and serving on the endpoint in models.json.")
		return
	}
	if len(other) > 0 {
		c.add("model health", StatusWarn, strings.Join(other, ", "),
			"Degraded models still serve; unknown means the first health check has not completed yet.")
		return
	}
	c.add("model health", StatusPass, fmt.Sprintf("all %d models healthy", len(health)), "")
}

// checkContexts compares the context window each backend actually accepts with
// what models.json pins. A mismatch shows up as customer requests rejected with
// "exceeds the available context size" after the marketplace advertised more.
func (c *checker) checkContexts(models map[string]modelEntry) {
	if len(models) == 0 {
		return
	}
	var mismatched, unknown []string
	for id, m := range models {
		actual, ok := c.backendContext(id, m)
		if !ok {
			unknown = append(unknown, id)
			continue
		}
		if m.ContextLength > 0 && m.ContextLength != actual {
			mismatched = append(mismatched, fmt.Sprintf("%s: models.json says %d, backend serves %d", id, m.ContextLength, actual))
		}
	}
	sort.Strings(mismatched)
	switch {
	case len(mismatched) > 0:
		c.add("context window", StatusFail, strings.Join(mismatched, "; "),
			"Requests sized to the advertised window will be rejected. Fix context_length or raise the backend's limit.")
	case len(unknown) == len(models):
		c.add("context window", StatusWarn, "no backend reported a context window", "")
	default:
		c.add("context window", StatusPass, "reported context matches every backend", "")
	}
}

// backendContext returns the context window the backend reports for this
// specific model. One endpoint routinely serves several models (Ollama, a
// proxy, a multi-model vLLM), so the entry must be matched by id — taking the
// first would compare one model's limit against another's.
func (c *checker) backendContext(id string, m modelEntry) (int, bool) {
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			MaxModelLen int    `json:"max_model_len"`
		} `json:"data"`
	}
	req, err := http.NewRequest("GET", strings.TrimRight(m.Endpoint, "/")+"/v1/models", nil)
	if err != nil {
		return 0, false
	}
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Data) == 0 {
		return 0, false
	}
	want := m.servedName(id)
	for _, d := range out.Data {
		if strings.EqualFold(d.ID, want) || strings.EqualFold(d.ID, id) {
			if d.MaxModelLen > 0 {
				return d.MaxModelLen, true
			}
			return 0, false
		}
	}
	// A single-model backend that names itself differently is still unambiguous.
	if len(out.Data) == 1 && out.Data[0].MaxModelLen > 0 {
		return out.Data[0].MaxModelLen, true
	}
	return 0, false
}

// checkCompletions sends one real, minimal completion per model. This is the
// only check that exercises the inference engine: a backend whose engine has
// died, or whose upstream credentials expired, answers /v1/models normally and
// fails here.
func (c *checker) checkCompletions(models map[string]modelEntry) {
	if len(models) == 0 {
		return
	}
	var failed []string
	var probed, skipped int
	for id, m := range models {
		if !m.chatCompatible() {
			skipped++
			continue
		}
		probed++
		name := m.servedName(id)
		body, _ := json.Marshal(map[string]interface{}{
			"model":      name,
			"messages":   []map[string]string{{"role": "user", "content": "ping"}},
			"max_tokens": 1,
		})
		req, err := http.NewRequest("POST", strings.TrimRight(m.Endpoint, "/")+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if m.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+m.APIKey)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			failed = append(failed, fmt.Sprintf("%s: HTTP %d %s", id, resp.StatusCode, strings.TrimSpace(string(snippet))))
		}
	}
	sort.Strings(failed)
	if len(failed) > 0 {
		c.add("inference probe", StatusFail, strings.Join(failed, "; "),
			"These models pass health checks but cannot serve. Check the backend engine and any upstream credentials.")
		return
	}
	msg := fmt.Sprintf("all %d models completed a request", probed)
	if skipped > 0 {
		msg += fmt.Sprintf(" (%d non-chat models not probed)", skipped)
	}
	c.add("inference probe", StatusPass, msg, "")
}

// checkTraffic flags models that are registered and healthy but have never been
// called: usually a sign the model ID is not routable on the marketplace.
func (c *checker) checkTraffic() {
	var metrics struct {
		ModelMetrics map[string]struct {
			TotalRequests int64 `json:"total_requests"`
		} `json:"model_metrics"`
	}
	if err := c.getJSON("/api/v1/computing/inference/metrics", &metrics); err != nil {
		return
	}
	var idle []string
	for id, m := range metrics.ModelMetrics {
		if m.TotalRequests == 0 {
			idle = append(idle, id)
		}
	}
	sort.Strings(idle)
	if len(idle) > 0 {
		c.add("traffic", StatusWarn,
			fmt.Sprintf("no requests since start for: %s", strings.Join(idle, ", ")),
			"Confirm the model ID matches the marketplace catalog entry.")
		return
	}
	c.add("traffic", StatusPass, "every model has served requests", "")
}

// checkDisk warns before a volume fills. A full disk took a provider down by
// way of an unrotated log, which no other check would have predicted.
func (c *checker) checkDisk() {
	seen := make(map[string]bool)
	for _, dir := range []string{c.opt.RepoPath, c.opt.LogDir} {
		if dir == "" {
			continue
		}
		var st syscall.Statfs_t
		if err := syscall.Statfs(dir, &st); err != nil {
			continue
		}
		key := fmt.Sprintf("%d", st.Fsid)
		if seen[key] {
			continue
		}
		seen[key] = true

		freeGB := float64(st.Bavail) * float64(st.Bsize) / 1e9
		totalGB := float64(st.Blocks) * float64(st.Bsize) / 1e9
		pct := 100 * (1 - freeGB/totalGB)
		msg := fmt.Sprintf("%s: %.1f GB free of %.0f GB (%.0f%% used)", dir, freeGB, totalGB, pct)
		if freeGB < c.opt.MinFreeGB {
			c.add("disk space", StatusWarn, msg, "Free space or move logs and models to a larger volume.")
		} else {
			c.add("disk space", StatusPass, msg, "")
		}
	}
}

func (c *checker) getJSON(path string, out interface{}) error {
	resp, err := c.client.Get(strings.TrimRight(c.opt.APIBase, "/") + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
