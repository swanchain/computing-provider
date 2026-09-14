package autoswitch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
)

// SnapshotFunc builds the view of the world a cycle decides on.
type SnapshotFunc func(ctx context.Context) (*Snapshot, error)

// DecideFunc asks the planner.
type DecideFunc func(ctx context.Context, snapshot *Snapshot) (*Decision, error)

// ExecuteFunc carries out an approved plan.
//
// It is given the planner's reason so that whatever it notifies — the
// operator's mail, a log line — records the argument the node acted on rather
// than only the outcome.
type ExecuteFunc func(ctx context.Context, decision *Decision) error

// Scheduler re-decides which models to serve, on an interval.
//
// It owns no policy of its own: every refusal comes from the guardrails, and
// the scheduler's whole job is to run a cycle, persist what happened, and stop
// when told. That separation is what lets the guardrails be tested without a
// clock and the scheduler without a planner.
type Scheduler struct {
	Policy   Policy
	Interval time.Duration
	History  *History

	Snapshot SnapshotFunc
	Decide   DecideFunc
	Execute  ExecuteFunc

	// Now exists so tests can drive dwell and the daily cap without waiting.
	Now func() time.Time

	// OnCycle, when set, is called with the outcome of every cycle. The
	// dashboard and the logs both read this rather than the scheduler
	// reaching out to them.
	OnCycle func(CycleOutcome)

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// CycleOutcome is what one cycle did. Distinct from Outcome, which pairs a
// past decision with what it actually earned.
type CycleOutcome struct {
	At       time.Time `json:"at"`
	Decision *Decision `json:"decision,omitempty"`
	Verdict  Verdict   `json:"verdict"`
	Executed bool      `json:"executed"`
	Err      string    `json:"error,omitempty"`
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return 30 * time.Minute
}

// Start begins the loop. It returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	s.stop = make(chan struct{})
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()

		ticker := time.NewTicker(s.interval())
		defer ticker.Stop()

		logs.GetLogger().Infof("auto-switch: re-planning every %s", s.interval())

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-ticker.C:
				s.RunCycle(ctx)
			}
		}
	}()
}

// Stop ends the loop and waits for an in-flight cycle to finish.
//
// Waiting matters: a cycle interrupted between starting a model and recording
// that it did would leave the history disagreeing with reality, and the next
// start would plan against a node it misremembers.
func (s *Scheduler) Stop() {
	if s.stop == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// RunCycle runs one full cycle: snapshot, plan, judge, and act if allowed.
//
// Every exit path records a proposal, so the consecutive-agreement rule sees
// the cycles that refused as well as the ones that acted. Without that, a
// planner that proposed a switch, was refused for one cycle, then proposed it
// again would look like two consecutive agreeing cycles.
func (s *Scheduler) RunCycle(ctx context.Context) CycleOutcome {
	now := s.now()
	outcome := CycleOutcome{At: now}

	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		outcome.Err = fmt.Sprintf("could not build a snapshot: %v", err)
		logs.GetLogger().Warnf("auto-switch: %s", outcome.Err)
		s.report(outcome)
		return outcome
	}

	decision, decideErr := s.Decide(ctx, snapshot)
	if decideErr != nil {
		// A planner that cannot be reached or parsed is a fault to report,
		// never a decision to invent. Passing nil to the guardrails produces
		// the same refusal any other invalid decision would.
		outcome.Err = fmt.Sprintf("planner failed: %v", decideErr)
		logs.GetLogger().Warnf("auto-switch: %s", outcome.Err)
	}
	outcome.Decision = decision

	// Recorded before the verdict, so that "two consecutive cycles agree"
	// counts this one. Recording afterwards means the rule can only ever be
	// satisfied one cycle late, which on a 30 minute interval is half an hour
	// of an opportunity the node had already decided twice about.
	//
	// A failed cycle records a keep, because two proposals either side of an
	// outage did not agree consecutively.
	if decision != nil {
		s.History.RecordProposal(now, decision)
	} else {
		s.History.RecordProposal(now, &Decision{Action: ActionKeep})
	}

	verdict := EvaluateWithHistory(decision, snapshot, s.Policy, s.History, now)
	outcome.Verdict = verdict

	if !verdict.Allowed || verdict.Effective.Action != ActionSwitch {
		if len(verdict.Violations) > 0 {
			logs.GetLogger().Infof("auto-switch: keeping current models (%s)", verdict.Violations[0])
		}
		s.persist()
		s.report(outcome)
		return outcome
	}

	logs.GetLogger().Infof("auto-switch: switching — serve %v, stop %v (%s)",
		verdict.Effective.Serve, verdict.Effective.Stop, verdict.Effective.Reason)

	if err := s.Execute(ctx, &verdict.Effective); err != nil {
		// Not recorded as a switch. A failed execution has not changed what
		// the node serves, and recording it would start a dwell period the
		// node has not earned, blocking the retry.
		outcome.Err = fmt.Sprintf("switch failed: %v", err)
		logs.GetLogger().Errorf("auto-switch: %s", outcome.Err)
		s.persist()
		s.report(outcome)
		return outcome
	}

	var baseline float64
	if snapshot.CurrentDailyUSD != nil {
		baseline = *snapshot.CurrentDailyUSD
	}
	s.History.RecordSwitch(now, &verdict.Effective, baseline)
	outcome.Executed = true

	s.persist()
	s.report(outcome)
	return outcome
}

func (s *Scheduler) persist() {
	if s.History == nil {
		return
	}
	if err := s.History.Save(); err != nil {
		// Worth shouting about: unsaved history means the next start honours
		// no dwell time and no daily cap.
		logs.GetLogger().Errorf("auto-switch: could not save history, so dwell time and the daily cap will not survive a restart: %v", err)
	}
}

func (s *Scheduler) report(outcome CycleOutcome) {
	if s.OnCycle == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logs.GetLogger().Errorf("auto-switch: OnCycle panicked: %v", r)
		}
	}()
	s.OnCycle(outcome)
}
