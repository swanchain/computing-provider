package computing

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/swanchain/computing-provider-v2/internal/cliproxy"
	"github.com/swanchain/computing-provider-v2/internal/db"
)

// The accounts behind each CLIProxyAPI instance this node uses.
//
// Every outage of a proxied model so far has been an account problem, not a
// server one: a credential that stopped refreshing, a subscription whose usage
// limit was reached, a cooldown the proxy held after the limit reset. None of
// that shows in a model's health — the proxy answers /v1/models throughout —
// so it was found from failed requests or by running `cliproxy status` by
// hand. This reads it from each instance's management API, which reports the
// subscription's own usage windows as the upstream last stated them.

// ProxyQuotaWindow is one of a subscription's usage windows.
type ProxyQuotaWindow struct {
	Label         string     `json:"label"`
	WindowMinutes int        `json:"window_minutes"`
	UsedPercent   float64    `json:"used_percent"`
	ResetAt       *time.Time `json:"reset_at,omitempty"`
}

// ProxyCooldown is a model the proxy is holding back on an account.
type ProxyCooldown struct {
	Model   string    `json:"model,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	RetryAt time.Time `json:"retry_at"`
}

// ProxyAccount is one credential in an instance.
type ProxyAccount struct {
	Account       string `json:"account"`
	Provider      string `json:"provider,omitempty"`
	Plan          string `json:"plan,omitempty"`
	Status        string `json:"status"`
	StatusMessage string `json:"status_message,omitempty"`
	Disabled      bool   `json:"disabled"`
	Unavailable   bool   `json:"unavailable"`
	// Expires is the stored access token's expiry. The proxy refreshes it
	// before then, so a past date means refreshing has stopped working.
	Expires         *time.Time         `json:"expires,omitempty"`
	Success         int64              `json:"success"`
	Failed          int64              `json:"failed"`
	Windows         []ProxyQuotaWindow `json:"windows"`
	QuotaObservedAt *time.Time         `json:"quota_observed_at,omitempty"`
	Cooldowns       []ProxyCooldown    `json:"cooldowns"`
}

// ProxyInstance is one CLIProxyAPI endpoint and what this node uses it for.
type ProxyInstance struct {
	Endpoint string `json:"endpoint"`
	// Roles: "hub" when it is a model's endpoint, "local" when it is a
	// model's local_endpoint for the local gateway.
	Roles    []string       `json:"roles"`
	Models   []string       `json:"models"`
	Accounts []ProxyAccount `json:"accounts"`
	Error    string         `json:"error,omitempty"`
	// Tokens the instance served in the last 24 hours, every client included,
	// from its usage queue; unmeasured when fewer than two samples exist.
	TokensIn24h  int64 `json:"tokens_in_24h"`
	TokensOut24h int64 `json:"tokens_out_24h"`
	Measured24h  bool  `json:"measured_24h"`
	// Per-model token counts from this proxy's usage queue, including traffic
	// from clients other than this node. A model needs two samples to measure.
	ModelUsage24h []ProxyModelUsage `json:"model_usage_24h"`
}

// ProxyModelUsage is one model's measured token consumption through a proxy.
type ProxyModelUsage struct {
	Model        string `json:"model"`
	TokensIn24h  int64  `json:"tokens_in_24h"`
	TokensOut24h int64  `json:"tokens_out_24h"`
	Measured24h  bool   `json:"measured_24h"`
}

// ProxyAccountsReport is the answer to GET /inference/proxy-accounts.
type ProxyAccountsReport struct {
	Instances []ProxyInstance `json:"instances"`
	// Note explains an empty report the operator can fix.
	Note string `json:"note,omitempty"`
}

type authFilesResponse struct {
	Files []struct {
		Account       string `json:"account"`
		Email         string `json:"email"`
		Provider      string `json:"provider"`
		Status        string `json:"status"`
		StatusMessage string `json:"status_message"`
		Path          string `json:"path"`
		Name          string `json:"name"`
		Disabled      bool   `json:"disabled"`
		Unavailable   bool   `json:"unavailable"`
		Success       int64  `json:"success"`
		Failed        int64  `json:"failed"`
		Quota         struct {
			ObservedAt *time.Time        `json:"observed_at"`
			Signals    map[string]string `json:"signals"`
		} `json:"quota"`
		Cooldowns []struct {
			ModelKey string    `json:"model_key"`
			Reason   string    `json:"reason"`
			RetryAt  time.Time `json:"retry_at"`
		} `json:"cooldowns"`
	} `json:"files"`
}

// ProxyAccounts reports every proxy endpoint among the node's models.
func (s *InferenceService) ProxyAccounts(ctx context.Context) ProxyAccountsReport {
	report := ProxyAccountsReport{Instances: []ProxyInstance{}}
	b := s.usageSampler
	if b == nil || b.managementKey == "" {
		report.Note = "Set [Inference] CLIProxyManagementKey to the proxies' management key to see their accounts."
		return report
	}

	roles := map[string]map[string]bool{}
	models := map[string]map[string]bool{}
	note := func(ep, role, model string) {
		ep = strings.TrimRight(ep, "/")
		if ep == "" {
			return
		}
		if roles[ep] == nil {
			roles[ep], models[ep] = map[string]bool{}, map[string]bool{}
		}
		roles[ep][role], models[ep][model] = true, true
	}
	for id, m := range s.modelMappings {
		// A disabled model is out of service and uses no proxy. Its server is
		// usually stopped too, and an endpoint that cannot be reached is
		// indistinguishable from a proxy that is down, so it would be listed
		// as an empty proxy instance.
		if s.registry != nil {
			if rm, ok := s.registry.GetModel(id); ok && !rm.Enabled {
				continue
			}
		}
		// A local-only model's endpoint never sees hub traffic; it is the
		// local gateway's backend whichever field names it.
		if m.LocalOnly {
			note(m.Endpoint, "local", id)
			if m.LocalEndpoint != "" {
				note(m.LocalEndpoint, "local", id)
			}
			continue
		}
		note(m.Endpoint, "hub", id)
		if local := splitLocalEndpoint(m); local != "" {
			note(local, "local", id)
		}
	}
	endpoints := make([]string, 0, len(roles))
	for ep := range roles {
		endpoints = append(endpoints, ep)
	}
	sort.Strings(endpoints)

	names := s.modelEndpoints()
	creds := map[string]map[string]cliproxy.Credential{} // auth dir -> file -> credential
	for _, ep := range endpoints {
		var files authFilesResponse
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := b.management(cctx, ep+"/v0/management/auth-files", &files)
		cancel()
		if err != nil && strings.Contains(err.Error(), "HTTP 404") {
			continue // a model server, not a proxy
		}
		if err != nil && !b.knownProxy(ep) {
			// Unreachable and never seen answering as a proxy: most often a
			// model server that was stopped or moved while still configured.
			// Listing it would show an empty "proxy"; a real proxy that goes
			// down has drained usage before and is still listed.
			continue
		}
		inst := ProxyInstance{Endpoint: ep, Roles: sortedKeys(roles[ep]), Models: sortedKeys(models[ep]), Accounts: []ProxyAccount{}, ModelUsage24h: []ProxyModelUsage{}}
		if err != nil {
			inst.Error = err.Error()
		}
		for _, f := range files.Files {
			a := ProxyAccount{
				Account: f.Account, Provider: f.Provider, Status: f.Status, StatusMessage: f.StatusMessage,
				Disabled: f.Disabled, Unavailable: f.Unavailable, Success: f.Success, Failed: f.Failed,
				Windows: quotaWindows(f.Quota.Signals), QuotaObservedAt: f.Quota.ObservedAt, Cooldowns: []ProxyCooldown{},
			}
			if a.Account == "" {
				a.Account = f.Email
			}
			a.Plan = f.Quota.Signals["X-Codex-Plan-Type"]
			for _, c := range f.Cooldowns {
				a.Cooldowns = append(a.Cooldowns, ProxyCooldown{Model: c.ModelKey, Reason: c.Reason, RetryAt: c.RetryAt})
			}
			if f.Path != "" {
				dir := filepath.Dir(f.Path)
				if creds[dir] == nil {
					creds[dir] = map[string]cliproxy.Credential{}
					if list, err := cliproxy.ReadCredentials(dir); err == nil {
						for _, c := range list {
							creds[dir][c.File] = c
						}
					}
				}
				if c, ok := creds[dir][filepath.Base(f.Path)]; ok {
					// The file-name fallback is only a plan when it is not
					// just the account repeated, as for Claude logins.
					if a.Plan == "" && !strings.Contains(c.Plan, "@") {
						a.Plan = c.Plan
					}
					if !c.Expires.IsZero() {
						exp := c.Expires
						a.Expires = &exp
					}
				}
			}
			inst.Accounts = append(inst.Accounts, a)
		}
		inst.TokensIn24h, inst.TokensOut24h, inst.Measured24h, inst.ModelUsage24h = proxyTokens24h(ep, names[ep].Names)
		report.Instances = append(report.Instances, inst)
	}
	return report
}

// knownProxy reports whether ep has been read as a CLIProxyAPI usage queue,
// in this process or, from the stored per-model samples, before a restart.
func (b *BackendUsageSampler) knownProxy(ep string) bool {
	b.mu.Lock()
	_, seen := b.queueStart[ep]
	b.mu.Unlock()
	if seen {
		return true
	}
	database := db.NewDbService()
	if database == nil {
		return false
	}
	var rows []BackendUsageSample
	prefix := ep + perModelSep
	if err := database.Select("endpoint").Where("endpoint LIKE ?", prefix+"%").Limit(50).Find(&rows).Error; err != nil {
		return false
	}
	for _, r := range rows {
		// LIKE treats URL underscores as wildcards; the prefix check is exact.
		if strings.HasPrefix(r.Endpoint, prefix) {
			return true
		}
	}
	return false
}

// quotaWindows reads the usage windows from the rate-limit headers the
// proxy last saw. A window of zero minutes is one the plan does not have.
func quotaWindows(signals map[string]string) []ProxyQuotaWindow {
	out := []ProxyQuotaWindow{}
	for _, which := range []string{"Primary", "Secondary"} {
		prefix := "X-Codex-" + which + "-"
		minutes, err := strconv.Atoi(signals[prefix+"Window-Minutes"])
		if err != nil || minutes <= 0 {
			continue
		}
		w := ProxyQuotaWindow{Label: windowLabel(minutes), WindowMinutes: minutes}
		w.UsedPercent, _ = strconv.ParseFloat(signals[prefix+"Used-Percent"], 64)
		if at, err := strconv.ParseInt(signals[prefix+"Reset-At"], 10, 64); err == nil && at > 0 {
			t := time.Unix(at, 0).UTC()
			w.ResetAt = &t
		}
		out = append(out, w)
	}
	return out
}

func windowLabel(minutes int) string {
	switch {
	case minutes == 10080:
		return "weekly"
	case minutes%1440 == 0:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}

// proxyTokens24h sums an instance's per-model counters over the last day.
// names maps a served name (a model's local_model) to its model ID.
func proxyTokens24h(endpoint string, names map[string]string) (in, out int64, measured bool, models []ProxyModelUsage) {
	models = []ProxyModelUsage{}
	database := db.NewDbService()
	if database == nil {
		return
	}
	var rows []BackendUsageSample
	since := time.Now().Add(-24 * time.Hour)
	if err := database.Where("endpoint LIKE ? AND time >= ?", endpoint+perModelSep+"%", since).
		Order("endpoint, time").Find(&rows).Error; err != nil {
		return
	}
	return summarizeProxyTokens24h(endpoint, rows, names)
}

func summarizeProxyTokens24h(endpoint string, rows []BackendUsageSample, names map[string]string) (in, out int64, measured bool, models []ProxyModelUsage) {
	models = []ProxyModelUsage{}
	prefix := endpoint + perModelSep
	byKey := map[string][]BackendUsageSample{}
	for _, r := range rows {
		// SQL LIKE can treat URL underscores as wildcards; the exact prefix
		// check keeps another proxy's counters out of this instance.
		if !strings.HasPrefix(r.Endpoint, prefix) {
			continue
		}
		byKey[r.Endpoint] = append(byKey[r.Endpoint], r)
	}
	whole := func(t time.Time) time.Time { return time.Time{} }
	// A counter recorded under a served name rather than the model ID — work
	// seen before the model was configured — is the same model, and would
	// otherwise be listed twice. Each key is a separate counter, so deltas are
	// taken per key and only then added together.
	byModel := map[string]*ProxyModelUsage{}
	for key, ss := range byKey {
		model := strings.TrimPrefix(key, prefix)
		if model == "" {
			continue
		}
		if id, ok := names[model]; ok {
			model = id
		}
		row := byModel[model]
		if row == nil {
			row = &ProxyModelUsage{Model: model}
			byModel[model] = row
		}
		if len(ss) >= 2 {
			tin := int64(promptDeltas(ss, whole)[time.Time{}])
			tout := int64(generatedDeltas(ss, whole)[time.Time{}])
			row.Measured24h = true
			row.TokensIn24h += tin
			row.TokensOut24h += tout
			measured = true
			in += tin
			out += tout
		}
	}
	for _, row := range byModel {
		models = append(models, *row)
	}
	sort.Slice(models, func(i, j int) bool {
		iTotal := models[i].TokensIn24h + models[i].TokensOut24h
		jTotal := models[j].TokensIn24h + models[j].TokensOut24h
		if iTotal != jTotal {
			return iTotal > jTotal
		}
		return models[i].Model < models[j].Model
	})
	return
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
