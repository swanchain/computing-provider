package setup

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Common inference server ports to scan
var DefaultPorts = []int{30000, 8000, 11434, 8080, 5000, 8001}

// modelNormalizationAliases maps normalized names to their canonical form
// Used for edge cases that can't be handled by algorithmic normalization
var modelNormalizationAliases = map[string]string{
	// Phi models
	"phi-3-mini":   "phi-3-mini",
	"phi-3-medium": "phi-3-medium",
	"phi-3-small":  "phi-3-small",

	// Mistral variants
	"mistral-7b-v0-1": "mistral-7b",
	"mistral-7b-v0-2": "mistral-7b",
	"mistral-7b-v0-3": "mistral-7b",
}

// swanModelAliases maps normalized model names to exact Swan Inference model IDs
// This handles cases where normalization alone isn't enough to match Ollama/HuggingFace
// model names to Swan Inference model IDs
var swanModelAliases = map[string]string{
	// === Llama Family ===
	"llama-3-1-8b":           "meta-llama/Llama-3.1-8B-Instruct",
	"llama-3-2-3b":           "meta-llama/Llama-3.2-3B-Instruct",
	"llama-3-2-1b":           "meta-llama/Llama-3.2-1B-Instruct",
	"llama-3-3-70b":          "meta-llama/Llama-3.3-70B-Instruct",
	"llama-3-3-70b-instruct": "meta-llama/Llama-3.3-70B-Instruct",
	"lumimaid-8b":            "NeverSleep/Llama-3-Lumimaid-8B-v0.1",

	// === Qwen Family ===
	"qwen-2-5-7b":         "Qwen/Qwen2.5-7B-Instruct",
	"qwen-2-5-14b":        "Qwen/Qwen2.5-14B-Instruct",
	"qwen-2-5-32b":        "Qwen/Qwen2.5-32B-Instruct",
	"qwen-2-5-72b":        "Qwen/Qwen2.5-72B-Instruct",
	"qwen-3-235b":         "Qwen/Qwen3-235B-A22B-Instruct-2507",
	"qwen-3-embedding-8b": "Qwen/Qwen3-Embedding-8B",

	// === Mistral Family ===
	"mistral-small-24b": "mistralai/Mistral-Small-3.2-24B-Instruct-2506",

	// === DeepSeek Family ===
	"deepseek-r-1":      "deepseek-ai/DeepSeek-R1-0528",
	"deepseek-r-1-671b": "deepseek-ai/DeepSeek-R1-0528",
	"deepseek-v-3":      "deepseek-ai/DeepSeek-V3-0324",
	"deepseek-v-3-671b": "deepseek-ai/DeepSeek-V3-0324",

	// === Image Models ===
	"flux-1-schnell":       "black-forest-labs/FLUX.1-schnell",
	"flux-schnell":         "black-forest-labs/FLUX.1-schnell",
	"sd-1-5":               "stable-diffusion-v1-5/stable-diffusion-v1-5",
	"stable-diffusion-1-5": "stable-diffusion-v1-5/stable-diffusion-v1-5",
	"dreamshaper-8":        "Lykon/dreamshaper-8",

	// === Audio Models ===
	"whisper-large-v-3":    "Systran/faster-whisper-large-v3",
	"faster-whisper-large": "Systran/faster-whisper-large-v3",

	// === Embedding/Reranking ===
	"bge-reranker-v-2-m-3": "BAAI/bge-reranker-v2-m3",
	"bge-reranker":         "BAAI/bge-reranker-v2-m3",
}

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

	// Base memory requirements for model sizes (FP16)
	// These are ordered by size to ensure larger patterns match first (e.g., "70b" before "7b")
	sizePatterns := []struct {
		pattern string
		memory  int
	}{
		{"405b", 810000}, // ~810GB
		{"70b", 140000},  // ~140GB
		{"65b", 130000},
		{"34b", 68000},
		{"33b", 66000},
		{"32b", 64000},
		{"30b", 60000},
		{"27b", 54000},
		{"24b", 48000},
		{"14b", 28000},
		{"13b", 26000},
		{"12b", 24000},
		{"11b", 22000},
		{"8b", 16000},
		{"7b", 14000},
		{"6b", 12000},
		{"4b", 8000},
		{"3b", 6000},
		{"2b", 4000},
		{"1b", 2000},
		{"0.5b", 1000},
		{"500m", 1000},
	}

	// Find base memory requirement
	baseMemory := 8000 // Default for unknown models
	for _, sp := range sizePatterns {
		if strings.Contains(modelLower, sp.pattern) {
			baseMemory = sp.memory
			break
		}
	}

	// Apply quantization multiplier
	quantMultiplier := detectQuantizationMultiplier(modelLower)
	return int(float64(baseMemory) * quantMultiplier)
}

