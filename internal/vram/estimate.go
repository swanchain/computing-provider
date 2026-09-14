// Package vram works out how much GPU memory a model needs, so the node can
// decide whether it can run one before downloading it.
//
// This exists because the marketplace does not say. The model-demand table
// publishes vram_known: false for every model it lists, and a node choosing
// models automatically cannot act on "unknown" — it can only refuse. Deriving
// the figure locally is what turns a permanently-refusing scheduler into a
// useful one.
//
// The arithmetic is deliberately conservative and says so when it is guessing.
// An over-estimate declines a model that would have fitted, which costs some
// revenue. An under-estimate is an out-of-memory kill partway through a load,
// after the weights have been pulled, on a node that was serving traffic. Those
// are not comparable, so every unresolved question resolves towards "more
// memory than you think" or towards Unknown.
package vram

import (
	"fmt"
	"strings"
)

// Source says how much an estimate can be trusted. The ordering is deliberate:
// a lower tier's answer is never promoted to a higher tier's confidence.
type Source string

const (
	// SourceMeasured is what the model actually consumed when this node ran
	// it. The only figure that needs no caveat.
	SourceMeasured Source = "measured"

	// SourceDerived is computed from the model's published parameter counts
	// and architecture.
	SourceDerived Source = "derived"

	// SourceUnknown means the node could not work it out. It is a refusal,
	// not a zero.
	SourceUnknown Source = "unknown"
)

// GiB is one gibibyte, the unit VRAM is actually sold and reported in.
const GiB = 1024 * 1024 * 1024

// DefaultOverheadGiB covers the CUDA context, cuBLAS workspace and compute
// buffers that every backend allocates on top of weights and cache.
//
// Measured at 0.99 GiB for llama.cpp serving a 27B at 64k context on this
// project's reference node. One gibibyte is the honest round number; it is not
// a fudge factor absorbing the rest of the model.
const DefaultOverheadGiB = 1.0

// Plan is how the node intends to serve a model. The same weights need very
// different amounts of memory depending on these, so an estimate without them
// is answering a different question than the one being asked.
type Plan struct {
	// ContextLength in tokens. Zero uses the model's own maximum, which is
	// usually far more than a node would actually serve.
	ContextLength int

	// Concurrency is how many sequences are served at once — llama.cpp's
	// --parallel, vLLM's --max-num-seqs. It multiplies the cache, and it is
	// the term most often forgotten.
	Concurrency int

	// KVBytes is bytes per cache element. Zero means 2, for fp16. Use 1.0625
	// for llama.cpp's q8_0 (one byte plus a block scale), 1 for fp8.
	KVBytes float64

	// WeightBytes overrides bytes per parameter, for serving at a precision
	// other than the one the weights are published in — a 4-bit quantisation
	// of a bf16 repo, say. Zero uses the published dtypes.
	WeightBytes float64

	// OverheadGiB overrides DefaultOverheadGiB.
	OverheadGiB float64

	// SafetyFactor overrides DefaultSafetyFactor. Set it to 1 to get the raw
	// arithmetic, which is what a test comparing against a measured figure
	// wants and what a scheduling decision does not.
	SafetyFactor float64
}

func (p Plan) safetyFactor() float64 {
	if p.SafetyFactor <= 0 {
		return DefaultSafetyFactor
	}
	return p.SafetyFactor
}

func (p Plan) concurrency() int {
	if p.Concurrency < 1 {
		return 1
	}
	return p.Concurrency
}

func (p Plan) kvBytes() float64 {
	if p.KVBytes <= 0 {
		return 2 // fp16
	}
	return p.KVBytes
}

func (p Plan) overhead() float64 {
	if p.OverheadGiB <= 0 {
		return DefaultOverheadGiB
	}
	return p.OverheadGiB
}

// Attention names the cache layout, because it changes the arithmetic by
// multiples rather than percentages.
const (
	AttentionMHA     = "mha"     // every head has its own K and V
	AttentionGQA     = "gqa"     // heads share K and V
	AttentionMLA     = "mla"     // compressed latent (DeepSeek V2/V3)
	AttentionHybrid  = "hybrid"  // only some layers cache at all
	AttentionSliding = "sliding" // cache bounded by a window, not the context
)

