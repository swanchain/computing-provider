package computing

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
)

// selfCheckDelay lets the provider settle before the first audit: models need
// to pass a health check and register before "not registered" means anything.
const selfCheckDelay = 5 * time.Minute

// defaultSelfCheckInterval guards the timer against a zero configured value.
const defaultSelfCheckInterval = 10 * time.Minute

// modelController is the part of the registry the runner needs, kept narrow so
// the auto-heal logic can be tested without a live service.
type modelController interface {
	DisableModel(modelID string) error
	EnableModel(modelID string) error
	IsModelEnabled(modelID string) (enabled bool, known bool)
}

// selfCheckRunner audits the provider on a timer and, when a backend cannot
// serve, takes the model out of routing until it can again.
//
// Health checks probe /v1/models, which most backends answer without touching
// the inference engine, so a model can look healthy while failing every
// request. Left alone the node keeps accepting traffic it cannot fulfil and
// every failure is attributed to it. Deregistering is the protective move: no
// traffic is better than traffic that fails.
type selfCheckRunner struct {
	notifier *alerts.Notifier
	opts     func() selfcheck.Options
	cfg      func() conf.SelfCheck
	models   modelController

	stopCh chan struct{}
	wg     sync.WaitGroup

	mu sync.Mutex
	// consecutive backend-owned probe failures per model.
	failures map[string]int
	// Models this runner disabled, so an operator's own disable is never
	// undone. Re-enabling a model somebody switched off deliberately would be
	// worse than leaving a broken one off.
	autoDisabled map[string]bool
}

func newSelfCheckRunner(n *alerts.Notifier, opts func() selfcheck.Options, cfg func() conf.SelfCheck, models modelController) *selfCheckRunner {
	return &selfCheckRunner{
		notifier:     n,
		opts:         opts,
		cfg:          cfg,
		models:       models,
		stopCh:       make(chan struct{}),
		failures:     make(map[string]int),
		autoDisabled: make(map[string]bool),
	}
}

func (r *selfCheckRunner) Start() {
	if r == nil || !r.cfg().Enabled() {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		timer := time.NewTimer(selfCheckDelay)
		defer timer.Stop()
		for {
			select {
			case <-r.stopCh:
				return
			case <-timer.C:
				r.runOnce()
				// Re-read each tick so a config change takes effect without a
				// restart. A zero or negative interval would busy-loop full
				// audits, each firing real completions at every backend.
				next := r.cfg().Interval()
				if next <= 0 {
					next = defaultSelfCheckInterval
				}
				timer.Reset(next)
			}
		}
	}()
}

func (r *selfCheckRunner) Stop() {
	if r == nil {
		return
	}
	select {
	case <-r.stopCh:
	default:
		close(r.stopCh)
	}
	r.wg.Wait()
}

func (r *selfCheckRunner) runOnce() {
	defer func() {
		if err := recover(); err != nil {
			logs.GetLogger().Errorf("[selfcheck] panic recovered: %v", err)
		}
	}()

	opts := r.opts()
	opts.ExpectedProbeFailures = r.disabledByUs()
	report := selfcheck.Run(opts)
	r.act(report)
	r.report(report)
}

// act disables models whose backend cannot serve and re-enables the ones that
// recover.
func (r *selfCheckRunner) act(report selfcheck.Report) {
	cfg := r.cfg()
	if r.models == nil || len(report.Probes) == 0 {
		return
	}

	ids := make([]string, 0, len(report.Probes))
	for id := range report.Probes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		probe := report.Probes[id]
		if probe.Skipped {
			continue
		}

		enabled, known := r.models.IsModelEnabled(id)
		if !known {
			// Gone from the registry: drop our bookkeeping so a model that
			// returns later starts from a clean slate.
			r.forget(id)
			continue
		}
		if enabled {
			// Whoever enabled it — us or an operator — this is no longer a
			// model we are holding down. Keeping the flag would let a later
			// deliberate disable be undone on the next successful probe.
			r.mu.Lock()
			delete(r.autoDisabled, id)
			r.mu.Unlock()
		}

		switch {
		case probe.OK:
			r.onProbeSuccess(id, enabled, cfg)
		case probe.BackendAtFault():
			r.onBackendFailure(id, enabled, probe, cfg)
		default:
			// A client-owned failure — an over-long prompt, a rate limit — says
			// nothing about the backend, so it must not count toward disabling.
			r.resetFailures(id)
		}
	}
}

