package computing

import (
	"os"
	"path/filepath"
	"testing"
)

func writeModelsJSON(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// "disabled": true in models.json keeps a model off across restarts, where an
// API disable lasts only for the life of the process. A reload applies the
// file's value only when it changes, so it does not undo an API decision.
func TestModelsJSONDisabledPersists(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{"m": {"endpoint": "http://127.0.0.1:1", "disabled": true}}`)

	r := NewModelRegistry(dir, nil)
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	if m := r.models["m"]; m.Enabled || m.State != ModelStateDisabled {
		t.Fatalf("new process: enabled=%v state=%v, want disabled", m.Enabled, m.State)
	}

	if err := r.EnableModel("m"); err != nil {
		t.Fatal(err)
	}
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	if !r.models["m"].Enabled {
		t.Fatal("reload of an unchanged file undid an API enable")
	}

	if err := r.DisableModel("m"); err != nil {
		t.Fatal(err)
	}
	writeModelsJSON(t, dir, `{"m": {"endpoint": "http://127.0.0.1:1"}}`)
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	if m := r.models["m"]; !m.Enabled || m.State == ModelStateDisabled {
		t.Fatalf("after removing disabled: enabled=%v state=%v, want enabled", m.Enabled, m.State)
	}
}
