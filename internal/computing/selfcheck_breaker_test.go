package computing

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
)

func hubFailure(model string, status int, reason string) RequestMetric {
	return RequestMetric{Source: SourceHub, Model: model, StatusCode: status, ErrorReason: reason}
}

func hubSuccess(model string) RequestMetric {
	return RequestMetric{Source: SourceHub, Model: model, Success: true, StatusCode: 200}
}

// waitForCalls waits for the breaker's asynchronous disable to land.
func waitForCalls(t *testing.T, m *fakeModels, n int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := m.history(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return m.history()
}

// settle gives a disable that should not happen the chance to happen anyway.
func settle(t *testing.T, m *fakeModels) []string {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	return m.history()
}

// The reason the breaker exists: a backend that dies under load must leave
// routing after a few failed requests, not at the next audit.
func TestBreakerTripsAfterConsecutiveBackendFailures(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	r.ObserveRequest(hubFailure("org/a", 502, "connection refused"))
	r.ObserveRequest(hubFailure("org/a", 503, "engine dead"))
	if got := settle(t, models); len(got) != 0 {
		t.Fatalf("tripped after two failures: %v", got)
	}

	r.ObserveRequest(hubFailure("org/a", 500, "CUDA error"))
	if got := waitForCalls(t, models, 1); len(got) != 1 || got[0] != "disable:org/a" {
		t.Fatalf("history = %v, want one disable after the third failure", got)
	}
	held := r.AutoDisabledModels()
	if reason := held["org/a"]; reason == "" {
		t.Fatalf("auto_disabled_models = %v, want org/a with a reason", held)
	}
}

// One good answer means the backend is serving; the failures before it were
// not an outage.
func TestBreakerSuccessResetsTheStreak(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	r.ObserveRequest(hubSuccess("org/a"))
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	if got := settle(t, models); len(got) != 0 {
		t.Fatalf("tripped across a success: %v", got)
	}
}

// A prompt the backend rejects, or this node's own limiter, is the backend
// answering — it must neither count nor let an older streak survive.
func TestBreakerIgnoresClientOwnedFailures(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	for i := 0; i < 5; i++ {
		r.ObserveRequest(hubFailure("org/a", 400, "prompt too long"))
		r.ObserveRequest(hubFailure("org/a", 429, "concurrency limit"))
	}
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	r.ObserveRequest(hubFailure("org/a", 400, "prompt too long"))
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	if got := settle(t, models); len(got) != 0 {
		t.Fatalf("client-owned failures pulled a healthy model: %v", got)
	}
}

// The hub giving up on a request, or the node shutting down under it, says
// nothing about the backend in either direction.
func TestBreakerIgnoresCancellations(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	for i := 0; i < 5; i++ {
		r.ObserveRequest(hubFailure("org/a", 0, cancelledError("consumer_disconnected")))
		r.ObserveRequest(hubFailure("org/a", 502, `Post "http://127.0.0.1:30000/v1/chat/completions": context canceled`))
	}
	if got := settle(t, models); len(got) != 0 {
		t.Fatalf("cancellations tripped the breaker: %v", got)
	}

	// Nor do they break up a real streak.
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	r.ObserveRequest(hubFailure("org/a", 0, cancelledError("timeout")))
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	if got := waitForCalls(t, models, 1); len(got) != 1 {
		t.Fatalf("history = %v, want a trip on three faults around a cancel", got)
	}
}

// Only routed work is what the hub scores. The operator's own benchmark
// hammering a rate-limited proxy through the local gateway must not take the
// model off the marketplace.
func TestBreakerIgnoresTrafficThatIsNotRouted(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	for _, src := range []RequestSource{SourceLocal, SourceHealth, SourceSelfCheck} {
		for i := 0; i < 5; i++ {
			req := hubFailure("org/a", 502, "x")
			req.Source = src
			r.ObserveRequest(req)
		}
	}
	if got := settle(t, models); len(got) != 0 {
		t.Fatalf("non-routed failures tripped the breaker: %v", got)
	}
}

// Three failures spread over a quiet week are not an outage.
func TestBreakerForgetsOldFailures(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())
	clock := time.Unix(1_800_000_000, 0)
	r.now = func() time.Time { return clock }

	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	clock = clock.Add(2 * time.Minute)
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	clock = clock.Add(2 * time.Minute)
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	if got := settle(t, models); len(got) != 0 {
		t.Fatalf("failures minutes apart tripped the breaker: %v", got)
	}

	clock = clock.Add(10 * time.Second)
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	clock = clock.Add(10 * time.Second)
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	if got := waitForCalls(t, models, 1); len(got) != 1 {
		t.Fatalf("history = %v, want a trip once three failures fall inside the window", got)
	}
}

