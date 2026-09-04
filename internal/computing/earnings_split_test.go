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

// The series states the interval each point spans, so the UI labels a point by
// what it aggregated rather than re-deriving the rule from the requested
// window and drifting from it.
func TestEarningsHistoryReportsItsBucket(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	metrics := metricsFor(map[string]ModelTokenCounts{"a": {In: 10}})
	snaps := []HistoricalDataPoint{
		snap(t0, 0, 0, nil),
		snap(t0.Add(30*time.Minute), 100, 0, nil),
		snap(t0.Add(90*time.Minute), 200, 0, nil),
	}

	hourly := CalculateEarningsHistory(context.Background(), snaps, metrics, nil, "24h", time.Hour)
	if hourly.BucketSeconds != 3600 {
		t.Errorf("hourly bucket = %d seconds, want 3600", hourly.BucketSeconds)
	}
	if len(hourly.Points) != 2 {
		t.Errorf("hourly gave %d points, want 2 (samples fall in two different hours)", len(hourly.Points))
	}

	daily := CalculateEarningsHistory(context.Background(), snaps, metrics, nil, "7d", 24*time.Hour)
	if daily.BucketSeconds != 86400 {
		t.Errorf("daily bucket = %d seconds, want 86400", daily.BucketSeconds)
	}
	if len(daily.Points) != 1 {
		t.Errorf("daily gave %d points, want 1 — all three samples are the same day", len(daily.Points))
	}
}

func usd(v float64) *float64 { return &v }

func snapPlatform(t time.Time, in, out int64, models map[string]ModelTokenCounts, platform *float64) HistoricalDataPoint {
	p := snap(t, in, out, models)
	p.PlatformEarningsUSD = platform
	return p
}

// The platform's lifetime figure is the number that governs. Differencing it
// gives what it says was earned in an interval, with no local rate arithmetic.
func TestEarningsHistoryPrefersThePlatformLedger(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	models := map[string]ModelTokenCounts{"a": {In: 1_000_000}}
	// A local rate that would give a wildly different answer, to prove it is
	// not the one being used.
	prices := fixedPrices{"a": {ProviderInputPrice: 999, ProviderOutputPrice: 999}}

	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snapPlatform(t0, 0, 0, map[string]ModelTokenCounts{"a": {}}, usd(10)),
			snapPlatform(t0.Add(time.Minute), 1_000_000, 0, models, usd(12.5)),
		},
		metricsFor(models), prices, "24h", 0)

	last := series.Points[len(series.Points)-1]
	if !last.Authoritative {
		t.Error("bucket should be marked authoritative when it came from the ledger")
	}
	if last.USD < 2.49 || last.USD > 2.51 {
		t.Errorf("bucket = %.4f, want the ledger delta 2.50 — not the local rate", last.USD)
	}
	if series.AuthoritativePoints != 1 {
		t.Errorf("authoritative points = %d, want 1", series.AuthoritativePoints)
	}
}

// Samples with no platform figure keep the local estimate, and say so.
func TestEarningsHistoryFallsBackWhenLedgerAbsent(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	models := map[string]ModelTokenCounts{"a": {In: 1_000_000}}
	prices := fixedPrices{"a": {ProviderInputPrice: 2, ProviderOutputPrice: 0}}

	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snap(t0, 0, 0, map[string]ModelTokenCounts{"a": {}}),
			snap(t0.Add(time.Minute), 1_000_000, 0, models),
		},
		metricsFor(models), prices, "24h", 0)

	last := series.Points[len(series.Points)-1]
	if last.Authoritative {
		t.Error("a bucket with no platform figure must not claim to be authoritative")
	}
	if last.USD < 1.99 || last.USD > 2.01 {
		t.Errorf("fallback = %.4f, want the locally priced 2.00", last.USD)
	}
	if series.AuthoritativePoints != 0 {
		t.Errorf("authoritative points = %d, want 0", series.AuthoritativePoints)
	}
}

// The platform reports no per-model split, so the local share is rescaled onto
// the authoritative total. The bar's height is the platform's; its division is
// this node's estimate — but the segments must still sum to the bar.
func TestEarningsHistoryRescalesModelSplitOntoLedgerTotal(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	models := map[string]ModelTokenCounts{"a": {In: 3_000_000}, "b": {In: 1_000_000}}
	prices := fixedPrices{
		"a": {ProviderInputPrice: 1},
		"b": {ProviderInputPrice: 1},
	}

	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snapPlatform(t0, 0, 0, map[string]ModelTokenCounts{"a": {}, "b": {}}, usd(0)),
			snapPlatform(t0.Add(time.Minute), 4_000_000, 0, models, usd(8)),
		},
		metricsFor(models), prices, "24h", 0)

	last := series.Points[len(series.Points)-1]
	if last.USD < 7.99 || last.USD > 8.01 {
		t.Fatalf("bucket = %.4f, want the ledger's 8.00", last.USD)
	}
	sum := 0.0
	for _, m := range last.Models {
		sum += m.USD
	}
	if sum < 7.99 || sum > 8.01 {
		t.Errorf("model segments sum to %.4f, want them to fill the 8.00 bar", sum)
	}
	// Local rates gave a:b = 3:1, so the rescaled split must keep that ratio.
	if got := last.Models["a"].USD; got < 5.99 || got > 6.01 {
		t.Errorf("model a = %.4f, want 6.00 (3:1 of the ledger total)", got)
	}
	if last.Unattributed > 0.01 {
		t.Errorf("unattributed = %.4f, want ~0 once the split fills the bar", last.Unattributed)
	}
}

// A lifetime total that goes down is a correction or a payout on the platform
// side, not negative earnings.
func TestEarningsHistoryTreatsLedgerDecreaseAsZero(t *testing.T) {
	t0 := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	models := map[string]ModelTokenCounts{"a": {In: 10}}
	series := CalculateEarningsHistory(context.Background(),
		[]HistoricalDataPoint{
			snapPlatform(t0, 0, 0, nil, usd(50)),
			snapPlatform(t0.Add(time.Minute), 10, 0, nil, usd(40)),
		},
		metricsFor(models), nil, "24h", 0)

	last := series.Points[len(series.Points)-1]
	if last.USD != 0 {
		t.Errorf("bucket = %.4f, want 0 — a decrease must not subtract from the window", last.USD)
	}
}
