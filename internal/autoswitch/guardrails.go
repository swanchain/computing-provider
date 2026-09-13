package autoswitch

import (
	"fmt"
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

// Deferred rule identifiers, for the checks that need scheduler state.
const (
	DeferredDwell    = "min_dwell (needs the time of the last switch)"
	DeferredConfirm  = "confirm_cycles (needs consecutive agreeing cycles)"
	DeferredDayLimit = "max_switches_day (needs today's switch count)"
)

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

// describeRequirement renders a model's VRAM requirement for an error message,
// distinguishing an unpublished requirement from a published one.
func describeRequirement(m MarketModel) string {
	if !m.VRAMKnown || m.MinVRAMGB <= 0 {
		return "not published"
	}
	return fmt.Sprintf("%d GB", m.MinVRAMGB)
}

type budget struct {
	needed   int
	freed    int
	exceeded bool
}

// vramBudget adds up what a plan needs against what it frees.
//
// A model with no published requirement contributes nothing to `needed`, which
// would make an unmeasured model look free. That is safe only because such a
// model is already rejected by RuleNotKnownToFit; this check exists for the
// case where requirements are published and the arithmetic is the binding
// constraint.
func vramBudget(serve, stop []string, snapshot *Snapshot) budget {
	catalogue := snapshot.marketByID()
	serving := snapshot.servingIDs()

	var b budget
	for _, id := range serve {
		if m, ok := catalogue[id]; ok && m.VRAMKnown && m.MinVRAMGB > 0 {
			b.needed += m.MinVRAMGB
		}
	}
	for _, id := range stop {
		if _, isServing := serving[id]; !isServing {
			continue
		}
		if m, ok := catalogue[id]; ok && m.VRAMKnown && m.MinVRAMGB > 0 {
			b.freed += m.MinVRAMGB
		}
	}

	// Models staying resident keep holding their VRAM.
	var resident int
	for id, m := range serving {
		if contains(stop, id) {
			continue
		}
		if entry, ok := catalogue[m.ModelID]; ok && entry.VRAMKnown && entry.MinVRAMGB > 0 {
			resident += entry.MinVRAMGB
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
