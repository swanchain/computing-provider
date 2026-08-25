package computing

import "testing"

// swan-inference#455: the declaration must be conservative. Everything in it is
// a claim the marketplace verifies by measurement, so an unmappable value is
// omitted rather than guessed.

func TestNormalizeQuantizationClosedEnum(t *testing.T) {
	cases := []struct{ format, quant, want string }{
		{"awq", "", "int4"},
		{"gguf", "q4_k_m", "int4"},
		{"", "w4a16", "int4"},
		{"", "q8_0", "int8"},
		{"fp8", "", "fp8"},
		{"bf16", "", "bf16"},
		{"int4", "", "int4"},
		{"", "", ""},
		{"exl2", "unknown-scheme", ""},
	}
	for _, c := range cases {
		if got := normalizeQuantization(c.format, c.quant); got != c.want {
			t.Errorf("normalizeQuantization(%q,%q) = %q, want %q", c.format, c.quant, got, c.want)
		}
	}
}

func TestDeclaredModalitiesByCategory(t *testing.T) {
	cases := map[string]struct{ in, out string }{
		"text-generation": {"text", "text"},
		"multimodal":      {"text", "text"},
		"embedding":       {"text", "embeddings"},
		"image":           {"text", "image"},
		"audio":           {"audio", "transcription"},
		"":                {"text", "text"}, // unknown falls back to text->text
	}
	for cat, want := range cases {
		in, out := declaredModalitiesFor(cat, 32768)
		if len(in) == 0 || len(out) == 0 {
			t.Fatalf("category %q produced an empty modality list", cat)
		}
		if in[0].Type != want.in || out[0].Type != want.out {
			t.Errorf("category %q -> %s/%s, want %s/%s", cat, in[0].Type, out[0].Type, want.in, want.out)
		}
	}
	// Multimodal must additionally declare image input.
	in, _ := declaredModalitiesFor("multimodal", 32768)
	if len(in) < 2 || in[1].Type != "image" {
		t.Error("multimodal must declare image input, or the marketplace cannot route vision requests")
	}
}

func TestContextLengthOnlyDeclaredWhenKnown(t *testing.T) {
	in, _ := declaredModalitiesFor("text-generation", 0)
	if in[0].MaxContextLength != nil {
		t.Error("an unknown context window must be omitted, not declared as 0")
	}
	in, _ = declaredModalitiesFor("text-generation", 65536)
	if in[0].MaxContextLength == nil || in[0].MaxContextLength.Value != 65536 {
		t.Error("a known context window must be declared")
	}
}

func TestEngineDetectionIsConservative(t *testing.T) {
	cases := map[string]string{
		"vllm/vllm-openai:latest":    "vllm",
		"lmsysorg/sglang:latest":     "sglang",
		"ghcr.io/ggml-org/llama.cpp": "llama.cpp",
		"ollama/ollama":              "ollama",
		"some-unknown-image:1.0":     "",
	}
	for container, want := range cases {
		if got := detectEngineName(ModelMapping{Container: container}); got != want {
			t.Errorf("detectEngineName(%q) = %q, want %q", container, got, want)
		}
	}
}
