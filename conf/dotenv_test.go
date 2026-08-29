package conf

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, dir, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(dir, EnvFileName)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, `# the provider's secrets
SMTP_PASSWORD=app-password-here
export INFERENCE_API_KEY=sk-prov-abc

EMPTY=
`, 0o600)

	for _, k := range []string{"SMTP_PASSWORD", "INFERENCE_API_KEY", "EMPTY"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	if err := LoadEnvFile(dir); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if got := os.Getenv("SMTP_PASSWORD"); got != "app-password-here" {
		t.Errorf("SMTP_PASSWORD = %q", got)
	}
	// "export FOO=bar" is what muscle memory produces; it must work.
	if got := os.Getenv("INFERENCE_API_KEY"); got != "sk-prov-abc" {
		t.Errorf("INFERENCE_API_KEY = %q, want the export prefix to be tolerated", got)
	}
	if got := os.Getenv("EMPTY"); got != "" {
		t.Errorf("EMPTY = %q, want empty", got)
	}
}

// The real environment must win, so a single run can be overridden without
// editing the file.
func TestRealEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "SMTP_PASSWORD=from-file\n", 0o600)

	t.Setenv("SMTP_PASSWORD", "from-shell")
	if err := LoadEnvFile(dir); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SMTP_PASSWORD"); got != "from-shell" {
		t.Errorf("SMTP_PASSWORD = %q, want the shell value to win", got)
	}
}

// A password containing a # or spaces is only safe if quotes are honoured;
// silently keeping them would corrupt the credential in a way that surfaces as
// an opaque auth failure.
func TestQuotedAndAwkwardValues(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, `A="has spaces and # hash"
B='single quoted'
C=plain#notacomment
D=  spaced-out
`, 0o600)

	for _, k := range []string{"A", "B", "C", "D"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	if err := LoadEnvFile(dir); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"A": "has spaces and # hash",
		"B": "single quoted",
		"C": "plain#notacomment",
		"D": "spaced-out",
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// No .env is the normal case for most providers and must not be an error.
func TestMissingFileIsNotAnError(t *testing.T) {
	if err := LoadEnvFile(t.TempDir()); err != nil {
		t.Fatalf("a missing .env should be silent, got %v", err)
	}
}

func TestLooseModeIsWarnedButStillLoaded(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "SMTP_PASSWORD=loose\n", 0o644)
	os.Unsetenv("SMTP_PASSWORD")
	t.Cleanup(func() { os.Unsetenv("SMTP_PASSWORD") })

	if err := LoadEnvFile(dir); err != nil {
		t.Fatalf("a world-readable file should warn, not fail: %v", err)
	}
	if got := os.Getenv("SMTP_PASSWORD"); got != "loose" {
		t.Errorf("SMTP_PASSWORD = %q, want it loaded despite the warning", got)
	}
}

func TestParseEnvLine(t *testing.T) {
	for _, tc := range []struct {
		line     string
		key, val string
		assigns  bool
	}{
		{"FOO=bar", "FOO", "bar", true},
		{"  FOO = bar  ", "FOO", "bar", true},
		{"export FOO=bar", "FOO", "bar", true},
		{"# comment", "", "", false},
		{"", "", "", false},
		{"no-equals-here", "", "", false},
		{"FOO=a=b", "FOO", "a=b", true},
	} {
		k, v, ok := parseEnvLine(tc.line)
		if ok != tc.assigns || k != tc.key || v != tc.val {
			t.Errorf("parseEnvLine(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.line, k, v, ok, tc.key, tc.val, tc.assigns)
		}
	}
}
