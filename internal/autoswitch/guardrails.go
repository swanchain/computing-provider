package autoswitch

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Decision is what a planner returns. The shape is fixed by the JSON schema
// the planner is constrained to.
type Decision struct {
	Action           string   `json:"action"`
	Serve            []string `json:"serve"`
	Stop             []string `json:"stop"`
	ExpectedDailyUSD float64  `json:"expected_daily_usd"`
	Reason           string   `json:"reason"`
}

// Actions a decision may carry.
const (
	ActionKeep   = "keep"
	ActionSwitch = "switch"
)

// Policy is the operator's configured limits.
type Policy struct {
	Pin            []string
	Deny           []string
	MinMarginUSD   float64
	MinDwell       time.Duration
	ConfirmCycles  int
	MaxSwitchesDay int
}

// Violation is one broken rule.
type Violation struct {
	// Rule is a stable identifier, so an operator can grep their logs for
	// the same rule across releases.
	Rule string `json:"rule"`

	// Detail names the model and the number that failed.
	Detail string `json:"detail"`
}

func (v Violation) String() string { return v.Rule + ": " + v.Detail }

// Verdict is the guardrails' answer.
type Verdict struct {
	// Allowed reports whether the decision may be executed as proposed.
	Allowed bool `json:"allowed"`

	// Violations are the rules that stopped it.
	Violations []Violation `json:"violations,omitempty"`

	// Deferred names rules a single cycle cannot decide, because they need
	// history the scheduler keeps. They are listed rather than silently
	// skipped: a one-shot `plan` that printed "allowed" without saying which
	// checks it had not run would be read as a green light it is not.
	Deferred []string `json:"deferred,omitempty"`

	// Effective is what would actually happen. On any violation this is a
	// keep — never a partial switch, because executing the half of a plan
	// that passed leaves the node in a state the planner never proposed.
	Effective Decision `json:"effective"`
}

// Rule identifiers.
const (
	RuleUnparseable    = "unparseable_decision"
	RuleUnknownAction  = "unknown_action"
	RuleEmptySwitch    = "switch_serves_nothing"
	RuleServeStopClash = "model_in_serve_and_stop"
	RuleUnknownModel   = "model_not_in_market"
	RuleNotKnownToFit  = "vram_not_known_to_fit"
	RuleVRAMBudget     = "exceeds_vram_budget"
	RuleDenied         = "model_on_deny_list"
	RulePinnedStop     = "stops_a_pinned_model"
	RuleNotServed      = "stops_a_model_not_served"
	RuleUnmanagedStop  = "stops_an_unmanaged_model"
	RuleMargin         = "gain_below_margin"
	RuleMarginUnknown  = "current_earnings_unknown"
)

// Deferred rule identifiers, for the checks a single cycle cannot make.
const (
	DeferredDwell    = "min_dwell (needs the time of the last switch)"
	DeferredConfirm  = "confirm_cycles (needs consecutive agreeing cycles)"
	DeferredDayLimit = "max_switches_day (needs today's switch count)"
)

// Rules that need the scheduler's own history.
const (
	RuleDwell       = "within_min_dwell"
	RuleNotAgreed   = "not_enough_agreeing_cycles"
	RuleDailyLimit  = "daily_switch_limit_reached"
	RuleHistoryLost = "scheduler_history_unavailable"
)

// EvaluateWithHistory is Evaluate plus the three rules that need memory.
//
// Split from Evaluate rather than folded into it so that `inference plan` can
// run the stateless half honestly, naming what it has not checked, while the
// scheduler runs all of it. One function that silently skipped the stateful
// rules when handed a nil history would make a dry run look like a green light.
func EvaluateWithHistory(decision *Decision, snapshot *Snapshot, policy Policy, history *History, now time.Time) Verdict {
	verdict := Evaluate(decision, snapshot, policy)

	// A keep is always allowed and changes nothing, so none of these apply.
	if verdict.Effective.Action == ActionKeep && verdict.Allowed {
		verdict.Deferred = nil
		return verdict
	}
	if !verdict.Allowed {
		return verdict
	}

	// Every rule below is now actually checked, so nothing is deferred.
	verdict.Deferred = nil

	if history == nil {
		// Without memory the node cannot tell how long ago it last acted.
		// Refusing is the only safe reading: acting would honour no dwell
		// time and no daily cap.
		verdict.Allowed = false
		verdict.Violations = append(verdict.Violations, Violation{
			Rule:   RuleHistoryLost,
			Detail: "the scheduler has no history, so dwell time and the daily cap cannot be honoured",
		})
		verdict.Effective = keep("guardrails refused the proposal")
		return verdict
	}

	if policy.MinDwell > 0 {
		if last := history.LastSwitch(); last != nil {
			if elapsed := now.Sub(last.At); elapsed < policy.MinDwell {
				verdict.Violations = append(verdict.Violations, Violation{
					Rule: RuleDwell,
					Detail: fmt.Sprintf("last switch was %s ago, inside the %s dwell time",
						elapsed.Round(time.Minute), policy.MinDwell),
				})
			}
		}
	}

	if policy.ConfirmCycles > 1 {
		agreed := history.ConsecutiveAgreement(Fingerprint(decision))
		if agreed < policy.ConfirmCycles {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule: RuleNotAgreed,
				Detail: fmt.Sprintf("%d of %d consecutive cycles have proposed this plan",
					agreed, policy.ConfirmCycles),
			})
		}
	}

	if policy.MaxSwitchesDay > 0 {
		since := now.Add(-24 * time.Hour)
		if made := history.SwitchesSince(since); made >= policy.MaxSwitchesDay {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule: RuleDailyLimit,
				Detail: fmt.Sprintf("%d switches in the last 24 hours, at the limit of %d",
					made, policy.MaxSwitchesDay),
			})
		}
	}

	if len(verdict.Violations) > 0 {
		verdict.Allowed = false
		verdict.Effective = keep("guardrails refused the proposal")
		sort.SliceStable(verdict.Violations, func(i, j int) bool {
			return verdict.Violations[i].Rule < verdict.Violations[j].Rule
		})
	}
	return verdict
}

