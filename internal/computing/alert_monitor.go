package computing

import (
	"fmt"
	"sync"
	"time"

	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
)

// alertMonitor watches for conditions that leave the provider running but not
// earning: a model that stops answering, a lost connection to Swan Inference,
// and a model that is health-green while failing most of its requests.
//
// The last case is the one health checks cannot see. A backend can serve
// /v1/models perfectly while every completion returns an upstream error, so the
// only evidence is the request outcomes themselves.
// Health strings as reported by ModelRegistry.
const (
	healthUnknown   = "unknown"
	healthHealthy   = "healthy"
	healthUnhealthy = "unhealthy"
)

type alertMonitor struct {
	notifier *alerts.Notifier
	cfg      conf.Alerts
	metrics  func() *InferenceMetricsData
	isConn   func() bool
	// health returns the authoritative current health of every model. The
	// registry dispatches each health callback on its own goroutine, so two
	// snapshots taken microseconds apart can arrive out of order and leave a
	// model latched in the wrong state. Re-reading on the tick corrects that
	// within a minute instead of waiting for the next transition.
	health func() map[string]string

	stopCh chan struct{}
	wg     sync.WaitGroup

	mu             sync.Mutex
	disconnectedAt time.Time
	disconnAlerted bool
	// Request counts at the last evaluation, so each window measures new
	// traffic rather than the lifetime ratio, which would stay elevated long
	// after a model recovered.
	lastTotal  map[string]int64
	lastFailed map[string]int64
	rateFiring map[string]bool
	lastHealth map[string]string
	// Models we actually alerted on. A recovery notice for a failure the
	// operator was never told about is noise, not information — and "degraded"
	// still serves, so it is deliberately not alerted going down.
	alertedUnhealthy map[string]bool
}

func newAlertMonitor(n *alerts.Notifier, cfg conf.Alerts, metrics func() *InferenceMetricsData, isConn func() bool, health func() map[string]string) *alertMonitor {
	return &alertMonitor{
		notifier:         n,
		cfg:              cfg,
		metrics:          metrics,
		isConn:           isConn,
		health:           health,
		stopCh:           make(chan struct{}),
		lastTotal:        make(map[string]int64),
		lastFailed:       make(map[string]int64),
		rateFiring:       make(map[string]bool),
		lastHealth:       make(map[string]string),
		alertedUnhealthy: make(map[string]bool),
	}
}

func (a *alertMonitor) Start() {
	if a == nil || !a.notifier.Enabled() {
		return
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-a.stopCh:
				return
			case <-ticker.C:
				a.checkConnection()
				a.checkErrorRates()
				a.reconcileHealth()
			}
		}
	}()
}

func (a *alertMonitor) Stop() {
	if a == nil || !a.notifier.Enabled() {
		return
	}
	select {
	case <-a.stopCh:
	default:
		close(a.stopCh)
	}
	a.wg.Wait()
}

// checkConnection alerts once the WebSocket has been down longer than the
// configured grace period, so a normal reconnect does not page anyone.
func (a *alertMonitor) checkConnection() {
	connected := a.isConn()

	a.mu.Lock()
	defer a.mu.Unlock()

	if connected {
		if a.disconnAlerted {
			down := time.Since(a.disconnectedAt).Round(time.Second)
			a.disconnAlerted = false
			a.notifier.ClearCooldown(alerts.EventDisconnected, "")
			a.notifier.Fire(alerts.EventReconnected, "",
				fmt.Sprintf("Reconnected to Swan Inference after %s", down),
				alerts.SeverityInfo, map[string]string{"downtime": down.String()})
		}
		a.disconnectedAt = time.Time{}
		return
	}

	if a.disconnectedAt.IsZero() {
		a.disconnectedAt = time.Now()
		return
	}
	grace := time.Duration(a.cfg.DisconnectAfterMin) * time.Minute
	if !a.disconnAlerted && time.Since(a.disconnectedAt) >= grace {
		a.disconnAlerted = true
		down := time.Since(a.disconnectedAt).Round(time.Second)
		a.notifier.Fire(alerts.EventDisconnected, "",
			fmt.Sprintf("Disconnected from Swan Inference for %s — no requests can be received", down),
			alerts.SeverityCritical, map[string]string{"downtime": down.String()})
	}
}

