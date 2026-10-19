package computing

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
)

// The request breaker.
//
// The audit decides whether a model can serve by probing it every
// IntervalMinutes. That is the right cadence for a backend that is quietly
// broken, and far too slow for one that dies under load: between two audits
// every routed request fails, and the hub counts each one against this node.
// The breaker watches the routed requests themselves and pulls a model after
// TripAfterRequestFailures consecutive backend failures, using the same
// classification, the same disable path and the same recovery the audit uses.
// A model it pulls is one the audit then holds and returns, so an operator's
// own disable is still never undone by either.

// reflapWindow is how soon after a breaker recovery a second trip counts as
// flapping. A backend that answers a one-token probe but fails real work would
// otherwise cycle in and out every RecoveryProbeSeconds, failing a few routed
// requests each time; after a repeat trip it waits for the full audit instead.
const reflapWindow = 10 * time.Minute

// requestStreak is one model's run of consecutive backend-owned failures.
type requestStreak struct {
	count int
	last  time.Time
	// lastErr is the most recent failure, for the alert and the status page.
	lastErr string
}

// ObserveRequest is told the outcome of every routed request. It is called on
// the request's own goroutine, so anything slow is handed off.
func (r *selfCheckRunner) ObserveRequest(req RequestMetric) {
	if r == nil || req.Source != SourceHub {
		return
	}
	cfg := r.cfg()
	threshold := cfg.TripThreshold()
	// Without the audit nothing would ever re-enable a tripped model, so the
	// breaker is part of it and off when it is.
	if !cfg.Enabled() || !cfg.AutoDisableEnabled() || threshold == 0 {
		return
	}

	switch requestVerdict(req) {
	case verdictIgnore:
		return
	case verdictHealthy:
		r.mu.Lock()
		delete(r.streaks, req.Model)
		r.mu.Unlock()
		return
	}

	now := r.now()
	r.mu.Lock()
	s := r.streaks[req.Model]
	if s == nil || now.Sub(s.last) > cfg.TripWindow() {
		s = &requestStreak{}
		r.streaks[req.Model] = s
	}
	s.count++
	s.last = now
	s.lastErr = describeRequestFailure(req)
	count, lastErr := s.count, s.lastErr
	if count < threshold || r.autoDisabled[req.Model] {
		r.mu.Unlock()
		return
	}
	// Claim the model before letting go of the lock, so the requests failing
	// alongside this one do not each try to disable it.
	reason := fmt.Sprintf("request breaker: %d consecutive failed requests (%s)", count, lastErr)
	r.autoDisabled[req.Model] = true
	r.heldReason[req.Model] = reason
	delete(r.streaks, req.Model)
	if at, ok := r.recoveredAt[req.Model]; ok && now.Sub(at) < reflapWindow {
		r.slowRecovery[req.Model] = true
	}
	slow := r.slowRecovery[req.Model]
	r.mu.Unlock()

	// DisableModel re-registers with Swan Inference, a network send; it must
	// not hold up the response this request still owes the hub.
	go r.trip(req.Model, reason, count, lastErr, req.StatusCode, slow)
}

// trip disables a model the breaker has claimed. reason is passed in rather
// than read back, because an audit running in between drops it with the claim.
func (r *selfCheckRunner) trip(id, reason string, count int, lastErr string, status int, slow bool) {
	enabled, known := r.models.IsModelEnabled(id)
	if !known || !enabled {
		// Gone, or already off: an operator's disable is theirs, and claiming
		// it would let the recovery probe undo it.
		r.mu.Lock()
		delete(r.autoDisabled, id)
		delete(r.heldReason, id)
		delete(r.slowRecovery, id)
		r.mu.Unlock()
		return
	}
	if err := r.models.DisableModel(id); err != nil {
		logs.GetLogger().Warnf("[selfcheck] request breaker could not disable %s: %v", id, err)
		r.mu.Lock()
		delete(r.autoDisabled, id)
		delete(r.heldReason, id)
		r.mu.Unlock()
		return
	}
	// An audit that ran between the claim and the disable saw the model still
	// enabled and dropped the claim. Restate it, or the model stays off with
	// nothing that will ever bring it back.
	r.mu.Lock()
	r.autoDisabled[id] = true
	r.heldReason[id] = reason
	r.mu.Unlock()

	recovery := "It will be re-registered automatically once a probe succeeds."
	if slow {
		recovery = "It tripped again soon after recovering, so it now waits for the next full self-check before it is re-registered."
	}
	logs.GetLogger().Warnf("[selfcheck] %s failed %d consecutive routed requests (%s) — deregistered from Swan Inference", id, count, lastErr)
	r.notifier.Fire("model_auto_disabled", id,
		fmt.Sprintf("%s failed %d consecutive routed requests (%s) and has been deregistered from Swan Inference. %s",
			id, count, lastErr, recovery),
		alerts.SeverityCritical, map[string]string{
			"trigger":  "requests",
			"failures": fmt.Sprintf("%d", count),
			"status":   fmt.Sprintf("%d", status),
		})
}

type verdict int

