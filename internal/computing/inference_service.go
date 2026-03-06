package computing

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/wallet"
)

// streamingHttpClient is a shared HTTP client for streaming inference requests
// with connection pooling to avoid creating new connections for each request
var streamingHttpClient = &http.Client{
	Timeout: 5 * time.Minute,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// Disable compression to reduce latency for streaming
		DisableCompression: true,
	},
}

// ModelMapping represents a model-to-endpoint mapping from models.json
type ModelMapping struct {
	Container    string `json:"container"`
	Endpoint     string `json:"endpoint"`
	GPUMemory    int    `json:"gpu_memory"`
	Category     string `json:"category"`
	LocalModel   string `json:"local_model"`             // Actual model name for local inference server (e.g., Ollama model name)
	Format       string `json:"format,omitempty"`        // Weight format: "fp16", "fp8", "awq", "gptq", "gguf"
	Quantization string `json:"quantization,omitempty"`  // Quantization detail: "q4_k_m", "q8_0", "w4a16", etc.
	APIKey       string `json:"api_key,omitempty"`       // API key for authenticated model endpoints (e.g., vLLM --api-key)
}

// InferenceService manages the Inference client and inference handling
type InferenceService struct {
	client             *InferenceClient
	nodeID             string
	cpPath             string
	modelMappings      map[string]ModelMapping
	registry           *ModelRegistry
	healthChecker      *ModelHealthChecker
	rateLimiter        *RateLimiter
	concurrencyLimiter *ConcurrencyLimiter
	retryPolicy        *RetryPolicy
	gpuCollector       *GPUMetricsCollector
	metricsHistory     *MetricsHistory
}

// NewInferenceService creates a new Inference service
func NewInferenceService(nodeID, cpPath string) *InferenceService {
	// Create GPU metrics collector
	gpuCollector := NewGPUMetricsCollector()

	// Create health checker with default config
	healthChecker := NewModelHealthChecker(DefaultHealthCheckConfig())

	// Create model registry with health checker
	registry := NewModelRegistry(cpPath, healthChecker)

	// Create rate limiter with GPU awareness
	rateLimiter := NewRateLimiter(DefaultRateLimiterConfig(), gpuCollector)

	// Create concurrency limiter with GPU awareness
	concurrencyLimiter := NewConcurrencyLimiter(DefaultConcurrencyConfig(), gpuCollector)

	// Create retry policy
	retryPolicy := NewRetryPolicy(DefaultRetryConfig())

	// Create metrics history for persistence
	metricsHistory := NewMetricsHistory()

	s := &InferenceService{
		nodeID:             nodeID,
		cpPath:             cpPath,
		modelMappings:      make(map[string]ModelMapping),
		registry:           registry,
		healthChecker:      healthChecker,
		rateLimiter:        rateLimiter,
		concurrencyLimiter: concurrencyLimiter,
		retryPolicy:        retryPolicy,
		gpuCollector:       gpuCollector,
		metricsHistory:     metricsHistory,
	}

	// Set up registry callbacks to update modelMappings for backward compatibility
	registry.SetCallbacks(
		func(model *RegisteredModel) {
			// On model added
			s.modelMappings[model.ID] = ModelMapping{
				Container:    model.Container,
				Endpoint:     model.Endpoint,
				GPUMemory:    model.GPUMemory,
				Category:     model.Category,
				Format:       model.Format,
				Quantization: model.Quantization,
				APIKey:       model.APIKey,
			}
			s.updateClientModels()
		},
		func(modelID string) {
			// On model removed
			delete(s.modelMappings, modelID)
			s.updateClientModels()
		},
		func(model *RegisteredModel) {
			// On model updated
			s.modelMappings[model.ID] = ModelMapping{
				Container:    model.Container,
				Endpoint:     model.Endpoint,
				GPUMemory:    model.GPUMemory,
				Category:     model.Category,
				Format:       model.Format,
				Quantization: model.Quantization,
				APIKey:       model.APIKey,
			}
		},
	)

	s.loadModelMappings()
	return s
}

