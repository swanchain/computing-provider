package setup

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Common inference server ports to scan
var DefaultPorts = []int{30000, 8000, 11434, 8080, 5000, 8001}

// ServerType represents the type of inference server
type ServerType string

const (
	ServerTypeSGLang   ServerType = "sglang"
	ServerTypeVLLM     ServerType = "vllm"
	ServerTypeOllama   ServerType = "ollama"
	ServerTypeOpenAI   ServerType = "openai-compatible"
	ServerTypeUnknown  ServerType = "unknown"
)

// DiscoveredServer represents a discovered inference server
type DiscoveredServer struct {
	Host       string     `json:"host"`
	Port       int        `json:"port"`
	Type       ServerType `json:"type"`
	Models     []string   `json:"models"`
	Healthy    bool       `json:"healthy"`
	Endpoint   string     `json:"endpoint"`
	RawModels  []ModelInfo `json:"raw_models,omitempty"`
}

// ModelInfo represents information about a model
type ModelInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// ModelDiscovery handles auto-discovery of model servers
type ModelDiscovery struct {
	httpClient *http.Client
}

// NewModelDiscovery creates a new model discovery instance
func NewModelDiscovery() *ModelDiscovery {
	return &ModelDiscovery{
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// DiscoverAll scans all default ports and returns discovered servers
func (d *ModelDiscovery) DiscoverAll() []DiscoveredServer {
	return d.DiscoverOnPorts("localhost", DefaultPorts)
}

// DiscoverOnPorts scans specified ports on a host
func (d *ModelDiscovery) DiscoverOnPorts(host string, ports []int) []DiscoveredServer {
	var servers []DiscoveredServer

	for _, port := range ports {
		if server := d.probePort(host, port); server != nil {
			servers = append(servers, *server)
		}
	}

	return servers
}

// probePort checks if a port has a running inference server
func (d *ModelDiscovery) probePort(host string, port int) *DiscoveredServer {
	// First check if port is open
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return nil
	}
	conn.Close()

	endpoint := fmt.Sprintf("http://%s", address)

	// Try to detect server type and get models
	serverType, models := d.detectServerType(endpoint, port)
	if serverType == ServerTypeUnknown {
		return nil
	}

	return &DiscoveredServer{
		Host:     host,
		Port:     port,
		Type:     serverType,
		Models:   models,
		Healthy:  true,
		Endpoint: endpoint,
	}
}

// detectServerType tries to identify the server type and available models
func (d *ModelDiscovery) detectServerType(endpoint string, port int) (ServerType, []string) {
	// Ollama typically runs on 11434
	if port == 11434 {
		if models, err := d.tryOllama(endpoint); err == nil {
			return ServerTypeOllama, models
		}
	}

	// Try OpenAI-compatible API (SGLang, vLLM, etc.)
	if models, serverType, err := d.tryOpenAICompatible(endpoint); err == nil {
		return serverType, models
	}

	// Try Ollama API on non-standard port
	if models, err := d.tryOllama(endpoint); err == nil {
		return ServerTypeOllama, models
	}

	// Check if there's a health endpoint at least
	if d.checkHealth(endpoint) {
		return ServerTypeOpenAI, nil
	}

	return ServerTypeUnknown, nil
}

// tryOpenAICompatible tries to get models from an OpenAI-compatible API
func (d *ModelDiscovery) tryOpenAICompatible(endpoint string) ([]string, ServerType, error) {
	resp, err := d.httpClient.Get(endpoint + "/v1/models")
	if err != nil {
		return nil, ServerTypeUnknown, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ServerTypeUnknown, fmt.Errorf("non-200 status: %d", resp.StatusCode)
	}

	var result struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, ServerTypeUnknown, err
	}

	var models []string
	for _, m := range result.Data {
		models = append(models, m.ID)
	}

	// Try to determine specific server type
	serverType := d.detectSpecificServerType(endpoint)

	return models, serverType, nil
}

// detectSpecificServerType tries to detect if it's SGLang or vLLM
func (d *ModelDiscovery) detectSpecificServerType(endpoint string) ServerType {
	// Try SGLang-specific endpoint
	resp, err := d.httpClient.Get(endpoint + "/get_server_args")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return ServerTypeSGLang
		}
	}

	// Try vLLM-specific endpoint
	resp, err = d.httpClient.Get(endpoint + "/version")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var version struct {
				Version string `json:"version"`
			}
			if json.NewDecoder(resp.Body).Decode(&version) == nil {
				if strings.Contains(strings.ToLower(version.Version), "vllm") {
					return ServerTypeVLLM
				}
			}
		}
	}

	return ServerTypeOpenAI
}

