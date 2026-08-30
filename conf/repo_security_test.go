package conf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecureRepoTightensExposedSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"private_key", "machine_fingerprint", "config.toml", "models.json", "dashboard.token", EnvFileName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "keystore"), 0o755); err != nil {
		t.Fatal(err)
	}

	changed := SecureRepo(dir)
	if len(changed) != 8 {
		t.Errorf("reported %d changes, want 8 (dir + keystore + 6 files): %v", len(changed), changed)
	}

	for _, name := range []string{"private_key", "machine_fingerprint", "config.toml", "models.json", "dashboard.token", EnvFileName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s left at %04o, still readable by others", name, perm)
		}
	}
	for _, name := range []string{".", "keystore"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("dir %s left at %04o", name, perm)
		}
	}
}

// Already-correct permissions must not be reported as changes, or every start
// logs a repair that did not happen.
func TestSecureRepoIsQuietWhenAlreadyTight(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, SecretDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private_key"), []byte("x"), SecretFileMode); err != nil {
		t.Fatal(err)
	}
	if changed := SecureRepo(dir); len(changed) != 0 {
		t.Errorf("reported %v, want no changes", changed)
	}
}

// It must never widen permissions: a 0400 key stays 0400.
func TestSecureRepoNeverWidens(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, SecretDirMode)
	p := filepath.Join(dir, "private_key")
	if err := os.WriteFile(p, []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	SecureRepo(dir)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o400 {
		t.Errorf("mode = %04o, want 0400 — tightening must not loosen", got)
	}
}

func TestSecureRepoIgnoresMissingFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, SecretDirMode)
	if changed := SecureRepo(dir); len(changed) != 0 {
		t.Errorf("an empty repo should need no changes, got %v", changed)
	}
}

// A repo inside a git working tree is the hazard that lost-identity risk comes
// from: .gitignore prevents committing, but `git clean -xfd` deletes it.
func TestGitRepoContaining(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "local-dev", "cp-prod")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, inRepo := GitRepoContaining(nested)
	if !inRepo {
		t.Fatal("a repo nested under a working tree should be detected")
	}
	if resolved, _ := filepath.EvalSymlinks(got); resolved != mustResolve(t, root) {
		t.Errorf("root = %q, want %q", got, root)
	}
}

// The nearest working tree wins, so a nested checkout is reported rather than
// some distant ancestor.
func TestGitRepoContainingPrefersTheNearestRoot(t *testing.T) {
	outer := t.TempDir()
	if err := os.Mkdir(filepath.Join(outer, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "vendor", "inner")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(inner, "cp")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, inRepo := GitRepoContaining(deep)
	if !inRepo {
		t.Fatal("expected a working tree to be found")
	}
	if resolved, _ := filepath.EvalSymlinks(got); resolved != mustResolve(t, inner) {
		t.Errorf("root = %q, want the nearest (%q)", got, inner)
	}
}

// A path that cannot be stat'd is not a repo, and must not error.
func TestGitRepoContainingUnreadablePath(t *testing.T) {
	if _, inRepo := GitRepoContaining(filepath.Join(t.TempDir(), "does", "not", "exist")); inRepo {
		// Walking up from a missing path can still legitimately find a real
		// working tree above it, so only assert we do not panic or hang.
		t.Log("walked up to a real working tree; acceptable")
	}
}

func mustResolve(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