// updateClientModels updates the client's model list based on ready models
func (s *InferenceService) updateClientModels() {
	if s.client != nil && s.registry != nil {
		models := s.registry.GetReadyModelIDs()
		s.client.models = models
		if s.client.IsConnected() {
			s.client.register()
		}
	}
}

// loadModelMappings loads model-to-endpoint mappings from models.json
func (s *InferenceService) loadModelMappings() {
	modelsPath := filepath.Join(s.cpPath, "models.json")
	data, err := os.ReadFile(modelsPath)
	if err != nil {
		logs.GetLogger().Debugf("No models.json found at %s", modelsPath)
		return
	}

	if err := json.Unmarshal(data, &s.modelMappings); err != nil {
		logs.GetLogger().Errorf("Failed to parse models.json: %v", err)
		return
	}

	logs.GetLogger().Infof("Loaded %d model mappings from models.json", len(s.modelMappings))
	for model, mapping := range s.modelMappings {
		logs.GetLogger().Infof("  - %s -> %s", model, mapping.Endpoint)
	}
}

// Start initializes and starts the Inference client
func (s *InferenceService) Start() error {
	config := conf.GetConfig()
	if !config.Inference.Enable {
		logs.GetLogger().Info("Inference mode is disabled")
		return nil
	}

	// Start model registry (loads models and starts file watcher)
	if err := s.registry.Start(); err != nil {
		logs.GetLogger().Warnf("Failed to start model registry: %v", err)
	}

	// Start health checker
	s.healthChecker.Start()

	// Start rate limiter (with adaptive GPU-aware adjustment)
	s.rateLimiter.Start()

	// Start concurrency limiter (with GPU memory awareness)
	s.concurrencyLimiter.Start()

	// Get owner and worker addresses
	ownerAddr, workerAddr, err := GetOwnerAddressAndWorkerAddress()
	if err != nil {
		logs.GetLogger().Warnf("Failed to get addresses from CP account: %v", err)
		// For dev mode: use wallet address as owner
		ownerAddr = s.getDefaultWalletAddress()
		workerAddr = s.nodeID
	}

	s.client = NewInferenceClient(s.nodeID, workerAddr, ownerAddr)
	s.client.SetInferenceHandler(s.handleInference)
	s.client.SetStreamingInferenceHandler(s.handleStreamingInference)
	s.client.SetWarmupHandler(s.handleWarmup)

	// Set up model mappings provider for format/quantization and engine detection
	s.client.SetModelMappingsProvider(func() map[string]ModelMapping {
		return s.modelMappings
	})

	// Set up model health provider for heartbeats (backup for health update messages)
	s.client.SetModelHealthProvider(func() map[string]string {
		if s.registry != nil {
			return s.registry.GetAllModelHealthMap()
		}
		return nil
	})

	// Set up health update callback to notify Swan Inference when model health changes
	s.registry.SetHealthUpdateCallback(func(modelHealth map[string]string) {
		if s.client != nil && s.client.IsConnected() {
			s.client.SendModelHealthUpdate(modelHealth)
		}
	})

	if err := s.client.Start(); err != nil {
		return fmt.Errorf("failed to start Inference client: %w", err)
	}

	// Start metrics history recorder
	if s.metricsHistory != nil {
		if err := s.metricsHistory.Start(func() *InferenceMetrics {
			return s.GetMetrics()
		}); err != nil {
			logs.GetLogger().Warnf("Failed to start metrics history: %v", err)
		}
	}

	logs.GetLogger().Infof("Inference service started with node ID: %s, owner: %s", s.nodeID, ownerAddr)
	return nil
}

// getDefaultWalletAddress returns the first wallet address from keystore (for dev mode)
func (s *InferenceService) getDefaultWalletAddress() string {
	localWallet, err := wallet.SetupWallet(wallet.WalletRepo)
	if err != nil {
		logs.GetLogger().Warnf("Failed to setup wallet: %v", err)
		return ""
	}

	addresses, err := localWallet.List()
	if err != nil || len(addresses) == 0 {
		logs.GetLogger().Warnf("No wallet addresses found")
		return ""
	}

	// Remove "wallet-" prefix if present
	addr := addresses[0]
	if strings.HasPrefix(addr, "wallet-") {
		addr = strings.TrimPrefix(addr, "wallet-")
	}

	logs.GetLogger().Infof("Using wallet address as owner for dev mode: %s", addr)
	return addr
}

