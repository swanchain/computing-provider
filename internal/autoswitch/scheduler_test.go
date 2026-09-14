package autoswitch

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// harness drives a scheduler with a fixed clock and a scripted planner.
type harness struct {
	t        *testing.T
	sched    *Scheduler
	snapshot *Snapshot
	decision *Decision
	decideEr error
	execErr  error
	executed []Decision
	clock    time.Time
}

func newHarness(t *testing.T, policy Policy) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		clock: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
		snapshot: node(
			[]ServingModel{served("old/Model", true, false, 1.00)},
			[]MarketModel{fitting("new/Model", 8, 20), fitting("other/Model", 8, 20)},
		),
	}
	history, err := LoadHistory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.sched = &Scheduler{
		Policy:   policy,
		History:  history,
		Now:      func() time.Time { return h.clock },
		Snapshot: func(context.Context) (*Snapshot, error) { return h.snapshot, nil },
		Decide: func(context.Context, *Snapshot) (*Decision, error) {
			if h.decideEr != nil {
				return nil, h.decideEr
			}
			return h.decision, nil
		},
		Execute: func(_ context.Context, d *Decision) error {
			if h.execErr != nil {
				return h.execErr
			}
			h.executed = append(h.executed, *d)
			return nil
		},
	}
	return h
}

func (h *harness) run() CycleOutcome { return h.sched.RunCycle(context.Background()) }

func (h *harness) advance(d time.Duration) { h.clock = h.clock.Add(d) }

func switchTo(model string, usd float64) *Decision {
	return &Decision{
		Action: ActionSwitch, Serve: []string{model}, Stop: []string{"old/Model"},
		ExpectedDailyUSD: usd, Reason: "better paid",
	}
}

// One agreeing cycle is not two. A planner that changes its mind between
// cycles is noise, and acting on the first proposal is what flapping is.
func TestSwitchNeedsConsecutiveAgreement(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 2})
	h.decision = switchTo("new/Model", 20)

	first := h.run()
	if first.Executed {
		t.Fatal("switched on the first agreeing cycle")
	}
	if !violated(first.Verdict, RuleNotAgreed) {
		t.Errorf("violations = %+v", first.Verdict.Violations)
	}

	second := h.run()
	if !second.Executed {
		t.Fatalf("did not switch after two agreeing cycles: %+v", second.Verdict.Violations)
	}
	if len(h.executed) != 1 || h.executed[0].Serve[0] != "new/Model" {
		t.Errorf("executed = %+v", h.executed)
	}
}

// Agreeing means proposing the same plan. Two cycles wanting different models
// are not agreement, and counting them as such is how a node flaps while
// appearing to deliberate.
func TestDifferentPlansAreNotAgreement(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 2})

	h.decision = switchTo("new/Model", 20)
	h.run()
	h.decision = switchTo("other/Model", 20)
	out := h.run()

	if out.Executed {
		t.Fatal("switched after two cycles that proposed different models")
	}
	if !violated(out.Verdict, RuleNotAgreed) {
		t.Errorf("violations = %+v", out.Verdict.Violations)
	}
}

// A failed cycle breaks a run of agreement: two cycles either side of an
// outage did not agree consecutively.
func TestAFailedCycleBreaksAgreement(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 2})

	h.decision = switchTo("new/Model", 20)
	h.run()

	h.decideEr = fmt.Errorf("planner unreachable")
	h.run()
	h.decideEr = nil

	out := h.run()
	if out.Executed {
		t.Fatal("an outage between two proposals counted as consecutive agreement")
	}
}

// A switch costs minutes of zero revenue. Without a dwell floor a node flaps
// between two models a few cents apart.
func TestDwellTimeBlocksARapidSecondSwitch(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1, MinDwell: 2 * time.Hour})
	h.decision = switchTo("new/Model", 20)

	if !h.run().Executed {
		t.Fatal("the first switch was refused")
	}

	h.advance(30 * time.Minute)
	h.decision = switchTo("other/Model", 40)
	out := h.run()
	if out.Executed {
		t.Fatal("switched again 30 minutes into a 2 hour dwell")
	}
	if !violated(out.Verdict, RuleDwell) {
		t.Errorf("violations = %+v", out.Verdict.Violations)
	}

	h.advance(2 * time.Hour)
	if !h.run().Executed {
		t.Error("still refused after the dwell time had passed")
	}
}

func TestDailyLimitStopsFurtherSwitches(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1, MaxSwitchesDay: 2})

	for i := 0; i < 2; i++ {
		h.decision = switchTo(fmt.Sprintf("new/Model"), float64(20+i))
		if !h.run().Executed {
			t.Fatalf("switch %d was refused", i+1)
		}
		h.advance(time.Hour)
	}

	h.decision = switchTo("other/Model", 50)
	out := h.run()
	if out.Executed {
		t.Fatal("a third switch was made against a limit of two")
	}
	if !violated(out.Verdict, RuleDailyLimit) {
		t.Errorf("violations = %+v", out.Verdict.Violations)
	}

	// The window rolls: a day later the limit no longer binds.
	h.advance(25 * time.Hour)
	if !h.run().Executed {
		t.Error("the daily limit did not roll off after 24 hours")
	}
}

// A failed execution has not changed what the node serves. Recording it would
// start a dwell period the node has not earned and block the retry.
func TestAFailedSwitchIsNotRecorded(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1, MinDwell: time.Hour})
	h.decision = switchTo("new/Model", 20)
	h.execErr = fmt.Errorf("docker refused")

	out := h.run()
	if out.Executed {
		t.Fatal("a failed execution was reported as executed")
	}
	if out.Err == "" {
		t.Error("the failure was not reported")
	}
	if h.sched.History.LastSwitch() != nil {
		t.Fatal("a failed switch was recorded, which would start a dwell period")
	}

	// The retry must not be blocked by a dwell period that never began.
	h.execErr = nil
	if !h.run().Executed {
		t.Error("the retry was blocked by a switch that never happened")
	}
}