// A burst of concurrent failures is one outage and one disable, not one per
// request.
func TestBreakerDisablesOnceUnderConcurrentFailures(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.ObserveRequest(hubFailure("org/a", 502, "x"))
		}()
	}
	wg.Wait()
	if got := settle(t, models); len(got) != 1 {
		t.Fatalf("history = %v, want exactly one disable", got)
	}
}

// A model the operator already switched off is theirs. The breaker must not
// claim it, or the recovery probe would switch it back on.
func TestBreakerNeverClaimsAnOperatorDisabledModel(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": false})
	r := newTestRunner(models, testSelfCheckCfg())
	r.probeOne = func(string) (selfcheck.ProbeResult, error) {
		return selfcheck.ProbeResult{OK: true, StatusCode: 200}, nil
	}

	for i := 0; i < 3; i++ {
		r.ObserveRequest(hubFailure("org/a", 502, "x")) // in flight when it was disabled
	}
	settle(t, models)
	r.probeHeld()
	if got := models.history(); len(got) != 0 {
		t.Fatalf("history = %v, want no action on an operator-disabled model", got)
	}
	if held := r.AutoDisabledModels(); len(held) != 0 {
		t.Fatalf("auto_disabled_models = %v, want none", held)
	}
}

// The fast recovery probe is what makes the breaker cheap to trip: the model
// is back within a minute of the backend recovering.
func TestRecoveryProbeReturnsATrippedModel(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	for i := 0; i < 3; i++ {
		r.ObserveRequest(hubFailure("org/a", 502, "x"))
	}
	waitForCalls(t, models, 1)

	healthy := false
	var probed []string
	r.probeOne = func(id string) (selfcheck.ProbeResult, error) {
		probed = append(probed, id)
		if healthy {
			return selfcheck.ProbeResult{OK: true, StatusCode: 200}, nil
		}
		return selfcheck.ProbeResult{Error: "connection refused"}, nil
	}

	r.probeHeld()
	if got := models.history(); len(got) != 1 {
		t.Fatalf("re-enabled while the backend still fails: %v", got)
	}

	healthy = true
	r.probeHeld()
	if got := models.history(); len(got) != 2 || got[1] != "enable:org/a" {
		t.Fatalf("history = %v, want disable then enable", got)
	}
	if len(probed) != 2 {
		t.Fatalf("probed %v, want one probe per round", probed)
	}
	if held := r.AutoDisabledModels(); len(held) != 0 {
		t.Fatalf("still held after recovery: %v", held)
	}
}

// A backend that passes a one-token probe but fails real work would cycle in
// and out every minute. After a repeat trip it waits for the full audit.
func TestRepeatTripWaitsForTheAudit(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())
	r.probeOne = func(string) (selfcheck.ProbeResult, error) {
		return selfcheck.ProbeResult{OK: true, StatusCode: 200}, nil
	}

	trip := func(wantCalls int) {
		for i := 0; i < 3; i++ {
			r.ObserveRequest(hubFailure("org/a", 502, "x"))
		}
		if got := waitForCalls(t, models, wantCalls); len(got) != wantCalls {
			t.Fatalf("history = %v, want %d calls", got, wantCalls)
		}
	}

	trip(1)
	r.probeHeld() // fast recovery
	trip(3)       // and straight back out

	r.probeHeld()
	if got := models.history(); len(got) != 3 {
		t.Fatalf("history = %v: a flapping model was fast-recovered again", got)
	}

	// The audit still brings it back.
	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {OK: true, StatusCode: 200}}))
	if got := models.history(); len(got) != 4 || got[3] != "enable:org/a" {
		t.Fatalf("history = %v, want the audit to re-enable it", got)
	}
}

