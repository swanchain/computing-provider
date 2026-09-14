package vram

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ModelConfig is the part of a HuggingFace config.json that decides cache size.
//
// Parsed permissively: a field this struct does not know about is ignored, and
// a field it knows about but the model omits falls back to something derivable.
// config.json is written by whoever published the model and there is no schema.
type ModelConfig struct {
	ModelType             string `json:"model_type"`
	NumHiddenLayers       int    `json:"num_hidden_layers"`
	HiddenSize            int    `json:"hidden_size"`
	NumAttentionHeads     int    `json:"num_attention_heads"`
	NumKeyValueHeads      int    `json:"num_key_value_heads"`
	HeadDim               int    `json:"head_dim"`
	MaxPositionEmbeddings int    `json:"max_position_embeddings"`
	TorchDtype            string `json:"torch_dtype"`

	// Hybrid attention. Qwen3-Next and Qwen3.5 carry most of their layers as
	// linear attention, which holds no key-value cache at all. Assuming every
	// layer caches over-estimates such a model by several gibibytes.
	LayerTypes            []string `json:"layer_types"`
	FullAttentionInterval int      `json:"full_attention_interval"`

	// Sliding-window attention bounds the cache by the window rather than by
	// the context.
	SlidingWindow    int   `json:"sliding_window"`
	UseSlidingWindow *bool `json:"use_sliding_window"`

	// Multi-head Latent Attention (DeepSeek V2/V3) compresses key and value
	// into one latent vector per token per layer, so the ordinary formula
	// over-estimates it by an order of magnitude.
	KVLoraRank    int `json:"kv_lora_rank"`
	QKRopeHeadDim int `json:"qk_rope_head_dim"`

	// TextConfig holds the language model's own fields on a multimodal repo,
	// where the top level describes the wrapper instead.
	TextConfig *ModelConfig `json:"text_config"`
}

// ParseModelConfig reads a config.json.
//
// When the language model's fields are nested under text_config — the usual
// shape for a multimodal repo — the nested block is used, because the outer one
// describes a wrapper that holds no cache.
func ParseModelConfig(data []byte) (*ModelConfig, error) {
	var cfg ModelConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config.json is not readable: %w", err)
	}
	if cfg.NumHiddenLayers == 0 && cfg.TextConfig != nil && cfg.TextConfig.NumHiddenLayers > 0 {
		nested := *cfg.TextConfig
		if nested.ModelType == "" {
			nested.ModelType = cfg.ModelType
		}
		return &nested, nil
	}
	return &cfg, nil
}

// headDim returns the size of one attention head.
func (c *ModelConfig) headDim() int {
	if c.HeadDim > 0 {
		return c.HeadDim
	}
	if c.HiddenSize > 0 && c.NumAttentionHeads > 0 {
		return c.HiddenSize / c.NumAttentionHeads
	}
	return 0
}

// kvHeads returns the number of key-value heads, which equals the attention
// head count on a model that predates grouped-query attention.
func (c *ModelConfig) kvHeads() int {
	if c.NumKeyValueHeads > 0 {
		return c.NumKeyValueHeads
	}
	return c.NumAttentionHeads
}

// isMLA reports whether the model uses Multi-head Latent Attention.
func (c *ModelConfig) isMLA() bool {
	return c.KVLoraRank > 0
}

// cachingLayers returns how many layers hold a key-value cache, and a note when
// that is not all of them.
func (c *ModelConfig) cachingLayers() (int, string) {
	if len(c.LayerTypes) > 0 {
		full := 0
		for _, t := range c.LayerTypes {
			// Anything not explicitly a non-caching layer is counted as
			// caching. Guessing the other way would under-estimate a
			// layer type this code has not seen before.
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "linear_attention", "mamba", "recurrent", "conv":
			default:
				full++
			}
		}
		if full != len(c.LayerTypes) {
			return full, fmt.Sprintf("hybrid attention: %d of %d layers hold a KV cache", full, len(c.LayerTypes))
		}
		return full, ""
	}

	if c.FullAttentionInterval > 1 && c.NumHiddenLayers > 0 {
		full := c.NumHiddenLayers / c.FullAttentionInterval
		if full < 1 {
			full = 1
		}
		return full, fmt.Sprintf("hybrid attention: every %dth of %d layers holds a KV cache",
			c.FullAttentionInterval, c.NumHiddenLayers)
	}

	return c.NumHiddenLayers, ""
}

