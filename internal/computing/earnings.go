package computing

import (
	"context"
	"sort"
)

// tokensPerPriceUnit is the block prices are quoted in: ModelPrice carries the
// rate for one million tokens.
const tokensPerPriceUnit = 1_000_000

// ModelEarnings is what one model has earned from served traffic.
type ModelEarnings struct {
	Model     string `json:"model"`
	TokensIn  int64  `json:"tokens_in"`
	TokensOut int64  `json:"tokens_out"`
	// InputUSD and OutputUSD are split because the two rates differ by roughly
	// an order of magnitude on most models, so a single total hides which side
	// of the traffic is actually paying.
	InputUSD  float64 `json:"input_usd"`
	OutputUSD float64 `json:"output_usd"`
	TotalUSD  float64 `json:"total_usd"`
	// Priced is false when no rate was available, so the UI can say "unpriced"
	// rather than showing a confident $0.00 for a model that has served work.
	Priced bool `json:"priced"`
}

// Earnings is the provider's own reckoning of what it has earned.
type Earnings struct {
	Models   []ModelEarnings `json:"models"`
	TotalUSD float64         `json:"total_usd"`
	Currency string          `json:"currency"`
	// Unpriced counts models with served tokens but no rate, so a total that is
	// lower than expected has a visible reason rather than looking like a loss.
	Unpriced int `json:"unpriced_models"`
}

// priceLookup is the part of the price catalog earnings needs.
type priceLookup interface {
	Prices(ctx context.Context, modelIDs []string) (map[string]ModelPrice, error)
}

// CalculateEarnings values served tokens at the provider payout rates.
//
// It is computed from this node's own counters, which cover only routed work:
// health and self-check probes are written to the request history but never to
// the aggregate token totals, so the node cannot bill itself for checking
// itself.
//
// This is the provider's own arithmetic, not a statement of account. It is
// worth showing next to the platform's figure precisely because the two can
// disagree — and when they do, that disagreement is the useful information.
func CalculateEarnings(ctx context.Context, metrics *InferenceMetricsData, prices priceLookup) *Earnings {
	out := &Earnings{Currency: "USD", Models: []ModelEarnings{}}
	if metrics == nil || len(metrics.ModelMetrics) == 0 {
		return out
	}

	ids := make([]string, 0, len(metrics.ModelMetrics))
	for id := range metrics.ModelMetrics {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var rates map[string]ModelPrice
	if prices != nil {
		if p, err := prices.Prices(ctx, ids); err == nil {
			rates = p
		}
	}

	for _, id := range ids {
		m := metrics.ModelMetrics[id]
		if m == nil || (m.TotalTokensIn == 0 && m.TotalTokensOut == 0) {
			continue // Nothing served; a zero row is noise.
		}
		row := ModelEarnings{Model: id, TokensIn: m.TotalTokensIn, TokensOut: m.TotalTokensOut}
		if r, ok := rates[id]; ok && (r.ProviderInputPrice > 0 || r.ProviderOutputPrice > 0) {
			row.InputUSD = float64(m.TotalTokensIn) / tokensPerPriceUnit * r.ProviderInputPrice
			row.OutputUSD = float64(m.TotalTokensOut) / tokensPerPriceUnit * r.ProviderOutputPrice
			row.TotalUSD = row.InputUSD + row.OutputUSD
			row.Priced = true
			out.TotalUSD += row.TotalUSD
		} else {
			out.Unpriced++
		}
		out.Models = append(out.Models, row)
	}

	// Highest earner first: that is the model an operator wants to see at the
	// top, and it puts unpriced rows at the bottom where they read as caveats.
	sort.SliceStable(out.Models, func(i, j int) bool { return out.Models[i].TotalUSD > out.Models[j].TotalUSD })
	return out
}
