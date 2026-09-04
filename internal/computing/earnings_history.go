package computing

import (
	"context"
	"time"
)

// EarningsPoint is one bucket of the earnings chart.
type EarningsPoint struct {
	Timestamp time.Time `json:"timestamp"`
	TokensIn  int64     `json:"tokens_in"`
	TokensOut int64     `json:"tokens_out"`
	USD       float64   `json:"usd"`
	// Models splits this bucket by model, for samples recorded with a per-model
	// breakdown. Absent for intervals stored before that was captured: those
	// are unattributed, not zero, and the UI must say which it is rather than
	// inventing a split from today's mix.
	Models map[string]ModelEarningsPoint `json:"models,omitempty"`
	// Unattributed is the part of USD this bucket cannot assign to any model.
	Unattributed float64 `json:"unattributed,omitempty"`
}

// ModelEarningsPoint is one model's contribution to a bucket.
type ModelEarningsPoint struct {
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	USD       float64 `json:"usd"`
}

// EarningsSeries is a priced time series built from this node's own history.
type EarningsSeries struct {
	Points   []EarningsPoint `json:"points"`
	TotalUSD float64         `json:"total_usd"`
	Currency string          `json:"currency"`
	Duration string          `json:"duration"`
	// Restarts is how many times the counters reset inside the window. Those
	// buckets contribute only the tokens served after the restart, so the total
	// is a floor rather than an exact figure, and the UI should say so.
	Restarts int `json:"restarts"`
	// Covers is how far back the stored history actually reaches. Asking for 30
	// days of a database that holds 7 should not silently look like a month of
	// near-zero earnings.
	Covers string `json:"covers,omitempty"`
	// BucketSeconds is the interval each point spans. Sent so the UI can label
	// and describe points from what was actually aggregated, rather than
	// re-deriving the rule from the requested duration and drifting from it.
	BucketSeconds int `json:"bucket_seconds,omitempty"`
}

// blendedRate is the average provider payout per million tokens, weighted by
// what each model has actually served.
//
// The stored history keeps only node-wide token totals, not a per-model split,
// so the exact model mix in any past bucket is unknown. Weighting today's rates
// by the tokens each model has actually served is the closest available
// approximation, and it is why this series is explicitly the node's own
// estimate rather than a statement of earnings.
func blendedRate(metrics *InferenceMetricsData, rates map[string]ModelPrice) (in, out float64) {
	var tin, tout int64
	for id, m := range metrics.ModelMetrics {
		r, ok := rates[id]
		if !ok || m == nil {
			continue
		}
		in += float64(m.TotalTokensIn) * r.ProviderInputPrice
		out += float64(m.TotalTokensOut) * r.ProviderOutputPrice
		tin += m.TotalTokensIn
		tout += m.TotalTokensOut
	}
	if tin > 0 {
		in /= float64(tin)
	}
	if tout > 0 {
		out /= float64(tout)
	}
	return in, out
}

