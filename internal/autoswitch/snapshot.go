// Package autoswitch decides which models a node should be serving.
//
// The split through the whole package is between the planner, which is a
// language model and where the fuzzy judgement lives — weighing a rising trend
// against a crowded model, a subscription-heavy standard-tier model against a
// thinner premium one — and the guardrails, which are ordinary code and where
// the operator's money is protected. The planner proposes; code decides whether
// the proposal is allowed. Keeping the two separate is the point: a model that
// hallucinates a model ID, or proposes unloading the only model that earns,
// must not be able to act on it.
package autoswitch

import "github.com/swanchain/computing-provider-v2/internal/market"

// Snapshot is the structured input a planner is given.
//
// Everything here is observed, never inferred: what the node is, what it is
// serving, what the marketplace looks like, and what the last decision claimed
// against what it actually earned. A planner cannot check any of it, so
// anything speculative put in here would come back as a confident decision.
type Snapshot struct {
	Node    NodeInfo       `json:"node"`
	Serving []ServingModel `json:"serving"`
	Market  []MarketModel  `json:"market"`

	// CurrentDailyUSD is what this node earns per day right now, measured.
	// It is the baseline a proposal's projection is compared against, and it
	// is a pointer because "not known" and "zero" are different answers: a
	// zero baseline makes every switch look profitable, which is exactly the
	// mistake that must not be made silently.
	CurrentDailyUSD *float64 `json:"current_daily_usd,omitempty"`

	Previous *Outcome `json:"previous_decision,omitempty"`
}

// NodeInfo describes the hardware a plan has to fit inside.
type NodeInfo struct {
	Name           string `json:"name"`
	GPUModel       string `json:"gpu_model"`
	GPUCount       int    `json:"gpu_count"`
	VRAMPerGPUGB   int    `json:"vram_per_gpu_gb"`
	TotalVRAMGB    int    `json:"total_vram_gb"`
	ServingEngines string `json:"serving_engines,omitempty"`
}

// ServingModel is a model this node currently serves.
type ServingModel struct {
	ModelID string `json:"model_id"`
	Backend string `json:"backend,omitempty"`

	// Managed says whether computing-provider started this server. An
	// unmanaged one cannot be stopped by a switch, so a plan plotting around
	// it would be acting on a false premise.
	Managed bool `json:"managed"`

	// Pinned says the model may never be stopped.
	Pinned bool `json:"pinned"`

	Endpoint string `json:"endpoint"`

	// Healthy is nil when the node cannot tell — the daemon is not running,
	// or has no opinion yet. Nil rather than false: a planner shown
	// "healthy: false" for every model reasons about an outage that is not
	// happening, and says so in its decision.
	Healthy *bool `json:"healthy,omitempty"`

	// EarnedUSD24h is what this model actually earned over the last 24
	// hours, as opposed to what anything estimated it would.
	EarnedUSD24h float64 `json:"earned_usd_24h"`
}

// MarketModel is one model as the marketplace describes it.
type MarketModel struct {
	ModelID  string `json:"model_id"`
	Category string `json:"category"`

	// The provider-side rates, which are what this node would be paid.
	ProviderInputPrice  float64 `json:"provider_input_price"`
	ProviderOutputPrice float64 `json:"provider_output_price"`

	OnlineProviders int     `json:"online_providers"`
	Requests24h     int     `json:"requests_24h"`
	Tokens24h       int64   `json:"tokens_24h"`
	DemandTrend     string  `json:"demand_trend"`
	DemandChangePct float64 `json:"demand_change_pct"`

	MinVRAMGB int  `json:"min_vram_gb"`
	VRAMKnown bool `json:"vram_known"`

	// VRAMFit is the classification the guardrails will apply, included in
	// the snapshot so the planner can see which models are ineligible before
	// it proposes one. It is restated by the guardrails regardless — a
	// planner that ignored this field must still not be able to act.
	VRAMFit string `json:"vram_fit"`

	EstEntrantDailyUSD float64 `json:"est_entrant_daily_usd"`
	EntrantBasis       string  `json:"entrant_basis,omitempty"`
	PlanCovered        bool    `json:"plan_covered"`
	SubscriptionShare  float64 `json:"subscription_share"`
	ContextLength      int     `json:"context_length,omitempty"`
	PromptTokensP95    int     `json:"prompt_tokens_p95,omitempty"`
}

// Outcome pairs a previous decision with what it actually produced.
//
// This is the only way the node learns that a model looked good on the
// entrant estimate and paid a third of it, and it is also the audit trail:
// every switch has a stated reason and a measured result.
type Outcome struct {
	DecidedAt       string   `json:"decided_at"`
	Action          string   `json:"action"`
	Served          []string `json:"served"`
	Stopped         []string `json:"stopped"`
	ProjectedDayUSD float64  `json:"projected_daily_usd"`
	RealisedDayUSD  float64  `json:"realised_daily_usd"`
	Reason          string   `json:"reason"`
}

// servingIDs returns the model IDs currently served.
func (s *Snapshot) servingIDs() map[string]ServingModel {
	out := make(map[string]ServingModel, len(s.Serving))
	for _, m := range s.Serving {
		out[m.ModelID] = m
	}
	return out
}

// marketByID indexes the market snapshot.
func (s *Snapshot) marketByID() map[string]MarketModel {
	out := make(map[string]MarketModel, len(s.Market))
	for _, m := range s.Market {
		out[m.ModelID] = m
	}
	return out
}

// KnownFit reports whether a market model is known to fit this node.
func (s *Snapshot) KnownFit(m MarketModel) bool {
	return market.FitsKnown(m.MinVRAMGB, m.VRAMKnown, s.Node.TotalVRAMGB)
}
