// Package cliproxy inspects a local CLIProxyAPI instance: the OAuth
// credentials it holds, and whether the models it fronts actually serve.
//
// CLIProxyAPI turns a personal ChatGPT/Claude/Gemini subscription into an
// OpenAI-compatible endpoint, which a provider can then list in models.json.
// It is a separate program with its own release cadence, so this package drives
// it rather than embedding it.
//
// The failure this exists for is not the obvious one. A credential can be
// unexpired, enabled, and still rejected by the upstream — the proxy answers
// `GET /v1/models` from a static registry either way, so the model looks
// healthy while every completion fails. Reading the credential files alone
// cannot tell you that; only a real completion can. Hence Probe.
package cliproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultAuthDir is where CLIProxyAPI keeps credentials unless its config says
// otherwise.
const DefaultAuthDir = "~/.cli-proxy-api"

// CredentialState classifies a credential without disclosing it.
type CredentialState string

const (
	StateValid    CredentialState = "valid"
	StateExpiring CredentialState = "expiring"
	StateExpired  CredentialState = "expired"
	StateDisabled CredentialState = "disabled"
	StateUnknown  CredentialState = "unknown"
)

// ExpiringWindow is how far ahead of expiry a credential is worth flagging.
// A week gives an operator time to re-auth before traffic starts failing.
const ExpiringWindow = 7 * 24 * time.Hour

// Credential is the non-secret metadata of one stored login.
//
// Token fields are deliberately absent: this struct is printed, logged and
// serialised to JSON, and a credential file holds an access token, a refresh
// token and an id token. None of them are ever read into here.
type Credential struct {
	File     string          `json:"file"`
	Email    string          `json:"email,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Plan     string          `json:"plan,omitempty"`
	Expires  time.Time       `json:"expires,omitempty"`
	Disabled bool            `json:"disabled"`
	State    CredentialState `json:"state"`
	// Err records a file that could not be read or parsed. A credential
	// directory with unreadable entries is itself a finding.
	Err string `json:"error,omitempty"`
}

// authFile is the on-disk shape. Only non-secret fields are declared, so a
// token cannot reach this program's memory by accident.
type authFile struct {
	Email    string `json:"email"`
	Type     string `json:"type"`
	Expired  string `json:"expired"`
	Disabled bool   `json:"disabled"`
}

// ExpandPath resolves a leading ~ against the user's home directory.
func ExpandPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return path
}

// AuthDirFromConfig reads `auth-dir:` out of a CLIProxyAPI config.yaml.
//
// Deliberately a line scan rather than a YAML dependency: one scalar is wanted
// from a file this program does not own, and taking a parser (and its upgrade
// path) for that is a poor trade.
func AuthDirFromConfig(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || !strings.HasPrefix(trimmed, "auth-dir:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "auth-dir:"))
		value = strings.Trim(value, `"'`)
		if value == "" {
			continue
		}
		return value, nil
	}
	return "", fmt.Errorf("no auth-dir set in %s", configPath)
}

// classify decides a credential's state. Disabled wins over expiry: a
// credential the proxy has taken out of rotation is out regardless of its
// clock.
func classify(disabled bool, expires time.Time, now time.Time) CredentialState {
	switch {
	case disabled:
		return StateDisabled
	case expires.IsZero():
		return StateUnknown
	case !expires.After(now):
		return StateExpired
	case expires.Sub(now) < ExpiringWindow:
		return StateExpiring
	default:
		return StateValid
	}
}

// planFromName recovers the plan suffix from CLIProxyAPI's file naming
// (`codex-someone@example.com-plus.json`). It is cosmetic, so anything that
// does not fit the pattern yields no plan rather than an error.
func planFromName(name, provider, email string) string {
	base := strings.TrimSuffix(name, ".json")
	base = strings.TrimPrefix(base, provider+"-")
	base = strings.TrimPrefix(base, email+"-")
	if base == strings.TrimSuffix(name, ".json") {
		return ""
	}
	return base
}

// ReadCredentials lists the credentials in an auth directory, newest expiry
// first. A missing directory is an error; an unreadable file inside it is
// reported on that credential and does not abandon the rest.
func ReadCredentials(authDir string) ([]Credential, error) {
	dir := ExpandPath(authDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	creds := make([]Credential, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		cred := Credential{File: entry.Name(), State: StateUnknown}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			cred.Err = err.Error()
			creds = append(creds, cred)
			continue
		}
		var parsed authFile
		if err := json.Unmarshal(data, &parsed); err != nil {
			cred.Err = "not valid JSON: " + err.Error()
			creds = append(creds, cred)
			continue
		}

		cred.Email = parsed.Email
		cred.Provider = parsed.Type
		cred.Disabled = parsed.Disabled
		cred.Plan = planFromName(entry.Name(), parsed.Type, parsed.Email)
		if parsed.Expired != "" {
			if ts, err := time.Parse(time.RFC3339, parsed.Expired); err == nil {
				cred.Expires = ts.UTC()
			}
		}
		cred.State = classify(cred.Disabled, cred.Expires, now)
		creds = append(creds, cred)
	}

	sort.Slice(creds, func(i, j int) bool {
		if creds[i].Expires.Equal(creds[j].Expires) {
			return creds[i].File < creds[j].File
		}
		return creds[i].Expires.After(creds[j].Expires)
	})
	return creds, nil
}

