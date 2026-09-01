package computing

import (
	"context"
	"testing"
	"time"
)

func pt(min int, in, out int64) HistoricalDataPoint {
	return HistoricalDataPoint{
		Timestamp:      time.Unix(1750000000, 0).Add(time.Duration(min) * time.Minute),
		TotalTokensIn:  in,
		TotalTokensOut: out,
	}
}

func oneModel(in, out int64) *InferenceMetricsData {
	return &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"org/a": {TotalTokensIn: in, TotalTokensOut: out},
	}}
}

// A bucket's earnings are the difference from the previous sample, and the
// first sample only sets the baseline — counting its whole cumulative total
// would attribute every token the node ever served to one bucket.
func TestHistoryDiffsCumulativeCounters(t *testing.T) {
	s := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{pt(0, 1_000_000, 0), pt(60, 3_000_000, 0), pt(120, 4_000_000, 0)},
		oneModel(4_000_000, 0),
		fakePrices{rates: map[string]ModelPrice{"org/a": {ProviderInputPrice: 1.0}}},
		"24h")

	if len(s.Points) != 3 {
		t.Fatalf("got %d points, want 3", len(s.Points))
	}
	if s.Points[0].TokensIn != 0 {
		t.Errorf("first point should be a baseline, got %d tokens", s.Points[0].TokensIn)
	}
	if s.Points[1].TokensIn != 2_000_000 || s.Points[2].TokensIn != 1_000_000 {
		t.Errorf("deltas wrong: %d then %d", s.Points[1].TokensIn, s.Points[2].TokensIn)
	}
	if s.TotalUSD != 3.0 {
		t.Errorf("total = %v, want 3.0", s.TotalUSD)
	}
}

// Counters reset on restart — 17 times in a week of real history — so a
// decrease is a restart, not negative earnings. Getting this wrong subtracts a
// whole process's traffic from the chart.
func TestHistoryTreatsACounterResetAsARestart(t *testing.T) {
	s := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{pt(0, 5_000_000, 0), pt(60, 8_000_000, 0), pt(120, 1_000_000, 0), pt(180, 2_500_000, 0)},
		oneModel(2_500_000, 0),
		fakePrices{rates: map[string]ModelPrice{"org/a": {ProviderInputPrice: 1.0}}},
		"24h")

	if s.Restarts != 1 {
		t.Errorf("restarts = %d, want 1", s.Restarts)
	}
	for i, p := range s.Points {
		if p.TokensIn < 0 || p.USD < 0 {
			t.Errorf("point %d is negative: %+v", i, p)
		}
	}
	// 3M before the restart, then 1M at it, then 1.5M after.
	if s.TotalUSD != 5.5 {
		t.Errorf("total = %v, want 5.5", s.TotalUSD)
	}
}

// Pricing uses a blend weighted by what each model actually served, since the
// stored history has no per-model split.
func TestHistoryPricesWithABlendedRate(t *testing.T) {
	metrics := &InferenceMetricsData{ModelMetrics: map[string]*ModelMetrics{
		"cheap": {TotalTokensIn: 3_000_000},
		"dear":  {TotalTokensIn: 1_000_000},
	}}
	s := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{pt(0, 0, 0), pt(60, 4_000_000, 0)},
		metrics,
		fakePrices{rates: map[string]ModelPrice{
			"cheap": {ProviderInputPrice: 1.0},
			"dear":  {ProviderInputPrice: 5.0},
		}},
		"24h")

	// Blended: (3M*1 + 1M*5) / 4M = 2.0 per million-token unit.
	if got := s.Points[1].USD; got < 7.9 || got > 8.1 {
		t.Errorf("blended price gave %v, want about 8.0", got)
	}
}

func TestHistoryWithNoDataIsSafe(t *testing.T) {
	s := CalculateEarningsHistory(context.Background(), nil, nil, nil, "24h")
	if s == nil || len(s.Points) != 0 || s.TotalUSD != 0 {
		t.Errorf("got %+v, want an empty series", s)
	}
}

// Asking for 30 days of a database holding 7 must not look like three weeks of
// near-zero earnings.
func TestHistoryReportsWhatItActuallyCovers(t *testing.T) {
	s := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{pt(0, 0, 0), pt(120, 1_000, 0)},
		oneModel(1_000, 0), fakePrices{}, "30d")
	if s.Covers == "" {
		t.Error("the series should say how far back it actually reaches")
	}
}