// Stop gracefully shuts down the Inference service
func (s *InferenceService) Stop() {
	if s.client != nil {
		s.client.Stop()
	}
	if s.healthChecker != nil {
		s.healthChecker.Stop()
	}
	if s.registry != nil {
		s.registry.Stop()
	}
	if s.rateLimiter != nil {
		s.rateLimiter.Stop()
	}
	if s.concurrencyLimiter != nil {
		s.concurrencyLimiter.Stop()
	}
	if s.metricsHistory != nil {
		s.metricsHistory.Stop()
	}
}

// Name returns the service name for the supervisor
func (s *InferenceService) Name() string {
	return "inference-service"
}

// IsHealthy returns whether the service is healthy (connected to Swan Inference)
func (s *InferenceService) IsHealthy() bool {
	if s.client == nil {
		return false
	}
	return s.client.IsConnected()
}

// handleInference processes inference requests from Inference service
func (s *InferenceService) handleInference(payload InferencePayload) (*InferenceResponse, error) {
	logs.GetLogger().Infof("Handling inference for model: %s, endpoint: %s", payload.ModelID, payload.EndpointID)

	// Try to get endpoint and local model name from registry first (preferred)
	var endpoint string
	var localModel string
	var apiKey string

	if ep, ok := s.registry.GetModelEndpoint(payload.ModelID); ok {
		endpoint = ep
		localModel = s.registry.GetLocalModelName(payload.ModelID)
		apiKey = s.registry.GetModelAPIKey(payload.ModelID)
	} else {
		// Fall back to direct mapping lookup for backward compatibility
		mapping, mapOk := s.modelMappings[payload.ModelID]
		if !mapOk {
			return nil, &ModelServerError{
				StatusCode: 404,
				Message:    fmt.Sprintf("model %s not deployed on this provider", payload.ModelID),
			}
		}
		endpoint = mapping.Endpoint
		localModel = mapping.LocalModel
		apiKey = mapping.APIKey
	}

	// Check model health before forwarding
	if s.healthChecker != nil && !s.healthChecker.IsModelHealthy(payload.ModelID) {
		logs.GetLogger().Warnf("Model %s is unhealthy, but attempting request anyway", payload.ModelID)
	}

	logs.GetLogger().Infof("Using Docker endpoint for model %s: %s (local: %s)", payload.ModelID, endpoint, localModel)
	response, err := s.forwardToDockerModel(endpoint, payload.Request, payload.ModelID, localModel, apiKey)
	if err != nil {
		// Preserve ModelServerError type for status code extraction
		var modelErr *ModelServerError
		if errors.As(err, &modelErr) {
			return nil, modelErr
		}
		return nil, fmt.Errorf("inference failed: %w", err)
	}
	return &InferenceResponse{
		Response: response,
	}, nil
}

// checkForOpenAIError detects error responses returned with HTTP 200 by some model servers.
// Some servers (e.g., certain Ollama versions) return errors in the response body with 200 status.
func checkForOpenAIError(response json.RawMessage) error {
	if len(response) == 0 {
		return &ModelServerError{
			StatusCode: 502,
			Message:    "empty response from model server",
		}
	}

	var errBody struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    interface{} `json:"code"` // Can be string or int
		} `json:"error"`
	}
	if err := json.Unmarshal(response, &errBody); err == nil && errBody.Error.Message != "" {
		statusCode := 500
		// Try to extract numeric code
		switch v := errBody.Error.Code.(type) {
		case float64:
			statusCode = int(v)
		case string:
			if v == "model_not_found" {
				statusCode = 404
			}
		}
		return &ModelServerError{
			StatusCode: statusCode,
			Body:       response,
			Message:    errBody.Error.Message,
		}
	}

	return nil
}