func (r *selfCheckRunner) onProbeSuccess(id string, enabled bool, cfg conf.SelfCheck) {
	r.resetFailures(id)
	if enabled {
		return // Already serving; act() has cleared any stale flag.
	}

	r.mu.Lock()
	ours := r.autoDisabled[id]
	r.mu.Unlock()
	if !ours || !cfg.AutoRecoverEnabled() {
		return // Somebody disabled this on purpose; leave it alone.
	}

	if err := r.models.EnableModel(id); err != nil {
		logs.GetLogger().Warnf("[selfcheck] failed to re-enable %s: %v", id, err)
		return
	}
	r.mu.Lock()
	delete(r.autoDisabled, id)
	r.mu.Unlock()

	logs.GetLogger().Infof("[selfcheck] %s can serve again — re-registered with Swan Inference", id)
	r.notifier.Fire("model_auto_recovered", id,
		fmt.Sprintf("%s is serving again and has been re-registered with Swan Inference", id),
		alerts.SeverityInfo, nil)
}

func (r *selfCheckRunner) onBackendFailure(id string, enabled bool, probe selfcheck.ProbeResult, cfg conf.SelfCheck) {
	r.mu.Lock()
	r.failures[id]++
	count := r.failures[id]
	r.mu.Unlock()

	if !enabled || !cfg.AutoDisableEnabled() || count < cfg.FailuresBeforeDisable {
		return
	}

	if err := r.models.DisableModel(id); err != nil {
		logs.GetLogger().Warnf("[selfcheck] failed to disable %s: %v", id, err)
		return
	}
	r.mu.Lock()
	r.autoDisabled[id] = true
	r.mu.Unlock()

	reason := probe.Error
	if probe.StatusCode > 0 {
		reason = fmt.Sprintf("HTTP %d: %s", probe.StatusCode, probe.Error)
	}
	logs.GetLogger().Warnf("[selfcheck] %s failed %d consecutive probes (%s) — deregistered from Swan Inference", id, count, reason)
	r.notifier.Fire("model_auto_disabled", id,
		fmt.Sprintf("%s failed %d consecutive inference probes (%s) and has been deregistered from Swan Inference. "+
			"It will be re-registered automatically once the backend serves again.", id, count, reason),
		alerts.SeverityCritical, map[string]string{
			"failures": fmt.Sprintf("%d", count),
			"status":   fmt.Sprintf("%d", probe.StatusCode),
		})
}

// disabledByUs snapshots the models this runner has taken out of service.
func (r *selfCheckRunner) disabledByUs() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.autoDisabled) == 0 {
		return nil
	}
	out := make(map[string]bool, len(r.autoDisabled))
	for id := range r.autoDisabled {
		out[id] = true
	}
	return out
}

// forget drops all bookkeeping for a model, so the maps cannot grow without
// bound as models come and go from models.json.
func (r *selfCheckRunner) forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.failures, id)
	delete(r.autoDisabled, id)
}

func (r *selfCheckRunner) resetFailures(id string) {
	r.mu.Lock()
	delete(r.failures, id)
	r.mu.Unlock()
}

// report logs the audit and alerts only on failures. An "all clear" that
// arrives every ten minutes trains an operator to filter it, and then the one
// that matters is filtered too.
func (r *selfCheckRunner) report(report selfcheck.Report) {
	problems := report.Problems()
	if len(problems) == 0 {
		logs.GetLogger().Infof("Self-check: %s", report.Summary())
		return
	}

	var lines []string
	for _, p := range problems {
		logs.GetLogger().Warnf("Self-check %s: %s — %s", p.Status, p.Name, p.Message)
		lines = append(lines, fmt.Sprintf("%s: %s", p.Name, p.Message))
	}
	if !report.Failed() {
		return // Warnings are worth a log line, not an alert.
	}
	r.notifier.Fire("selfcheck_failed", "",
		fmt.Sprintf("Self-check found problems (%s): %s", report.Summary(), strings.Join(lines, "; ")),
		alerts.SeverityCritical, map[string]string{"summary": report.Summary()})
}

// selfCheckOptions builds the audit's inputs from current config.
func selfCheckOptions(cpPath string) selfcheck.Options {
	opt := selfcheck.Options{RepoPath: cpPath}
	cfg := conf.GetConfig()
	if cfg == nil {
		return opt
	}
	opt.LogDir = cfg.Log.Dir
	opt.APIBase = fmt.Sprintf("http://localhost:%d", cfg.API.Port)
	opt.ConfigModels = cfg.Inference.Models
	return opt
}
