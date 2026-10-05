package computing

import (
	"testing"
	"time"
)

func seriesWith(points ...UsagePoint) UsageSeries {
	return UsageSeries{Duration: "24h", BucketSeconds: 3600, Points: points}
}

func usagePt(ts time.Time, model, src string, in, out int64) UsagePoint {
	return UsagePoint{
		Timestamp: ts,
		Sources:   map[string]UsageTotals{src: {Requests: 1, TokensIn: in, TokensOut: out}},
		Models:    map[string]map[string]UsageTotals{model: {src: {Requests: 1, TokensIn: in, TokensOut: out}}},
	}
}

// The account's earnings cover every machine, so the combined usage must too:
// a model served on two machines adds up, and one served only on a peer
// appears at all.
func TestCombineUsageSumsMachines(t *testing.T) {
	h0 := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	h1 := h0.Add(time.Hour)
	local := seriesWith(usagePt(h0, "glm", "hub", 100, 10), usagePt(h1, "glm", "hub", 50, 5))
	peer := seriesWith(usagePt(h0, "glm", "hub", 30, 3), usagePt(h0, "cydonia", "hub", 900, 90))

	got := CombineUsage("c2-03", local, map[string]UsageSeries{"gb10": peer}, nil)

	if len(got.Points) != 2 {
		t.Fatalf("points = %d", len(got.Points))
	}
	if g := got.Points[0].Models["glm"]["hub"]; g.TokensIn != 130 || g.TokensOut != 13 {
		t.Errorf("glm at h0 = %+v, want both machines summed", g)
	}
	if c := got.Points[0].Models["cydonia"]["hub"]; c.TokensIn != 900 {
		t.Errorf("a peer-only model is missing: %+v", c)
	}
	if got.Totals["hub"].TokensIn != 1080 {
		t.Errorf("hub totals = %+v", got.Totals["hub"])
	}
	if len(got.Machines) != 2 || !got.Machines[0].Self || got.Machines[0].Name != "c2-03" {
		t.Fatalf("machines = %+v", got.Machines)
	}
	if got.Machines[0].Totals.TokensIn != 150 || got.Machines[1].Totals.TokensIn != 930 {
		t.Errorf("per-machine totals = %+v / %+v", got.Machines[0].Totals, got.Machines[1].Totals)
	}
	if got.Machines[1].Points[1].TokensIn != 0 || got.Machines[1].Points[0].TokensIn != 930 {
		t.Errorf("per-machine points not aligned with the combined buckets: %+v", got.Machines[1].Points)
	}
}

// A peer that could not be read is shown as such, not silently dropped —
// dropping it would read as that machine being idle.
func TestCombineUsageReportsUnreachablePeers(t *testing.T) {
	h0 := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	got := CombineUsage("c2-03", seriesWith(usagePt(h0, "glm", "hub", 1, 1)), nil, map[string]string{"gb10": "connection refused"})
	if len(got.Machines) != 2 || got.Machines[1].Name != "gb10" || got.Machines[1].Error == "" {
		t.Errorf("machines = %+v", got.Machines)
	}
}