// stripMarkdownJSONFences removes markdown code fences from JSON content.
// Models sometimes return ```json\n{...}\n``` instead of raw JSON when
// response_format.type == "json_object" is set.
func stripMarkdownJSONFences(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "```") {
		return content
	}
	// Remove opening fence (```json or ```)
	idx := strings.Index(trimmed, "\n")
	if idx < 0 {
		return content
	}
	trimmed = trimmed[idx+1:]
	// Remove closing fence
	if strings.HasSuffix(strings.TrimSpace(trimmed), "```") {
		trimmed = strings.TrimSpace(trimmed)
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimRight(trimmed, "\n\r ")
	}
	return trimmed
}

// postProcessResponse applies provider-side fixes to model responses:
// 1. Strips markdown fences from json_object responses
// 2. Ensures prompt_tokens_details exists in usage (prevents nil dereference in clients)
func postProcessResponse(request json.RawMessage, response json.RawMessage) json.RawMessage {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(request, &reqMap); err != nil {
		return response
	}

	// Check if response_format.type == "json_object"
	needsJSONFix := false
	if rf, ok := reqMap["response_format"].(map[string]interface{}); ok {
		if rfType, ok := rf["type"].(string); ok && (rfType == "json_object" || rfType == "json_schema") {
			needsJSONFix = true
		}
	}

	var respMap map[string]interface{}
	if err := json.Unmarshal(response, &respMap); err != nil {
		return response
	}

	modified := false

	// Fix 1: Strip markdown fences from json_object/json_schema responses
	if needsJSONFix {
		if choices, ok := respMap["choices"].([]interface{}); ok {
			for i, choice := range choices {
				choiceMap, ok := choice.(map[string]interface{})
				if !ok {
					continue
				}
				message, ok := choiceMap["message"].(map[string]interface{})
				if !ok {
					continue
				}
				content, ok := message["content"].(string)
				if !ok {
					continue
				}
				stripped := stripMarkdownJSONFences(content)
				if stripped != content {
					message["content"] = stripped
					choiceMap["message"] = message
					choices[i] = choiceMap
					modified = true
				}
			}
		}
	}

	// Fix 2: Ensure prompt_tokens_details exists in usage
	if usage, ok := respMap["usage"].(map[string]interface{}); ok {
		if _, exists := usage["prompt_tokens_details"]; !exists {
			usage["prompt_tokens_details"] = map[string]interface{}{
				"cached_tokens": 0,
			}
			respMap["usage"] = usage
			modified = true
		}
	}

	if !modified {
		return response
	}

	result, err := json.Marshal(respMap)
	if err != nil {
		return response
	}
	return result
}

// forwardToDockerModel forwards inference request to a Docker container endpoint
func (s *InferenceService) forwardToDockerModel(endpoint string, request json.RawMessage, modelID, localModel, apiKey string) (json.RawMessage, error) {
	// Substitute model name if local_model is configured
	modifiedRequest := s.substituteModelName(request, modelID, localModel)

	var headers http.Header
	if apiKey != "" {
		headers = http.Header{}
		headers.Set("Authorization", "Bearer "+apiKey)
	}
	httpClient := NewHttpClient(endpoint, headers)

	var response json.RawMessage
	if err := httpClient.PostJSON("/v1/chat/completions", modifiedRequest, &response); err != nil {
		return nil, fmt.Errorf("failed to forward request to Docker model: %w", err)
	}

	// Check for error responses returned with HTTP 200
	if err := checkForOpenAIError(response); err != nil {
		return nil, err
	}

	// Post-process: strip markdown fences from JSON responses, ensure prompt_tokens_details
	response = postProcessResponse(modifiedRequest, response)

	return response, nil
}

