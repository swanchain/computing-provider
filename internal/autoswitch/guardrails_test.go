package autoswitch

import (
	"strings"
	"testing"
)

// node builds a snapshot with one 40 GB node, some served models and a market.
//
// The measured baseline defaults to the sum of what the served models earned,
// because a snapshot with no baseline at all is refused outright and every
// test would then pass for the same uninteresting reason.
func node(serving []ServingModel, models []MarketModel) *Snapshot {
	var baseline float64
	for _, m := range serving {
		baseline += m.EarnedUSD24h
	}
	return &Snapshot{
		Node:            NodeInfo{Name: "test", GPUCount: 4, VRAMPerGPUGB: 10, TotalVRAMGB: 40},
		Serving:         serving,
		Market:          models,
		CurrentDailyUSD: &baseline,
	}
}

// nodeWithoutBaseline is the same snapshot with the measured earnings missing.
func nodeWithoutBaseline(serving []ServingModel, models []MarketModel) *Snapshot {
	s := node(serving, models)
	s.CurrentDailyUSD = nil
	return s
}

func fitting(id string, vramGB int, entrantUSD float64) MarketModel {
	return MarketModel{
		ModelID: id, MinVRAMGB: vramGB, VRAMKnown: true,
		VRAMFit: "fits", EstEntrantDailyUSD: entrantUSD,
	}
}

// unmeasured is the shape every model on the live network currently has: a
// requirement that was never established.
func unmeasured(id string, entrantUSD float64) MarketModel {
	return MarketModel{
		ModelID: id, MinVRAMGB: 0, VRAMKnown: false,
		VRAMFit: "unknown", EstEntrantDailyUSD: entrantUSD,
	}
}

func served(id string, managed, pinned bool, earned float64) ServingModel {
	return ServingModel{ModelID: id, Managed: managed, Pinned: pinned, EarnedUSD24h: earned}
}

// violated reports whether a verdict broke a particular rule.
func violated(v Verdict, rule string) bool {
	for _, x := range v.Violations {
		if x.Rule == rule {
			return true
		}
	}
	return false
}

func TestKeepIsAlwaysAllowed(t *testing.T) {
	v := Evaluate(&Decision{Action: ActionKeep, Reason: "nothing better"}, node(nil, nil), Policy{MinMarginUSD: 99})
	if !v.Allowed {
		t.Fatalf("a keep was refused: %+v", v.Violations)
	}
	if v.Effective.Action != ActionKeep {
		t.Errorf("effective action = %q", v.Effective.Action)
	}
}

// A planner that returns nothing usable must not be read as agreement.
func TestUnparseableDecisionIsRefused(t *testing.T) {
	v := Evaluate(nil, node(nil, nil), Policy{})
	if v.Allowed {
		t.Fatal("a nil decision was allowed")
	}
	if !violated(v, RuleUnparseable) {
		t.Errorf("violations = %+v", v.Violations)
	}
	if v.Effective.Action != ActionKeep {
		t.Errorf("effective action = %q, want keep", v.Effective.Action)
	}
}

func TestUnknownActionIsRefused(t *testing.T) {
	v := Evaluate(&Decision{Action: "restart"}, node(nil, nil), Policy{})
	if v.Allowed || !violated(v, RuleUnknownAction) {
		t.Errorf("verdict = %+v", v)
	}
}

// This is the rule the live network exercises today: not one model publishes a
// VRAM requirement, so nothing is known to fit and every switch is refused.
func TestServingAModelWithNoPublishedRequirementIsRefused(t *testing.T) {
	snapshot := node(nil, []MarketModel{unmeasured("big/Model", 9.99)})

	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"big/Model"}, ExpectedDailyUSD: 9.99,
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if v.Allowed {
		t.Fatal("a model with no published requirement was allowed")
	}
	if !violated(v, RuleNotKnownToFit) {
		t.Errorf("violations = %+v", v.Violations)
	}
	for _, x := range v.Violations {
		if x.Rule == RuleNotKnownToFit && !strings.Contains(x.Detail, "not published") {
			t.Errorf("detail should say the requirement is unpublished, got %q", x.Detail)
		}
	}
}

// A planner can invent a plausible model ID. Serving one would declare a model
// the hub does not route and the node cannot run.
func TestServingAModelNotInTheMarketIsRefused(t *testing.T) {
	snapshot := node(nil, []MarketModel{fitting("real/Model", 8, 5)})

	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"invented/Model-9000"}, ExpectedDailyUSD: 50,
	}, snapshot, Policy{})

	if v.Allowed || !violated(v, RuleUnknownModel) {
		t.Errorf("verdict = %+v", v)
	}
}

func TestStoppingAPinnedModelIsRefused(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("planner/Model", true, true, 1)},
		[]MarketModel{fitting("other/Model", 8, 20)},
	)

	v := Evaluate(&Decision{
		Action: ActionSwitch,
		Serve:  []string{"other/Model"},
		Stop:   []string{"planner/Model"},
		// Large enough that the margin rule cannot be what refuses it.
		ExpectedDailyUSD: 500,
	}, snapshot, Policy{Pin: []string{"planner/Model"}, MinMarginUSD: 0.50})

	if v.Allowed {
		t.Fatal("a pinned model was allowed to be stopped")
	}
	if !violated(v, RulePinnedStop) {
		t.Errorf("violations = %+v", v.Violations)
	}
}