// detectQuantizationMultiplier returns a multiplier based on quantization level
// FP16 = 1.0 (baseline), lower bit quantization reduces memory requirements
func detectQuantizationMultiplier(modelName string) float64 {
	// Quantization patterns and their memory multipliers relative to FP16
	// FP16: 16 bits per parameter (baseline)
	// FP8/INT8: 8 bits per parameter (~50% of FP16)
	// Q4: 4 bits per parameter (~25% of FP16)
	// Q2: 2 bits per parameter (~12.5% of FP16)

	quantPatterns := []struct {
		patterns   []string
		multiplier float64
	}{
		// 2-bit quantization
		{[]string{"q2_k", "q2-k", "iq2"}, 0.15},
		// 3-bit quantization
		{[]string{"q3_k", "q3-k", "iq3"}, 0.22},
		// 4-bit quantization (most common for consumer hardware)
		{[]string{"q4_0", "q4-0", "q4_k", "q4-k", "q4_1", "q4-1", "iq4", "int4", "w4a16"}, 0.30},
		// 5-bit quantization
		{[]string{"q5_0", "q5-0", "q5_k", "q5-k", "q5_1", "q5-1"}, 0.38},
		// 6-bit quantization
		{[]string{"q6_k", "q6-k"}, 0.45},
		// 8-bit quantization
		{[]string{"q8_0", "q8-0", "q8_k", "q8-k", "int8", "fp8", "w8a8"}, 0.55},
		// BF16 (same size as FP16)
		{[]string{"bf16"}, 1.0},
		// FP32 (double the size of FP16)
		{[]string{"fp32", "f32"}, 2.0},
	}

	for _, qp := range quantPatterns {
		for _, pattern := range qp.patterns {
			if strings.Contains(modelName, pattern) {
				return qp.multiplier
			}
		}
	}

	// Default to FP16 if no quantization detected
	return 1.0
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
	Slug     string `json:"slug"`
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
	// Fast path: direct HF ID match (SGLang/vLLM report HF repo IDs directly, case-insensitive)
	for _, swan := range swanModels {
		if strings.EqualFold(localModel, swan.ID) {
			return &ModelMatch{
				LocalModel:    localModel,
				SwanModelID:   swan.ID,
				SwanModelName: swan.Name,
				Confidence:    1.0,
			}
		}
	}

	normalizedLocal := normalizeModelName(localModel)

	// Check alias first for exact mapping (Ollama/HuggingFace -> Swan ID)
	if aliasID, ok := swanModelAliases[normalizedLocal]; ok {
		for _, swan := range swanModels {
			if strings.EqualFold(swan.ID, aliasID) {
				return &ModelMatch{
					LocalModel:    localModel,
					SwanModelID:   swan.ID,
					SwanModelName: swan.Name,
					Confidence:    1.0, // Alias = exact match
				}
			}
		}
	}

	// Slug fallback: if normalized local name matches a Swan model's slug, exact match
	for _, swan := range swanModels {
		if swan.Slug != "" && strings.EqualFold(normalizedLocal, normalizeModelName(swan.Slug)) {
			return &ModelMatch{
				LocalModel:    localModel,
				SwanModelID:   swan.ID,
				SwanModelName: swan.Name,
				Confidence:    1.0,
			}
		}
	}

	// Fall back to score-based matching
	var bestMatch *ModelMatch
	var bestScore float64

	for _, swan := range swanModels {
		normalizedSwan := normalizeModelName(swan.ID)
		score := calculateMatchScore(normalizedLocal, normalizedSwan, localModel, swan.ID)

		// Also score against slug and take the higher score
		if swan.Slug != "" {
			normalizedSlug := normalizeModelName(swan.Slug)
			slugScore := calculateMatchScore(normalizedLocal, normalizedSlug, localModel, swan.Slug)
			if slugScore > score {
				score = slugScore
			}
		}

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
	prefixes := []string{"meta-llama/", "mistralai/", "qwen/", "deepseek-ai/", "google/", "microsoft/", "nvidia/"}
	for _, prefix := range prefixes {
		name = strings.TrimPrefix(name, prefix)
	}

	// Remove common suffixes
	suffixes := []string{":latest", "-instruct", "-chat", "-base", "-hf", "-gguf"}
	for _, suffix := range suffixes {
		name = strings.TrimSuffix(name, suffix)
	}

	// Normalize separators: replace colons, underscores with hyphens
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")

	// Insert hyphens between letters and digits (digit-aware normalization)
	// This handles "llama3" -> "llama-3" and "qwen2" -> "qwen-2"
	name = insertHyphenBetweenLettersAndDigits(name)

	// Remove double hyphens
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}

	// Apply known aliases for edge cases that can't be normalized algorithmically
	if alias, ok := modelNormalizationAliases[name]; ok {
		name = alias
	}

	return strings.Trim(name, "-")
}