// effectiveContext returns the number of tokens the cache must hold, and a note
// when a sliding window makes that fewer than the requested context.
func (c *ModelConfig) effectiveContext(requested int) (int, string) {
	ctx := requested
	if ctx <= 0 {
		ctx = c.MaxPositionEmbeddings
	}
	if ctx <= 0 {
		return 0, ""
	}

	// A sliding window only bounds the cache when the model actually uses it.
	// Qwen publishes a window alongside use_sliding_window: false, and
	// honouring it there would halve an estimate that should not move.
	windowed := c.SlidingWindow > 0 && (c.UseSlidingWindow == nil || *c.UseSlidingWindow)
	if windowed && c.SlidingWindow < ctx {
		return c.SlidingWindow, fmt.Sprintf("sliding window of %d tokens bounds the cache below the %d-token context",
			c.SlidingWindow, ctx)
	}
	return ctx, ""
}

// KVCache returns the cache size in GiB for a plan, the attention layout it
// assumed, the number of caching layers, and any corrections it applied.
func (c *ModelConfig) KVCache(plan Plan) (gib float64, attention string, layers int, notes []string, err error) {
	if c.NumHiddenLayers <= 0 {
		return 0, "", 0, nil, fmt.Errorf("config.json does not say how many layers the model has")
	}

	layers, layerNote := c.cachingLayers()
	if layerNote != "" {
		notes = append(notes, layerNote)
	}
	if layers <= 0 {
		return 0, "", 0, nil, fmt.Errorf("no layer holds a KV cache, which cannot be right")
	}

	ctx, ctxNote := c.effectiveContext(plan.ContextLength)
	if ctxNote != "" {
		notes = append(notes, ctxNote)
	}
	if ctx <= 0 {
		return 0, "", 0, nil, fmt.Errorf("no context length given and config.json does not declare a maximum")
	}

	// elementsPerToken is per layer, and already accounts for storing both a
	// key and a value where the architecture stores them separately.
	var elementsPerToken int
	switch {
	case c.isMLA():
		// One compressed latent per token per layer, not a key and a value
		// per head. DeepSeek-V3 caches 512 + 64 elements where the ordinary
		// formula would have predicted 2 x 128 heads x 128 dims.
		elementsPerToken = c.KVLoraRank + c.QKRopeHeadDim
		attention = AttentionMLA
		notes = append(notes, fmt.Sprintf("multi-head latent attention: %d elements per token per layer, not %d",
			elementsPerToken, 2*c.kvHeads()*c.headDim()))

	default:
		headDim := c.headDim()
		kvHeads := c.kvHeads()
		if headDim <= 0 || kvHeads <= 0 {
			return 0, "", 0, nil, fmt.Errorf("config.json does not give head dimensions")
		}
		elementsPerToken = 2 * kvHeads * headDim
		if c.NumKeyValueHeads > 0 && c.NumKeyValueHeads < c.NumAttentionHeads {
			attention = AttentionGQA
		} else {
			attention = AttentionMHA
		}
	}

	// A hybrid or windowed model is described by that first: it is the fact
	// that changes the number most.
	if layerNote != "" {
		attention = AttentionHybrid
	} else if ctxNote != "" {
		attention = AttentionSliding
	}

	bytes := float64(elementsPerToken) * float64(layers) * float64(ctx) *
		float64(plan.concurrency()) * plan.kvBytes()

	return bytes / GiB, attention, layers, notes, nil
}
