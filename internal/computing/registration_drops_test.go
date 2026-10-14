package computing

import (
	"strings"
	"testing"
)

// A model leaving the registered set is named with the reason, and counted,
// so an operator need not diff two registration lines to find it.
func TestRegistrationDropsNameTheModelAndReason(t *testing.T) {
	s := NewInferenceService("test-node", t.TempDir())
	s.registry = NewModelRegistry("", nil)
	s.registry.models["org/steady"] = &RegisteredModel{ID: "org/steady", Enabled: true, State: ModelStateReady, Health: ModelHealthHealthy}
	s.registry.models["org/flaky"] = &RegisteredModel{ID: "org/flaky", Enabled: true, State: ModelStateReady, Health: ModelHealthHealthy}
	s.registry.models["org/off"] = &RegisteredModel{ID: "org/off", Enabled: true, State: ModelStateReady, Health: ModelHealthHealthy}
	s.healthChecker = NewModelHealthChecker(DefaultHealthCheckConfig())
	s.client = &InferenceClient{metrics: NewInferenceMetrics()}
	// The startup registration is config.toml's list, which may name a model
	// that is disabled; the first ready set replacing it is not a drop.
	s.client.SetModelList([]string{"org/steady", "org/flaky", "org/off", "org/stale"})

	s.updateClientModels()
	if d := s.GetRegistrationDrops(); len(d) != 0 {
		t.Fatalf("replacing the config.toml list counted drops: %v", d)
	}

	setHealth := func(id string, h ModelHealth, state ModelState) {
		s.registry.mu.Lock()
		s.registry.models[id].Health, s.registry.models[id].State = h, state
		s.registry.mu.Unlock()
	}
	setHealth("org/flaky", ModelHealthUnhealthy, ModelStateUnhealthy)
	s.healthChecker.mu.Lock()
	s.healthChecker.statuses["org/flaky"] = &ModelStatus{ModelID: "org/flaky", ConsecutiveFails: 3, LastDeepError: "timeout after 30s"}
	s.healthChecker.mu.Unlock()
	if err := s.registry.DisableModel("org/off"); err != nil {
		t.Fatal(err)
	}
	s.updateClientModels()

	setHealth("org/flaky", ModelHealthHealthy, ModelStateReady)
	s.updateClientModels()
	setHealth("org/flaky", ModelHealthUnhealthy, ModelStateUnhealthy)
	s.updateClientModels()

	d := s.GetRegistrationDrops()
	if _, ok := d["org/steady"]; ok {
		t.Errorf("a model that never left was counted: %v", d)
	}
	if f := d["org/flaky"]; f.Count != 2 || !strings.Contains(f.LastReason, "failed 3 time(s)") || !strings.Contains(f.LastReason, "timeout after 30s") {
		t.Errorf("flaky = %+v, want 2 drops with the probe failure as the reason", f)
	}
	if o := d["org/off"]; o.Count != 1 || o.LastReason != "disabled" {
		t.Errorf("off = %+v, want 1 drop, reason disabled", o)
	}
}
