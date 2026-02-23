package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrerequisiteChecker(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping prerequisite check in short mode")
	}

	checker := NewPrerequisiteChecker()
	results := checker.CheckAll()

	// Should have at least Docker check
	if len(results) == 0 {
		t.Error("Expected at least one prerequisite check result")
	}

	// Verify all results have names
	for _, r := range results {
		if r.Name == "" {
			t.Error("Prerequisite result should have a name")
		}
	}
}

func TestModelDiscovery(t *testing.T) {
	discovery := NewModelDiscovery()

	// Test discovery on a port that's unlikely to be open
	servers := discovery.DiscoverOnPorts("localhost", []int{59999})
	if len(servers) != 0 {
		t.Error("Expected no servers on unused port")
	}
}

func TestEstimateGPUMemory(t *testing.T) {
	tests := []struct {
		model    string
		expected int
	}{
		{"llama-3.2-3b", 6000},
		{"llama-70b", 140000},
		{"qwen-2.5-7b", 14000},
		{"unknown-model", 8000}, // default
	}

	for _, tt := range tests {
		result := EstimateGPUMemory(tt.model)
		if result != tt.expected {
			t.Errorf("EstimateGPUMemory(%s) = %d, expected %d", tt.model, result, tt.expected)
		}
	}
}

func TestDetectModelCategory(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		{"llama-3.2-3b", "text-generation"},
		{"stable-diffusion-xl", "image-generation"},
		{"bge-large", "embeddings"},
		{"whisper-large", "audio"},
		{"llava-v1.5", "vision"},
		{"codellama-7b", "code-generation"},
	}

	for _, tt := range tests {
		result := DetectModelCategory(tt.model)
		if result != tt.expected {
			t.Errorf("DetectModelCategory(%s) = %s, expected %s", tt.model, result, tt.expected)
		}
	}
}