// substituteModelName replaces the model name in the request if local_model is configured
func (s *InferenceService) substituteModelName(request json.RawMessage, modelID, localModel string) json.RawMessage {
	if localModel == "" {
		return request
	}

	// Parse the request to modify the model field
	var reqMap map[string]interface{}
	if err := json.Unmarshal(request, &reqMap); err != nil {
		logs.GetLogger().Warnf("Failed to parse request for model substitution: %v", err)
		return request
	}

	// Replace the model field with the local model name
	if _, ok := reqMap["model"]; ok {
		logs.GetLogger().Debugf("Substituting model name: %s -> %s", modelID, localModel)
		reqMap["model"] = localModel
	}

	modifiedRequest, err := json.Marshal(reqMap)
	if err != nil {
		logs.GetLogger().Warnf("Failed to marshal modified request: %v", err)
		return request
	}

	return modifiedRequest
}

// GetClient returns the Inference client
func (s *InferenceService) GetClient() *InferenceClient {
	return s.client
}

// IsConnected returns whether the Inference client is connected
func (s *InferenceService) IsConnected() bool {
	if s.client == nil {
		return false
	}
	return s.client.IsConnected()
}

// GetActiveModels returns the list of active model deployments
func (s *InferenceService) GetActiveModels() []string {
	var activeModels []string
	for modelName := range s.modelMappings {
		activeModels = append(activeModels, modelName)
	}
	return activeModels
}

// GetMetrics returns the current inference metrics
func (s *InferenceService) GetMetrics() *InferenceMetrics {
	if s.client == nil {
		return nil
	}
	metrics := s.client.GetMetrics()
	return &metrics
}

// GetMetricsPrometheus returns metrics in Prometheus text format
func (s *InferenceService) GetMetricsPrometheus() string {
	if s.client == nil {
		return ""
	}
	return s.client.GetMetricsPrometheus()
}

// RegisterModels updates the models this provider serves
func (s *InferenceService) RegisterModels(models []string) {
	if s.client != nil {
		s.client.models = models
		// Re-register with new model list
		if s.client.IsConnected() {
			s.client.register()
		}
	}
}

// handleStreamingInference processes streaming inference requests
func (s *InferenceService) handleStreamingInference(requestID string, payload InferencePayload, sendChunk func(chunk []byte, done bool) error) *StreamResult {
	logs.GetLogger().Infof("Handling streaming inference for model: %s, endpoint: %s", payload.ModelID, payload.EndpointID)

	// Try to get endpoint and local model name from registry first (preferred)
	var endpoint string
	var localModel string
	var apiKey string

	if ep, ok := s.registry.GetModelEndpoint(payload.ModelID); ok {
		endpoint = ep
		localModel = s.registry.GetLocalModelName(payload.ModelID)
		apiKey = s.registry.GetModelAPIKey(payload.ModelID)
	} else {
		// Fall back to direct mapping lookup for backward compatibility
		mapping, mapOk := s.modelMappings[payload.ModelID]
		if !mapOk {
			return &StreamResult{Error: &ModelServerError{
				StatusCode: 404,
				Message:    fmt.Sprintf("model %s not deployed on this provider", payload.ModelID),
			}}
		}
		endpoint = mapping.Endpoint
		localModel = mapping.LocalModel
		apiKey = mapping.APIKey
	}

	// Check model health before forwarding
	if s.healthChecker != nil && !s.healthChecker.IsModelHealthy(payload.ModelID) {
		logs.GetLogger().Warnf("Model %s is unhealthy, but attempting streaming request anyway", payload.ModelID)
	}

	logs.GetLogger().Infof("Using Docker endpoint for streaming model %s: %s (local: %s)", payload.ModelID, endpoint, localModel)
	return s.streamFromDockerModel(endpoint, payload.Request, payload.ModelID, localModel, apiKey, sendChunk)
}