// Estimate is what a model would need.
type Estimate struct {
	ModelID string `json:"model_id"`
	Source  Source `json:"source"`

	WeightsGiB  float64 `json:"weights_gib"`
	KVCacheGiB  float64 `json:"kv_cache_gib"`
	OverheadGiB float64 `json:"overhead_gib"`
	TotalGiB    float64 `json:"total_gib"`

	// Attention records which cache layout was used.
	Attention string `json:"attention,omitempty"`

	// ContextLength and Concurrency are the plan the figure answers for.
	// Reported back because the number is meaningless without them.
	ContextLength int `json:"context_length,omitempty"`
	Concurrency   int `json:"concurrency,omitempty"`

	// KVLayers is how many layers actually hold a cache, which is not always
	// every layer.
	KVLayers int `json:"kv_layers,omitempty"`

	// Notes records every correction and every assumption, so a surprising
	// figure can be argued with rather than merely disbelieved.
	Notes []string `json:"notes,omitempty"`

	// SafetyFactor is the padding applied to the raw arithmetic.
	SafetyFactor float64 `json:"safety_factor,omitempty"`

	// Reason is why the estimate is Unknown.
	Reason string `json:"reason,omitempty"`
}

// Known reports whether the estimate is usable for an automatic decision.
func (e *Estimate) Known() bool {
	return e != nil && e.Source != SourceUnknown && e.TotalGiB > 0
}

// FitsIn reports whether the model fits in a given VRAM budget, in GiB.
//
// An unknown estimate never fits. That is the same rule internal/market
// applies, and for the same reason: the code this replaced read a missing
// requirement as "fits" and passed 685 GB models onto 10 GB cards.
func (e *Estimate) FitsIn(budgetGiB float64) bool {
	return e.Known() && e.TotalGiB <= budgetGiB
}

// String renders the estimate for a log line or a table cell.
func (e *Estimate) String() string {
	if e == nil {
		return "unknown"
	}
	if !e.Known() {
		if e.Reason != "" {
			return "unknown (" + e.Reason + ")"
		}
		return "unknown"
	}
	return fmt.Sprintf("%.1f GiB (%s: weights %.1f + kv %.1f + overhead %.1f)",
		e.TotalGiB, e.Source, e.WeightsGiB, e.KVCacheGiB, e.OverheadGiB)
}

// unknown builds a refusal carrying its reason.
func unknown(modelID, reason string, notes ...string) *Estimate {
	return &Estimate{
		ModelID: modelID,
		Source:  SourceUnknown,
		Reason:  reason,
		Notes:   notes,
	}
}

// dtypeBytes maps the dtype names HuggingFace reports in its safetensors
// summary onto bytes per element.
//
// An unrecognised dtype is an error rather than a guess: a new float format
// assumed to be two bytes when it is four under-estimates the model by half,
// which is the failure direction that ends in an OOM.
var dtypeBytes = map[string]float64{
	"F64": 8, "I64": 8, "U64": 8,
	"F32": 4, "I32": 4, "U32": 4,
	"BF16": 2, "F16": 2, "I16": 2, "U16": 2,
	"F8_E4M3": 1, "F8_E5M2": 1, "I8": 1, "U8": 1, "BOOL": 1,
	"F4": 0.5, "I4": 0.5, "U4": 0.5,
}

// WeightsGiB sums a safetensors parameter breakdown into bytes.
//
// The breakdown is used rather than a single total because a model is often
// mixed: DeepSeek-V3 publishes 680B parameters at fp8 alongside 3.9B at bf16
// and 41M at fp32, and treating all 684B as one precision is wrong either way.
func WeightsGiB(params map[string]int64) (float64, error) {
	if len(params) == 0 {
		return 0, fmt.Errorf("no parameter counts published")
	}
	var bytes float64
	for dtype, count := range params {
		if count <= 0 {
			continue
		}
		size, ok := dtypeBytes[strings.ToUpper(strings.TrimSpace(dtype))]
		if !ok {
			return 0, fmt.Errorf("unrecognised dtype %q", dtype)
		}
		bytes += float64(count) * size
	}
	if bytes <= 0 {
		return 0, fmt.Errorf("parameter counts sum to zero")
	}
	return bytes / GiB, nil
}

// WeightsGiBAt sums parameters at a single overridden precision, for serving a
// repo at a quantisation it is not published in.
func WeightsGiBAt(totalParams int64, bytesPerParam float64) (float64, error) {
	if totalParams <= 0 {
		return 0, fmt.Errorf("no parameter count published")
	}
	if bytesPerParam <= 0 {
		return 0, fmt.Errorf("bytes per parameter must be positive")
	}
	return float64(totalParams) * bytesPerParam / GiB, nil
}
