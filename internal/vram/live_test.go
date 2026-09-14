package vram

import (
	"context"
	"os"
	"testing"
)

// Live checks run against the real model hub. They are skipped by default:
// the unit tests must pass on a machine with no network, and a hub outage must
// not read as a broken build.
//
//	VRAM_LIVE=1 go test ./internal/vram/ -run TestLive -v
func liveOrSkip(t *testing.T) {
	t.Helper()
	if os.Getenv("VRAM_LIVE") == "" {
		t.Skip("set VRAM_LIVE=1 to run checks against the real model hub")
	}
}

func TestLiveDeriveAgainstTheDemandTable(t *testing.T) {
	liveOrSkip(t)

	const budgetGiB = 40.0 // the reference node: 4x RTX 3080

	cases := []struct {
		id   string
		plan Plan
		what string
	}{
		{"Qwen/Qwen3.8-27B", Plan{ContextLength: 65536, KVBytes: BytesQ8_0, WeightBytes: BytesQ4_K_S}, "this node's own model, q4 weights and q8_0 cache"},
		{"Qwen/Qwen2.5-7B-Instruct", Plan{ContextLength: 32768}, "ordinary grouped-query attention"},
		{"deepseek-ai/DeepSeek-V3-0324", Plan{ContextLength: 32768}, "multi-head latent attention"},
		{"meta-llama/Llama-3.3-70B-Instruct", Plan{ContextLength: 32768}, "gated repository"},
		{"deepseek-ai/DeepSeek-V3.2", Plan{ContextLength: 32768}, "top of the demand table"},
		{"zai-org/GLM-5.2", Plan{ContextLength: 32768}, "second in the demand table"},
	}

	for _, tc := range cases {
		e := Derive(context.Background(), &Hub{}, tc.id, tc.plan)
		verdict := "REFUSE"
		if e.FitsIn(budgetGiB) {
			verdict = "FITS "
		}
		t.Logf("%s  %-36s %s", verdict, tc.id, e)
		if e.Attention != "" {
			t.Logf("       %-36s attention=%s kv_layers=%d", "", e.Attention, e.KVLayers)
		}
		for _, n := range e.Notes {
			t.Logf("       %-36s · %s", "", n)
		}
	}
}

// The estimator has to reproduce what this node actually measured, or it is not
// worth trusting for a model the node has never run.
//
// Qwen3.8-27B on two RTX 3080s: 14.30 GiB of q4 weights, a q8_0 cache at 65536
// tokens, measured at 17.42 GiB total by nvidia-smi.
func TestLiveMatchesTheMeasuredReferenceNode(t *testing.T) {
	liveOrSkip(t)

	const measured = 17.42

	// Unpadded first, to check the arithmetic itself rather than the margin.
	raw := Derive(context.Background(), &Hub{}, "Qwen/Qwen3.8-27B", Plan{
		ContextLength: 65536,
		KVBytes:       BytesQ8_0,
		WeightBytes:   BytesQ4_K_S,
		SafetyFactor:  1,
	})
	if !raw.Known() {
		t.Fatalf("estimate is unknown: %s", raw.Reason)
	}
	t.Logf("raw      %.2f GiB against %.2f measured (delta %+.2f)", raw.TotalGiB, measured, raw.TotalGiB-measured)

	// The hybrid-attention correction is the whole point: without it the cache
	// term is four times too large.
	if raw.Attention != AttentionHybrid {
		t.Errorf("attention = %q, want hybrid — Qwen3.8-27B carries 48 linear layers against 16 full", raw.Attention)
	}
	if raw.KVLayers != 16 {
		t.Errorf("kv layers = %d, want 16", raw.KVLayers)
	}
	if delta := raw.TotalGiB - measured; delta < -1.5 || delta > 1.5 {
		t.Errorf("raw estimate %.2f GiB is %.2f from the measured %.2f", raw.TotalGiB, delta, measured)
	}

	// Padded, which is what a scheduling decision actually sees. It must come
	// out at or above measured: the whole purpose of the margin is that the
	// node never plans against a figure lower than reality.
	padded := Derive(context.Background(), &Hub{}, "Qwen/Qwen3.8-27B", Plan{
		ContextLength: 65536,
		KVBytes:       BytesQ8_0,
		WeightBytes:   BytesQ4_K_S,
	})
	t.Logf("padded   %.2f GiB against %.2f measured (delta %+.2f)", padded.TotalGiB, measured, padded.TotalGiB-measured)
	if padded.TotalGiB < measured {
		t.Errorf("padded estimate %.2f GiB is below the measured %.2f — the margin is meant to prevent exactly this",
			padded.TotalGiB, measured)
	}
}

// The estimator has to agree with what the node is demonstrably running.
//
// Qwen3.8-27B is 60.2 GiB at its published bf16 and this node serves it in
// 17.4 GiB, because it serves a Q4 GGUF. An estimator that only sized the
// published precision would refuse a model the node is running right now.
func TestLiveFitWithinFindsAServeablePrecision(t *testing.T) {
	liveOrSkip(t)

	const budget = 40.0
	plan := Plan{ContextLength: 32768}

	for _, id := range []string{
		"Qwen/Qwen3.8-27B",
		"TheDrummer/Cydonia-24B-v4.3",
		"deepseek-ai/DeepSeek-V3.2",
	} {
		r := FitWithin(context.Background(), &Hub{}, id, plan, budget, "")
		t.Logf("%-32s fits=%-5v precision=%-10s %s", id, r.Fits, r.Precision, r.Estimate)
	}

	// The two this node actually serves must come out as servable.
	for _, id := range []string{"Qwen/Qwen3.8-27B", "TheDrummer/Cydonia-24B-v4.3"} {
		r := FitWithin(context.Background(), &Hub{}, id, plan, budget, "")
		if !r.Fits {
			t.Errorf("%s reported as unservable, but this node is serving it: %s", id, r.Estimate)
		}
		if !r.RequiresQuantisation {
			t.Errorf("%s fitted at its published precision, which would be surprising", id)
		}
	}

	// And a 685B model must not be rescued by quantisation.
	r := FitWithin(context.Background(), &Hub{}, "deepseek-ai/DeepSeek-V3.2", plan, budget, "")
	if r.Fits {
		t.Errorf("DeepSeek-V3.2 reported as fitting 40 GiB: %s", r.Estimate)
	}
}
