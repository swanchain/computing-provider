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
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return nil }, func() bool { return true })

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
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true })

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
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true })

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
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return data }, func() bool { return true })

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
	m := newAlertMonitor(alerts.New(cfg, "node1", "cp"), cfg, func() *InferenceMetricsData { return nil }, func() bool { return connected })

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
	m := newAlertMonitor(n, cfg, func() *InferenceMetricsData { return nil }, func() bool { return false })
	// None of these should panic or block.
	m.Start()
	m.onHealthChange(map[string]string{"a": "unhealthy"})
	m.checkConnection()
	m.checkErrorRates()
	m.Stop()
}
