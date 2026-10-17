package computing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func newLimitsService(t *testing.T, modelsJSON string) *InferenceService {
	t.Helper()
	dir := t.TempDir()
	writeModelsJSON(t, dir, modelsJSON)
	s := NewInferenceService("test-node", dir)
	s.registry = NewModelRegistry(dir, nil)
	s.rateLimiter = NewRateLimiter(DefaultRateLimiterConfig(), nil)
	s.concurrencyLimiter = NewConcurrencyLimiter(DefaultConcurrencyConfig(), nil)
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	s.client.SetModelCapacityProvider(s.concurrencyLimiter.ModelCapacity)
	if err := s.registry.loadConfig(); err != nil {
		t.Fatal(err)
	}
	for _, m := range s.registry.GetAllModels() {
		s.modelMappings[m.ID] = mappingFor(m)
		s.applyModelLimits(m.ID, limitsOf(m))
	}
	return s
}

func readLimitsEntry(t *testing.T, dir, id string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	return all[id]
}

// The models.json fields reach both limiters, and the declared capacity the
// hub is told follows max_concurrent rather than the built-in default.
func TestModelsJSONLimitsAreAppliedAndDeclared(t *testing.T) {
	s := newLimitsService(t, `{"m": {"endpoint": "http://127.0.0.1:1", "max_concurrent": 2, "rpm": 30},
	                          "d": {"endpoint": "http://127.0.0.1:2"}}`)

	if got := s.concurrencyLimiter.ModelCapacity("m"); got != 2 {
		t.Fatalf("declared capacity for m = %d, want 2", got)
	}
	if got := s.concurrencyLimiter.ModelCapacity("d"); got != DefaultConcurrencyConfig().DefaultModelMax {
		t.Fatalf("declared capacity for d = %d, want the default", got)
	}
	rate, burst, ok := s.rateLimiter.ModelLimit("m")
	if !ok || rate != 0.5 || burst != 30 {
		t.Fatalf("rate limit for m = %.2f/s burst %d ok=%v, want 0.50/s burst 30", rate, burst, ok)
	}
	if _, _, ok := s.rateLimiter.ModelLimit("d"); ok {
		t.Fatal("d has no rpm, yet has its own bucket")
	}
	s.client.SetModelList([]string{"m", "d"})
	declared := map[string]int64{}
	for _, d := range s.client.buildModelDeclarations() {
		for _, c := range d.Capacity {
			if c.Type == "concurrency" {
				declared[d.ModelID] = c.Value
			}
		}
	}
	if declared["m"] != 2 || declared["d"] != int64(DefaultConcurrencyConfig().DefaultModelMax) {
		t.Fatalf("declared concurrency = %v", declared)
	}
}

// 30 rpm is a per-minute statement, so thirty arriving together pass and the
// thirty-first in the same minute does not.
func TestRPMAdmitsAMinutesWorthAtOnce(t *testing.T) {
	s := newLimitsService(t, `{"m": {"endpoint": "http://127.0.0.1:1", "rpm": 30}}`)
	for i := 0; i < 30; i++ {
		if !s.rateLimiter.AllowModel("m") {
			t.Fatalf("request %d of 30 refused under a 30 rpm limit", i+1)
		}
	}
	if s.rateLimiter.AllowModel("m") {
		t.Fatal("31st request in the minute admitted")
	}
}