// CalculateEarningsHistory prices the stored metrics history.
//
// The stored counters are cumulative and reset whenever the provider restarts,
// so a bucket's contribution is the difference from the previous sample — and a
// decrease means a restart, not negative earnings. Seventeen such resets in a
// week of real history is a normal number, so this is the common path, not an
// edge case: treating a reset naively would subtract a whole process's traffic
// from the chart.
// bucketSize is the display interval the per-sample deltas are summed into.
// Differencing must happen at the finest resolution available and only then be
// aggregated: down-sampling first and differencing after loses everything
// served between a bucket's start and its last restart, which on a node that
// restarts a dozen times a week is most of it.
func CalculateEarningsHistory(ctx context.Context, snapshots []HistoricalDataPoint, metrics *InferenceMetricsData, prices priceLookup, duration string, bucket time.Duration) *EarningsSeries {
	out := &EarningsSeries{
		Points: []EarningsPoint{}, Currency: "USD", Duration: duration,
		BucketSeconds: int(bucket / time.Second),
	}
	if len(snapshots) == 0 || metrics == nil {
		return out
	}

	var rates map[string]ModelPrice
	if prices != nil {
		ids := make([]string, 0, len(metrics.ModelMetrics))
		for id := range metrics.ModelMetrics {
			ids = append(ids, id)
		}
		if p, err := prices.Prices(ctx, ids); err == nil {
			rates = p
		}
	}
	inRate, outRate := blendedRate(metrics, rates)

	var prevIn, prevOut int64
	prevModels := map[string]ModelTokenCounts{}
	first := true
	for _, s := range snapshots {
		var dIn, dOut int64
		switch {
		case first:
			// Nothing to difference against: the first sample establishes the
			// baseline rather than counting its whole cumulative total as
			// having been earned inside this window.
			first = false
		case s.TotalTokensIn < prevIn || s.TotalTokensOut < prevOut:
			// The counters went backwards, so the process restarted. What this
			// sample holds is everything served since that restart.
			out.Restarts++
			dIn, dOut = s.TotalTokensIn, s.TotalTokensOut
		default:
			dIn, dOut = s.TotalTokensIn-prevIn, s.TotalTokensOut-prevOut
		}
		prevIn, prevOut = s.TotalTokensIn, s.TotalTokensOut

		// Rates are per million tokens, and the deltas are raw token counts.
		usd := float64(dIn)/tokensPerPriceUnit*inRate + float64(dOut)/tokensPerPriceUnit*outRate
		out.TotalUSD += usd

		// Split the same delta by model where the sample carries one. Each
		// model is differenced against its own previous value, because models
		// are added and removed independently and a restart resets them all.
		perModel, attributed := splitByModel(s.ModelTokens, prevModels, rates, inRate, outRate)
		if s.ModelTokens != nil {
			prevModels = s.ModelTokens
		}
		// Only what the split could not account for is unattributed. Comparing
		// against the bucket's own priced total keeps the segments summing to
		// the bar rather than to a separately-rounded figure.
		unattributed := usd - attributed
		if unattributed < 0 {
			unattributed = 0
		}

		key := s.Timestamp
		if bucket > 0 {
			key = s.Timestamp.Truncate(bucket)
		}
		if n := len(out.Points); n > 0 && out.Points[n-1].Timestamp.Equal(key) {
			p := &out.Points[n-1]
			p.TokensIn += dIn
			p.TokensOut += dOut
			p.USD += usd
			p.Unattributed += unattributed
			mergeModelPoints(p, perModel)
			continue
		}
		point := EarningsPoint{
			Timestamp: key, TokensIn: dIn, TokensOut: dOut, USD: usd,
			Unattributed: unattributed,
		}
		mergeModelPoints(&point, perModel)
		out.Points = append(out.Points, point)
	}

	if n := len(snapshots); n > 0 {
		out.Covers = snapshots[n-1].Timestamp.Sub(snapshots[0].Timestamp).Round(time.Hour).String()
	}
	return out
}

// splitByModel differences each model's cumulative counters against the
// previous sample and prices the result, returning the per-model contributions
// and the total USD they account for.
//
// A model priced at its own rate is exact; one with no published rate falls
// back to the node's blended rate so its tokens still appear rather than
// silently vanishing from the chart.
func splitByModel(
	current, previous map[string]ModelTokenCounts,
	rates map[string]ModelPrice,
	blendedIn, blendedOut float64,
) (map[string]ModelEarningsPoint, float64) {
	if len(current) == 0 {
		return nil, 0
	}
	split := make(map[string]ModelEarningsPoint, len(current))
	var attributed float64
	for id, now := range current {
		before, seen := previous[id]
		var dIn, dOut int64
		switch {
		case !seen:
			// First sight of this model in the window. Its counter starts from
			// whatever it already held, so treating the whole value as earned
			// here would credit traffic served before the window.
			dIn, dOut = 0, 0
		case now.In < before.In || now.Out < before.Out:
			// Restart: everything this counter holds was served since it.
			dIn, dOut = now.In, now.Out
		default:
			dIn, dOut = now.In-before.In, now.Out-before.Out
		}
		if dIn == 0 && dOut == 0 {
			continue
		}
		inRate, outRate := blendedIn, blendedOut
		if r, ok := rates[id]; ok {
			inRate, outRate = r.ProviderInputPrice, r.ProviderOutputPrice
		}
		usd := float64(dIn)/tokensPerPriceUnit*inRate + float64(dOut)/tokensPerPriceUnit*outRate
		split[id] = ModelEarningsPoint{TokensIn: dIn, TokensOut: dOut, USD: usd}
		attributed += usd
	}
	return split, attributed
}

// mergeModelPoints accumulates a sample's per-model split into a bucket.
func mergeModelPoints(point *EarningsPoint, split map[string]ModelEarningsPoint) {
	if len(split) == 0 {
		return
	}
	if point.Models == nil {
		point.Models = make(map[string]ModelEarningsPoint, len(split))
	}
	for id, add := range split {
		existing := point.Models[id]
		existing.TokensIn += add.TokensIn
		existing.TokensOut += add.TokensOut
		existing.USD += add.USD
		point.Models[id] = existing
	}
}