// handleWarmup processes model warmup requests from Swan Inference
func (s *InferenceService) handleWarmup(payload WarmupPayload) (*WarmupResponse, error) {
	logs.GetLogger().Infof("Handling warmup for model: %s (type: %s)", payload.ModelID, payload.WarmupType)

	// Try to get endpoint and local model name from registry first (preferred)
	var endpoint string
	var localModel string
	var apiKey string

	if ep, ok := s.registry.GetModelEndpoint(payload.ModelID); ok {
		endpoint = ep
		localModel = s.registry.GetLocalModelName(payload.ModelID)
		apiKey = s.registry.GetModelAPIKey(payload.ModelID)
	} else {
		// Fall back to direct mapping lookup for backward compatibility
		mapping, mapOk := s.modelMappings[payload.ModelID]
		if !mapOk {
			return nil, fmt.Errorf("model %s not deployed on this provider", payload.ModelID)
		}
		endpoint = mapping.Endpoint
		localModel = mapping.LocalModel
		apiKey = mapping.APIKey
	}

	logs.GetLogger().Infof("Warming up model %s at endpoint %s (local: %s)", payload.ModelID, endpoint, localModel)

	// Use local model name if configured, otherwise use the Swan Inference model ID
	modelNameForRequest := payload.ModelID
	if localModel != "" {
		modelNameForRequest = localModel
	}

	// Send a simple warmup request to load the model into memory
	warmupRequest := map[string]interface{}{
		"model": modelNameForRequest,
		"messages": []map[string]string{
			{"role": "user", "content": "Hi"},
		},
		"max_tokens": 1, // Minimal response to save resources
	}

	// Pass the map directly to PostJSON which handles marshaling.
	// Previously we marshaled to []byte first, causing double-encoding
	// (json.Marshal encodes []byte as base64).
	var headers http.Header
	if apiKey != "" {
		headers = http.Header{}
		headers.Set("Authorization", "Bearer "+apiKey)
	}
	httpClient := NewHttpClient(endpoint, headers)
	var response json.RawMessage
	if err := httpClient.PostJSON("/v1/chat/completions", warmupRequest, &response); err != nil {
		return nil, fmt.Errorf("warmup request failed: %w", err)
	}

	logs.GetLogger().Infof("Model %s warmed up successfully", payload.ModelID)

	return &WarmupResponse{
		ModelID: payload.ModelID,
		Success: true,
	}, nil
}

// streamFromDockerModel streams inference response from a model endpoint
func (s *InferenceService) streamFromDockerModel(endpoint string, request json.RawMessage, modelID, localModel, apiKey string, sendChunk func(chunk []byte, done bool) error) *StreamResult {
	result := &StreamResult{}

	// Ensure stream is set to true in the request and request usage
	var reqMap map[string]interface{}
	if err := json.Unmarshal(request, &reqMap); err != nil {
		result.Error = fmt.Errorf("failed to parse request: %w", err)
		return result
	}
	reqMap["stream"] = true
	// Request usage stats in streaming response (OpenAI-compatible)
	reqMap["stream_options"] = map[string]interface{}{
		"include_usage": true,
	}
	// Substitute model name if local_model is configured
	if localModel != "" {
		logs.GetLogger().Debugf("Substituting model name in stream: %s -> %s", modelID, localModel)
		reqMap["model"] = localModel
	}
	modifiedRequest, err := json.Marshal(reqMap)
	if err != nil {
		result.Error = fmt.Errorf("failed to marshal modified request: %w", err)
		return result
	}

	// Make streaming request to model using shared HTTP client with connection pooling
	url := endpoint + "/v1/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(modifiedRequest))
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := streamingHttpClient.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("failed to send request: %w", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		result.Error = parseModelServerError(resp.StatusCode, body)
		return result
	}

	// Parse SSE stream and forward chunks
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			result.Error = fmt.Errorf("failed to read stream: %w", err)
			return result
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse SSE format: "data: {...}" or "data: [DONE]"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Check for end of stream
		if data == "[DONE]" {
			// Send final chunk with done=true
			if err := sendChunk(nil, true); err != nil {
				logs.GetLogger().Warnf("Failed to send final chunk: %v", err)
			}
			break
		}

		// Parse chunk for filtering and post-processing
		var chunkMap map[string]interface{}
		if err := json.Unmarshal([]byte(data), &chunkMap); err == nil {
			hasUsage := false

			// Extract usage information (OpenAI/SGLang sends usage in a chunk with empty choices)
			if usage, ok := chunkMap["usage"].(map[string]interface{}); ok && usage != nil {
				hasUsage = true
				if pt, ok := usage["prompt_tokens"].(float64); ok {
					result.TokensInput = int64(pt)
				}
				if ct, ok := usage["completion_tokens"].(float64); ok {
					result.TokensOutput = int64(ct)
				}
				// Fix: Ensure prompt_tokens_details exists in streaming usage
				if _, exists := usage["prompt_tokens_details"]; !exists {
					usage["prompt_tokens_details"] = map[string]interface{}{
						"cached_tokens": 0,
					}
					chunkMap["usage"] = usage
					if modified, err := json.Marshal(chunkMap); err == nil {
						data = string(modified)
					}
				}
			}

			// Fix: Skip chunks with empty choices array (causes "list index out of range" in clients)
			// But preserve usage-only chunks (SGLang sends usage with choices: [])
			if !hasUsage {
				if choices, ok := chunkMap["choices"].([]interface{}); ok && len(choices) == 0 {
					continue
				}
			}
		}

		// Forward the chunk data — abort immediately on send failure to avoid
		// flooding timeouts when the WebSocket send buffer is saturated
		if err := sendChunk([]byte(data), false); err != nil {
			logs.GetLogger().Warnf("Failed to send chunk for model %s, aborting stream: %v", modelID, err)
			result.Error = fmt.Errorf("send buffer full, stream aborted: %w", err)
			return result
		}
	}

	return result
}