// Pins are matched without regard to case, because an operator's config and
// the planner's reply are two different sources of the same string.
func TestPinMatchingIgnoresCase(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("Planner/Model", true, true, 1)},
		[]MarketModel{fitting("other/Model", 8, 20)},
	)
	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"other/Model"},
		Stop: []string{"Planner/Model"}, ExpectedDailyUSD: 500,
	}, snapshot, Policy{Pin: []string{"planner/model"}})

	if !violated(v, RulePinnedStop) {
		t.Errorf("a pin differing only in case did not apply: %+v", v.Violations)
	}
}

func TestServingADeniedModelIsRefused(t *testing.T) {
	snapshot := node(nil, []MarketModel{fitting("banned/Model", 8, 99)})

	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"banned/Model"}, ExpectedDailyUSD: 99,
	}, snapshot, Policy{Deny: []string{"banned/Model"}})

	if v.Allowed || !violated(v, RuleDenied) {
		t.Errorf("verdict = %+v", v)
	}
}

// computing-provider cannot stop a server it did not start, so a plan that
// depends on stopping one is not executable.
func TestStoppingAnUnmanagedModelIsRefused(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("hand/Run", false, false, 1)},
		[]MarketModel{fitting("other/Model", 8, 20)},
	)

	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"other/Model"},
		Stop: []string{"hand/Run"}, ExpectedDailyUSD: 500,
	}, snapshot, Policy{})

	if v.Allowed || !violated(v, RuleUnmanagedStop) {
		t.Errorf("verdict = %+v", v)
	}
}

func TestStoppingAModelNotServedIsRefused(t *testing.T) {
	snapshot := node(nil, []MarketModel{fitting("other/Model", 8, 20)})

	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"other/Model"},
		Stop: []string{"ghost/Model"}, ExpectedDailyUSD: 500,
	}, snapshot, Policy{})

	if !violated(v, RuleNotServed) {
		t.Errorf("violations = %+v", v.Violations)
	}
}

// The gain is measured against what the node actually earns, not against zero.
func TestMarginIsMeasuredAgainstRealisedEarnings(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("current/Model", true, false, 10.00)},
		[]MarketModel{fitting("other/Model", 8, 10.20)},
	)

	// $10.20 projected against $10.00 earned is a $0.20 gain, below $0.50.
	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"other/Model"}, ExpectedDailyUSD: 10.20,
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if v.Allowed {
		t.Fatal("a switch worth $0.20 cleared a $0.50 margin")
	}
	if !violated(v, RuleMargin) {
		t.Errorf("violations = %+v", v.Violations)
	}

	// The same plan clears a margin it actually beats.
	v = Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"other/Model"}, ExpectedDailyUSD: 10.20,
	}, snapshot, Policy{MinMarginUSD: 0.10})
	if !v.Allowed {
		t.Errorf("a $0.20 gain did not clear a $0.10 margin: %+v", v.Violations)
	}
}

func TestAllowedSwitchCarriesThePlan(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("old/Model", true, false, 1.00)},
		[]MarketModel{fitting("new/Model", 8, 20)},
	)

	v := Evaluate(&Decision{
		Action: ActionSwitch,
		Serve:  []string{"new/Model"},
		Stop:   []string{"old/Model"},
		// Repeated entries must not survive into the executed plan.
		ExpectedDailyUSD: 20,
		Reason:           "higher demand, fewer providers",
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if !v.Allowed {
		t.Fatalf("refused: %+v", v.Violations)
	}
	if v.Effective.Action != ActionSwitch {
		t.Errorf("effective action = %q", v.Effective.Action)
	}
	if len(v.Effective.Serve) != 1 || v.Effective.Serve[0] != "new/Model" {
		t.Errorf("serve = %v", v.Effective.Serve)
	}
	if v.Effective.Reason != "higher demand, fewer providers" {
		t.Errorf("the planner's reason was not carried through: %q", v.Effective.Reason)
	}
}

// Executing the half of a plan that passed leaves the node in a state the
// planner never proposed.
func TestAPartiallyValidPlanIsRefusedWhole(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("old/Model", true, false, 1.00)},
		[]MarketModel{fitting("good/Model", 8, 20), unmeasured("bad/Model", 20)},
	)

	v := Evaluate(&Decision{
		Action:           ActionSwitch,
		Serve:            []string{"good/Model", "bad/Model"},
		Stop:             []string{"old/Model"},
		ExpectedDailyUSD: 40,
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if v.Allowed {
		t.Fatal("a plan with one ineligible model was allowed")
	}
	if v.Effective.Action != ActionKeep {
		t.Errorf("effective action = %q, want keep", v.Effective.Action)
	}
	if len(v.Effective.Serve) != 0 {
		t.Errorf("a refused plan still proposed serving %v", v.Effective.Serve)
	}
}

func TestModelInBothServeAndStopIsRefused(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("a/Model", true, false, 1)},
		[]MarketModel{fitting("a/Model", 8, 20)},
	)
	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"a/Model"}, Stop: []string{"a/Model"},
		ExpectedDailyUSD: 500,
	}, snapshot, Policy{})

	if !violated(v, RuleServeStopClash) {
		t.Errorf("violations = %+v", v.Violations)
	}
}

