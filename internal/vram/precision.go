package vram

import (
	"context"
	"fmt"
)

// Precision is a weight format a node might serve a model in.
type Precision struct {
	Name  string
	Bytes float64
}

// ServingPrecisions are the formats a provider would actually serve, best
// quality first.
//
// Sizing a model only at its published precision answers the wrong question.
// Repositories publish bf16; providers serve quantised. This project's
// reference node runs a 27B model that is 60.2 GiB at bf16 in 17.4 GiB of
// VRAM, because it serves a Q4_K_S GGUF. An estimator that reported 60.2 GiB
// and refused would be refusing a model the node is demonstrably running.
var ServingPrecisions = []Precision{
	{"published", 0}, // 0 means "as published", resolved from the dtype breakdown
	{"fp8", BytesFP8},
	{"q8_0", BytesQ8_0},
	{"q6_k", BytesQ6_K},
	{"q5_k_m", BytesQ5_K_M},
	{"q4_k_m", BytesQ4_K_M},
}

// DefaultMinPrecision is the lowest quality a node will serve at by default.
//
// Q4_K_M rather than something smaller: below four bits the quality loss starts
// to show in a way a paying customer would notice, and a provider that ships
// degraded output to win a fit is trading reputation for VRAM.
const DefaultMinPrecision = "q4_k_m"

// FitResult is the outcome of asking whether a model can be served at all.
type FitResult struct {
	// Estimate at the precision that was chosen, or the published one when
	// nothing fits.
	*Estimate

	// Precision names the format the estimate assumes.
	Precision string `json:"precision"`

	// Fits reports whether the model can be served within the budget at some
	// acceptable precision.
	Fits bool `json:"fits"`

	// RequiresQuantisation is true when the model only fits below its
	// published precision.
	//
	// This is a caveat, not a detail: it means the node must obtain quantised
	// weights, and this package cannot verify that such a build exists. A
	// caller acting on it should confirm the quantised repository before
	// committing to a plan.
	RequiresQuantisation bool `json:"requires_quantisation"`
}

// FitWithin finds the highest-quality precision at which a model fits a budget.
//
// It walks from the published precision downwards and stops at the first that
// fits, so a model that fits without quantisation is never reported as needing
// it. Below minPrecision it gives up rather than continuing into formats that
// would visibly degrade output.
func FitWithin(ctx context.Context, hub *Hub, modelID string, plan Plan, budgetGiB float64, minPrecision string) *FitResult {
	if minPrecision == "" {
		minPrecision = DefaultMinPrecision
	}

	// Fetched once and reused across precisions: the parameter counts and the
	// config do not change with the weight format, and re-fetching per
	// precision would multiply the calls to the hub by six.
	if hub == nil {
		hub = &Hub{}
	}
	params, err := hub.Parameters(ctx, modelID)
	if err != nil {
		return &FitResult{Estimate: unknown(modelID, err.Error())}
	}
	cfg, cfgErr := hub.Config(ctx, modelID)

	var published *Estimate
	allowed := true

	for _, p := range ServingPrecisions {
		if !allowed {
			break
		}
		if p.Name == minPrecision {
			allowed = false // this one is still tried; anything smaller is not
		}

		attempt := plan
		attempt.WeightBytes = p.Bytes

		est := estimateFrom(modelID, params, cfg, cfgErr, attempt)
		if published == nil {
			published = est
			published.Notes = append(published.Notes,
				fmt.Sprintf("sized at its published precision; providers usually serve quantised"))
		}
		if !est.Known() {
			// The cache could not be sized, so no precision will help.
			return &FitResult{Estimate: est, Precision: p.Name}
		}
		if est.TotalGiB <= budgetGiB {
			result := &FitResult{
				Estimate:             est,
				Precision:            p.Name,
				Fits:                 true,
				RequiresQuantisation: p.Name != "published",
			}
			if result.RequiresQuantisation {
				est.Notes = append(est.Notes, fmt.Sprintf(
					"fits only at %s; this assumes a %s build of the weights exists, which has not been verified",
					p.Name, p.Name))
			}
			return result
		}
	}

	// Nothing fits. Report the published estimate, because "needs 709.8 GiB"
	// is a more useful refusal than "does not fit at q4_k_m".
	return &FitResult{Estimate: published, Precision: "published"}
}

// estimateFrom builds an estimate from already-fetched metadata.
func estimateFrom(modelID string, params *SafetensorsSummary, cfg *ModelConfig, cfgErr error, plan Plan) *Estimate {
	var (
		weights float64
		err     error
	)
	if plan.WeightBytes > 0 {
		weights, err = WeightsGiBAt(params.TotalParams(), plan.WeightBytes)
	} else {
		weights, err = WeightsGiB(params.Parameters)
	}
	if err != nil {
		return unknown(modelID, fmt.Sprintf("weights could not be sized: %v", err))
	}

	if cfgErr != nil {
		est := unknown(modelID, fmt.Sprintf("cache size unavailable: %v", cfgErr))
		est.WeightsGiB = weights
		est.Notes = append(est.Notes,
			fmt.Sprintf("weights alone are %.1f GiB, which is a floor and not a requirement", weights))
		return est
	}

	kv, attention, layers, notes, err := cfg.KVCache(plan)
	if err != nil {
		est := unknown(modelID, fmt.Sprintf("cache size could not be computed: %v", err))
		est.WeightsGiB = weights
		return est
	}

	factor := plan.safetyFactor()
	est := &Estimate{
		ModelID:       modelID,
		Source:        SourceDerived,
		WeightsGiB:    weights,
		KVCacheGiB:    kv,
		OverheadGiB:   plan.overhead(),
		Attention:     attention,
		KVLayers:      layers,
		ContextLength: plan.ContextLength,
		Concurrency:   plan.concurrency(),
		SafetyFactor:  factor,
		Notes:         notes,
	}
	est.TotalGiB = (weights + kv + est.OverheadGiB) * factor
	if est.ContextLength == 0 {
		est.ContextLength = cfg.MaxPositionEmbeddings
	}
	return est
}
