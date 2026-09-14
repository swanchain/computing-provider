package vram

import "strings"

// Bytes per parameter for common weight formats.
//
// The quantised values are *measured*, not the nominal bit width, and the
// difference is not cosmetic. A k-quant stores block scales and minimums
// alongside the quantised weights, and it leaves some tensors — embeddings,
// output projections — at a higher precision. Q4_K_S weighs 0.5528 bytes per
// parameter on the reference node's own 27B file, not the 0.5 its name
// suggests: taking the nominal figure under-states the weight term by 9.6%,
// which is an under-estimate, which is the direction that ends in an OOM.
//
// These are close enough for a fit decision and are all erring high within
// their format. Where the exact file is on disk, measure it instead.
const (
	BytesFP32 = 4.0
	BytesFP16 = 2.0
	BytesBF16 = 2.0
	BytesFP8  = 1.0
	BytesINT8 = 1.0

	BytesQ8_0   = 1.0625 // one byte plus a block scale
	BytesQ6_K   = 0.82
	BytesQ5_K_M = 0.73
	BytesQ5_0   = 0.72
	BytesQ4_K_M = 0.58
	BytesQ4_K_S = 0.5528 // measured
	BytesQ4_0   = 0.56
	BytesQ3_K_M = 0.44
	BytesQ2_K   = 0.33

	// BytesAWQ4 and BytesGPTQ4 cover the 4-bit safetensors quantisations
	// vLLM and SGLang serve, which carry scales and zero points per group.
	BytesAWQ4  = 0.60
	BytesGPTQ4 = 0.60
)

// quantBytes maps the names that appear in model IDs and filenames onto bytes
// per parameter. Longest match wins, so q4_k_m is not matched as q4_0.
var quantBytes = []struct {
	name  string
	bytes float64
}{
	{"q4_k_m", BytesQ4_K_M}, {"q4_k_s", BytesQ4_K_S}, {"q5_k_m", BytesQ5_K_M},
	{"q3_k_m", BytesQ3_K_M}, {"q4_k", BytesQ4_K_M}, {"q5_k", BytesQ5_K_M},
	{"q8_0", BytesQ8_0}, {"q6_k", BytesQ6_K}, {"q5_0", BytesQ5_0},
	{"q4_0", BytesQ4_0}, {"q2_k", BytesQ2_K},
	{"awq", BytesAWQ4}, {"gptq", BytesGPTQ4},
	{"fp8", BytesFP8}, {"f8", BytesFP8}, {"int8", BytesINT8}, {"w8a8", BytesINT8},
	{"bf16", BytesBF16}, {"fp16", BytesFP16}, {"f16", BytesFP16},
	{"fp32", BytesFP32}, {"f32", BytesFP32},
}

// BytesPerParam guesses a weight format's size from a name — a model ID, a
// quantisation tag, a GGUF filename. It returns 0 when nothing matches, which
// callers must treat as unknown rather than substituting a default.
func BytesPerParam(name string) float64 {
	lower := strings.ToLower(name)
	for _, q := range quantBytes {
		if strings.Contains(lower, q.name) {
			return q.bytes
		}
	}
	return 0
}

// DefaultSafetyFactor pads a derived estimate.
//
// A derived figure is built from published metadata, a nominal quantisation
// size and a fixed overhead constant, and each of those carries a few percent
// of error in an unknown direction. Ten percent is roughly the spread observed
// between quantisation formats of the same nominal bit width, and it buys back
// the asymmetry that runs through this package: over-estimating declines a
// model that would have fitted, under-estimating kills a node mid-load.
//
// It is applied to derived estimates only. A measured figure is not a guess and
// is not padded.
const DefaultSafetyFactor = 1.10
