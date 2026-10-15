package computing

import (
	"os"
	"path/filepath"
	"testing"
)

// A disabled model is offered to nobody, so an engine probe against it buys
// nothing; on a metered proxy it spends quota and records a failure every
// rotation for a model the operator already knows does not work.
func TestDisabledModelsGetNoEngineProbe(t *testing.T) {
	dir := t.TempDir()
	models := `{"org/off": {"endpoint": "http://127.0.0.1:1", "disabled": true},
	            "org/on":  {"endpoint": "http://127.0.0.1:1"}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	r := NewModelRegistry(dir, h)
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}

	if h.chatCompatible("org/off") {
		t.Error("a model disabled in models.json would be sent deep-check completions")
	}
	if !h.chatCompatible("org/on") {
		t.Error("an enabled model lost its deep check")
	}

	if err := r.EnableModel("org/off"); err != nil {
		t.Fatal(err)
	}
	if !h.chatCompatible("org/off") {
		t.Error("enabling a model did not restore its deep check")
	}
	if err := r.DisableModel("org/on"); err != nil {
		t.Fatal(err)
	}
	if h.chatCompatible("org/on") {
		t.Error("disabling a model through the API left its deep check running")
	}
}
