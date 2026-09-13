package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeModelsFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(ModelsPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readModelsFile(t *testing.T, dir string) map[string]map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(ModelsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("models.json is not valid JSON: %v\n%s", err, data)
	}
	return out
}

func TestRegisterModelCreatesTheFile(t *testing.T) {
	dir := t.TempDir()
	spec := &ServeSpec{ModelID: "Qwen/Qwen2.5-7B-Instruct", ContextLength: 32768, GPUMemory: 14000}
	inst := &Instance{Endpoint: "http://localhost:30000"}

	if err := RegisterModel(dir, spec, inst); err != nil {
		t.Fatal(err)
	}

	got := readModelsFile(t, dir)["Qwen/Qwen2.5-7B-Instruct"]
	if got["endpoint"] != "http://localhost:30000" {
		t.Errorf("endpoint = %v", got["endpoint"])
	}
	if got["category"] != "text-generation" {
		t.Errorf("category = %v, want the default text-generation", got["category"])
	}
	if got["context_length"] != float64(32768) {
		t.Errorf("context_length = %v", got["context_length"])
	}
	if got["gpu_memory"] != float64(14000) {
		t.Errorf("gpu_memory = %v", got["gpu_memory"])
	}
}

// models.json is operator-authored and has gained fields more than once. A
// read-modify-write that decoded it through a fixed struct would drop whatever
// this binary was not compiled to understand — including from models it was
// not even asked to touch.
func TestRegisterModelPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeModelsFile(t, dir, `{
  "other/Model": {"endpoint": "http://localhost:9999", "some_future_field": {"nested": true}},
  "Qwen/Qwen2.5-7B-Instruct": {"endpoint": "http://localhost:1", "api_key": "sk-local", "another_future_field": 7}
}`)

	spec := &ServeSpec{ModelID: "Qwen/Qwen2.5-7B-Instruct"}
	if err := RegisterModel(dir, spec, &Instance{Endpoint: "http://localhost:30000"}); err != nil {
		t.Fatal(err)
	}

	models := readModelsFile(t, dir)

	untouched := models["other/Model"]
	if untouched["some_future_field"] == nil {
		t.Error("a field on an unrelated model was dropped")
	}

	updated := models["Qwen/Qwen2.5-7B-Instruct"]
	if updated["endpoint"] != "http://localhost:30000" {
		t.Errorf("endpoint = %v, want the new one", updated["endpoint"])
	}
	if updated["api_key"] != "sk-local" {
		t.Error("api_key was dropped by a restart")
	}
	if updated["another_future_field"] != float64(7) {
		t.Error("an unknown field on the updated model was dropped")
	}
}

// The file can carry a backend's api_key, so it must not be world-readable.
func TestRegisterModelWritesRestrictivePermissions(t *testing.T) {
	dir := t.TempDir()
	if err := RegisterModel(dir, &ServeSpec{ModelID: "m"}, &Instance{Endpoint: "http://localhost:1"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ModelsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != modelsFileMode {
		t.Errorf("models.json mode = %o, want %o", perm, modelsFileMode)
	}
}

func TestDeregisterModelRemovesOnlyItsOwnEntry(t *testing.T) {
	dir := t.TempDir()
	writeModelsFile(t, dir, `{
  "a/Model": {"endpoint": "http://localhost:30000"},
  "b/Model": {"endpoint": "http://localhost:30001"}
}`)

	if err := DeregisterModel(dir, "a/Model", "http://localhost:30000"); err != nil {
		t.Fatal(err)
	}

	models := readModelsFile(t, dir)
	if _, ok := models["a/Model"]; ok {
		t.Error("a/Model was not removed")
	}
	if _, ok := models["b/Model"]; !ok {
		t.Error("b/Model was removed too")
	}
}

// If models.json has since been pointed at a different server for this model,
// the entry no longer describes the container being stopped. Removing it would
// deregister a model that is still being served.
func TestDeregisterModelLeavesAReassignedEntry(t *testing.T) {
	dir := t.TempDir()
	writeModelsFile(t, dir, `{"a/Model": {"endpoint": "http://localhost:39999"}}`)

	if err := DeregisterModel(dir, "a/Model", "http://localhost:30000"); err != nil {
		t.Fatal(err)
	}

	if _, ok := readModelsFile(t, dir)["a/Model"]; !ok {
		t.Error("an entry pointing at a different server was removed")
	}
}

func TestDeregisterModelIsAnEmptyOperationWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := DeregisterModel(dir, "missing/Model", "http://localhost:1"); err != nil {
		t.Fatalf("deregistering an absent model should succeed: %v", err)
	}
	writeModelsFile(t, dir, `{"a/Model": {"endpoint": "http://localhost:1"}}`)
	if err := DeregisterModel(dir, "missing/Model", ""); err != nil {
		t.Fatal(err)
	}
	if len(readModelsFile(t, dir)) != 1 {
		t.Error("an unrelated entry was disturbed")
	}
}

func TestRegisteredEndpoint(t *testing.T) {
	dir := t.TempDir()
	if _, declared, err := RegisteredEndpoint(dir, "a/Model"); err != nil || declared {
		t.Errorf("no models.json: declared = %v, err = %v", declared, err)
	}

	writeModelsFile(t, dir, `{"a/Model": {"endpoint": "http://localhost:30001"}}`)
	endpoint, declared, err := RegisteredEndpoint(dir, "a/Model")
	if err != nil {
		t.Fatal(err)
	}
	if !declared || endpoint != "http://localhost:30001" {
		t.Errorf("endpoint = %q, declared = %v", endpoint, declared)
	}
}

func TestReadModelsRejectsCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	writeModelsFile(t, dir, `{"a/Model": `)

	// Failing is the point: overwriting a file that failed to parse would
	// discard whatever the operator had in it.
	if err := RegisterModel(dir, &ServeSpec{ModelID: "b/Model"}, &Instance{Endpoint: "http://x"}); err == nil {
		t.Fatal("expected an error rather than a silent overwrite")
	}
	data, err := os.ReadFile(ModelsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"a/Model": ` {
		t.Errorf("the unparseable file was modified: %s", data)
	}
}

// The daemon watches models.json with fsnotify. A truncate-then-write would be
// observed as an empty file and every model deregistered for as long as the
// write took, so the replacement has to be atomic.
func TestWriteModelsLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	if err := RegisterModel(dir, &ServeSpec{ModelID: "a/Model"}, &Instance{Endpoint: "http://x"}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Base(e.Name()) != "models.json" {
			t.Errorf("left behind %s", e.Name())
		}
	}
}
