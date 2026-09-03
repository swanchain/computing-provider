package computing

import (
	"context"
	"testing"
	"time"
)

// snap builds one stored sample with a per-model split.
func snap(t time.Time, in, out int64, models map[string]ModelTokenCounts) HistoricalDataPoint {
	return HistoricalDataPoint{
		Timestamp: t, TotalTokensIn: in, TotalTokensOut: out, ModelTokens: models,
	}
}

type fixedPrices map[string]ModelPrice

func (f fixedPrices) Prices(_ context.Context, ids []string) (map[string]ModelPrice, error) {
	out := map[string]ModelPrice{}
	for _, id := range ids {
		if p, ok := f[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

func metricsFor(models map[string]ModelTokenCounts) *InferenceMetricsData {
	mm := map[string]*ModelMetrics{}
	for id, c := range models {
		mm[id] = &ModelMetrics{TotalTokensIn: c.In, TotalTokensOut: c.Out}
	}
	return &InferenceMetricsData{ModelMetrics: mm}
}

func TestEarningsHistorySplitsByModel(t *testing.T) {
	t0 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	final := map[string]ModelTokenCounts{"a": {In: 3_000_000, Out: 1_000_000}, "b": {In: 1_000_000, Out: 0}}
	prices := fixedPrices{
		"a": {ProviderInputPrice: 1, ProviderOutputPrice: 10},
		"b": {ProviderInputPrice: 2, ProviderOutputPrice: 20},
	}

	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snap(t0, 0, 0, map[string]ModelTokenCounts{"a": {}, "b": {}}),
			snap(t0.Add(time.Minute), 4_000_000, 1_000_000, final),
		},
		metricsFor(final), prices, "24h", 0)

	if len(series.Points) != 2 {
		t.Fatalf("got %d points, want 2", len(series.Points))
	}
	p := series.Points[1]
	if len(p.Models) != 2 {
		t.Fatalf("split has %d models, want 2: %+v", len(p.Models), p.Models)
	}

	// a: 3M in @ $1/M + 1M out @ $10/M = $13
	if got := p.Models["a"].USD; got < 12.99 || got > 13.01 {
		t.Errorf("model a = %.4f, want ~13.00 (priced at its own rate, not the blend)", got)
	}
	// b: 1M in @ $2/M = $2
	if got := p.Models["b"].USD; got < 1.99 || got > 2.01 {
		t.Errorf("model b = %.4f, want ~2.00", got)
	}
	if p.Models["a"].TokensIn != 3_000_000 || p.Models["b"].TokensIn != 1_000_000 {
		t.Errorf("token split wrong: %+v", p.Models)
	}
}

// Samples stored before the per-model column existed carry no split. Those
// intervals must report as unattributed, never as zero and never invented.
func TestEarningsHistoryMarksUnattributedWhenNoSplitStored(t *testing.T) {
	t0 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	metrics := metricsFor(map[string]ModelTokenCounts{"a": {In: 1_000_000, Out: 1_000_000}})
	prices := fixedPrices{"a": {ProviderInputPrice: 1, ProviderOutputPrice: 1}}

	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snap(t0, 0, 0, nil),
			snap(t0.Add(time.Minute), 1_000_000, 1_000_000, nil),
		},
		metrics, prices, "24h", 0)

	p := series.Points[len(series.Points)-1]
	if len(p.Models) != 0 {
		t.Errorf("a sample with no stored split must not invent one, got %+v", p.Models)
	}
	if p.USD <= 0 {
		t.Fatalf("bucket should still be priced node-wide, got %v", p.USD)
	}
	if p.Unattributed <= 0 {
		t.Errorf("unattributed = %v, want the whole bucket when nothing can be split", p.Unattributed)
	}
}

// A restart resets every per-model counter. What the sample holds afterwards is
// everything served since the restart, not a negative delta.
func TestEarningsHistorySplitSurvivesRestart(t *testing.T) {
	t0 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	prices := fixedPrices{"a": {ProviderInputPrice: 1, ProviderOutputPrice: 1}}
	after := map[string]ModelTokenCounts{"a": {In: 500_000, Out: 0}}

	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snap(t0, 0, 0, map[string]ModelTokenCounts{"a": {}}),
			snap(t0.Add(time.Minute), 2_000_000, 0, map[string]ModelTokenCounts{"a": {In: 2_000_000}}),
			snap(t0.Add(2*time.Minute), 500_000, 0, after), // counters went backwards
		},
		metricsFor(after), prices, "24h", 0)

	if series.Restarts != 1 {
		t.Errorf("restarts = %d, want 1", series.Restarts)
	}
	last := series.Points[len(series.Points)-1]
	if got := last.Models["a"].TokensIn; got != 500_000 {
		t.Errorf("post-restart split = %d tokens, want the 500000 served since it (not a negative delta)", got)
	}
}

// A model first seen mid-window brings a counter that already holds traffic
// served before the window. Crediting all of it here would inflate that bucket.
func TestEarningsHistoryIgnoresPreexistingTotalOfANewlySeenModel(t *testing.T) {
	t0 := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	prices := fixedPrices{"b": {ProviderInputPrice: 1, ProviderOutputPrice: 1}}
	second := map[string]ModelTokenCounts{"a": {In: 10}, "b": {In: 9_000_000}}

	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snap(t0, 0, 0, map[string]ModelTokenCounts{"a": {}}),
			snap(t0.Add(time.Minute), 9_000_010, 0, second),
		},
		metricsFor(second), prices, "24h", 0)

	last := series.Points[len(series.Points)-1]
	if _, ok := last.Models["b"]; ok {
		t.Errorf("model b was seen for the first time; its pre-existing total must not be credited: %+v", last.Models)
	}
}
