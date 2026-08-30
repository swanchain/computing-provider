package computing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
)

// capture starts a webhook that records the events posted to it.
func capture(t *testing.T) (url string, events func() []alerts.Event, close func()) {
	t.Helper()
	var mu sync.Mutex
	var got []alerts.Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e alerts.Event
		_ = json.NewDecoder(r.Body).Decode(&e)
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return srv.URL, func() []alerts.Event {
			// Delivery is asynchronous; give it a moment to land.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				mu.Lock()
				n := len(got)
				mu.Unlock()
				if n > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			mu.Lock()
			defer mu.Unlock()
			return append([]alerts.Event(nil), got...)
		}, func() {
			srv.Close()
		}
}

func testCfg(url string) conf.Alerts {
	c := conf.Alerts{WebhookURL: url}
	applyTestDefaults(&c)
	return c
}

// applyTestDefaults mirrors conf.applyAlertDefaults, which is unexported.
func applyTestDefaults(c *conf.Alerts) {
	c.CooldownMinutes = 15
	c.DisconnectAfterMin = 5
	c.ErrorRateThreshold = 0.5
	c.ErrorRateMinRequests = 10
}

func TestHealthChangeAlertsOnlyOnTransition(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return nil }, func() bool { return true }, nil)

	// First observation is a baseline: a model that is already healthy is not news.
	m.onHealthChange(map[string]string{"a": "healthy", "b": "healthy"})
	// Repeating the same state must not alert either.
	m.onHealthChange(map[string]string{"a": "healthy", "b": "healthy"})
	// Only b changes.
	m.onHealthChange(map[string]string{"a": "healthy", "b": "unhealthy"})

	got := events()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].Event != alerts.EventModelUnhealthy || got[0].ModelID != "b" {
		t.Errorf("got %s for %s, want %s for b", got[0].Event, got[0].ModelID, alerts.EventModelUnhealthy)
	}
	if got[0].Severity != alerts.SeverityCritical {
		t.Errorf("severity = %s, want critical", got[0].Severity)
	}
}

func TestErrorRateAlertsWhenHealthyButFailing(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	// The gpt-5.x case: health checks pass, every request returns an upstream error.
	data := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"m": {ModelName: "m", TotalRequests: 20, FailedReqs: 20},
	}}
	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true }, nil)

	m.checkErrorRates()

	got := events()
	if len(got) != 1 || got[0].Event != alerts.EventModelErrorRate {
		t.Fatalf("want one %s event, got %+v", alerts.EventModelErrorRate, got)
	}
	if got[0].Details["failed"] != "20" || got[0].Details["total"] != "20" {
		t.Errorf("details = %v, want 20/20", got[0].Details)
	}
}

func TestErrorRateIgnoresLowTraffic(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	// Two failures out of two is 100%, but far too little traffic to act on.
	data := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"m": {ModelName: "m", TotalRequests: 2, FailedReqs: 2},
	}}
	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true }, nil)

	m.checkErrorRates()
	if got := events(); len(got) != 0 {
		t.Fatalf("want no events below the request floor, got %+v", got)
	}
}

func TestErrorRateMeasuresEachWindowSeparately(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	data := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"m": {ModelName: "m", TotalRequests: 20, FailedReqs: 20},
	}}
	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true }, nil)

	m.checkErrorRates() // fires
	// A healthy window follows: 20 more requests, none failed. The lifetime
	// ratio is still 50%, but the new window is clean, so this must recover.
	data.ModelMetrics["m"].TotalRequests = 40
	m.checkErrorRates()

	got := events()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (fire then recover): %+v", len(got), got)
	}
	if got[1].Event != alerts.EventErrorRateNormal {
		t.Errorf("second event = %s, want %s", got[1].Event, alerts.EventErrorRateNormal)
	}
}

func TestDisconnectAlertWaitsForGracePeriod(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	connected := false
	cfg := testCfg(url)
	cfg.DisconnectAfterMin = 5
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return nil }, func() bool { return connected }, nil)

	m.checkConnection() // starts the clock, no alert
	m.checkConnection() // still inside the grace period
	if got := events(); len(got) != 0 {
		t.Fatalf("alerted during the grace period: %+v", got)
	}

	// Backdate the disconnect past the grace period.
	m.mu.Lock()
	m.disconnectedAt = time.Now().Add(-10 * time.Minute)
	m.mu.Unlock()
	m.checkConnection()

	got := events()
	if len(got) != 1 || got[0].Event != alerts.EventDisconnected {
		t.Fatalf("want one %s event, got %+v", alerts.EventDisconnected, got)
	}

	connected = true
	m.checkConnection()
	if got := events(); len(got) != 2 || got[1].Event != alerts.EventReconnected {
		t.Fatalf("want a %s event after recovery, got %+v", alerts.EventReconnected, got)
	}
}

func TestDisabledNotifierIsInert(t *testing.T) {
	cfg := conf.Alerts{}
	applyTestDefaults(&cfg)
	n := alerts.New(cfg, "node1", "cp")
	if n.Enabled() {
		t.Fatal("notifier with no URL should be disabled")
	}
	m := newAlertMonitor(n, cfg, func() *InferenceMetricsData { return nil }, func() bool { return false }, nil)
	// None of these should panic or block.
	m.Start()
	m.onHealthChange(map[string]string{"a": "unhealthy"})
	m.checkConnection()
	m.checkErrorRates()
	m.Stop()
}

