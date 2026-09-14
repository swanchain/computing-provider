package vram

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func near(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, want %.4f (±%.4f)", what, got, want, tol)
	}
}

// A model is often mixed precision. DeepSeek-V3 publishes 680B parameters at
// fp8 alongside 3.9B at bf16, and treating all of them as one precision is
// wrong in whichever direction you pick.
func TestWeightsUsesThePerDtypeBreakdown(t *testing.T) {
	got, err := WeightsGiB(map[string]int64{
		"F8_E4M3": 680_571_043_840,
		"BF16":    3_918_786_560,
		"F32":     41_555_600,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := (680_571_043_840*1.0 + 3_918_786_560*2.0 + 41_555_600*4.0) / GiB
	near(t, got, want, 0.01, "weights")

	// Assuming a single precision would be wrong by hundreds of gibibytes.
	single, _ := WeightsGiBAt(684_531_386_000, 2)
	if math.Abs(single-got) < 100 {
		t.Errorf("single-precision assumption differed by only %.1f GiB; the test is not exercising the point", single-got)
	}
}

// A new float format assumed to be two bytes when it is four under-states the
// model by half, which is the direction that ends in an OOM.
func TestWeightsRefusesAnUnknownDtype(t *testing.T) {
	if _, err := WeightsGiB(map[string]int64{"FP6_SECRET": 1000}); err == nil {
		t.Error("an unrecognised dtype was silently accepted")
	}
	if _, err := WeightsGiB(nil); err == nil {
		t.Error("an empty breakdown was accepted")
	}
}

func TestParseModelConfigPrefersTextConfig(t *testing.T) {
	cfg, err := ParseModelConfig([]byte(`{
		"model_type": "wrapper",
		"text_config": {"num_hidden_layers": 48, "hidden_size": 4096, "num_attention_heads": 32, "num_key_value_heads": 8}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NumHiddenLayers != 48 {
		t.Errorf("layers = %d, want the nested 48", cfg.NumHiddenLayers)
	}
	if cfg.ModelType != "wrapper" {
		t.Errorf("model type = %q, want it carried down from the outer block", cfg.ModelType)
	}
}

func TestKVCacheGroupedQueryAttention(t *testing.T) {
	// Qwen2.5-7B: 28 layers, 4 KV heads, head dim 128.
	cfg := &ModelConfig{
		NumHiddenLayers: 28, HiddenSize: 3584,
		NumAttentionHeads: 28, NumKeyValueHeads: 4,
	}
	gib, attention, layers, _, err := cfg.KVCache(Plan{ContextLength: 32768})
	if err != nil {
		t.Fatal(err)
	}
	want := float64(2*4*128*28*32768*2) / GiB
	near(t, gib, want, 0.01, "kv cache")
	if attention != AttentionGQA {
		t.Errorf("attention = %q, want gqa", attention)
	}
	if layers != 28 {
		t.Errorf("layers = %d, want 28", layers)
	}
}

// Without grouped-query attention every head caches its own key and value.
func TestKVCacheFallsBackToAttentionHeadsWhenNoKVHeads(t *testing.T) {
	cfg := &ModelConfig{NumHiddenLayers: 32, HiddenSize: 4096, NumAttentionHeads: 32}
	gib, attention, _, _, err := cfg.KVCache(Plan{ContextLength: 4096})
	if err != nil {
		t.Fatal(err)
	}
	want := float64(2*32*128*32*4096*2) / GiB
	near(t, gib, want, 0.01, "kv cache")
	if attention != AttentionMHA {
		t.Errorf("attention = %q, want mha", attention)
	}
}

// The correction that matters most: Qwen3.8-27B declares 48 linear-attention
// layers against 16 full. Caching all 64 over-estimates it four-fold.
func TestKVCacheCountsOnlyCachingLayers(t *testing.T) {
	layerTypes := make([]string, 0, 64)
	for i := 0; i < 64; i++ {
		if (i+1)%4 == 0 {
			layerTypes = append(layerTypes, "full_attention")
		} else {
			layerTypes = append(layerTypes, "linear_attention")
		}
	}
	cfg := &ModelConfig{
		NumHiddenLayers: 64, HiddenSize: 5120,
		NumAttentionHeads: 24, NumKeyValueHeads: 4, HeadDim: 256,
		LayerTypes: layerTypes,
	}

	gib, attention, layers, notes, err := cfg.KVCache(Plan{ContextLength: 65536, KVBytes: BytesQ8_0})
	if err != nil {
		t.Fatal(err)
	}
	if layers != 16 {
		t.Fatalf("caching layers = %d, want 16", layers)
	}
	if attention != AttentionHybrid {
		t.Errorf("attention = %q, want hybrid", attention)
	}
	want := 2 * 4 * 256 * 16 * 65536 * BytesQ8_0 / GiB
	near(t, gib, want, 0.01, "kv cache")

	// The naive reading is four times larger; a note has to say why it is not.
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, " "), "16 of 64") {
		t.Errorf("notes do not explain the correction: %v", notes)
	}
}

// Some configs declare the interval instead of listing every layer.
func TestKVCacheHonoursFullAttentionInterval(t *testing.T) {
	cfg := &ModelConfig{
		NumHiddenLayers: 64, HiddenSize: 5120,
		NumAttentionHeads: 24, NumKeyValueHeads: 4, HeadDim: 256,
		FullAttentionInterval: 4,
	}
	_, attention, layers, _, err := cfg.KVCache(Plan{ContextLength: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if layers != 16 {
		t.Errorf("caching layers = %d, want 16", layers)
	}
	if attention != AttentionHybrid {
		t.Errorf("attention = %q, want hybrid", attention)
	}
}

// A layer type this code has not seen is counted as caching, because guessing
// the other way under-estimates.
func TestUnrecognisedLayerTypeCountsAsCaching(t *testing.T) {
	cfg := &ModelConfig{
		NumHiddenLayers: 4, HiddenSize: 512, NumAttentionHeads: 8, NumKeyValueHeads: 8,
		LayerTypes: []string{"full_attention", "something_new", "linear_attention", "full_attention"},
	}
	_, _, layers, _, err := cfg.KVCache(Plan{ContextLength: 128})
	if err != nil {
		t.Fatal(err)
	}
	if layers != 3 {
		t.Errorf("caching layers = %d, want 3 — an unknown layer type must count as caching", layers)
	}
}

// DeepSeek-V3 caches 512+64 elements per token per layer where the ordinary
// formula predicts 2 x 128 heads x 128 dims — a factor of about 25.
func TestKVCacheMultiHeadLatentAttention(t *testing.T) {
	cfg := &ModelConfig{
		NumHiddenLayers: 61, HiddenSize: 7168,
		NumAttentionHeads: 128, KVLoraRank: 512, QKRopeHeadDim: 64,
	}
	gib, attention, _, notes, err := cfg.KVCache(Plan{ContextLength: 32768})
	if err != nil {
		t.Fatal(err)
	}
	if attention != AttentionMLA {
		t.Errorf("attention = %q, want mla", attention)
	}
	want := float64((512+64)*61*32768*2) / GiB
	near(t, gib, want, 0.01, "kv cache")

	naive := float64(2*128*128*61*32768*2) / GiB
	if gib >= naive/10 {
		t.Errorf("MLA cache %.1f GiB is not meaningfully below the naive %.1f GiB", gib, naive)
	}
	if len(notes) == 0 {
		t.Error("the MLA correction was applied without explaining itself")
	}
}

func TestSlidingWindowBoundsTheCache(t *testing.T) {
	enabled := true
	cfg := &ModelConfig{
		NumHiddenLayers: 26, HiddenSize: 2304, NumAttentionHeads: 8, NumKeyValueHeads: 4,
		SlidingWindow: 4096, UseSlidingWindow: &enabled,
	}
	gib, attention, _, notes, err := cfg.KVCache(Plan{ContextLength: 32768})
	if err != nil {
		t.Fatal(err)
	}
	if attention != AttentionSliding {
		t.Errorf("attention = %q, want sliding", attention)
	}
	want := float64(2*4*288*26*4096*2) / GiB
	near(t, gib, want, 0.01, "kv cache")
	if len(notes) == 0 {
		t.Error("the window correction was applied without explaining itself")
	}
}

// Qwen publishes a sliding_window alongside use_sliding_window: false.
// Honouring the window there would halve an estimate that should not move.
func TestSlidingWindowIgnoredWhenDisabled(t *testing.T) {
	disabled := false
	cfg := &ModelConfig{
		NumHiddenLayers: 28, HiddenSize: 3584, NumAttentionHeads: 28, NumKeyValueHeads: 4,
		SlidingWindow: 4096, UseSlidingWindow: &disabled,
	}
	gib, attention, _, _, err := cfg.KVCache(Plan{ContextLength: 32768})
	if err != nil {
		t.Fatal(err)
	}
	if attention == AttentionSliding {
		t.Error("a disabled sliding window was applied")
	}
	want := float64(2*4*128*28*32768*2) / GiB
	near(t, gib, want, 0.01, "kv cache")
}

// Concurrency multiplies the cache, and it is the term most often forgotten.
func TestConcurrencyMultipliesTheCache(t *testing.T) {
	cfg := &ModelConfig{NumHiddenLayers: 28, HiddenSize: 3584, NumAttentionHeads: 28, NumKeyValueHeads: 4}
	one, _, _, _, _ := cfg.KVCache(Plan{ContextLength: 8192, Concurrency: 1})
	four, _, _, _, _ := cfg.KVCache(Plan{ContextLength: 8192, Concurrency: 4})
	near(t, four, one*4, 0.01, "cache at concurrency 4")

	// Zero must mean one, not zero.
	zero, _, _, _, _ := cfg.KVCache(Plan{ContextLength: 8192})
	near(t, zero, one, 0.0001, "cache at unset concurrency")
}

func TestKVCacheRefusesAnIncompleteConfig(t *testing.T) {
	if _, _, _, _, err := (&ModelConfig{}).KVCache(Plan{ContextLength: 1024}); err == nil {
		t.Error("a config with no layers was accepted")
	}
	cfg := &ModelConfig{NumHiddenLayers: 8}
	if _, _, _, _, err := cfg.KVCache(Plan{ContextLength: 1024}); err == nil {
		t.Error("a config with no head dimensions was accepted")
	}
	noCtx := &ModelConfig{NumHiddenLayers: 8, HiddenSize: 512, NumAttentionHeads: 8, NumKeyValueHeads: 8}
	if _, _, _, _, err := noCtx.KVCache(Plan{}); err == nil {
		t.Error("no context length and no declared maximum was accepted")
	}
}

// An unknown estimate must never satisfy a fit check. This is the same rule
// internal/market applies, and for the same reason.
func TestUnknownNeverFits(t *testing.T) {
	e := unknown("a/Model", "no metadata")
	if e.Known() {
		t.Error("an unknown estimate reported itself as known")
	}
	if e.FitsIn(10000) {
		t.Error("an unknown estimate fitted an enormous budget")
	}
	var nilEstimate *Estimate
	if nilEstimate.Known() || nilEstimate.FitsIn(10000) {
		t.Error("a nil estimate was treated as known")
	}
	// A zero total is not a model that needs no memory.
	zero := &Estimate{Source: SourceDerived}
	if zero.Known() {
		t.Error("a zero-total estimate reported itself as known")
	}
}

func TestFitsIn(t *testing.T) {
	e := &Estimate{Source: SourceDerived, TotalGiB: 39.5}
	if !e.FitsIn(40) {
		t.Error("39.5 GiB did not fit 40 GiB")
	}
	if e.FitsIn(39) {
		t.Error("39.5 GiB fitted 39 GiB")
	}
}

// Longest match wins, or q4_k_m is read as q4_0 and the estimate drifts.
func TestBytesPerParamMatchesTheLongestName(t *testing.T) {
	cases := map[string]float64{
		"Qwen3-27B-Q4_K_M.gguf":     BytesQ4_K_M,
		"model-q4_k_s.gguf":         BytesQ4_K_S,
		"foo-Q8_0.gguf":             BytesQ8_0,
		"Llama-3.1-8B-AWQ":          BytesAWQ4,
		"Mistral-Small-FP8-Dynamic": BytesFP8,
		"something-bf16":            BytesBF16,
	}
	for name, want := range cases {
		if got := BytesPerParam(name); got != want {
			t.Errorf("BytesPerParam(%q) = %v, want %v", name, got, want)
		}
	}

	// No match must be zero, so a caller cannot mistake a default for a fact.
	if got := BytesPerParam("Some-Model-Without-A-Tag"); got != 0 {
		t.Errorf("BytesPerParam of an untagged name = %v, want 0", got)
	}
}

// The nominal bit width is not the file size. Taking 0.5 for Q4_K_S
// under-states weights by ~10%, which is the direction that ends in an OOM.
func TestQuantisationConstantsExceedTheirNominalBitWidth(t *testing.T) {
	if BytesQ4_K_S <= 0.5 {
		t.Errorf("Q4_K_S = %v, which is at or below the nominal 0.5 and so under-states real files", BytesQ4_K_S)
	}
	if BytesQ8_0 <= 1.0 {
		t.Errorf("Q8_0 = %v, which ignores the block scale", BytesQ8_0)
	}
}

// --- hub behaviour ---

func testHub(t *testing.T, handler http.HandlerFunc) *Hub {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Hub{BaseURL: srv.URL}
}

func TestDeriveEndToEnd(t *testing.T) {
	hub := testHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/models/") {
			w.Write([]byte(`{"safetensors":{"total":7615616512,"parameters":{"BF16":7615616512}}}`))
			return
		}
		w.Write([]byte(`{"num_hidden_layers":28,"hidden_size":3584,"num_attention_heads":28,"num_key_value_heads":4,"max_position_embeddings":32768}`))
	})

	e := Derive(context.Background(), hub, "Qwen/Qwen2.5-7B-Instruct", Plan{ContextLength: 32768, SafetyFactor: 1})
	if !e.Known() {
		t.Fatalf("unknown: %s", e.Reason)
	}
	near(t, e.WeightsGiB, 7615616512*2/float64(GiB), 0.01, "weights")
	near(t, e.KVCacheGiB, float64(2*4*128*28*32768*2)/GiB, 0.01, "kv")
	near(t, e.TotalGiB, e.WeightsGiB+e.KVCacheGiB+DefaultOverheadGiB, 0.01, "total")
	if e.Source != SourceDerived {
		t.Errorf("source = %q", e.Source)
	}
}

// A derived estimate is padded; the note has to say so, or a surprising figure
// cannot be argued with.
func TestDerivePadsAndSaysSo(t *testing.T) {
	hub := testHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/models/") {
			w.Write([]byte(`{"safetensors":{"total":1000000000,"parameters":{"BF16":1000000000}}}`))
			return
		}
		w.Write([]byte(`{"num_hidden_layers":4,"hidden_size":512,"num_attention_heads":8,"num_key_value_heads":8,"max_position_embeddings":2048}`))
	})

	raw := Derive(context.Background(), hub, "a/Model", Plan{ContextLength: 2048, SafetyFactor: 1})
	padded := Derive(context.Background(), hub, "a/Model", Plan{ContextLength: 2048})

	near(t, padded.TotalGiB, raw.TotalGiB*DefaultSafetyFactor, 0.01, "padded total")
	if !strings.Contains(strings.Join(padded.Notes, " "), "padded") {
		t.Errorf("the padding was applied without a note: %v", padded.Notes)
	}
	if strings.Contains(strings.Join(raw.Notes, " "), "padded") {
		t.Error("an unpadded estimate claimed to be padded")
	}
}

// A gated repository gives up its parameter counts but not its config. The
// weights are still worth reporting, but a total that silently omitted the
// cache would be an under-estimate presented as a fact.
func TestDeriveStaysUnknownWhenOnlyWeightsAreReadable(t *testing.T) {
	hub := testHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/models/") {
			w.Write([]byte(`{"safetensors":{"total":70553706496,"parameters":{"BF16":70553706496}}}`))
			return
		}
		http.Error(w, "gated", http.StatusUnauthorized)
	})

	e := Derive(context.Background(), hub, "meta-llama/Llama-3.3-70B-Instruct", Plan{ContextLength: 32768})
	if e.Known() {
		t.Fatal("an estimate with no cache term reported itself as known")
	}
	if e.WeightsGiB <= 0 {
		t.Error("the weight size that was readable was thrown away")
	}
	if !strings.Contains(e.Reason, "gated") {
		t.Errorf("the reason does not say the repo is gated: %q", e.Reason)
	}
	if !strings.Contains(strings.Join(e.Notes, " "), "floor") {
		t.Errorf("the notes do not mark the weight figure as a floor: %v", e.Notes)
	}
}

func TestDeriveReportsARepoWithNoSafetensors(t *testing.T) {
	hub := testHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"someone/model-GGUF"}`))
	})
	e := Derive(context.Background(), hub, "someone/model-GGUF", Plan{})
	if e.Known() {
		t.Fatal("a repo with no parameter counts produced a known estimate")
	}
	if !strings.Contains(e.Reason, "safetensors") {
		t.Errorf("reason = %q", e.Reason)
	}
}

