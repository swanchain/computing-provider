package computing

import "testing"

// The registry callbacks overwrite whatever models.json loaded, so a field
// omitted from the mirror does not merely go missing — it erases a value that
// was read correctly moments earlier. context_length was lost exactly that way.
func TestMappingForCopiesEveryField(t *testing.T) {
	model := &RegisteredModel{
		ID:            "org/model",
		Container:     "c",
		Endpoint:      "http://backend:8000",
		GPUMemory:     16000,
		Category:      "text-generation",
		LocalModel:    "model-local-name",
		Format:        "awq",
		Quantization:  "w4a16",
		APIKey:        "sk-local",
		ContextLength: 65536,
	}

	got := mappingFor(model)

	for _, tc := range []struct {
		field string
		got   any
		want  any
	}{
		{"Container", got.Container, model.Container},
		{"Endpoint", got.Endpoint, model.Endpoint},
		{"GPUMemory", got.GPUMemory, model.GPUMemory},
		{"Category", got.Category, model.Category},
		{"LocalModel", got.LocalModel, model.LocalModel},
		{"Format", got.Format, model.Format},
		{"Quantization", got.Quantization, model.Quantization},
		{"APIKey", got.APIKey, model.APIKey},
		{"ContextLength", got.ContextLength, model.ContextLength},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.field, tc.got, tc.want)
		}
	}
}

// The end-to-end consequence: an explicit override in models.json must reach
// the declaration as an override, not be erased into "unknown".
func TestModelsJSONOverrideSurvivesRegistryCallback(t *testing.T) {
	s := newContextService(map[string]ModelMapping{
		"org/proxied": {Endpoint: "http://proxy", ContextLength: 65536},
	})
	// The backend publishes nothing, as llama.cpp and Ollama do not.
	s.healthChecker.recordDetectedContext("org/proxied", nil)

	// Simulate the registry reporting the same model back, which is what
	// overwrote the mapping before.
	s.modelMappings["org/proxied"] = mappingFor(&RegisteredModel{
		ID:            "org/proxied",
		Endpoint:      "http://proxy",
		ContextLength: 65536,
	})

	info := s.ModelContext("org/proxied")
	if info.Length != 65536 {
		t.Errorf("length = %d, want the operator's 65536", info.Length)
	}
	if info.Source != ContextSourceOverride {
		t.Errorf("source = %q, want %q", info.Source, ContextSourceOverride)
	}

	declared := s.resolveModelContexts()
	if declared["org/proxied"].Length != 65536 {
		t.Errorf("declaration = %+v, want the override to be declared", declared["org/proxied"])
	}
}
