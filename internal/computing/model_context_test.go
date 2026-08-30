package computing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseModelContexts(t *testing.T) {
	// vLLM/SGLang style listing
	body := `{"object":"list","data":[
		{"id":"TheDrummer/Cydonia-24B-v4.3","object":"model","max_model_len":131072},
		{"id":"meta-llama/Llama-3.2-3B-Instruct","object":"model","max_model_len":8192},
		{"id":"no-context-model","object":"model"}
	]}`
	contexts := parseModelContexts(strings.NewReader(body))
	if contexts["TheDrummer/Cydonia-24B-v4.3"] != 131072 {
		t.Errorf("expected 131072, got %d", contexts["TheDrummer/Cydonia-24B-v4.3"])
	}
	if contexts["meta-llama/Llama-3.2-3B-Instruct"] != 8192 {
		t.Errorf("expected 8192, got %d", contexts["meta-llama/Llama-3.2-3B-Instruct"])
	}
	if _, ok := contexts["no-context-model"]; ok {
		t.Error("model without max_model_len should be omitted")
	}
}

func TestParseModelContextsOllamaStyle(t *testing.T) {
	// Ollama's /v1/models has no max_model_len at all
	body := `{"object":"list","data":[{"id":"qwen2.5:7b","object":"model","owned_by":"library"}]}`
	if contexts := parseModelContexts(strings.NewReader(body)); len(contexts) != 0 {
		t.Errorf("expected no contexts from Ollama-style listing, got %v", contexts)
	}
	if contexts := parseModelContexts(strings.NewReader("not json")); contexts != nil {
		t.Errorf("expected nil on invalid JSON, got %v", contexts)
	}
}

func TestProbeEndpointDetectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"m1","max_model_len":32768}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	contexts, err := h.probeEndpoint(srv.URL, "")
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if contexts["m1"] != 32768 {
		t.Errorf("expected detected context 32768, got %v", contexts)
	}
}

func TestRecordDetectedContextLocalNameMatch(t *testing.T) {
	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	h.RegisterModel("google/gemma-4-31b-it", "http://x", "", "gemma4:31b-it-qat", "")
	h.RegisterModel("TheDrummer/Cydonia-24B-v4.3", "http://y", "", "", "")

	// Matched via local name
	h.recordDetectedContext("google/gemma-4-31b-it", map[string]int{"gemma4:31b-it-qat": 8192})
	if got := h.GetDetectedContext("google/gemma-4-31b-it"); got != 8192 {
		t.Errorf("expected 8192 via local name, got %d", got)
	}

	// Matched via model ID, case-insensitive
	h.recordDetectedContext("TheDrummer/Cydonia-24B-v4.3", map[string]int{"thedrummer/cydonia-24b-v4.3": 131072})
	if got := h.GetDetectedContext("TheDrummer/Cydonia-24B-v4.3"); got != 131072 {
		t.Errorf("expected 131072 via case-insensitive ID, got %d", got)
	}

	// Unregister clears detection
	h.UnregisterModel("google/gemma-4-31b-it")
	if got := h.GetDetectedContext("google/gemma-4-31b-it"); got != 0 {
		t.Errorf("expected 0 after unregister, got %d", got)
	}
}

func TestBuildModelMetadataHeartbeatShape(t *testing.T) {
	c := &InferenceClient{models: []string{"model-a", "model-b", "model-c"}}
	c.SetModelContextsProvider(func() map[string]int {
		return map[string]int{"model-a": 32768}
	})
	c.SetModelMappingsProvider(func() map[string]ModelMapping {
		return map[string]ModelMapping{
			"model-b": {Format: "awq", Quantization: "w4a16"},
			"model-c": {}, // no metadata at all -> omitted
		}
	})

	infos := c.buildModelMetadata()
	if len(infos) != 2 {
		t.Fatalf("expected 2 entries (model-c omitted), got %d: %+v", len(infos), infos)
	}
	byID := map[string]ModelInfo{}
	for _, i := range infos {
		byID[i.ModelID] = i
	}
	if byID["model-a"].ContextLength != 32768 {
		t.Errorf("expected context 32768 for model-a, got %d", byID["model-a"].ContextLength)
	}
	if byID["model-b"].Format != "awq" || byID["model-b"].Quantization != "w4a16" {
		t.Errorf("expected format/quant for model-b, got %+v", byID["model-b"])
	}
}

func TestResolveModelContextsPrecedence(t *testing.T) {
	h := NewModelHealthChecker(DefaultHealthCheckConfig())
	h.RegisterModel("model-a", "http://x", "", "", "")
	h.recordDetectedContext("model-a", map[string]int{"model-a": 32768})
	h.RegisterModel("model-b", "http://x", "", "", "")
	h.recordDetectedContext("model-b", map[string]int{"model-b": 65536})

	s := &InferenceService{
		healthChecker: h,
		modelMappings: map[string]ModelMapping{
			"model-a": {Endpoint: "http://x"},                       // detected only
			"model-b": {Endpoint: "http://x", ContextLength: 16384}, // manual override wins
			"model-c": {Endpoint: "http://x"},                       // unknown -> omitted
		},
	}

	contexts := s.resolveModelContexts()
	if contexts["model-a"] != 32768 {
		t.Errorf("expected detected 32768 for model-a, got %d", contexts["model-a"])
	}
	if contexts["model-b"] != 16384 {
		t.Errorf("expected manual override 16384 for model-b, got %d", contexts["model-b"])
	}
	if _, ok := contexts["model-c"]; ok {
		t.Error("model with unknown context should be omitted")
	}
}