// keep is the decision used whenever a proposal is refused.
func keep(reason string) Decision {
	return Decision{Action: ActionKeep, Serve: nil, Stop: nil, Reason: reason}
}

// Evaluate checks a planner's decision against the snapshot and the policy.
//
// It is deliberately total: every path returns a Verdict whose Effective field
// is safe to execute. There is no way to get a partial switch out of it.
func Evaluate(decision *Decision, snapshot *Snapshot, policy Policy) Verdict {
	verdict := Verdict{
		Deferred:  []string{DeferredDwell, DeferredConfirm, DeferredDayLimit},
		Effective: keep("guardrails refused the proposal"),
	}

	// An absent decision is the unparseable case: the planner returned
	// something that could not be read as a decision at all.
	if decision == nil {
		verdict.Violations = append(verdict.Violations, Violation{
			Rule:   RuleUnparseable,
			Detail: "the planner returned no usable decision",
		})
		return verdict
	}

	action := strings.ToLower(strings.TrimSpace(decision.Action))
	switch action {
	case ActionKeep:
		// A keep needs no further checking: it changes nothing, which is
		// always allowed.
		verdict.Allowed = true
		verdict.Effective = keep(decision.Reason)
		verdict.Deferred = nil
		return verdict
	case ActionSwitch:
	default:
		verdict.Violations = append(verdict.Violations, Violation{
			Rule:   RuleUnknownAction,
			Detail: fmt.Sprintf("%q is neither %q nor %q", decision.Action, ActionKeep, ActionSwitch),
		})
		return verdict
	}

	serve := dedupe(decision.Serve)
	stop := dedupe(decision.Stop)

	if len(serve) == 0 && len(stop) == 0 {
		verdict.Violations = append(verdict.Violations, Violation{
			Rule:   RuleEmptySwitch,
			Detail: "action is switch but neither serve nor stop names a model",
		})
		return verdict
	}

	for _, id := range serve {
		if contains(stop, id) {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule:   RuleServeStopClash,
				Detail: id + " appears in both serve and stop",
			})
		}
	}

	deny := set(policy.Deny)
	pin := set(policy.Pin)
	serving := snapshot.servingIDs()
	catalogue := snapshot.marketByID()

	// --- what may be served ---
	for _, id := range serve {
		if deny[strings.ToLower(id)] {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule:   RuleDenied,
				Detail: id + " is on the operator's deny list",
			})
			continue
		}

		entry, known := catalogue[id]
		if !known {
			// A planner can invent a plausible model ID. Serving one would
			// declare a model the hub does not route and the node cannot
			// run.
			verdict.Violations = append(verdict.Violations, Violation{
				Rule:   RuleUnknownModel,
				Detail: id + " is not in the market snapshot",
			})
			continue
		}

		if !snapshot.KnownFit(entry) {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule: RuleNotKnownToFit,
				Detail: fmt.Sprintf("%s: requirement %s against %d GB total",
					id, describeRequirement(entry), snapshot.Node.TotalVRAMGB),
			})
		}
	}

	// --- what may be stopped ---
	for _, id := range stop {
		if pin[strings.ToLower(id)] {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule:   RulePinnedStop,
				Detail: id + " is pinned and may never be stopped",
			})
			continue
		}
		current, isServing := serving[id]
		if !isServing {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule:   RuleNotServed,
				Detail: id + " is not currently served",
			})
			continue
		}
		if !current.Managed {
			// computing-provider cannot stop a server it did not start, so a
			// plan that depends on stopping one is not executable.
			verdict.Violations = append(verdict.Violations, Violation{
				Rule:   RuleUnmanagedStop,
				Detail: id + " was not started by computing-provider and cannot be stopped by it",
			})
		}
	}

	// --- does the result fit ---
	if budget := vramBudget(serve, stop, snapshot); budget.exceeded {
		verdict.Violations = append(verdict.Violations, Violation{
			Rule: RuleVRAMBudget,
			Detail: fmt.Sprintf("plan needs %d GB after freeing %d GB, node has %d GB",
				budget.needed, budget.freed, snapshot.Node.TotalVRAMGB),
		})
	}

	// --- is it worth it ---
	//
	// Checked last so that a plan failing on economics still reports the
	// safety violations alongside; an operator tuning MinMarginUSD should
	// not have to raise it repeatedly to discover the plan was unsafe too.
	if snapshot.CurrentDailyUSD == nil {
		// Without a measured baseline the gain cannot be computed, and
		// assuming zero would make every proposal look profitable. Refusing
		// is the only safe reading, and it is reported as its own rule so an
		// operator can see that the economics were not merely unfavourable
		// but unmeasurable.
		verdict.Violations = append(verdict.Violations, Violation{
			Rule:   RuleMarginUnknown,
			Detail: "this node's current daily earnings are unknown, so the gain from switching cannot be computed",
		})
	} else {
		current := *snapshot.CurrentDailyUSD
		gain := decision.ExpectedDailyUSD - current
		if gain < policy.MinMarginUSD {
			verdict.Violations = append(verdict.Violations, Violation{
				Rule: RuleMargin,
				Detail: fmt.Sprintf("projected $%.2f/day against $%.2f/day now is a $%.2f gain, below the $%.2f margin",
					decision.ExpectedDailyUSD, current, gain, policy.MinMarginUSD),
			})
		}
	}

	if len(verdict.Violations) > 0 {
		sort.SliceStable(verdict.Violations, func(i, j int) bool {
			return verdict.Violations[i].Rule < verdict.Violations[j].Rule
		})
		return verdict
	}

	verdict.Allowed = true
	verdict.Effective = Decision{
		Action:           ActionSwitch,
		Serve:            serve,
		Stop:             stop,
		ExpectedDailyUSD: decision.ExpectedDailyUSD,
		Reason:           decision.Reason,
	}
	return verdict
}

