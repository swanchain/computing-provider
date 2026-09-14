// Package modelmem is the node's memory of models it has actually run.
//
// Published metadata says what a model should need. This says what it did need,
// here, on this hardware, with these settings — and where the weights came
// from. The two disagree often enough to matter: TheDrummer/Cydonia-24B-v4.3
// derives to 30.7 GiB from its published bf16 parameters, and runs on this
// project's reference node in 18.7 GiB, because it is served as a 4-bit AWQ
// build that no amount of reading the base repository would reveal.
//
// So a model an estimate calls too large may be one the operator has already
// made work. Once that is known, it should never have to be rediscovered.
package modelmem

import (
	"fmt"
	"strings"
	"time"
)

// Status is what happened last time this model was run here.
type Status string

const (
	// StatusWorking means it loaded and served.
	StatusWorking Status = "working"

	// StatusFailed means it did not, and Note says why.
	StatusFailed Status = "failed"
)

// Weights records where a model's files came from and what form they are in.
//
// The point is reproducibility: a year later, "it worked" is useless without
// knowing which build worked. A 24B model has a dozen quantisations and they do
// not behave alike.
type Weights struct {
	// Repo is the source repository, e.g. a HuggingFace repo ID.
	Repo string `json:"repo,omitempty"`

	// Revision pins it, when known.
	Revision string `json:"revision,omitempty"`

	// URL is where it was fetched from, when that was not a plain repo.
	URL string `json:"url,omitempty"`

	// LocalPath is where the weights live on this node.
	LocalPath string `json:"local_path,omitempty"`

	// Format is the weight format actually served: gguf, awq, gptq, fp8,
	// bf16. This is the field that most often explains a disagreement with a
	// derived estimate.
	Format string `json:"format,omitempty"`

	// Quantisation is the specific build, e.g. Q4_K_S, AWQ-4bit.
	Quantisation string `json:"quantisation,omitempty"`

	// SizeBytes is the on-disk size, which is the one weight figure that
	// needs no assumptions at all.
	SizeBytes int64 `json:"size_bytes,omitempty"`
}

// Serving records the configuration that produced the measurement.
//
// A measurement without its settings is not reusable. The same weights at 128k
// context and eight concurrent sequences need several times the memory they do
// at 32k and one, so a record that omitted them would be quoted back at the
// wrong scale.
type Serving struct {
	Backend       string   `json:"backend,omitempty"`
	Image         string   `json:"image,omitempty"`
	GPUs          string   `json:"gpus,omitempty"`
	ContextLength int      `json:"context_length,omitempty"`
	Concurrency   int      `json:"concurrency,omitempty"`
	KVQuant       string   `json:"kv_quant,omitempty"`
	ExtraArgs     []string `json:"extra_args,omitempty"`
}

// Hardware records the machine a measurement was taken on.
//
// Carried because a measurement does not transfer. Twenty gibibytes across two
// 10 GB cards is not the same as twenty on one 24 GB card: the first fits only
// if the model shards, and nothing in a single total says whether it does.
type Hardware struct {
	GPUModel     string `json:"gpu_model,omitempty"`
	GPUCount     int    `json:"gpu_count,omitempty"`
	VRAMPerGPUGB int    `json:"vram_per_gpu_gb,omitempty"`
}

// Measurement is what the model actually consumed.
type Measurement struct {
	// VRAMGiB is the total across every GPU it occupied.
	VRAMGiB float64 `json:"vram_gib"`

	// PerGPUGiB is the split, which is what decides whether it fits a
	// particular arrangement of cards rather than a particular total.
	PerGPUGiB []float64 `json:"per_gpu_gib,omitempty"`

	At time.Time `json:"at"`
}

// Record is everything the node remembers about one model.
type Record struct {
	ModelID string `json:"model_id"`

	Status Status `json:"status"`

	// Note is the operator's or the node's own explanation, and it matters
	// most for a failure: "OOM at 64k, fine at 32k" is the difference between
	// never trying again and trying correctly.
	Note string `json:"note,omitempty"`

	Weights  Weights      `json:"weights"`
	Serving  Serving      `json:"serving"`
	Hardware Hardware     `json:"hardware"`
	Measured *Measurement `json:"measured,omitempty"`

	// Pinned marks a record the node must not overwrite automatically.
	//
	// This is the operator saying "I know this works, stop second-guessing
	// it". Without it, one failed start after a transient upstream problem
	// would erase a configuration that took an afternoon to find.
	Pinned bool `json:"pinned,omitempty"`

	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Successes int       `json:"successes"`
	Failures  int       `json:"failures"`
}

// Works reports whether this model is known to run here.
func (r *Record) Works() bool {
	return r != nil && r.Status == StatusWorking
}

// VRAMGiB returns the measured requirement, or 0 when there is none.
func (r *Record) VRAMGiB() float64 {
	if r == nil || r.Measured == nil {
		return 0
	}
	return r.Measured.VRAMGiB
}

// AppliesTo reports whether a measurement can be reused for a given plan.
//
// Deliberately strict, and asymmetric. A record taken at a larger context than
// the one being asked about is reusable, because the model will need less; one
// taken at a smaller context is not, because it says nothing about the larger.
// The same holds for concurrency. Different hardware is never reusable.
func (r *Record) AppliesTo(hw Hardware, contextLength, concurrency int) bool {
	if r == nil || r.Measured == nil || !r.Works() {
		return false
	}

	if r.Hardware.GPUModel != "" && hw.GPUModel != "" && r.Hardware.GPUModel != hw.GPUModel {
		return false
	}
	if r.Hardware.GPUCount > 0 && hw.GPUCount > 0 && r.Hardware.GPUCount != hw.GPUCount {
		return false
	}
	if r.Hardware.VRAMPerGPUGB > 0 && hw.VRAMPerGPUGB > 0 && r.Hardware.VRAMPerGPUGB != hw.VRAMPerGPUGB {
		return false
	}

	if contextLength > 0 && r.Serving.ContextLength > 0 && r.Serving.ContextLength < contextLength {
		return false
	}
	if concurrency > 0 && r.Serving.Concurrency > 0 && r.Serving.Concurrency < concurrency {
		return false
	}
	return true
}

// Describe renders the record as one line.
func (r *Record) Describe() string {
	if r == nil {
		return "unknown"
	}
	parts := []string{string(r.Status)}
	if v := r.VRAMGiB(); v > 0 {
		parts = append(parts, fmt.Sprintf("%.1f GiB measured", v))
	}
	if r.Weights.Quantisation != "" {
		parts = append(parts, r.Weights.Quantisation)
	} else if r.Weights.Format != "" {
		parts = append(parts, r.Weights.Format)
	}
	if r.Serving.Backend != "" {
		parts = append(parts, r.Serving.Backend)
	}
	if r.Serving.ContextLength > 0 {
		parts = append(parts, fmt.Sprintf("%d ctx", r.Serving.ContextLength))
	}
	return strings.Join(parts, ", ")
}