// A probe that could not be sent is not a verdict on the backend.
func TestRecoveryProbeErrorLeavesTheModelHeld(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())
	for i := 0; i < 3; i++ {
		r.ObserveRequest(hubFailure("org/a", 502, "x"))
	}
	waitForCalls(t, models, 1)
	r.probeOne = func(string) (selfcheck.ProbeResult, error) {
		return selfcheck.ProbeResult{}, errors.New("models.json unreadable")
	}
	r.probeHeld()
	if got := models.history(); len(got) != 1 {
		t.Fatalf("history = %v, want the model still held", got)
	}
}

func TestBreakerCanBeTurnedOff(t *testing.T) {
	off := false
	cases := map[string]conf.SelfCheck{
		"breaker off":      {TripAfterRequestFailures: -1},
		"auto-disable off": {AutoDisable: &off},
		"self-check off":   {Enable: &off},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			models := newFakeModels(map[string]bool{"org/a": true})
			r := newTestRunner(models, cfg)
			for i := 0; i < 10; i++ {
				r.ObserveRequest(hubFailure("org/a", 502, "x"))
			}
			if got := settle(t, models); len(got) != 0 {
				t.Fatalf("history = %v, want no action", got)
			}
		})
	}
}

func TestBreakerThresholdIsConfigurable(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, conf.SelfCheck{TripAfterRequestFailures: 1})
	r.ObserveRequest(hubFailure("org/a", 502, "x"))
	if got := waitForCalls(t, models, 1); len(got) != 1 {
		t.Fatalf("history = %v, want a trip on the first failure", got)
	}
}

// The breaker only sees what RecordRequestEnd reports, which is routed work.
// Probes and the local gateway use RecordRequest and must not reach it.
func TestRequestEndObserversSeeOnlyRoutedRequests(t *testing.T) {
	m := NewInferenceMetrics()
	var seen []RequestMetric
	m.OnRequestEnd(func(req RequestMetric) { seen = append(seen, req) })

	m.RecordRequest(RequestMetric{Model: "org/a", Source: SourceSelfCheck})
	m.RecordRequestStart("org/a", false)
	m.RecordRequestEnd(RequestMetric{Model: "org/a", Source: SourceHub, StatusCode: 503})

	if len(seen) != 1 || seen[0].StatusCode != 503 {
		t.Fatalf("observers saw %+v, want the one routed request with its status", seen)
	}
}

// An audit landing between the breaker's claim and its disable sees the model
// still enabled and drops the claim. The disable must restate it, or nothing
// would ever re-enable the model.
func TestTripRestatesAClaimAnAuditDropped(t *testing.T) {
	models := newFakeModels(map[string]bool{"org/a": true})
	r := newTestRunner(models, testSelfCheckCfg())

	r.mu.Lock()
	r.autoDisabled["org/a"] = true
	r.heldReason["org/a"] = "request breaker: test"
	r.mu.Unlock()
	// The audit runs now, while the model is still enabled.
	r.act(probeReport(map[string]selfcheck.ProbeResult{"org/a": {OK: true, StatusCode: 200}}))

	r.trip("org/a", "request breaker: test", 3, "HTTP 502: x", 502, false)
	if held := r.AutoDisabledModels(); held["org/a"] != "request breaker: test" {
		t.Fatalf("auto_disabled_models = %v, want the claim restated", held)
	}
	r.probeOne = func(string) (selfcheck.ProbeResult, error) {
		return selfcheck.ProbeResult{OK: true, StatusCode: 200}, nil
	}
	r.probeHeld()
	if got := models.history(); len(got) != 2 || got[1] != "enable:org/a" {
		t.Fatalf("history = %v, want the model recovered", got)
	}
}