// === Model Management API ===

// GetRegistry returns the model registry
func (s *InferenceService) GetRegistry() *ModelRegistry {
	return s.registry
}

// GetHealthChecker returns the model health checker
func (s *InferenceService) GetHealthChecker() *ModelHealthChecker {
	return s.healthChecker
}

// GetAllModels returns all registered models with their status
func (s *InferenceService) GetAllModels() []*RegisteredModel {
	if s.registry == nil {
		return nil
	}
	return s.registry.GetAllModels()
}

// GetModelStatus returns the status of a specific model
func (s *InferenceService) GetModelStatus(modelID string) (*RegisteredModel, bool) {
	if s.registry == nil {
		return nil, false
	}
	return s.registry.GetModel(modelID)
}

// GetModelHealth returns the health status of a specific model
func (s *InferenceService) GetModelHealth(modelID string) (*ModelStatus, bool) {
	if s.healthChecker == nil {
		return nil, false
	}
	return s.healthChecker.GetModelStatus(modelID)
}

// GetAllModelHealth returns health status of all models
func (s *InferenceService) GetAllModelHealth() map[string]*ModelStatus {
	if s.healthChecker == nil {
		return nil
	}
	return s.healthChecker.GetAllStatuses()
}

// EnableModel enables a model for serving requests
func (s *InferenceService) EnableModel(modelID string) error {
	if s.registry == nil {
		return fmt.Errorf("registry not initialized")
	}
	return s.registry.EnableModel(modelID)
}

// DisableModel disables a model from serving requests
func (s *InferenceService) DisableModel(modelID string) error {
	if s.registry == nil {
		return fmt.Errorf("registry not initialized")
	}
	return s.registry.DisableModel(modelID)
}

// ReloadModels manually triggers a reload of the models configuration
func (s *InferenceService) ReloadModels() error {
	if s.registry == nil {
		return fmt.Errorf("registry not initialized")
	}
	return s.registry.ReloadConfig()
}

// ForceHealthCheck triggers an immediate health check for a model
func (s *InferenceService) ForceHealthCheck(modelID string) {
	if s.healthChecker != nil {
		s.healthChecker.ForceCheck(modelID)
	}
}

// GetModelsSummary returns a summary of model statuses
func (s *InferenceService) GetModelsSummary() map[string]interface{} {
	if s.registry == nil {
		return map[string]interface{}{
			"total":     0,
			"ready":     0,
			"unhealthy": 0,
			"disabled":  0,
		}
	}
	return s.registry.GetStatusSummary()
}

// === Request Management API ===

