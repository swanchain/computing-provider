package computing

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/alerts"
	"github.com/swanchain/computing-provider-v2/internal/selfcheck"
)

// selfCheckInterval is how often the daemon audits itself. Drift is slow —
// a stale config or a context mismatch persists until someone changes it — so
// this is about catching it eventually, not quickly. Availability problems are
// handled by health checks and alertMonitor on a scale of seconds.
const selfCheckInterval = 24 * time.Hour

// selfCheckDelay lets the provider settle before the first audit: models need
// to pass a health check and register before "not registered" means anything.
const selfCheckDelay = 5 * time.Minute

// selfCheckRunner runs the audit on a timer and reports only problems.
//
// A daily "all clear" that arrives 365 times a year gets filtered into a folder
// nobody reads, and then the one that matters is filtered too. Passing runs are
// logged locally; only failures reach the webhook.
type selfCheckRunner struct {
	notifier *alerts.Notifier
	opts     func() selfcheck.Options

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func newSelfCheckRunner(n *alerts.Notifier, opts func() selfcheck.Options) *selfCheckRunner {
	return &selfCheckRunner{notifier: n, opts: opts, stopCh: make(chan struct{})}
}

func (r *selfCheckRunner) Start() {
	if r == nil {
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
				timer.Reset(selfCheckInterval)
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

	report := selfcheck.Run(r.opts())
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
		// Warnings alone are worth a log line but not an alert.
		return
	}
	r.notifier.Fire("selfcheck_failed", "",
		fmt.Sprintf("Daily self-check found problems (%s): %s", report.Summary(), strings.Join(lines, "; ")),
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