// insertHyphenBetweenLettersAndDigits inserts hyphens at letter-digit boundaries
// Examples: "llama3" -> "llama-3", "phi3" -> "phi-3", "qwen2" -> "qwen-2"
func insertHyphenBetweenLettersAndDigits(s string) string {
	if len(s) < 2 {
		return s
	}

	var result strings.Builder
	result.Grow(len(s) + 10) // Pre-allocate with some extra space for hyphens

	for i := 0; i < len(s); i++ {
		result.WriteByte(s[i])

		if i < len(s)-1 {
			curr := s[i]
			next := s[i+1]

			// Insert hyphen between letter and digit (but not if already hyphenated)
			if isLetter(curr) && isDigit(next) {
				result.WriteByte('-')
			}
			// Also insert hyphen between digit and letter for cases like "3b" already handled,
			// but we want "70b" to stay "70b", so only do letter->digit
		}
	}

	return result.String()
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// calculateMatchScore calculates a match score between normalized names
func calculateMatchScore(normalizedLocal, normalizedSwan, originalLocal, originalSwan string) float64 {
	// Exact match after normalization
	if normalizedLocal == normalizedSwan {
		return 1.0
	}

	// Extract model family, version, and size for comparison
	localFamily, localVersion, localSize := extractModelInfoV2(normalizedLocal)
	swanFamily, swanVersion, swanSize := extractModelInfoV2(normalizedSwan)

	// Check for containment with family validation
	// This prevents false positives like "phi" matching "dolphin-phi"
	if strings.Contains(normalizedLocal, normalizedSwan) || strings.Contains(normalizedSwan, normalizedLocal) {
		// Only give high containment score if families match
		if localFamily != "" && swanFamily != "" && localFamily == swanFamily {
			return 0.85
		}
		// Lower score for containment without family match
		return 0.6
	}

	// Family must match for further scoring
	if localFamily == "" || swanFamily == "" {
		return 0
	}

	// Check family similarity
	familyScore := 0.0
	if localFamily == swanFamily {
		familyScore = 0.5
	} else if strings.Contains(localFamily, swanFamily) || strings.Contains(swanFamily, localFamily) {
		familyScore = 0.3
	}

	if familyScore == 0 {
		return 0
	}

	// Version match bonus
	versionScore := 0.0
	if localVersion != "" && swanVersion != "" && localVersion == swanVersion {
		versionScore = 0.25
	}

	// Size match bonus
	sizeScore := 0.0
	if localSize != "" && swanSize != "" && localSize == swanSize {
		sizeScore = 0.25
	}

	return familyScore + versionScore + sizeScore
}

// extractModelInfo extracts model family and size from normalized name
// Deprecated: Use extractModelInfoV2 for better version extraction
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

// versionPattern matches version numbers like "3", "3-2", "2-5", etc.
var versionPattern = regexp.MustCompile(`^(\d+(?:-\d+)*)$`)

// extractModelInfoV2 extracts model family, version, and size from normalized name
// Examples:
//
//	"llama-3-2-3b" -> family="llama", version="3-2", size="3b"
//	"qwen-2-5-7b" -> family="qwen", version="2-5", size="7b"
//	"mistral-7b" -> family="mistral", version="", size="7b"
//	"phi-3-mini" -> family="phi", version="3", size="mini"
func extractModelInfoV2(name string) (family, version, size string) {
	// Common size patterns (ordered by length to match longer patterns first)
	sizePatterns := []string{
		"405b", "70b", "65b", "34b", "33b", "32b", "30b", "27b", "24b",
		"14b", "13b", "12b", "11b", "8b", "7b", "6b", "4b", "3b", "2b", "1b",
		"0-5b", "500m", "mini", "small", "medium", "large", "xl", "xxl",
	}

	// Find size pattern
	for _, pattern := range sizePatterns {
		idx := strings.Index(name, pattern)
		if idx >= 0 {
			size = pattern
			// Everything before the size is family+version
			prefix := strings.Trim(name[:idx], "-")
			family, version = splitFamilyAndVersion(prefix)
			return family, version, size
		}
	}

	// No size found - try to extract family and version anyway
	family, version = splitFamilyAndVersion(name)
	return family, version, ""
}

// splitFamilyAndVersion separates the family name from version numbers
// Examples:
//
//	"llama-3-2" -> family="llama", version="3-2"
//	"qwen-2-5" -> family="qwen", version="2-5"
//	"mistral" -> family="mistral", version=""
//	"deepseek-v-2" -> family="deepseek", version="2"
func splitFamilyAndVersion(s string) (family, version string) {
	parts := strings.Split(s, "-")
	if len(parts) == 0 {
		return s, ""
	}

	// Find where version numbers start
	// Version is typically at the end, consisting of digits or "v" followed by digits
	versionStart := len(parts)
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		// Check if this part is a version component (pure digits or "v" prefix)
		if versionPattern.MatchString(part) {
			versionStart = i
		} else if part == "v" && i < len(parts)-1 {
			// Handle "deepseek-v-2" style versions
			versionStart = i
		} else {
			// Stop when we hit a non-version component
			break
		}
	}

	// Don't treat the entire string as a version
	if versionStart == 0 {
		return s, ""
	}

	family = strings.Join(parts[:versionStart], "-")
	if versionStart < len(parts) {
		versionParts := parts[versionStart:]
		// Remove "v" prefix if present
		if len(versionParts) > 0 && versionParts[0] == "v" {
			versionParts = versionParts[1:]
		}
		version = strings.Join(versionParts, "-")
	}

	return family, version
}
