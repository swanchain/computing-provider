package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConfig = `# Computing provider configuration
[API]
  Port = 9087

[Inference]
  Enable = true
  # the models this node declares
  Models = ["TheDrummer/Cydonia-24B-v4.3", "Qwen/Qwen3.8-27B"]
  WebSocketURL = "wss://inference-ws.swanchain.io"

[Inference.AutoSwitch]
  Enable = false
  Models = ["should-not-be-touched"]
`

func TestReplaceInferenceModels(t *testing.T) {
	got, changed, err := replaceInferenceModels(sampleConfig, []string{"Qwen/Qwen3.8-27B", "zai-org/GLM-4.7-Flash"})
	if err != nil {
		t.Fatalf("replaceInferenceModels: %v", err)
	}
	if !changed {
		t.Fatal("expected the config to change")
	}
	if !strings.Contains(got, `  Models = ["Qwen/Qwen3.8-27B", "zai-org/GLM-4.7-Flash"]`) {
		t.Errorf("Models line not rewritten:\n%s", got)
	}
	if strings.Contains(got, "Cydonia") {
		t.Error("the removed model is still listed")
	}
}

// The operator's own file: comments, spacing and unrelated keys must survive,
// since this rewrites a hand-edited config rather than re-encoding one.
func TestReplaceInferenceModelsPreservesTheRestOfTheFile(t *testing.T) {
	got, _, err := replaceInferenceModels(sampleConfig, []string{"a/b"})
	if err != nil {
		t.Fatalf("replaceInferenceModels: %v", err)
	}
	for _, want := range []string{
		"# Computing provider configuration",
		"  Port = 9087",
		"  # the models this node declares",
		`  WebSocketURL = "wss://inference-ws.swanchain.io"`,
		"[Inference.AutoSwitch]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from the config:\n%s", want, got)
		}
	}
}

// [Inference.AutoSwitch] is a different table. Its own Models key is not the
// declared-model list and must not be rewritten.
func TestReplaceInferenceModelsLeavesSubtableAlone(t *testing.T) {
	got, _, err := replaceInferenceModels(sampleConfig, []string{"a/b"})
	if err != nil {
		t.Fatalf("replaceInferenceModels: %v", err)
	}
	if !strings.Contains(got, `Models = ["should-not-be-touched"]`) {
		t.Errorf("the AutoSwitch Models list was modified:\n%s", got)
	}
}

func TestReplaceInferenceModelsMultiLineArray(t *testing.T) {
	cfg := `[Inference]
  Models = [
    "one/a",
    "two/b",
  ]
  Enable = true
`
	got, changed, err := replaceInferenceModels(cfg, []string{"three/c"})
	if err != nil {
		t.Fatalf("replaceInferenceModels: %v", err)
	}
	if !changed {
		t.Fatal("expected a change")
	}
	if !strings.Contains(got, `  Models = ["three/c"]`) {
		t.Errorf("multi-line array not replaced:\n%s", got)
	}
	if !strings.Contains(got, "  Enable = true") {
		t.Errorf("the key after the array was eaten:\n%s", got)
	}
}

// A config that never declared Models is left alone rather than having the key
// invented in it.
func TestReplaceInferenceModelsAbsentKeyIsLeftAlone(t *testing.T) {
	cfg := "[Inference]\n  Enable = true\n"
	got, changed, err := replaceInferenceModels(cfg, []string{"a/b"})
	if err != nil {
		t.Fatalf("replaceInferenceModels: %v", err)
	}
	if changed || got != cfg {
		t.Errorf("config was modified when it declared no Models key:\n%s", got)
	}
}

func TestReplaceInferenceModelsNoInferenceTable(t *testing.T) {
	cfg := "[API]\n  Port = 1\n"
	got, changed, err := replaceInferenceModels(cfg, []string{"a/b"})
	if err != nil {
		t.Fatalf("replaceInferenceModels: %v", err)
	}
	if changed || got != cfg {
		t.Errorf("config without an [Inference] table was modified:\n%s", got)
	}
}

func TestReplaceInferenceModelsUnterminatedArrayIsAnError(t *testing.T) {
	cfg := "[Inference]\n  Models = [\"a/b\"\n"
	if _, _, err := replaceInferenceModels(cfg, []string{"c/d"}); err == nil {
		t.Error("expected an error for an unterminated array, got none")
	}
}

// Writing must not change the file's permissions: config.toml holds the
// provider API key and is deliberately not world-readable.
func TestWriteConfigModelsKeepsFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigModels(path, []string{"a/b"}); err != nil {
		t.Fatalf("writeConfigModels: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Models = ["a/b"]`) {
		t.Errorf("file not updated:\n%s", data)
	}
}

func TestWriteConfigModelsMissingFileIsNotAnError(t *testing.T) {
	if err := writeConfigModels(filepath.Join(t.TempDir(), "config.toml"), []string{"a/b"}); err != nil {
		t.Errorf("missing config.toml should be ignored, got %v", err)
	}
}

// The end to end shape: models.json is the source of truth, config.toml follows.
func TestSyncConfigModelsFollowsModelsJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(sampleConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	models := `{"zai-org/GLM-4.7-Flash":{"endpoint":"http://localhost:30000"},"Qwen/Qwen3.8-27B":{"endpoint":"http://localhost:30001"}}`
	if err := os.WriteFile(ModelsPath(dir), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SyncConfigModels(dir); err != nil {
		t.Fatalf("SyncConfigModels: %v", err)
	}
	data, err := os.ReadFile(ConfigPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	// Sorted, so the list is stable rather than reordering on every write.
	if !strings.Contains(string(data), `Models = ["Qwen/Qwen3.8-27B", "zai-org/GLM-4.7-Flash"]`) {
		t.Errorf("config.toml does not match models.json:\n%s", data)
	}
}