// Without memory the node cannot tell how long ago it last acted, so it must
// not act at all.
func TestNoHistoryRefusesToSwitch(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1})
	h.sched.History = nil
	h.decision = switchTo("new/Model", 20)

	// RecordProposal would nil-panic, so this also pins that the refusal
	// happens before anything touches the history.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a nil history panicked instead of refusing: %v", r)
		}
	}()

	v := EvaluateWithHistory(h.decision, h.snapshot, h.sched.Policy, nil, h.clock)
	if v.Allowed {
		t.Fatal("a switch was allowed with no history")
	}
	if !violated(v, RuleHistoryLost) {
		t.Errorf("violations = %+v", v.Violations)
	}
}

// The stateful rules must actually be checked, not merely listed as deferred.
func TestNothingIsDeferredOnceHistoryIsAvailable(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1})
	h.decision = switchTo("new/Model", 20)

	out := h.run()
	if len(out.Verdict.Deferred) != 0 {
		t.Errorf("deferred = %v, want nothing once the history is present", out.Verdict.Deferred)
	}
}

// The planner's reason has to reach whatever executes the switch, or the
// operator's notification records an outcome with no argument behind it.
func TestThePlannersReasonReachesExecution(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1})
	h.decision = switchTo("new/Model", 20)
	h.decision.Reason = "demand up 40%, two providers online"

	if !h.run().Executed {
		t.Fatal("refused")
	}
	if h.executed[0].Reason != "demand up 40%, two providers online" {
		t.Errorf("reason at execution = %q", h.executed[0].Reason)
	}
}

// Dwell and the daily cap are defeated by a restart unless the history is on
// disk. A crash loop would otherwise become a switch loop.
func TestHistorySurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	first, err := LoadHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	first.RecordSwitch(now, switchTo("new/Model", 20), 1.0)
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadHistory(dir)
	if err != nil {
		t.Fatal(err)
	}
	last := reloaded.LastSwitch()
	if last == nil {
		t.Fatal("the switch was not persisted")
	}
	if !last.At.Equal(now) {
		t.Errorf("recorded at %s, want %s", last.At, now)
	}
	if last.BaselineDayUSD != 1.0 {
		t.Errorf("baseline = %v, want the earnings at the time of the switch", last.BaselineDayUSD)
	}

	// And the dwell rule must now bind on the reloaded history.
	v := EvaluateWithHistory(switchTo("other/Model", 40),
		node([]ServingModel{served("old/Model", true, false, 1)}, []MarketModel{fitting("other/Model", 8, 40)}),
		Policy{MinMarginUSD: 0.5, ConfirmCycles: 1, MinDwell: 2 * time.Hour},
		reloaded, now.Add(30*time.Minute))
	if v.Allowed {
		t.Error("the dwell time did not survive the restart")
	}
}

// Starting from an empty history would quietly discard the dwell time and the
// daily cap — the state that exists to stop the node acting too often.
func TestACorruptHistoryIsAnErrorNotAReset(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir, "autoswitch-history.json", `{"switches": [`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHistory(dir); err == nil {
		t.Fatal("a corrupt history was silently reset to empty")
	}
}

func TestFingerprintIgnoresProseAndOrdering(t *testing.T) {
	a := &Decision{Action: ActionSwitch, Serve: []string{"a", "b"}, Stop: []string{"c"},
		ExpectedDailyUSD: 1.00, Reason: "one wording"}
	b := &Decision{Action: ActionSwitch, Serve: []string{"b", "a"}, Stop: []string{"c"},
		ExpectedDailyUSD: 1.01, Reason: "quite another"}

	if Fingerprint(a) != Fingerprint(b) {
		t.Errorf("the same plan fingerprinted differently:\n  %s\n  %s", Fingerprint(a), Fingerprint(b))
	}

	c := &Decision{Action: ActionSwitch, Serve: []string{"a"}, Stop: []string{"c"}}
	if Fingerprint(a) == Fingerprint(c) {
		t.Error("different plans share a fingerprint")
	}

	if Fingerprint(&Decision{Action: ActionKeep}) != ActionKeep {
		t.Error("a keep should fingerprint as a keep")
	}
}

// Stop must wait for an in-flight cycle: a cycle interrupted between starting a
// model and recording it leaves the history disagreeing with reality.
func TestStopWaitsForAnInFlightCycle(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1})
	h.sched.Interval = time.Millisecond
	h.decision = switchTo("new/Model", 20)

	h.sched.Start(context.Background())
	time.Sleep(20 * time.Millisecond)

	done := make(chan struct{})
	go func() { h.sched.Stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}
}

// A history that could not be loaded must make the scheduler refuse to switch,
// not crash the daemon it runs inside.
func TestRunCycleWithoutHistoryRefusesRatherThanPanicking(t *testing.T) {
	h := newHarness(t, Policy{MinMarginUSD: 0.50, ConfirmCycles: 1})
	h.sched.History = nil
	h.decision = switchTo("new/Model", 20)

	out := h.run()
	if out.Executed {
		t.Fatal("switched with no history, so dwell time and the daily cap were unenforced")
	}
	if !violated(out.Verdict, RuleHistoryLost) {
		t.Errorf("violations = %+v", out.Verdict.Violations)
	}
}
