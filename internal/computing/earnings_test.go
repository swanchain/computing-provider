package computing

import (
	"context"
	"errors"
	"testing"
)

type fakePrices struct {
	rates map[string]ModelPrice
	err   error
}

func (f fakePrices) Prices(_ context.Context, _ []string) (map[string]ModelPrice, error) {
	return f.rates, f.err
}

func metricsWith(models map[string][2]int64) *InferenceMetricsData {
	d := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{}}
	for id, t := range models {
		d.ModelMetrics[id] = &ModelMetrics{TotalTokensIn: t[0], TotalTokensOut: t[1]}
	}
	return d
}

func TestEarningsValueTokensAtProviderRates(t *testing.T) {
	e := CalculateEarnings(context.Background(),
		metricsWith(map[string][2]int64{"org/a": {2_000_000, 500_000}}),
		fakePrices{rates: map[string]ModelPrice{
			"org/a": {ProviderInputPrice: 0.10, ProviderOutputPrice: 0.40},
		}})

	if len(e.Models) != 1 {
		t.Fatalf("got %d rows, want 1", len(e.Models))
	}
	m := e.Models[0]
	// 2M in at $0.10/M = $0.20; 0.5M out at $0.40/M = $0.20
	if m.InputUSD != 0.20 || m.OutputUSD != 0.20 {
		t.Errorf("input=%v output=%v, want 0.20 and 0.20", m.InputUSD, m.OutputUSD)
	}
	if e.TotalUSD != 0.40 {
		t.Errorf("total = %v, want 0.40", e.TotalUSD)
	}
	if e.Currency != "USD" {
		t.Errorf("currency = %q, want USD", e.Currency)
	}
}

// A model with traffic but no rate must not read as a confident zero: that
// looks like it earned nothing rather than that we could not price it.
func TestUnpricedModelIsFlaggedNotZeroed(t *testing.T) {
	e := CalculateEarnings(context.Background(),
		metricsWith(map[string][2]int64{"org/priced": {1_000_000, 0}, "org/unknown": {5_000_000, 0}}),
		fakePrices{rates: map[string]ModelPrice{"org/priced": {ProviderInputPrice: 1.0}}})

	if e.Unpriced != 1 {
		t.Errorf("unpriced = %d, want 1", e.Unpriced)
	}
	for _, m := range e.Models {
		if m.Model == "org/unknown" {
			if m.Priced {
				t.Error("unknown model should not be marked priced")
			}
			if m.TokensIn != 5_000_000 {
				t.Errorf("tokens lost: %d", m.TokensIn)
			}
		}
	}
	if e.TotalUSD != 1.0 {
		t.Errorf("total = %v, want only the priced model's 1.0", e.TotalUSD)
	}
}

// Pricing being unavailable must not lose the token counts.
func TestPriceFailureStillReportsTokens(t *testing.T) {
	e := CalculateEarnings(context.Background(),
		metricsWith(map[string][2]int64{"org/a": {1_000, 2_000}}),
		fakePrices{err: errors.New("upstream down")})

	if len(e.Models) != 1 || e.Models[0].TokensOut != 2_000 {
		t.Fatalf("got %+v, want the row with its tokens", e.Models)
	}
	if e.Models[0].Priced || e.TotalUSD != 0 {
		t.Error("nothing should be priced when the catalog failed")
	}
	if e.Unpriced != 1 {
		t.Errorf("unpriced = %d, want 1", e.Unpriced)
	}
}

// Models that have served nothing are noise in an earnings table.
func TestModelsWithNoTrafficAreOmitted(t *testing.T) {
	e := CalculateEarnings(context.Background(),
		metricsWith(map[string][2]int64{"org/idle": {0, 0}, "org/busy": {10, 10}}),
		fakePrices{rates: map[string]ModelPrice{}})
	if len(e.Models) != 1 || e.Models[0].Model != "org/busy" {
		t.Errorf("got %+v, want only org/busy", e.Models)
	}
}

func TestHighestEarnerFirst(t *testing.T) {
	e := CalculateEarnings(context.Background(),
		metricsWith(map[string][2]int64{"org/small": {1_000_000, 0}, "org/big": {9_000_000, 0}}),
		fakePrices{rates: map[string]ModelPrice{
			"org/small": {ProviderInputPrice: 1}, "org/big": {ProviderInputPrice: 1},
		}})
	if e.Models[0].Model != "org/big" {
		t.Errorf("first row is %q, want the highest earner", e.Models[0].Model)
	}
}

func TestNilMetricsIsSafe(t *testing.T) {
	e := CalculateEarnings(context.Background(), nil, nil)
	if e == nil || e.TotalUSD != 0 || len(e.Models) != 0 {
		t.Errorf("got %+v, want an empty result", e)
	}
}
