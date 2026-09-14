package modelmem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(day int) time.Time {
	return time.Date(2026, 9, day, 12, 0, 0, 0, time.UTC)
}

func workingRecord(id string, gib float64) Record {
	return Record{
		ModelID:  id,
		Weights:  Weights{LocalPath: "/models/" + id, Quantisation: "AWQ"},
		Serving:  Serving{Backend: "vllm", ContextLength: 32768},
		Hardware: Hardware{GPUModel: "NVIDIA GeForce RTX 3080", GPUCount: 4, VRAMPerGPUGB: 10},
		Measured: &Measurement{VRAMGiB: gib, PerGPUGiB: []float64{gib / 2, gib / 2}, At: at(14)},
	}
}

func TestRecordAndReload(t *testing.T) {
	dir := t.TempDir()
	store, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.RecordSuccess(at(14), workingRecord("TheDrummer/Cydonia-24B-v4.3", 18.68))
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := reloaded.Get("TheDrummer/Cydonia-24B-v4.3")
	if r == nil {
		t.Fatal("record was not persisted")
	}
	if !r.Works() || r.VRAMGiB() != 18.68 {
		t.Errorf("record = %+v", r)
	}
	if r.Successes != 1 {
		t.Errorf("successes = %d", r.Successes)
	}
}

// A start that did not measure must not erase a measurement taken before, and
// one that did not know the quantisation must not blank a field set by hand.
func TestRecordSuccessMergesRatherThanReplacing(t *testing.T) {
	store, _ := Load(t.TempDir())
	store.RecordSuccess(at(14), workingRecord("a/Model", 18.68))

	store.RecordSuccess(at(15), Record{
		ModelID: "a/Model",
		Serving: Serving{Backend: "vllm"},
	})

	r := store.Get("a/Model")
	if r.VRAMGiB() != 18.68 {
		t.Errorf("a later unmeasured start erased the measurement: %v", r.VRAMGiB())
	}
	if r.Weights.Quantisation != "AWQ" {
		t.Errorf("quantisation was blanked: %q", r.Weights.Quantisation)
	}
	if r.Successes != 2 {
		t.Errorf("successes = %d, want 2", r.Successes)
	}
}

// One failed start is most often something transient. Throwing away a
// known-good configuration over it would make the memory worse than useless.
func TestAFailureDoesNotEraseAWorkingRecord(t *testing.T) {
	store, _ := Load(t.TempDir())
	store.RecordSuccess(at(14), workingRecord("a/Model", 18.68))

	store.RecordFailure(at(15), "a/Model", "upstream returned 502")

	r := store.Get("a/Model")
	if !r.Works() {
		t.Error("a model that has worked was marked failed after one bad start")
	}
	if r.VRAMGiB() != 18.68 {
		t.Error("the measurement was lost")
	}
	if r.Failures != 1 {
		t.Errorf("failures = %d", r.Failures)
	}
}

// A model that has never worked here is marked failed, with the reason.
func TestAModelThatNeverWorkedIsMarkedFailed(t *testing.T) {
	store, _ := Load(t.TempDir())
	store.RecordFailure(at(14), "big/Model", "OOM at 64k context")

	r := store.Get("big/Model")
	if r.Works() {
		t.Error("a model that never started was marked working")
	}
	if !strings.Contains(r.Note, "OOM") {
		t.Errorf("note = %q", r.Note)
	}
}

// The operator saying "I know this works" must survive automatic writes.
func TestPinnedRecordsAreNotOverwritten(t *testing.T) {
	store, _ := Load(t.TempDir())
	store.RecordSuccess(at(14), workingRecord("a/Model", 18.68))
	store.Pin("a/Model", true)

	store.RecordSuccess(at(15), Record{
		ModelID:  "a/Model",
		Measured: &Measurement{VRAMGiB: 99, At: at(15)},
		Note:     "overwritten",
	})

	r := store.Get("a/Model")
	if r.VRAMGiB() != 18.68 {
		t.Errorf("a pinned measurement was overwritten: %v", r.VRAMGiB())
	}
	if r.Note == "overwritten" {
		t.Error("a pinned note was overwritten")
	}
	if r.Successes != 2 {
		t.Errorf("the success was not counted: %d", r.Successes)
	}
}