// tryOllama tries to get models from Ollama API
func (d *ModelDiscovery) tryOllama(endpoint string) ([]string, error) {
	resp, err := d.httpClient.Get(endpoint + "/api/tags")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("non-200 status: %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name       string `json:"name"`
			ModifiedAt string `json:"modified_at"`
			Size       int64  `json:"size"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var models []string
	for _, m := range result.Models {
		models = append(models, m.Name)
	}

	return models, nil
}

// checkHealth checks if the server has a health endpoint
func (d *ModelDiscovery) checkHealth(endpoint string) bool {
	healthEndpoints := []string{"/health", "/v1/health", "/healthz", "/"}

	for _, path := range healthEndpoints {
		resp, err := d.httpClient.Get(endpoint + path)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true
		}
	}

	return false
}

// VerifyModel sends a minimal inference request to verify a model works
func (d *ModelDiscovery) VerifyModel(endpoint, model string, serverType ServerType) error {
	var reqBody []byte
	var url string
	var err error

	switch serverType {
	case ServerTypeOllama:
		url = endpoint + "/api/generate"
		req := map[string]interface{}{
			"model":  model,
			"prompt": "hi",
			"stream": false,
			"options": map[string]interface{}{
				"num_predict": 1,
			},
		}
		reqBody, err = json.Marshal(req)
	default:
		url = endpoint + "/v1/chat/completions"
		req := map[string]interface{}{
			"model": model,
			"messages": []map[string]string{
				{"role": "user", "content": "hi"},
			},
			"max_tokens": 1,
		}
		reqBody, err = json.Marshal(req)
	}

	if err != nil {
		return fmt.Errorf("failed to create verification request: %w", err)
	}

	// Use a longer timeout for verification
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		return fmt.Errorf("verification request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("verification returned status %d", resp.StatusCode)
	}

	return nil
}

// EstimateGPUMemory estimates GPU memory needed for a model based on its name
func EstimateGPUMemory(modelName string) int {
	modelLower := strings.ToLower(modelName)

	// Common model size patterns
	sizePatterns := map[string]int{
		"70b": 140000, // ~140GB
		"65b": 130000,
		"34b": 68000,
		"33b": 66000,
		"32b": 64000,
		"30b": 60000,
		"14b": 28000,
		"13b": 26000,
		"11b": 22000,
		"8b":  16000,
		"7b":  14000,
		"6b":  12000,
		"4b":  8000,
		"3b":  6000,
		"2b":  4000,
		"1b":  2000,
		"0.5b": 1000,
		"500m": 1000,
	}

	for pattern, memory := range sizePatterns {
		if strings.Contains(modelLower, pattern) {
			return memory
		}
	}

	// Default estimate for unknown models
	return 8000
}

// DetectModelCategory tries to detect the model category from its name
func DetectModelCategory(modelName string) string {
	modelLower := strings.ToLower(modelName)

	// Image generation models
	imageKeywords := []string{"flux", "sdxl", "stable-diffusion", "sd-", "dall", "imagen", "midjourney"}
	for _, kw := range imageKeywords {
		if strings.Contains(modelLower, kw) {
			return "image-generation"
		}
	}

	// Embedding models
	embeddingKeywords := []string{"embed", "bge", "e5-", "gte-", "nomic-embed"}
	for _, kw := range embeddingKeywords {
		if strings.Contains(modelLower, kw) {
			return "embeddings"
		}
	}

	// Audio/speech models
	audioKeywords := []string{"whisper", "tts", "speech", "audio"}
	for _, kw := range audioKeywords {
		if strings.Contains(modelLower, kw) {
			return "audio"
		}
	}

	// Vision models
	visionKeywords := []string{"vision", "vl", "-v-", "llava", "qwen-vl"}
	for _, kw := range visionKeywords {
		if strings.Contains(modelLower, kw) {
			return "vision"
		}
	}

	// Code models
	codeKeywords := []string{"code", "coder", "starcoder", "codellama", "deepseek-coder"}
	for _, kw := range codeKeywords {
		if strings.Contains(modelLower, kw) {
			return "code-generation"
		}
	}

	// Default to text generation for LLMs
	return "text-generation"
}

// SwanModel represents a model from Swan Inference API
type SwanModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Active   bool   `json:"active"`
}

// FetchSwanModels fetches the list of supported models from Swan Inference API
func FetchSwanModels(apiURL string) ([]SwanModel, error) {
	if apiURL == "" {
		apiURL = "https://inference-dev.swanchain.io/api/v1"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL + "/models?page_size=200")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			List []SwanModel `json:"list"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Filter active models
	var activeModels []SwanModel
	for _, m := range result.Data.List {
		if m.Active {
			activeModels = append(activeModels, m)
		}
	}

	return activeModels, nil
}

// ModelMatch represents a match between a local model and Swan Inference model
type ModelMatch struct {
	LocalModel    string  // Original local model name (e.g., "llama3.2:3b")
	SwanModelID   string  // Swan Inference model ID (e.g., "llama-3.2-3b")
	SwanModelName string  // Human-readable name
	Confidence    float64 // Match confidence (0-1)
}

// MatchModels matches local models to Swan Inference models
func MatchModels(localModels []string, swanModels []SwanModel) []ModelMatch {
	var matches []ModelMatch

	for _, local := range localModels {
		bestMatch := findBestMatch(local, swanModels)
		if bestMatch != nil {
			matches = append(matches, *bestMatch)
		}
	}

	return matches
}

// findBestMatch finds the best Swan model match for a local model name
func findBestMatch(localModel string, swanModels []SwanModel) *ModelMatch {
	normalizedLocal := normalizeModelName(localModel)

	var bestMatch *ModelMatch
	var bestScore float64

	for _, swan := range swanModels {
		normalizedSwan := normalizeModelName(swan.ID)
		score := calculateMatchScore(normalizedLocal, normalizedSwan, localModel, swan.ID)

		if score > bestScore && score >= 0.5 { // Minimum 50% match
			bestScore = score
			bestMatch = &ModelMatch{
				LocalModel:    localModel,
				SwanModelID:   swan.ID,
				SwanModelName: swan.Name,
				Confidence:    score,
			}
		}
	}

	return bestMatch
}

// normalizeModelName normalizes a model name for comparison
func normalizeModelName(name string) string {
	// Convert to lowercase
	name = strings.ToLower(name)

	// Remove common prefixes
	prefixes := []string{"meta-llama/", "mistralai/", "qwen/", "deepseek-ai/", "google/"}
	for _, prefix := range prefixes {
		name = strings.TrimPrefix(name, prefix)
	}

	// Remove common suffixes
	suffixes := []string{":latest", "-instruct", "-chat", "-base"}
	for _, suffix := range suffixes {
		name = strings.TrimSuffix(name, suffix)
	}

	// Normalize separators: replace colons, underscores with hyphens
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")

	// Remove double hyphens
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}

	return strings.Trim(name, "-")
}

