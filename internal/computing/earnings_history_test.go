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
		"24h", 0)

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
		"24h", 0)

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
		"24h", 0)

	// Blended: (3M*1 + 1M*5) / 4M = 2.0 per million-token unit.
	if got := s.Points[1].USD; got < 7.9 || got > 8.1 {
		t.Errorf("blended price gave %v, want about 8.0", got)
	}
}

func TestHistoryWithNoDataIsSafe(t *testing.T) {
	s := CalculateEarningsHistory(context.Background(), nil, nil, nil, "24h", 0)
	if s == nil || len(s.Points) != 0 || s.TotalUSD != 0 {
		t.Errorf("got %+v, want an empty series", s)
	}
}

// Asking for 30 days of a database holding 7 must not look like three weeks of
// near-zero earnings.
func TestHistoryReportsWhatItActuallyCovers(t *testing.T) {
	s := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{pt(0, 0, 0), pt(120, 1_000, 0)},
		oneModel(1_000, 0), fakePrices{}, "30d", 0)
	if s.Covers == "" {
		t.Error("the series should say how far back it actually reaches")
	}
}

// The bug this guards: a coarser display bucket must not change the total.
// Differencing was happening after down-sampling, so a restart inside a bucket
// swallowed everything served before it — the 30-day view of the same week
// reported a quarter of what the 7-day view did.
func TestBucketSizeDoesNotChangeTheTotal(t *testing.T) {
	// Two hours of samples with a restart in the middle of the second hour.
	samples := []HistoricalDataPoint{
		pt(0, 1_000_000, 0), pt(20, 2_000_000, 0), pt(40, 3_000_000, 0),
		pt(60, 4_000_000, 0), pt(80, 500_000, 0), pt(100, 1_500_000, 0),
	}
	metrics := oneModel(1_500_000, 0)
	rates := fakePrices{rates: map[string]ModelPrice{"org/a": {ProviderInputPrice: 1.0}}}

	fine := CalculateEarningsHistory(context.Background(), samples, metrics, rates, "24h", 0)
	hourly := CalculateEarningsHistory(context.Background(), samples, metrics, rates, "24h", time.Hour)
	daily := CalculateEarningsHistory(context.Background(), samples, metrics, rates, "30d", 24*time.Hour)

	if fine.TotalUSD != hourly.TotalUSD || hourly.TotalUSD != daily.TotalUSD {
		t.Errorf("totals diverge with bucket size: fine=%v hourly=%v daily=%v",
			fine.TotalUSD, hourly.TotalUSD, daily.TotalUSD)
	}
	// The total is accumulated independently of bucketing, so comparing totals
	// alone cannot catch a bucketing bug. What must hold is that the plotted
	// points still add up to it: a chart whose bars sum to something other than
	// the stated figure is worse than no chart.
	for _, s := range []*EarningsSeries{fine, hourly, daily} {
		var sum float64
		for _, p := range s.Points {
			sum += p.USD
		}
		if diff := sum - s.TotalUSD; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s: points sum to %v but total says %v", s.Duration, sum, s.TotalUSD)
		}
	}
	if len(daily.Points) >= len(fine.Points) {
		t.Errorf("a daily bucket should aggregate: %d points vs %d fine", len(daily.Points), len(fine.Points))
	}
	// Aggregation must not lose the restart count either.
	if daily.Restarts != fine.Restarts {
		t.Errorf("restarts = %d aggregated vs %d fine", daily.Restarts, fine.Restarts)
	}
}