// A measurement does not transfer between machines or upward in scale.
func TestAppliesTo(t *testing.T) {
	hw := Hardware{GPUModel: "NVIDIA GeForce RTX 3080", GPUCount: 4, VRAMPerGPUGB: 10}
	base := workingRecord("a/Model", 18.68)
	store, _ := Load(t.TempDir())
	r := store.RecordSuccess(at(14), base)

	if !r.AppliesTo(hw, 32768, 1) {
		t.Error("a record did not apply to the hardware it was measured on")
	}
	// Asking about less context than was measured is safe.
	if !r.AppliesTo(hw, 8192, 1) {
		t.Error("a record measured at 32k did not apply to an 8k question")
	}
	// Asking about more is not.
	if r.AppliesTo(hw, 131072, 1) {
		t.Error("a record measured at 32k was reused for a 128k question")
	}
	// Different hardware never applies.
	if r.AppliesTo(Hardware{GPUModel: "NVIDIA H100", GPUCount: 8, VRAMPerGPUGB: 80}, 32768, 1) {
		t.Error("a record was reused across different hardware")
	}
	// Neither does a failed one.
	failed := &Record{ModelID: "b/Model", Status: StatusFailed, Measured: &Measurement{VRAMGiB: 5}}
	if failed.AppliesTo(hw, 1024, 1) {
		t.Error("a failed record was treated as reusable")
	}
}

// This is the record of configurations that took real effort to find.
// Discarding it because one byte went wrong is not acceptable.
func TestACorruptStoreIsAnErrorNotAReset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StoreFile), []byte(`{"records": `), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a corrupt store was silently reset")
	}
}

// cp.md is generated from the store on every write, so the two cannot drift.
func TestSaveRegeneratesTheMarkdown(t *testing.T) {
	dir := t.TempDir()
	store, _ := Load(dir)
	store.SetNodeContext(NodeContext{NodeName: "Nova", GPUModel: "RTX 3080", GPUCount: 4, VRAMPerGPUGB: 10, TotalVRAMGB: 40})
	store.RecordSuccess(at(14), workingRecord("TheDrummer/Cydonia-24B-v4.3", 18.68))
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, MarkdownFile))
	if err != nil {
		t.Fatal(err)
	}
	md := string(data)

	for _, want := range []string{
		"Cydonia-24B-v4.3", // the model
		"18.68",            // the measurement
		"40 GB total",      // the node's limit
		"Do not edit",      // it is generated
		"models serve",     // the reproduction command
	} {
		if !strings.Contains(md, want) {
			t.Errorf("cp.md does not mention %q", want)
		}
	}
}

// The most useful thing the memory holds: an agent re-serving a known-good
// model should run what worked, not reconstruct it and get a flag wrong.
func TestReproduceCommandCarriesTheSettingsThatWorked(t *testing.T) {
	r := &Record{
		ModelID:  "Qwen/Qwen3.8-27B",
		Status:   StatusWorking,
		Weights:  Weights{LocalPath: "/models/q.gguf"},
		Serving:  Serving{Backend: "llamacpp", GPUs: "2,3", ContextLength: 65536, ExtraArgs: []string{"-fa", "on"}},
		Measured: &Measurement{VRAMGiB: 17.4},
	}
	cmd := ReproduceCommand(r)
	for _, want := range []string{
		"--backend llamacpp", "--weights /models/q.gguf", "--gpus 2,3",
		"--context-length 65536", "Qwen/Qwen3.8-27B", "-fa on",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command is missing %q:\n%s", want, cmd)
		}
	}

	// A record with nothing to reproduce must produce no command rather than
	// a broken one.
	if ReproduceCommand(&Record{ModelID: "x", Status: StatusFailed}) != "" {
		t.Error("a failed record produced a reproduction command")
	}
}