func TestCredentialsManager(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "setup-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mgr := NewCredentialsManager(tmpDir)

	// Test no credentials initially
	if mgr.HasApiKey() {
		t.Error("Expected no API key initially")
	}

	// Test save and load
	creds := &Credentials{
		ApiKey:     "sk-prov-test123",
		Email:      "test@example.com",
		ProviderID: "prov-123",
		Name:       "Test Provider",
	}

	if err := mgr.Save(creds); err != nil {
		t.Fatalf("Failed to save credentials: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(filepath.Join(tmpDir, "credentials.json"))
	if err != nil {
		t.Fatalf("Failed to stat credentials file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("Expected 0600 permissions, got %o", info.Mode().Perm())
	}

	// Test load
	loaded, err := mgr.Load()
	if err != nil {
		t.Fatalf("Failed to load credentials: %v", err)
	}

	if loaded.ApiKey != creds.ApiKey {
		t.Errorf("ApiKey mismatch: got %s, expected %s", loaded.ApiKey, creds.ApiKey)
	}
	if loaded.Email != creds.Email {
		t.Errorf("Email mismatch: got %s, expected %s", loaded.Email, creds.Email)
	}

	// Test HasApiKey
	if !mgr.HasApiKey() {
		t.Error("Expected HasApiKey to return true")
	}

	// Test GetApiKey
	if mgr.GetApiKey() != creds.ApiKey {
		t.Errorf("GetApiKey returned %s, expected %s", mgr.GetApiKey(), creds.ApiKey)
	}

	// Test delete
	if err := mgr.Delete(); err != nil {
		t.Fatalf("Failed to delete credentials: %v", err)
	}

	if mgr.HasApiKey() {
		t.Error("Expected no API key after delete")
	}
}

func TestMaskApiKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sk-prov-abcdefghijklmnop", "sk-prov-...mnop"},
		{"short", "***"},
		{"", "***"},
	}

	for _, tt := range tests {
		result := MaskApiKey(tt.input)
		if result != tt.expected {
			t.Errorf("MaskApiKey(%s) = %s, expected %s", tt.input, result, tt.expected)
		}
	}
}

func TestServerType(t *testing.T) {
	// Verify server type constants
	if ServerTypeSGLang != "sglang" {
		t.Error("ServerTypeSGLang should be 'sglang'")
	}
	if ServerTypeVLLM != "vllm" {
		t.Error("ServerTypeVLLM should be 'vllm'")
	}
	if ServerTypeOllama != "ollama" {
		t.Error("ServerTypeOllama should be 'ollama'")
	}
}

func TestNormalizeModelName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Ollama naming conventions
		{"llama3.2:3b", "llama-3-2-3b"},
		{"qwen2.5:7b", "qwen-2-5-7b"},
		{"phi3:mini", "phi-3-mini"},
		{"mistral:7b", "mistral-7b"},
		{"llama3.2:3b-instruct", "llama-3-2-3b"},

		// HuggingFace/vLLM naming conventions
		{"meta-llama/Llama-3.2-3B-Instruct", "llama-3-2-3b"},
		{"Qwen/Qwen2.5-7B-Instruct", "qwen-2-5-7b"},
		{"mistralai/Mistral-7B-Instruct-v0.1", "mistral-7b-instruct-v-0-1"},

		// Swan Inference naming conventions
		{"llama-3.2-3b", "llama-3-2-3b"},
		{"qwen-2.5-7b", "qwen-2-5-7b"},

		// Edge cases
		{"llama3:latest", "llama-3"},
		{"LLAMA3.2:3B", "llama-3-2-3b"},
	}

	for _, tt := range tests {
		result := normalizeModelName(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeModelName(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestExtractModelInfoV2(t *testing.T) {
	tests := []struct {
		input           string
		expectedFamily  string
		expectedVersion string
		expectedSize    string
	}{
		{"llama-3-2-3b", "llama", "3-2", "3b"},
		{"qwen-2-5-7b", "qwen", "2-5", "7b"},
		{"mistral-7b", "mistral", "", "7b"},
		{"phi-3-mini", "phi", "3", "mini"},
		{"deepseek-v-2-7b", "deepseek", "2", "7b"},
		{"llama-70b", "llama", "", "70b"},
		{"llama-3-70b", "llama", "3", "70b"},
	}

	for _, tt := range tests {
		family, version, size := extractModelInfoV2(tt.input)
		if family != tt.expectedFamily {
			t.Errorf("extractModelInfoV2(%q) family = %q, expected %q", tt.input, family, tt.expectedFamily)
		}
		if version != tt.expectedVersion {
			t.Errorf("extractModelInfoV2(%q) version = %q, expected %q", tt.input, version, tt.expectedVersion)
		}
		if size != tt.expectedSize {
			t.Errorf("extractModelInfoV2(%q) size = %q, expected %q", tt.input, size, tt.expectedSize)
		}
	}
}

func TestModelMatchingOllamaToSwan(t *testing.T) {
	// Simulate Swan models
	swanModels := []SwanModel{
		{ID: "llama-3.2-3b", Slug: "llama-3.2-3b", Name: "Llama 3.2 3B", Active: true},
		{ID: "llama-3.2-1b", Slug: "llama-3.2-1b", Name: "Llama 3.2 1B", Active: true},
		{ID: "qwen-2.5-7b", Slug: "qwen-2.5-7b", Name: "Qwen 2.5 7B", Active: true},
		{ID: "mistral-7b", Slug: "mistral-7b", Name: "Mistral 7B", Active: true},
		{ID: "phi-3-mini", Slug: "phi-3-mini", Name: "Phi 3 Mini", Active: true},
		{ID: "deepseek-v2-7b", Slug: "deepseek-v2-7b", Name: "DeepSeek V2 7B", Active: true},
	}

	tests := []struct {
		localModel      string
		expectedSwanID  string
		minConfidence   float64
		shouldMatch     bool
	}{
		// Ollama style → Swan ID (the main problem cases from the plan)
		{"llama3.2:3b", "llama-3.2-3b", 0.8, true},
		{"qwen2.5:7b", "qwen-2.5-7b", 0.8, true},
		{"mistral:7b", "mistral-7b", 0.5, true},
		{"phi3:mini", "phi-3-mini", 0.8, true},

		// vLLM/SGLang style → Swan ID
		{"meta-llama/Llama-3.2-3B-Instruct", "llama-3.2-3b", 0.8, true},
		{"Qwen/Qwen2.5-7B-Instruct", "qwen-2.5-7b", 0.8, true},

		// Should not match wrong size
		{"llama3.2:1b", "llama-3.2-1b", 0.8, true},
	}

	for _, tt := range tests {
		matches := MatchModels([]string{tt.localModel}, swanModels)

		if tt.shouldMatch {
			if len(matches) == 0 {
				t.Errorf("Expected %q to match a Swan model, got no matches", tt.localModel)
				continue
			}
			if matches[0].SwanModelID != tt.expectedSwanID {
				t.Errorf("MatchModels(%q) matched %q, expected %q",
					tt.localModel, matches[0].SwanModelID, tt.expectedSwanID)
			}
			if matches[0].Confidence < tt.minConfidence {
				t.Errorf("MatchModels(%q) confidence = %.2f, expected >= %.2f",
					tt.localModel, matches[0].Confidence, tt.minConfidence)
			}
		} else {
			if len(matches) > 0 {
				t.Errorf("Expected %q to NOT match, but got %q with confidence %.2f",
					tt.localModel, matches[0].SwanModelID, matches[0].Confidence)
			}
		}
	}
}

func TestModelMatchingCaseInsensitiveHFID(t *testing.T) {
	// Swan models use HuggingFace-style IDs with mixed case
	swanModels := []SwanModel{
		{ID: "meta-llama/Llama-3.1-8B-Instruct", Slug: "llama-3.1-8b-instruct", Name: "Llama 3.1 8B Instruct", Active: true},
		{ID: "Qwen/Qwen2.5-7B-Instruct", Slug: "qwen2.5-7b-instruct", Name: "Qwen 2.5 7B Instruct", Active: true},
	}

	tests := []struct {
		localModel     string
		expectedSwanID string
		description    string
	}{
		// Exact case — baseline
		{"meta-llama/Llama-3.1-8B-Instruct", "meta-llama/Llama-3.1-8B-Instruct", "exact match"},
		// All lowercase — the main bug scenario
		{"meta-llama/llama-3.1-8b-instruct", "meta-llama/Llama-3.1-8B-Instruct", "all lowercase"},
		// All uppercase
		{"META-LLAMA/LLAMA-3.1-8B-INSTRUCT", "meta-llama/Llama-3.1-8B-Instruct", "all uppercase"},
		// Qwen mixed case
		{"qwen/qwen2.5-7b-instruct", "Qwen/Qwen2.5-7B-Instruct", "qwen lowercase"},
	}

	for _, tt := range tests {
		matches := MatchModels([]string{tt.localModel}, swanModels)
		if len(matches) == 0 {
			t.Errorf("[%s] Expected %q to match, got no matches", tt.description, tt.localModel)
			continue
		}
		if matches[0].SwanModelID != tt.expectedSwanID {
			t.Errorf("[%s] MatchModels(%q) = %q, expected %q",
				tt.description, tt.localModel, matches[0].SwanModelID, tt.expectedSwanID)
		}
		if matches[0].Confidence < 1.0 {
			t.Errorf("[%s] MatchModels(%q) confidence = %.2f, expected 1.0 for direct HF ID match",
				tt.description, tt.localModel, matches[0].Confidence)
		}
	}
}

func TestEstimateGPUMemoryWithQuantization(t *testing.T) {
	tests := []struct {
		model       string
		expectedMin int
		expectedMax int
	}{
		// FP16 baselines
		{"llama-3.2-3b", 5000, 7000},
		{"llama-70b", 130000, 150000},
		{"qwen-2.5-7b", 13000, 15000},

		// Quantized models should use less memory
		{"llama-3.2-3b-q4_0", 1500, 2500},  // ~30% of FP16
		{"llama-70b-q4_k", 35000, 50000},   // ~30% of FP16
		{"llama-3.2-3b-q8_0", 2500, 4000},  // ~55% of FP16

		// Unknown model defaults
		{"unknown-model", 7000, 9000},
	}

	for _, tt := range tests {
		result := EstimateGPUMemory(tt.model)
		if result < tt.expectedMin || result > tt.expectedMax {
			t.Errorf("EstimateGPUMemory(%s) = %d, expected between %d and %d",
				tt.model, result, tt.expectedMin, tt.expectedMax)
		}
	}
}

func TestContainmentScoreWithFamilyValidation(t *testing.T) {
	// Test that containment without family match gets lower score
	// "phi" should not get high score for "dolphin-phi"
	swanModels := []SwanModel{
		{ID: "phi-3-mini", Slug: "phi-3-mini", Name: "Phi 3 Mini", Active: true},
		{ID: "dolphin-phi-2", Slug: "dolphin-phi-2", Name: "Dolphin Phi 2", Active: true},
	}

	// "phi3:mini" should match "phi-3-mini" with high confidence
	matches := MatchModels([]string{"phi3:mini"}, swanModels)
	if len(matches) == 0 {
		t.Fatal("Expected phi3:mini to match")
	}
	if matches[0].SwanModelID != "phi-3-mini" {
		t.Errorf("phi3:mini should match phi-3-mini, got %s", matches[0].SwanModelID)
	}
}

func TestModelMatchingSlugFallback(t *testing.T) {
	// Swan models where the HF ID won't normalize to match, but the slug will
	swanModels := []SwanModel{
		{ID: "mistralai/Mistral-Small-3.2-24B-Instruct-2506", Slug: "mistral-small-24b", Name: "Mistral Small 24B", Active: true},
		{ID: "deepseek-ai/DeepSeek-R1-0528", Slug: "deepseek-r1", Name: "DeepSeek R1", Active: true},
		{ID: "black-forest-labs/FLUX.1-schnell", Slug: "flux-1-schnell", Name: "FLUX.1 Schnell", Active: true},
	}

	tests := []struct {
		localModel     string
		expectedSwanID string
		minConfidence  float64
		description    string
	}{
		// Slug matches that wouldn't work via HF ID normalization alone
		{"mistral-small:24b", "mistralai/Mistral-Small-3.2-24B-Instruct-2506", 1.0, "ollama mistral-small matches slug"},
		{"deepseek-r1", "deepseek-ai/DeepSeek-R1-0528", 1.0, "deepseek-r1 matches slug"},
		{"flux-1-schnell", "black-forest-labs/FLUX.1-schnell", 1.0, "flux-1-schnell matches slug"},
	}

	for _, tt := range tests {
		matches := MatchModels([]string{tt.localModel}, swanModels)
		if len(matches) == 0 {
			t.Errorf("[%s] Expected %q to match, got no matches", tt.description, tt.localModel)
			continue
		}
		if matches[0].SwanModelID != tt.expectedSwanID {
			t.Errorf("[%s] MatchModels(%q) = %q, expected %q",
				tt.description, tt.localModel, matches[0].SwanModelID, tt.expectedSwanID)
		}
		if matches[0].Confidence < tt.minConfidence {
			t.Errorf("[%s] MatchModels(%q) confidence = %.2f, expected >= %.2f",
				tt.description, tt.localModel, matches[0].Confidence, tt.minConfidence)
		}
	}
}