// checkErrorRates compares each model's failures against its requests since the
// previous tick.
func (a *alertMonitor) checkErrorRates() {
	m := a.metrics()
	if m == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for id, mm := range m.ModelMetrics {
		if mm == nil {
			continue
		}
		total := mm.TotalRequests - a.lastTotal[id]
		failed := mm.FailedReqs - a.lastFailed[id]
		a.lastTotal[id] = mm.TotalRequests
		a.lastFailed[id] = mm.FailedReqs

		// A model that fails everything usually stops receiving traffic, so the
		// window falls below the floor. Evaluate recovery before the floor or a
		// fired incident never closes.
		belowFloor := total < int64(a.cfg.ErrorRateMinRequests)
		var ratio float64
		if total > 0 {
			ratio = float64(failed) / float64(total)
		}
		switch {
		case !belowFloor && ratio >= a.cfg.ErrorRateThreshold:
			if a.rateFiring[id] {
				// Already reported and still bad. Re-sending says nothing new
				// and trains the operator to filter the alert.
				continue
			}
			a.rateFiring[id] = true
			a.notifier.Fire(alerts.EventModelErrorRate, id,
				fmt.Sprintf("%s failed %d of %d requests (%.0f%%) — health checks pass but requests are failing",
					id, failed, total, ratio*100),
				alerts.SeverityCritical, map[string]string{
					"failed":   fmt.Sprintf("%d", failed),
					"total":    fmt.Sprintf("%d", total),
					"ratio":    fmt.Sprintf("%.2f", ratio),
					"endpoint": id,
				})
		case a.rateFiring[id] && (belowFloor || ratio < a.cfg.ErrorRateThreshold):
			a.rateFiring[id] = false
			a.notifier.ClearCooldown(alerts.EventModelErrorRate, id)
			a.notifier.Fire(alerts.EventErrorRateNormal, id,
				fmt.Sprintf("%s error rate back to normal (%d of %d failed)", id, failed, total),
				alerts.SeverityInfo, nil)
		}
	}
}

// reconcileHealth re-reads current health and feeds it through the same
// transition logic, so a snapshot that arrived out of order cannot leave a
// stale critical alert open indefinitely.
func (a *alertMonitor) reconcileHealth() {
	if a.health == nil {
		return
	}
	if current := a.health(); len(current) > 0 {
		a.onHealthChange(current)
	}
}

// onHealthChange fires model up/down alerts from the registry's health callback.
//
// The registry passes the health of every enabled model on each transition, not
// just the one that changed, so this diffs against the last seen state and only
// alerts on an actual transition. The first observation of a model establishes a
// baseline — a model that starts healthy is not news, one that starts unhealthy
// is.
func (a *alertMonitor) onHealthChange(modelHealth map[string]string) {
	if a == nil || !a.notifier.Enabled() {
		return
	}

	a.mu.Lock()
	type change struct{ id, health string }
	var changes []change
	for id, health := range modelHealth {
		prev, seen := a.lastHealth[id]
		if seen && prev == health {
			continue
		}
		a.lastHealth[id] = health
		// "unknown" is what a model reports before its first health check, so
		// unknown -> healthy is a model starting up, not one recovering. Only a
		// transition out of a state we have actually alerted on is news.
		if !seen || prev == healthUnknown {
			if health != healthUnhealthy {
				continue
			}
		}
		changes = append(changes, change{id, health})
	}
	// Forget models that are no longer registered, so a later re-add alerts again.
	for id := range a.lastHealth {
		if _, ok := modelHealth[id]; !ok {
			delete(a.lastHealth, id)
		}
	}
	a.mu.Unlock()

	for _, c := range changes {
		switch c.health {
		case healthUnhealthy:
			a.mu.Lock()
			a.alertedUnhealthy[c.id] = true
			a.mu.Unlock()
			a.notifier.Fire(alerts.EventModelUnhealthy, c.id,
				fmt.Sprintf("%s is unhealthy — it has been dropped from the models registered with Swan Inference", c.id),
				alerts.SeverityCritical, map[string]string{"health": c.health})
		case healthHealthy:
			a.mu.Lock()
			told := a.alertedUnhealthy[c.id]
			delete(a.alertedUnhealthy, c.id)
			a.mu.Unlock()
			if !told {
				// Never reported as failed, so there is nothing to close.
				// A model flapping healthy <-> degraded lands here: degraded
				// still serves requests, so neither direction is news.
				continue
			}
			a.notifier.ClearCooldown(alerts.EventModelUnhealthy, c.id)
			a.notifier.Fire(alerts.EventModelRecovered, c.id,
				fmt.Sprintf("%s is healthy again", c.id),
				alerts.SeverityInfo, map[string]string{"health": c.health})
		}
	}
}