// describeRequirement renders a model's VRAM requirement for an error message.
//
// It distinguishes three cases an operator needs to tell apart: the
// marketplace published a requirement, this node derived one, or neither — and
// the last is the only one that means "nobody knows".
func describeRequirement(m MarketModel) string {
	switch {
	case m.VRAMKnown && m.MinVRAMGB > 0:
		return fmt.Sprintf("%d GB published", m.MinVRAMGB)
	case m.EstimatedVRAMGiB > 0:
		return fmt.Sprintf("%.1f GiB estimated by this node", m.EstimatedVRAMGiB)
	default:
		return "not published and not derivable"
	}
}

type budget struct {
	needed   int
	freed    int
	exceeded bool
}

// requirementGB is a model's VRAM requirement, preferring the marketplace's
// published figure and falling back to what this node derived.
//
// Rounds a derived figure up. The budget check is comparing against a hard
// physical limit, and rounding 23.6 GiB down to 23 is how a plan that does not
// fit is approved.
func requirementGB(m MarketModel) int {
	if m.VRAMKnown && m.MinVRAMGB > 0 {
		return m.MinVRAMGB
	}
	if m.EstimatedVRAMGiB > 0 {
		return int(math.Ceil(m.EstimatedVRAMGiB))
	}
	return 0
}

// vramBudget adds up what a plan needs against what it frees.
//
// A model with neither a published nor a derived requirement contributes
// nothing to `needed`, which would make it look free. That is safe only
// because such a model is already rejected by RuleNotKnownToFit; this check
// exists for the case where requirements are known and the arithmetic is the
// binding constraint.
func vramBudget(serve, stop []string, snapshot *Snapshot) budget {
	catalogue := snapshot.marketByID()
	serving := snapshot.servingIDs()

	var b budget
	for _, id := range serve {
		if m, ok := catalogue[id]; ok {
			b.needed += requirementGB(m)
		}
	}
	for _, id := range stop {
		if _, isServing := serving[id]; !isServing {
			continue
		}
		if m, ok := catalogue[id]; ok {
			b.freed += requirementGB(m)
		}
	}

	// Models staying resident keep holding their VRAM.
	var resident int
	for id, m := range serving {
		if contains(stop, id) {
			continue
		}
		if entry, ok := catalogue[m.ModelID]; ok {
			resident += requirementGB(entry)
		}
	}

	b.exceeded = snapshot.Node.TotalVRAMGB > 0 && b.needed+resident > snapshot.Node.TotalVRAMGB
	return b
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func set(in []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out[s] = true
		}
	}
	return out
}