// Regression: models start at "unknown" before their first health check, so
// unknown -> healthy is a model booting, not one recovering. Alerting on it
// meant every restart emitted a spurious recovery per model.
func TestUnknownToHealthyIsNotARecovery(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return nil }, func() bool { return true }, nil)

	m.onHealthChange(map[string]string{"a": "unknown", "b": "unknown"})
	m.onHealthChange(map[string]string{"a": "healthy", "b": "unknown"})
	m.onHealthChange(map[string]string{"a": "healthy", "b": "healthy"})

	if got := events(); len(got) != 0 {
		t.Fatalf("startup should be silent, got %+v", got)
	}
}

// A model that fails everything stops being routed to, so its window falls
// under the request floor. The incident must still close.
func TestErrorRateRecoversWhenTrafficStops(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	data := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"m": {ModelName: "m", TotalRequests: 20, FailedReqs: 20},
	}}
	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true }, nil)

	m.checkErrorRates() // fires
	// Traffic dries up entirely: no new requests at all.
	m.checkErrorRates()

	got := events()
	if len(got) != 2 || got[1].Event != alerts.EventErrorRateNormal {
		t.Fatalf("want the incident to close when traffic stops, got %+v", got)
	}
}

// Recovery events must never be suppressed by the cooldown, or a receiver is
// left holding an incident that has already resolved.
func TestRecoveryIsNeverRateLimited(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	connected := false
	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return nil }, func() bool { return connected }, nil)

	for i := 0; i < 2; i++ {
		connected = false
		m.checkConnection()
		m.mu.Lock()
		m.disconnectedAt = time.Now().Add(-10 * time.Minute)
		m.mu.Unlock()
		m.checkConnection()
		connected = true
		m.checkConnection()
	}

	var recoveries int
	for _, e := range events() {
		if e.Event == alerts.EventReconnected {
			recoveries++
		}
	}
	if recoveries != 2 {
		t.Fatalf("got %d reconnect events, want 2 — the second must not be swallowed by the cooldown", recoveries)
	}
}

// A stale snapshot can latch a model in the wrong state; the periodic
// reconciliation must correct it without waiting for another transition.
func TestReconcileHealthClosesAStaleAlert(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	current := map[string]string{"a": "healthy"}
	cfg := testCfg(url)
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return nil },
		func() bool { return true }, func() map[string]string { return current })

	m.onHealthChange(map[string]string{"a": "healthy"})
	// An out-of-order snapshot leaves the model latched unhealthy.
	m.onHealthChange(map[string]string{"a": "unhealthy"})
	// Reality says otherwise; reconciliation should close it.
	m.reconcileHealth()

	got := events()
	if len(got) != 2 || got[1].Event != alerts.EventModelRecovered {
		t.Fatalf("want the stale alert closed by reconciliation, got %+v", got)
	}
}

// A standing bad state must alert once, not on every tick. At a 60-second
// period the old behaviour sent one every cooldown window, which trains an
// operator to filter the alert — and then the next real one goes with it.
func TestErrorRateAlertsOnceUntilItChanges(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	data := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"m": {ModelName: "m", TotalRequests: 20, FailedReqs: 20},
	}}
	cfg := testCfg(url)
	cfg.CooldownMinutes = 0 // no cooldown: prove the edge trigger, not the timer
	m := newAlertMonitor(alerts.New(cfg, "n", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true }, nil)

	// Five consecutive windows, all failing.
	for i := 0; i < 5; i++ {
		m.checkErrorRates()
		data.ModelMetrics["m"].TotalRequests += 20
		data.ModelMetrics["m"].FailedReqs += 20
	}

	var fired int
	for _, e := range events() {
		if e.Event == alerts.EventModelErrorRate {
			fired++
		}
	}
	if fired != 1 {
		t.Fatalf("model_error_rate fired %d times, want exactly 1 for an unchanged state", fired)
	}
}

// Recovering and failing again is a change each way, so both must alert.
func TestErrorRateAlertsAgainAfterRecovery(t *testing.T) {
	url, events, done := capture(t)
	defer done()

	data := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"m": {ModelName: "m", TotalRequests: 20, FailedReqs: 20},
	}}
	cfg := testCfg(url)
	cfg.CooldownMinutes = 0
	m := newAlertMonitor(alerts.New(cfg, "n", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true }, nil)

	m.checkErrorRates()                        // fires
	data.ModelMetrics["m"].TotalRequests += 20 // clean window
	m.checkErrorRates()                        // recovers
	data.ModelMetrics["m"].TotalRequests += 20
	data.ModelMetrics["m"].FailedReqs += 20 // bad again
	m.checkErrorRates()                     // fires again

	var fired, recovered int
	for _, e := range events() {
		switch e.Event {
		case alerts.EventModelErrorRate:
			fired++
		case alerts.EventErrorRateNormal:
			recovered++
		}
	}
	if fired != 2 || recovered != 1 {
		t.Fatalf("fired=%d recovered=%d, want 2 and 1", fired, recovered)
	}
}
