package computing

import (
	"sync"
	"testing"
	"time"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
)

// fakeModels stands in for the registry.
type fakeModels struct {
	mu      sync.Mutex
	enabled map[string]bool
	calls   []string
}

func newFakeModels(state map[string]bool) *fakeModels {
	return &fakeModels{enabled: state}
}

func (f *fakeModels) DisableModel(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.enabled[id]; !ok {
		return ErrModelNotFound
	}
	f.enabled[id] = false
	f.calls = append(f.calls, "disable:"+id)
	return nil
}

func (f *fakeModels) EnableModel(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.enabled[id]; !ok {
		return ErrModelNotFound
	}
	f.enabled[id] = true
	f.calls = append(f.calls, "enable:"+id)
	return nil
}

func (f *fakeModels) IsModelEnabled(id string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.enabled[id]
	return e, ok
}

func (f *fakeModels) history() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func testSelfCheckCfg() conf.SelfCheck {
	c := conf.SelfCheck{IntervalMinutes: 10, FailuresBeforeDisable: 2}
	return c
}

func newTestRunner(models modelController, cfg conf.SelfCheck) *selfCheckRunner {
	return newSelfCheckRunner(
		alerts.New(conf.Alerts{}, "node", "cp"), // no transport: Fire is inert
		func() selfcheck.Options { return selfcheck.Options{} },
		func() conf.SelfCheck { return cfg },
		models,
	)
}

func probeReport(probes map[string]selfcheck.ProbeResult) selfcheck.Report {
	return selfcheck.Report{Probes: probes}
}

// The CLIProxy case: the backend answers /v1/models but every completion fails
// with 503. Two consecutive failures should pull the model from routing.
func TestBackendFailureDisablesAfterThreshold(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	fail := probeReport(map[string]selfcheck.ProbeResult{
		"org/a": {StatusCode: 503, Error: "auth_unavailable"},
	})

	r.act(fail)
	if got := models.history(); len(got) != 0 {
		t.Fatalf("disabled after a single failure: %v — one blip must not pull a model", got)
	}

	r.act(fail)
	if got := models.history(); len(got) != 1 || got[0] != "disable:org/a" {
		t.Fatalf("history = %v, want one disable after the second failure", got)
	}
	if enabled, _ := models.IsModelEnabled("org/a"); enabled {
		t.Error("model should be disabled")
	}
}

// A recovered backend must come back on its own; otherwise a transient failure
// costs a model permanently, which is worse than the problem.
func TestRecoveryReEnables(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	fail := probeReport(map[string]selfcheck.ProbeResult{"org/a": {StatusCode: 503}})
	r.act(fail)
	r.act(fail)

	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {OK: true, StatusCode: 200}}))

	if got := models.history(); len(got) != 2 || got[1] != "enable:org/a" {
		t.Fatalf("history = %v, want disable then enable", got)
	}
	if enabled, _ := models.IsModelEnabled("org/a"); !enabled {
		t.Error("model should be re-enabled")
	}
}

// A model an operator switched off by hand must never be switched back on.
func TestOperatorDisabledModelIsNeverReEnabled(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": false}) // disabled by a human
	r := newTestRunner(models, testSelfCheckCfg())

	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {OK: true, StatusCode: 200}}))

	if got := models.history(); len(got) != 0 {
		t.Fatalf("history = %v, want no action on an operator-disabled model", got)
	}
	if enabled, _ := models.IsModelEnabled("org/a"); enabled {
		t.Error("an operator's disable must survive a successful probe")
	}
}

// A 400 is the client's fault — an over-long prompt against a wider advertised
// window looks the same as a broken backend in a raw failure count. Disabling
// on it would remove a healthy, earning model.
func TestClientErrorsNeverDisable(t *testing.T) {
	for _, status := range []int{400, 422, 429} {
		models := newFakeModels(map[string]bool{"org/a": true})
		r := newTestRunner(models, testSelfCheckCfg())
		rep := probeReport(map[string]selfcheck.ProbeResult{"org/a": {StatusCode: status, Error: "client"}})
		for i := 0; i < 5; i++ {
			r.act(rep)
		}
		if got := models.history(); len(got) != 0 {
			t.Errorf("status %d: history = %v, want no disable", status, got)
		}
	}
}

// Failures must be consecutive: a success in between clears the count.
func TestSuccessResetsTheFailureCount(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {StatusCode: 500}}))
	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {OK: true}}))
	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {StatusCode: 500}}))

	if got := models.history(); len(got) != 0 {
		t.Fatalf("history = %v, want no disable — the failures were not consecutive", got)
	}
}

func TestAutoDisableCanBeTurnedOff(t *testing.T) {
	off := false
	cfg := testSelfCheckCfg()
	cfg.AutoDisable = &off

	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, cfg)

	fail := probeReport(map[string]selfcheck.ProbeResult{"org/a": {StatusCode: 503}})
	for i := 0; i < 5; i++ {
		r.act(fail)
	}
	if got := models.history(); len(got) != 0 {
		t.Fatalf("history = %v, want nothing when AutoDisable is false", got)
	}
}

func TestAutoRecoverCanBeTurnedOff(t *testing.T) {
	off := false
	cfg := testSelfCheckCfg()
	cfg.AutoRecover = &off

	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, cfg)

	fail := probeReport(map[string]selfcheck.ProbeResult{"org/a": {StatusCode: 503}})
	r.act(fail)
	r.act(fail)
	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {OK: true}}))

	if got := models.history(); len(got) != 1 || got[0] != "disable:org/a" {
		t.Fatalf("history = %v, want the disable to stand with AutoRecover off", got)
	}
}

// Non-chat models (embeddings, image) are not probed, so they must never be
// judged by a probe that never ran.
func TestSkippedModelsAreIgnored(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/embed": true})
	r := newTestRunner(models, testSelfCheckCfg())

	rep := probeReport(map[string]selfcheck.ProbeResult{"org/embed": {Skipped: true}})
	for i := 0; i < 5; i++ {
		r.act(rep)
	}
	if got := models.history(); len(got) != 0 {
		t.Fatalf("history = %v, want no action for a skipped model", got)
	}
}

// A model that vanished from the registry between the audit and acting on it
// must not produce an error or a spurious action.
func TestUnknownModelIsIgnored(t *testing.T) {
	models := newFakeModels(map[string]bool{})
	r := newTestRunner(models, testSelfCheckCfg())
	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/gone": {StatusCode: 503}}))
	if got := models.history(); len(got) != 0 {
		t.Fatalf("history = %v, want nothing for an unknown model", got)
	}
}

func TestIntervalComesFromConfig(t *testing.T) {
	cfg := conf.SelfCheck{IntervalMinutes: 10}
	if got := cfg.Interval(); got != 10*time.Minute {
		t.Errorf("Interval() = %v, want 10m", got)
	}
}