// Removing the fields from models.json returns the model to the defaults; the
// semaphore is kept, so a slot held across the change is released cleanly.
func TestRemovingLimitsRestoresDefaults(t *testing.T) {
	s := newLimitsService(t, `{"m": {"endpoint": "http://127.0.0.1:1", "max_concurrent": 1, "rpm": 30}}`)
	tok, err := s.concurrencyLimiter.Acquire(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	s.applyModelLimits("m", ModelLimits{})
	if got := s.concurrencyLimiter.ModelCapacity("m"); got != DefaultConcurrencyConfig().DefaultModelMax {
		t.Fatalf("capacity after clearing = %d, want the default", got)
	}
	if s.concurrencyLimiter.ModelMaxPinned("m") {
		t.Fatal("cleared limit is still pinned against the GPU loop")
	}
	if _, _, ok := s.rateLimiter.ModelLimit("m"); ok {
		t.Fatal("cleared rpm left its bucket behind")
	}
	tok.Release()
	if active := s.concurrencyLimiter.GetMetrics().PerModelActive["m"]; active != 0 {
		t.Fatalf("active after release = %d, want 0", active)
	}
}

// An operator's limit is not resized by the GPU-aware loop, which would
// otherwise undo it within ten seconds on any model with gpu_memory set.
func TestGPULoopLeavesOperatorLimitsAlone(t *testing.T) {
	cl := NewConcurrencyLimiter(DefaultConcurrencyConfig(), nil)
	cl.SetModelMax("pinned", 2)
	cl.mu.Lock()
	cl.modelGPUMemory["pinned"] = 1000
	cl.modelGPUMemory["free"] = 1000
	cl.modelSems["free"] = NewSemaphore(cl.config.DefaultModelMax)
	cl.mu.Unlock()
	cl.resizeForGPU(3000) // room for three instances of either
	if got := cl.ModelCapacity("pinned"); got != 2 {
		t.Fatalf("operator limit resized to %d", got)
	}
	if got := cl.ModelCapacity("free"); got != 3 {
		t.Fatalf("unpinned model not resized: %d, want 3", got)
	}
}

// Setting limits through the API saves them to the model's entry without
// touching anything else in it, and a zero removes the key rather than
// writing an explicit 0.
func TestSetModelLimitsPersistsIntoTheEntry(t *testing.T) {
	s := newLimitsService(t, `{"m": {"endpoint": "http://127.0.0.1:1", "api_key": "k", "unknown_field": [1,2]}}`)
	dir := s.registry.configPath

	if err := s.SetModelLimits("m", ModelLimits{MaxConcurrent: 2, RPM: 30}); err != nil {
		t.Fatal(err)
	}
	entry := readLimitsEntry(t, dir, "m")
	if string(entry["max_concurrent"]) != "2" || string(entry["rpm"]) != "30" {
		t.Fatalf("entry after set = %s", entry)
	}
	var unknown []int
	if string(entry["api_key"]) != `"k"` || json.Unmarshal(entry["unknown_field"], &unknown) != nil || len(unknown) != 2 {
		t.Fatalf("other fields were not kept: %s", entry)
	}
	if got := s.ModelLimitsFor("m"); got != (ModelLimits{MaxConcurrent: 2, RPM: 30}) {
		t.Fatalf("status shows %+v", got)
	}
	if got := s.concurrencyLimiter.ModelCapacity("m"); got != 2 {
		t.Fatalf("capacity = %d, want 2", got)
	}

	if err := s.SetModelLimits("m", ModelLimits{MaxConcurrent: 2}); err != nil {
		t.Fatal(err)
	}
	entry = readLimitsEntry(t, dir, "m")
	if _, has := entry["rpm"]; has {
		t.Fatalf("rpm 0 should remove the key, got %s", entry["rpm"])
	}
	if _, _, ok := s.rateLimiter.ModelLimit("m"); ok {
		t.Fatal("rpm cleared in the file but not in the limiter")
	}

	// The reload the write triggers sees the registry already holding the
	// new values, so nothing fires.
	updated := make(chan string, 1)
	s.registry.onModelUpdated = func(m *RegisteredModel) { updated <- m.ID }
	if err := s.registry.loadConfig(); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-updated:
		t.Fatalf("reload after an API set reported %s as changed", id)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSetModelLimitsRefusesBadInput(t *testing.T) {
	s := newLimitsService(t, `{"m": {"endpoint": "http://127.0.0.1:1"}}`)
	if err := s.SetModelLimits("m", ModelLimits{MaxConcurrent: -1}); err == nil {
		t.Fatal("negative max_concurrent accepted")
	}
	if err := s.SetModelLimits("nope", ModelLimits{MaxConcurrent: 1}); err == nil {
		t.Fatal("unknown model accepted")
	}
}

// A change to either field in models.json is a change the registry reports,
// so a hot reload applies it; an unchanged file reports nothing.
func TestReloadNoticesLimitChanges(t *testing.T) {
	dir := t.TempDir()
	writeModelsJSON(t, dir, `{"m": {"endpoint": "http://127.0.0.1:1", "max_concurrent": 2}}`)
	r := NewModelRegistry(dir, nil)
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	var updates atomic.Int32
	r.onModelUpdated = func(*RegisteredModel) { updates.Add(1) }
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	writeModelsJSON(t, dir, `{"m": {"endpoint": "http://127.0.0.1:1", "max_concurrent": 2, "rpm": 30}}`)
	if err := r.loadConfig(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // the callback runs on its own goroutine
	if n := updates.Load(); n != 1 {
		t.Fatalf("updates = %d, want 1 (none for the unchanged file, one for the rpm)", n)
	}
	if m, _ := r.GetModel("m"); m.RPM != 30 || m.MaxConcurrent != 2 {
		t.Fatalf("after reload: %+v", limitsOf(m))
	}
}