const (
	verdictIgnore  verdict = iota // says nothing about the backend
	verdictHealthy                // the backend answered
	verdictFault                  // the backend could not serve
)

// requestVerdict classifies a routed request the way the audit classifies a
// probe, plus the two outcomes only real traffic has: the hub giving up on a
// request, and the node itself shutting down under it.
func requestVerdict(req RequestMetric) verdict {
	if req.Success {
		return verdictHealthy
	}
	if strings.HasPrefix(req.ErrorReason, cancelledByHubPrefix) ||
		strings.Contains(req.ErrorReason, "context canceled") {
		return verdictIgnore
	}
	probe := selfcheck.ProbeResult{StatusCode: req.StatusCode, Error: req.ErrorReason}
	if probe.BackendAtFault() {
		return verdictFault
	}
	// A 400 for an over-long prompt, or this node's own 429: the backend is up
	// and answering, which is as good as a success for the streak.
	return verdictHealthy
}

func describeRequestFailure(req RequestMetric) string {
	reason := req.ErrorReason
	if len(reason) > 200 {
		reason = reason[:200] + "…"
	}
	if req.StatusCode > 0 {
		return fmt.Sprintf("HTTP %d: %s", req.StatusCode, reason)
	}
	return reason
}

// startRecoveryProbes re-probes held models on a short timer, so a backend
// that recovers is back in routing within a minute rather than at the next
// audit. Each probe is one max_tokens:1 completion, sent only while a model is
// held out, and backs off per model while it keeps failing (see
// recordProbeFailure).
func (r *selfCheckRunner) startRecoveryProbes() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			wait := r.cfg().RecoveryProbeInterval()
			if wait <= 0 {
				// Off for now; config is re-read so turning it on applies
				// without a restart.
				wait = time.Minute
			}
			timer := time.NewTimer(wait)
			select {
			case <-r.stopCh:
				timer.Stop()
				return
			case <-timer.C:
			}
			if r.cfg().RecoveryProbeInterval() > 0 {
				r.probeHeld()
			}
		}
	}()
}

// probeHeld runs one recovery probe against each held model.
func (r *selfCheckRunner) probeHeld() {
	defer func() {
		if err := recover(); err != nil {
			logs.GetLogger().Errorf("[selfcheck] recovery probe panic recovered: %v", err)
		}
	}()
	cfg := r.cfg()
	if !cfg.AutoRecoverEnabled() || r.models == nil {
		return
	}

	r.mu.Lock()
	var ids []string
	for id := range r.autoDisabled {
		if !r.slowRecovery[id] {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	sort.Strings(ids)

	now := r.now()
	for _, id := range ids {
		enabled, known := r.models.IsModelEnabled(id)
		if !known {
			r.forget(id)
			continue
		}
		if enabled {
			continue // An operator turned it back on; act() will drop the claim.
		}
		r.mu.Lock()
		due := !now.Before(r.nextProbe[id])
		r.mu.Unlock()
		if !due {
			continue
		}
		res, err := r.probeOne(id)
		if err != nil {
			logs.GetLogger().Warnf("[selfcheck] recovery probe for %s not sent: %v", id, err)
			continue
		}
		if res.Skipped {
			continue
		}
		r.recordProbes(selfcheck.Report{Probes: map[string]selfcheck.ProbeResult{id: res}})
		if res.OK {
			r.onProbeSuccess(id, false, cfg)
			continue
		}
		r.recordProbeFailure(id, now, cfg)
	}
}

// recordProbeFailure doubles a held model's wait before its next recovery
// probe, from RecoveryProbeSeconds up to the audit interval. An upstream that
// is overloaded or rate-limiting answers every probe the same way; probing it
// each minute spends quota and can keep its limit window from clearing, while
// telling us nothing new. A backend that recovers quickly is still found on
// the first probes, and a long outage costs a handful of probes, not sixty an
// hour.
func (r *selfCheckRunner) recordProbeFailure(id string, now time.Time, cfg conf.SelfCheck) {
	base := cfg.RecoveryProbeInterval()
	limit := cfg.Interval()
	if limit <= 0 {
		limit = defaultSelfCheckInterval
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	wait := r.probeBackoff[id] * 2
	if wait < base {
		wait = base
	}
	if wait > limit {
		wait = limit
	}
	r.probeBackoff[id] = wait
	r.nextProbe[id] = now.Add(wait)
}

// clearBackoffLocked drops a model's recovery backoff; r.mu must be held.
func (r *selfCheckRunner) clearBackoffLocked(id string) {
	delete(r.probeBackoff, id)
	delete(r.nextProbe, id)
}

// AutoDisabledModels lists the models this runner holds out of routing and
// why each was pulled.
func (r *selfCheckRunner) AutoDisabledModels() map[string]string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.autoDisabled) == 0 {
		return nil
	}
	out := make(map[string]string, len(r.autoDisabled))
	for id := range r.autoDisabled {
		reason := r.heldReason[id]
		if reason == "" {
			reason = "self-check"
		}
		out[id] = reason
	}
	return out
}
