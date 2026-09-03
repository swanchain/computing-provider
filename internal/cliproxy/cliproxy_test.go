package cliproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func credFile(email, typ, expired string, disabled bool) string {
	return `{"email":"` + email + `","type":"` + typ + `","expired":"` + expired + `",` +
		`"disabled":` + map[bool]string{true: "true", false: "false"}[disabled] + `,` +
		`"access_token":"SECRET-ACCESS","refresh_token":"SECRET-REFRESH","id_token":"SECRET-ID"}`
}

func TestReadCredentialsClassifiesState(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	writeFile(t, dir, "codex-valid@example.com-plus.json",
		credFile("valid@example.com", "codex", now.Add(30*24*time.Hour).Format(time.RFC3339), false))
	writeFile(t, dir, "codex-soon@example.com-pro.json",
		credFile("soon@example.com", "codex", now.Add(48*time.Hour).Format(time.RFC3339), false))
	writeFile(t, dir, "codex-old@example.com-lite.json",
		credFile("old@example.com", "codex", now.Add(-24*time.Hour).Format(time.RFC3339), false))
	writeFile(t, dir, "codex-off@example.com-plus.json",
		credFile("off@example.com", "codex", now.Add(30*24*time.Hour).Format(time.RFC3339), true))

	creds, err := ReadCredentials(dir)
	if err != nil {
		t.Fatalf("ReadCredentials: %v", err)
	}
	if len(creds) != 4 {
		t.Fatalf("got %d credentials, want 4", len(creds))
	}

	byEmail := map[string]Credential{}
	for _, c := range creds {
		byEmail[c.Email] = c
	}
	for email, want := range map[string]CredentialState{
		"valid@example.com": StateValid,
		"soon@example.com":  StateExpiring,
		"old@example.com":   StateExpired,
		"off@example.com":   StateDisabled,
	} {
		if got := byEmail[email].State; got != want {
			t.Errorf("%s state = %q, want %q", email, got, want)
		}
	}

	// Disabled must win over an expiry still in the future: the proxy has
	// taken it out of rotation regardless of the clock.
	if byEmail["off@example.com"].Expires.Before(time.Now()) {
		t.Error("test setup wrong: the disabled credential should have a future expiry")
	}
}

// A credential file holds three tokens. None may reach the struct that gets
// printed and serialised.
func TestCredentialsNeverCarryTokens(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "codex-a@example.com-plus.json",
		credFile("a@example.com", "codex", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), false))

	creds, err := ReadCredentials(dir)
	if err != nil {
		t.Fatalf("ReadCredentials: %v", err)
	}
	out, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"SECRET-ACCESS", "SECRET-REFRESH", "SECRET-ID", "access_token", "refresh_token", "id_token"} {
		if strings.Contains(string(out), secret) {
			t.Errorf("serialised credential leaks %q: %s", secret, out)
		}
	}
}

func TestReadCredentialsReportsBadFileWithoutAbandoningTheRest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "codex-good@example.com-plus.json",
		credFile("good@example.com", "codex", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), false))
	writeFile(t, dir, "broken.json", "{not json")
	writeFile(t, dir, "notes.txt", "ignored, not a .json file")

	creds, err := ReadCredentials(dir)
	if err != nil {
		t.Fatalf("ReadCredentials: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("got %d credentials, want 2 (the .txt must be skipped)", len(creds))
	}
	var sawErr bool
	for _, c := range creds {
		if c.File == "broken.json" {
			sawErr = c.Err != ""
		}
	}
	if !sawErr {
		t.Error("the unparseable file should be reported as an error, not dropped silently")
	}
}

func TestReadCredentialsMissingDirIsAnError(t *testing.T) {
	if _, err := ReadCredentials(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a missing auth directory should be an error, not an empty list")
	}
}

func TestAuthDirFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("# comment\n#auth-dir: \"/commented/out\"\nport: 8317\nauth-dir: \"~/.cli-proxy-api\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := AuthDirFromConfig(path)
	if err != nil {
		t.Fatalf("AuthDirFromConfig: %v", err)
	}
	if got != "~/.cli-proxy-api" {
		t.Errorf("got %q, want ~/.cli-proxy-api (a commented line must not win)", got)
	}

	bare := filepath.Join(dir, "bare.yaml")
	if err := os.WriteFile(bare, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AuthDirFromConfig(bare); err == nil {
		t.Error("a config with no auth-dir should be an error")
	}
}

func TestPlanFromName(t *testing.T) {
	for _, tc := range []struct{ name, provider, email, want string }{
		{"codex-a@example.com-plus.json", "codex", "a@example.com", "plus"},
		{"codex-b@example.com-prolite.json", "codex", "b@example.com", "prolite"},
		{"something-else.json", "codex", "a@example.com", ""},
	} {
		if got := planFromName(tc.name, tc.provider, tc.email); got != tc.want {
			t.Errorf("planFromName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestModelsForEndpointUsesLocalModelName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	body := `{
      "openai/gpt-5.5":   {"endpoint":"http://localhost:8317","local_model":"gpt-5.5","api_key":"sk-swan-local"},
      "openai/gpt-5.4":   {"endpoint":"http://127.0.0.1:8317/","local_model":"gpt-5.4"},
      "vendor/no-rewrite":{"endpoint":"http://localhost:8317"},
      "Qwen/Qwen3.8-27B": {"endpoint":"http://localhost:30001"}
    }`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, apiKey, err := ModelsForEndpoint(path, "http://127.0.0.1:8317")
	if err != nil {
		t.Fatalf("ModelsForEndpoint: %v", err)
	}
	if apiKey != "sk-swan-local" {
		t.Errorf("api key = %q, want the one models.json already uses for this proxy", apiKey)
	}
	want := []string{"gpt-5.4", "gpt-5.5", "vendor/no-rewrite"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	// The local GPU backend on another port must not be dragged in.
	for _, m := range got {
		if m == "Qwen/Qwen3.8-27B" {
			t.Error("a model on a different endpoint was included")
		}
	}
}

func TestProbeReportsUpstreamErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.MaxTokens != 1 {
			t.Errorf("probe asked for %d tokens, want 1 — a probe must not cost real output", body.MaxTokens)
		}
		if body.Model == "broken" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"auth_unavailable: no auth available (providers=codex)","type":"server_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	results := Probe(context.Background(), srv.URL, "test-key", []string{"working", "broken"})
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !results[0].OK {
		t.Errorf("working model reported failure: %+v", results[0])
	}
	if results[1].OK {
		t.Error("broken model reported success")
	}
	if results[1].Status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", results[1].Status)
	}
	if !strings.Contains(results[1].Detail, "auth_unavailable") {
		t.Errorf("detail = %q, want the upstream message — that is the part worth reading", results[1].Detail)
	}
}

func TestProbeUnreachableEndpoint(t *testing.T) {
	// Port 1 on loopback refuses immediately.
	results := Probe(context.Background(), "http://127.0.0.1:1", "", []string{"m"})
	if len(results) != 1 || results[0].OK {
		t.Fatalf("expected a single failure, got %+v", results)
	}
	if results[0].Status != 0 {
		t.Errorf("status = %d, want 0 when the proxy could not be reached at all", results[0].Status)
	}
	if results[0].Detail == "" {
		t.Error("an unreachable endpoint should say why")
	}
}

func TestErrorMessageFallsBackToRawBody(t *testing.T) {
	if got := errorMessage([]byte(`{"error":{"message":"boom"}}`)); got != "boom" {
		t.Errorf("got %q, want boom", got)
	}
	if got := errorMessage([]byte("plain text failure")); got != "plain text failure" {
		t.Errorf("got %q, want the raw body", got)
	}
	long := strings.Repeat("x", 500)
	if got := errorMessage([]byte(long)); len(got) > 320 {
		t.Errorf("a long body should be truncated, got %d chars", len(got))
	}
}