// calculateMatchScore calculates a match score between normalized names
func calculateMatchScore(normalizedLocal, normalizedSwan, originalLocal, originalSwan string) float64 {
	// Exact match after normalization
	if normalizedLocal == normalizedSwan {
		return 1.0
	}

	// Check if one contains the other
	if strings.Contains(normalizedLocal, normalizedSwan) || strings.Contains(normalizedSwan, normalizedLocal) {
		return 0.9
	}

	// Extract model family and size for comparison
	localFamily, localSize := extractModelInfo(normalizedLocal)
	swanFamily, swanSize := extractModelInfo(normalizedSwan)

	// Family must match
	if localFamily == "" || swanFamily == "" {
		return 0
	}

	// Check family similarity
	familyScore := 0.0
	if localFamily == swanFamily {
		familyScore = 0.6
	} else if strings.Contains(localFamily, swanFamily) || strings.Contains(swanFamily, localFamily) {
		familyScore = 0.4
	}

	if familyScore == 0 {
		return 0
	}

	// Size match bonus
	sizeScore := 0.0
	if localSize != "" && swanSize != "" && localSize == swanSize {
		sizeScore = 0.3
	}

	return familyScore + sizeScore
}

// extractModelInfo extracts model family and size from normalized name
func extractModelInfo(name string) (family string, size string) {
	// Common size patterns
	sizePatterns := []string{"70b", "65b", "34b", "33b", "32b", "30b", "24b", "14b", "13b", "11b", "8b", "7b", "6b", "4b", "3b", "2b", "1b"}

	for _, pattern := range sizePatterns {
		if strings.Contains(name, pattern) {
			size = pattern
			// Extract family (everything before the size)
			idx := strings.Index(name, pattern)
			if idx > 0 {
				family = strings.Trim(name[:idx], "-")
			}
			break
		}
	}

	// If no size found, the whole name is the family
	if family == "" {
		family = name
	}

	return family, size
}