// ProbeResult is the outcome of one real completion through the proxy.
type ProbeResult struct {
	Model string `json:"model"`
	OK    bool   `json:"ok"`
	// Status is the HTTP status the proxy returned; 0 when it could not be
	// reached at all.
	Status int `json:"status,omitempty"`
	// Detail carries the upstream's own error message, which is the part worth
	// reading — "auth_unavailable" and "insufficient quota" call for very
	// different actions.
	Detail string `json:"detail,omitempty"`
}

type probeRequest struct {
	Model     string         `json:"model"`
	Messages  []probeMessage `json:"messages"`
	MaxTokens int            `json:"max_tokens"`
}

type probeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Probe sends a one-token completion for each model and reports what came back.
//
// A completion, not `GET /v1/models`: the proxy answers that from a static
// registry, so it stays 200 while every actual request fails. The whole point
// of this check is the gap between those two answers.
func Probe(ctx context.Context, endpoint, apiKey string, models []string) []ProbeResult {
	client := &http.Client{Timeout: 30 * time.Second}
	results := make([]ProbeResult, 0, len(models))

	for _, model := range models {
		result := ProbeResult{Model: model}
		body, err := json.Marshal(probeRequest{
			Model:     model,
			Messages:  []probeMessage{{Role: "user", Content: "ping"}},
			MaxTokens: 1,
		})
		if err != nil {
			result.Detail = err.Error()
			results = append(results, result)
			continue
		}

		url := strings.TrimSuffix(endpoint, "/") + "/v1/chat/completions"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			result.Detail = err.Error()
			results = append(results, result)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			result.Detail = err.Error()
			results = append(results, result)
			continue
		}
		respBody, _ := readLimited(resp)
		resp.Body.Close()

		result.Status = resp.StatusCode
		result.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
		if !result.OK {
			result.Detail = errorMessage(respBody)
		}
		results = append(results, result)
	}
	return results
}

// readLimited caps how much of a response is held, so a misbehaving endpoint
// cannot balloon this process.
func readLimited(resp *http.Response) ([]byte, error) {
	const maxBody = 64 << 10
	buf := &bytes.Buffer{}
	_, err := buf.ReadFrom(&limitedReader{r: resp.Body, n: maxBody})
	return buf.Bytes(), err
}

type limitedReader struct {
	r interface{ Read([]byte) (int, error) }
	n int
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, fmt.Errorf("response truncated")
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= n
	return n, err
}

// errorMessage digs the human-readable string out of an OpenAI-style error,
// falling back to the raw body when the shape is unfamiliar.
func errorMessage(body []byte) string {
	var wrapped struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Error.Message != "" {
		return wrapped.Error.Message
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	const maxDetail = 300
	if len(text) > maxDetail {
		return text[:maxDetail] + "…"
	}
	return text
}

// ModelsForEndpoint returns the models.json entries served by the given
// endpoint, with the local_model rewrite applied — that is the name the proxy
// itself answers to, and probing the marketplace name instead would report a
// failure the proxy never had.
//
// The api_key those entries carry is returned too. It is the key the provider
// already uses to reach this proxy, so a probe can authenticate the same way
// the real traffic does instead of asking the operator to repeat it.
func ModelsForEndpoint(modelsPath, endpoint string) (models []string, apiKey string, err error) {
	data, err := os.ReadFile(modelsPath)
	if err != nil {
		return nil, "", err
	}
	var parsed map[string]struct {
		Endpoint   string `json:"endpoint"`
		LocalModel string `json:"local_model"`
		APIKey     string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, "", err
	}

	want := normalizeEndpoint(endpoint)
	models = make([]string, 0, len(parsed))
	for id, entry := range parsed {
		if normalizeEndpoint(entry.Endpoint) != want {
			continue
		}
		if apiKey == "" {
			apiKey = entry.APIKey
		}
		if entry.LocalModel != "" {
			models = append(models, entry.LocalModel)
			continue
		}
		models = append(models, id)
	}
	sort.Strings(models)
	return models, apiKey, nil
}

// normalizeEndpoint makes localhost and 127.0.0.1 compare equal, since
// models.json and the --endpoint flag are written by different hands.
func normalizeEndpoint(endpoint string) string {
	e := strings.TrimSuffix(strings.TrimSpace(endpoint), "/")
	e = strings.ReplaceAll(e, "://localhost", "://127.0.0.1")
	return e
}