func TestDeriveSurfacesATransportFailure(t *testing.T) {
	hub := testHub(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	e := Derive(context.Background(), hub, "a/Model", Plan{})
	if e.Known() {
		t.Fatal("a failing hub produced a known estimate")
	}
	if !strings.Contains(e.Reason, "500") {
		t.Errorf("reason should carry the status: %q", e.Reason)
	}
}

// The token is only needed for gated config reads, but it must be sent when set.
func TestHubSendsTheToken(t *testing.T) {
	var auth string
	hub := testHub(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write([]byte(`{"safetensors":{"total":1,"parameters":{"BF16":1}}}`))
	})
	hub.Token = "hf_secret"

	if _, err := hub.Parameters(context.Background(), "a/Model"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer hf_secret" {
		t.Errorf("Authorization = %q", auth)
	}
}

// The hub's published total is not reliable. Cydonia-24B reports a breakdown of
// 23.6B parameters alongside a total of 414,720 — five orders of magnitude out.
// Trusting it sized a 24B model at 0.0 GiB and called it a comfortable fit.
func TestTotalParamsPrefersTheBreakdownOverThePublishedTotal(t *testing.T) {
	s := &SafetensorsSummary{
		Parameters: map[string]int64{"BF16": 23_572_403_200},
		Total:      414_720,
	}
	if got := s.TotalParams(); got != 23_572_403_200 {
		t.Errorf("TotalParams = %d, want the breakdown's 23,572,403,200", got)
	}

	// With no breakdown the published total is all there is.
	only := &SafetensorsSummary{Total: 7_000_000_000}
	if got := only.TotalParams(); got != 7_000_000_000 {
		t.Errorf("TotalParams = %d, want the published total as a fallback", got)
	}

	// Neither means zero, which callers must treat as unknown.
	if got := (&SafetensorsSummary{}).TotalParams(); got != 0 {
		t.Errorf("TotalParams = %d, want 0", got)
	}
}

// The whole failure reproduced: a quantisation override must size from the
// breakdown, or a 24B model comes out weighing nothing and reports as fitting.
func TestFitWithinDoesNotSizeAModelToNothing(t *testing.T) {
	hub := testHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/models/") {
			w.Write([]byte(`{"safetensors":{"total":414720,"parameters":{"BF16":23572403200}}}`))
			return
		}
		w.Write([]byte(`{"num_hidden_layers":40,"hidden_size":5120,"num_attention_heads":32,"num_key_value_heads":8,"max_position_embeddings":32768}`))
	})

	r := FitWithin(context.Background(), hub, "TheDrummer/Cydonia-24B-v4.3", Plan{ContextLength: 32768}, 40, "")
	if !r.Known() {
		t.Fatalf("unknown: %s", r.Reason)
	}
	if r.WeightsGiB < 5 {
		t.Errorf("a 23.6B model was sized at %.2f GiB of weights", r.WeightsGiB)
	}
}
