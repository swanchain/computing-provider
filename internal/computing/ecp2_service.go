package computing

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/swanchain/computing-provider-v2/conf"
)

// ModelMapping represents a model-to-endpoint mapping from models.json
type ModelMapping struct {
	Container string `json:"container"`
	Endpoint  string `json:"endpoint"`
	GPUMemory int    `json:"gpu_memory"`
	Category  string `json:"category"`
}

// ECP2Service manages the ECP2 client and inference handling
type ECP2Service struct {
	client        *ECP2Client
	nodeID        string
	cpPath        string
	modelMappings map[string]ModelMapping
}

// NewECP2Service creates a new ECP2 service
func NewECP2Service(nodeID, cpPath string) *ECP2Service {
	s := &ECP2Service{
		nodeID:        nodeID,
		cpPath:        cpPath,
		modelMappings: make(map[string]ModelMapping),
	}
	s.loadModelMappings()
	return s
}

// loadModelMappings loads model-to-endpoint mappings from models.json
func (s *ECP2Service) loadModelMappings() {
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

// Start initializes and starts the ECP2 client
func (s *ECP2Service) Start() error {
	config := conf.GetConfig()
	if !config.ECP2.Enable {
		logs.GetLogger().Info("ECP2 marketplace integration is disabled")
		return nil
	}

	// Get worker address
	_, workerAddr, err := GetOwnerAddressAndWorkerAddress()
	if err != nil {
		logs.GetLogger().Warnf("Failed to get worker address, using node ID: %v", err)
		workerAddr = s.nodeID
	}

	s.client = NewECP2Client(s.nodeID, workerAddr)
	s.client.SetInferenceHandler(s.handleInference)
	s.client.SetStreamingInferenceHandler(s.handleStreamingInference)

	if err := s.client.Start(); err != nil {
		return fmt.Errorf("failed to start ECP2 client: %w", err)
	}

	logs.GetLogger().Infof("ECP2 service started with provider ID: %s", s.nodeID)
	return nil
}

// Stop gracefully shuts down the ECP2 service
func (s *ECP2Service) Stop() {
	if s.client != nil {
		s.client.Stop()
	}
}

// handleInference processes inference requests from ECP2 service
func (s *ECP2Service) handleInference(payload InferencePayload) (*InferenceResponse, error) {
	logs.GetLogger().Infof("Handling inference for model: %s, endpoint: %s", payload.ModelID, payload.EndpointID)

	// Check if we have a Docker model mapping
	mapping, ok := s.modelMappings[payload.ModelID]
	if !ok {
		return nil, fmt.Errorf("model %s not deployed on this provider", payload.ModelID)
	}

	logs.GetLogger().Infof("Using Docker endpoint for model %s: %s", payload.ModelID, mapping.Endpoint)
	response, err := s.forwardToDockerModel(mapping.Endpoint, payload.Request)
	if err != nil {
		return nil, fmt.Errorf("inference failed: %w", err)
	}
	return &InferenceResponse{
		Response: response,
	}, nil
}

// forwardToDockerModel forwards inference request to a Docker container endpoint
func (s *ECP2Service) forwardToDockerModel(endpoint string, request json.RawMessage) (json.RawMessage, error) {
	httpClient := NewHttpClient(endpoint, nil)

	var response json.RawMessage
	if err := httpClient.PostJSON("/v1/chat/completions", request, &response); err != nil {
		return nil, fmt.Errorf("failed to forward request to Docker model: %w", err)
	}

	return response, nil
}

// GetClient returns the ECP2 client
func (s *ECP2Service) GetClient() *ECP2Client {
	return s.client
}

// IsConnected returns whether the ECP2 client is connected
func (s *ECP2Service) IsConnected() bool {
	if s.client == nil {
		return false
	}
	return s.client.IsConnected()
}

// GetActiveModels returns the list of active model deployments
func (s *ECP2Service) GetActiveModels() []string {
	var activeModels []string
	for modelName := range s.modelMappings {
		activeModels = append(activeModels, modelName)
	}
	return activeModels
}

// RegisterModels updates the models this provider serves
func (s *ECP2Service) RegisterModels(models []string) {
	if s.client != nil {
		s.client.models = models
		// Re-register with new model list
		if s.client.IsConnected() {
			s.client.register()
		}
	}
}

// handleStreamingInference processes streaming inference requests
func (s *ECP2Service) handleStreamingInference(requestID string, payload InferencePayload, sendChunk func(chunk []byte, done bool) error) *StreamResult {
	logs.GetLogger().Infof("Handling streaming inference for model: %s, endpoint: %s", payload.ModelID, payload.EndpointID)

	// Check if we have a Docker model mapping
	mapping, ok := s.modelMappings[payload.ModelID]
	if !ok {
		return &StreamResult{Error: fmt.Errorf("model %s not deployed on this provider", payload.ModelID)}
	}

	logs.GetLogger().Infof("Using Docker endpoint for streaming model %s: %s", payload.ModelID, mapping.Endpoint)
	return s.streamFromDockerModel(mapping.Endpoint, payload.Request, sendChunk)
}

// streamFromDockerModel streams inference response from a model endpoint
func (s *ECP2Service) streamFromDockerModel(endpoint string, request json.RawMessage, sendChunk func(chunk []byte, done bool) error) *StreamResult {
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
	modifiedRequest, err := json.Marshal(reqMap)
	if err != nil {
		result.Error = fmt.Errorf("failed to marshal modified request: %w", err)
		return result
	}

	// Create HTTP client with longer timeout for streaming
	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Make streaming request to model
	url := endpoint + "/v1/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(modifiedRequest))
	if err != nil {
		result.Error = fmt.Errorf("failed to create request: %w", err)
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Errorf("failed to send request: %w", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		result.Error = fmt.Errorf("model returned error: %s", string(body))
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

		// Try to extract usage information from the chunk (OpenAI returns usage in last content chunk)
		var chunkData struct {
			Usage *struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunkData); err == nil && chunkData.Usage != nil {
			result.TokensInput = chunkData.Usage.PromptTokens
			result.TokensOutput = chunkData.Usage.CompletionTokens
		}

		// Forward the chunk data
		if err := sendChunk([]byte(data), false); err != nil {
			logs.GetLogger().Warnf("Failed to send chunk: %v", err)
			// Continue trying to send remaining chunks
		}
	}

	return result
}
