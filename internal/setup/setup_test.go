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