// GetRateLimiterMetrics returns rate limiter metrics
func (s *InferenceService) GetRateLimiterMetrics() *RateLimiterMetrics {
	if s.rateLimiter == nil {
		return nil
	}
	metrics := s.rateLimiter.GetMetrics()
	return &metrics
}

// GetConcurrencyMetrics returns concurrency limiter metrics
func (s *InferenceService) GetConcurrencyMetrics() *ConcurrencyMetrics {
	if s.concurrencyLimiter == nil {
		return nil
	}
	metrics := s.concurrencyLimiter.GetMetrics()
	return &metrics
}

// GetRetryMetrics returns retry policy metrics
func (s *InferenceService) GetRetryMetrics() *RetryMetrics {
	if s.retryPolicy == nil {
		return nil
	}
	metrics := s.retryPolicy.GetMetrics()
	return &metrics
}

// SetGlobalRateLimit updates the global rate limit
func (s *InferenceService) SetGlobalRateLimit(tokensPerSecond float64) {
	if s.rateLimiter != nil {
		s.rateLimiter.globalBucket.SetRate(tokensPerSecond)
		logs.GetLogger().Infof("Updated global rate limit to %.2f req/s", tokensPerSecond)
	}
}

// SetModelRateLimit sets rate limit for a specific model
func (s *InferenceService) SetModelRateLimit(modelID string, tokensPerSecond float64, burstSize int) {
	if s.rateLimiter != nil {
		s.rateLimiter.SetModelLimit(modelID, tokensPerSecond, burstSize)
	}
}

// SetGlobalConcurrencyLimit updates the global concurrency limit
func (s *InferenceService) SetGlobalConcurrencyLimit(max int) {
	if s.concurrencyLimiter != nil {
		s.concurrencyLimiter.SetGlobalMax(max)
	}
}

// SetModelConcurrencyLimit sets concurrency limit for a specific model
func (s *InferenceService) SetModelConcurrencyLimit(modelID string, max int) {
	if s.concurrencyLimiter != nil {
		s.concurrencyLimiter.SetModelMax(modelID, max)
	}
}

// GetRequestManagementStatus returns combined status of all request management components
func (s *InferenceService) GetRequestManagementStatus() map[string]interface{} {
	status := make(map[string]interface{})

	if s.rateLimiter != nil {
		status["rate_limiter"] = s.rateLimiter.GetMetrics()
	}
	if s.concurrencyLimiter != nil {
		status["concurrency_limiter"] = s.concurrencyLimiter.GetMetrics()
	}
	if s.retryPolicy != nil {
		status["retry_policy"] = s.retryPolicy.GetMetrics()
	}

	return status
}

// GetRequestHistory returns recent request history, optionally filtered by model
func (s *InferenceService) GetRequestHistory(limit int, modelFilter string) []RequestMetric {
	if s.client == nil {
		return nil
	}
	return s.client.metrics.GetRequestHistory(limit, modelFilter)
}

// GetModelDetailedMetrics returns detailed metrics for a specific model including recent requests
func (s *InferenceService) GetModelDetailedMetrics(modelID string) map[string]interface{} {
	result := make(map[string]interface{})

	// Get model info from registry
	if model, ok := s.registry.GetModel(modelID); ok {
		result["model"] = model
	}

	// Get model health
	if health, ok := s.healthChecker.GetModelStatus(modelID); ok {
		result["health"] = health
	}

	// Get model metrics from InferenceMetrics
	if s.client != nil {
		metrics := s.client.GetMetrics()
		if mm, ok := metrics.ModelMetrics[modelID]; ok {
			result["metrics"] = mm
		}
	}

	// Get recent requests for this model
	result["recent_requests"] = s.GetRequestHistory(20, modelID)

	return result
}

// GetMetricsHistory returns historical metrics for the specified duration and resolution
func (s *InferenceService) GetMetricsHistory(duration, resolution time.Duration) ([]HistoricalDataPoint, error) {
	if s.metricsHistory == nil {
		return nil, nil
	}
	return s.metricsHistory.GetHistory(duration, resolution)
}