func TestSwitchThatNamesNothingIsRefused(t *testing.T) {
	v := Evaluate(&Decision{Action: ActionSwitch, ExpectedDailyUSD: 100}, node(nil, nil), Policy{})
	if v.Allowed || !violated(v, RuleEmptySwitch) {
		t.Errorf("verdict = %+v", v)
	}
}

// A plan that would need more VRAM than the node has must be refused even when
// every model in it individually fits.
func TestPlanExceedingTotalVRAMIsRefused(t *testing.T) {
	snapshot := node(
		[]ServingModel{served("resident/Model", true, false, 1)},
		[]MarketModel{
			fitting("resident/Model", 30, 1),
			fitting("new/Model", 24, 50),
		},
	)

	// 24 GB new alongside 30 GB resident is 54 GB on a 40 GB node.
	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"new/Model"}, ExpectedDailyUSD: 50,
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if v.Allowed {
		t.Fatal("a plan needing 54 GB was allowed on a 40 GB node")
	}
	if !violated(v, RuleVRAMBudget) {
		t.Errorf("violations = %+v", v.Violations)
	}

	// Freeing the resident model makes the same plan fit.
	v = Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"new/Model"}, Stop: []string{"resident/Model"},
		ExpectedDailyUSD: 50,
	}, snapshot, Policy{MinMarginUSD: 0.50})
	if !v.Allowed {
		t.Errorf("refused after freeing the resident model: %+v", v.Violations)
	}
}

// A one-shot cycle cannot judge dwell, agreement or the daily cap. Saying so is
// the point: a verdict that printed "allowed" without naming the checks it had
// not run would read as a green light it is not.
func TestDeferredRulesAreNamedOnASwitch(t *testing.T) {
	snapshot := node(nil, []MarketModel{fitting("new/Model", 8, 20)})
	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"new/Model"}, ExpectedDailyUSD: 20,
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if len(v.Deferred) != 3 {
		t.Fatalf("deferred = %v, want the three scheduler-state rules", v.Deferred)
	}
	joined := strings.Join(v.Deferred, " ")
	for _, want := range []string{"dwell", "confirm_cycles", "max_switches_day"} {
		if !strings.Contains(joined, want) {
			t.Errorf("deferred rules do not mention %s: %v", want, v.Deferred)
		}
	}
}

// A keep changes nothing, so there is nothing deferred to warn about.
func TestKeepDefersNothing(t *testing.T) {
	v := Evaluate(&Decision{Action: ActionKeep}, node(nil, nil), Policy{})
	if len(v.Deferred) != 0 {
		t.Errorf("a keep deferred %v", v.Deferred)
	}
}

// A zero baseline makes every proposal look profitable. Not knowing the
// baseline must therefore refuse, not default to zero.
func TestSwitchIsRefusedWhenCurrentEarningsAreUnknown(t *testing.T) {
	snapshot := nodeWithoutBaseline(nil, []MarketModel{fitting("new/Model", 8, 50)})

	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"new/Model"}, ExpectedDailyUSD: 50,
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if v.Allowed {
		t.Fatal("a switch was allowed against an unmeasured baseline")
	}
	if !violated(v, RuleMarginUnknown) {
		t.Errorf("violations = %+v", v.Violations)
	}
	// Reported as its own rule, not as an ordinary margin shortfall: the
	// economics were unmeasurable, not merely unfavourable.
	if violated(v, RuleMargin) {
		t.Error("an unmeasurable baseline was reported as a margin shortfall")
	}
}

// A keep needs no baseline: it changes nothing.
func TestKeepIsAllowedWithoutABaseline(t *testing.T) {
	v := Evaluate(&Decision{Action: ActionKeep}, nodeWithoutBaseline(nil, nil), Policy{})
	if !v.Allowed {
		t.Errorf("a keep was refused for want of a baseline: %+v", v.Violations)
	}
}

// A genuinely zero baseline is a measurement, and a profitable switch against
// it must be allowed. This is the case the nil check must not swallow.
func TestAMeasuredZeroBaselineStillAllowsAProfitableSwitch(t *testing.T) {
	zero := 0.0
	snapshot := node(nil, []MarketModel{fitting("new/Model", 8, 5)})
	snapshot.CurrentDailyUSD = &zero

	v := Evaluate(&Decision{
		Action: ActionSwitch, Serve: []string{"new/Model"}, ExpectedDailyUSD: 5,
	}, snapshot, Policy{MinMarginUSD: 0.50})

	if !v.Allowed {
		t.Errorf("refused against a measured zero baseline: %+v", v.Violations)
	}
}
