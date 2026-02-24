package computing

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/mitchellh/go-homedir"
	"github.com/swanchain/computing-provider-v2/conf"
	"github.com/swanchain/computing-provider-v2/internal/models"
)

// Inference WebSocket Protocol Types
type MessageType string

const (
	MsgTypeRegister          MessageType = "register"
	MsgTypeInference         MessageType = "inference"
	MsgTypeVerify            MessageType = "verify"
	MsgTypeHeartbeat         MessageType = "heartbeat"
	MsgTypeAck               MessageType = "ack"
	MsgTypeError             MessageType = "error"
	MsgTypeStreamChunk       MessageType = "stream_chunk"        // Streaming chunk to Swan Inference
	MsgTypeStreamEnd         MessageType = "stream_end"          // End of stream marker
	MsgTypeWarmup            MessageType = "warmup"              // Model warmup request
	MsgTypeModelHealthUpdate MessageType = "model_health_update" // Model health status update
)

// Message is the base WebSocket message structure
type Message struct {
	Type      MessageType     `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

// HardwareInfo contains GPU hardware specifications
type HardwareInfo struct {
	GPUType           string `json:"gpu_type"`
	GPUModel          string `json:"gpu_model"`
	VRAMGB            int    `json:"vram_gb"`
	GPUCount          int    `json:"gpu_count"`
	ComputeCapability string `json:"compute_capability"`
	DriverVersion     string `json:"driver_version"`
	CUDAVersion       string `json:"cuda_version"`
	ServingEngine     string `json:"serving_engine,omitempty"` // "vllm", "sglang", "llamacpp", "ollama", "tgi", "unknown"
}

// ModelInfo contains model identification and verification hash
type ModelInfo struct {
	ModelID      string `json:"model_id"`
	WeightHash   string `json:"weight_hash,omitempty"`   // Composite SHA256 of all weight files
	HashAlgo     string `json:"hash_algo,omitempty"`     // Hash algorithm, e.g. "sha256"
	Format       string `json:"format,omitempty"`        // Weight format: "fp16", "fp8", "awq", "gptq", "gguf"
	Quantization string `json:"quantization,omitempty"`  // Quantization detail: "q4_k_m", "q8_0", "w4a16", etc.
}

// VerifyResponsePayload is returned after processing a verification challenge
type VerifyResponsePayload struct {
	ChallengeID string          `json:"challenge_id"`
	Success     bool            `json:"success"`
	Response    json.RawMessage `json:"response"`
	Error       string          `json:"error,omitempty"`
}

// RegisterPayload is sent by provider on connection
type RegisterPayload struct {
	NodeID       string        `json:"node_id"`                      // Local node ID (not the DB provider ID)
	ProviderID   string        `json:"provider_id,omitempty"`        // Deprecated: use NodeID
	WorkerAddr   string        `json:"worker_addr"`
	OwnerAddr    string        `json:"owner_addr"`
	Token        string        `json:"token,omitempty"`              // API key for authentication (sk-prov-*)
	Signature    string        `json:"signature,omitempty"`
	Models       []string      `json:"models"`
	ModelHashes  []ModelInfo   `json:"model_hashes,omitempty"`       // Per-model composite hashes for verification
	Capabilities []string      `json:"capabilities"`
	Hardware     *HardwareInfo `json:"hardware,omitempty"`
}

// InferencePayload is sent to provider for inference request
type InferencePayload struct {
	EndpointID string          `json:"endpoint_id"`
	ModelID    string          `json:"model_id"`
	Request    json.RawMessage `json:"request"`
	Stream     bool            `json:"stream"` // Whether to stream the response
}

// InferenceResponse is returned by provider
type InferenceResponse struct {
	RequestID  string          `json:"request_id"`
	Response   json.RawMessage `json:"response"`
	Error      string          `json:"error,omitempty"`
	StatusCode int             `json:"status_code,omitempty"` // HTTP status code for Swan Inference to map to proper responses
	Latency    int64           `json:"latency_ms"`
}

// VerifyPayload is sent to provider for model verification
type VerifyPayload struct {
	ChallengeID   string          `json:"challenge_id"`
	ChallengeType string          `json:"challenge_type"`
	ModelID       string          `json:"model_id"`
	Challenge     json.RawMessage `json:"challenge"`
}

// HeartbeatPayload for liveness checks
type HeartbeatPayload struct {
	NodeID      string             `json:"node_id"`                      // Local node ID (not the DB provider ID)
	ProviderID  string             `json:"provider_id,omitempty"`        // Deprecated: use NodeID
	Timestamp   int64              `json:"timestamp"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
	ModelHealth map[string]string  `json:"model_health,omitempty"` // modelID -> health status (backup for health updates)
	Hardware    *HardwareInfo      `json:"hardware,omitempty"`     // GPU hardware info (periodically updated)
}

// AckPayload for acknowledgments
type AckPayload struct {
	RequestID string `json:"request_id"`
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
}

// ErrorPayload for error responses
type ErrorPayload struct {
	RequestID string `json:"request_id,omitempty"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
}

// StreamChunkPayload represents a streaming chunk sent to Swan Inference
type StreamChunkPayload struct {
	RequestID string          `json:"request_id"`
	Chunk     json.RawMessage `json:"chunk"` // OpenAI-compatible SSE chunk data
	Done      bool            `json:"done"`  // True when stream is complete
}

// StreamEndPayload signals end of stream with usage stats
type StreamEndPayload struct {
	RequestID    string `json:"request_id"`
	Latency      int64  `json:"latency_ms"`
	TokensInput  int64  `json:"tokens_input,omitempty"`
	TokensOutput int64  `json:"tokens_output,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"` // HTTP status code for error responses
	Error        string `json:"error,omitempty"`
}

// WarmupPayload is sent from Swan Inference to pre-load a model
type WarmupPayload struct {
	ModelID    string `json:"model_id"`
	WarmupType string `json:"warmup_type"` // "load" or "inference"
}

// WarmupResponse is returned by provider after warmup
type WarmupResponse struct {
	RequestID  string `json:"request_id"`
	ModelID    string `json:"model_id"`
	Success    bool   `json:"success"`
	LoadTimeMs int64  `json:"load_time_ms,omitempty"`
	MemoryMB   int64  `json:"memory_mb,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ModelHealthUpdatePayload is sent to Swan Inference when model health changes
type ModelHealthUpdatePayload struct {
	NodeID      string            `json:"node_id"`                      // Local node ID (not the DB provider ID)
	ProviderID  string            `json:"provider_id,omitempty"`        // Deprecated: use NodeID
	ModelHealth map[string]string `json:"model_health"` // modelID -> health status ("healthy", "degraded", "unhealthy")
	Timestamp   int64             `json:"timestamp"`
}

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 1024 * 1024 // 1MB

	// Reconnection delay
	reconnectDelay = 5 * time.Second
)

// InferenceHandler handles non-streaming inference requests from Inference service
type InferenceHandler func(payload InferencePayload) (*InferenceResponse, error)

// StreamResult contains the final result of a streaming inference including token usage
type StreamResult struct {
	TokensInput  int64
	TokensOutput int64
	Error        error
}

// StreamingInferenceHandler handles streaming inference requests
// It receives a callback to send chunks back to Swan Inference and returns token usage
type StreamingInferenceHandler func(requestID string, payload InferencePayload, sendChunk func(chunk []byte, done bool) error) *StreamResult

// WarmupHandler handles model warmup requests
type WarmupHandler func(payload WarmupPayload) (*WarmupResponse, error)

// InferenceClient manages WebSocket connection to Swan Inference service
type InferenceClient struct {
	nodeID                    string // Local node ID (not the DB provider ID which is resolved via API key)
	workerAddr                string
	ownerAddr                 string
	apiKey                    string // Provider API key for authentication (sk-prov-*)
	models                    []string
	wsURL                     string
	serviceURL                string // HTTP API URL for status checks
	conn                      *websocket.Conn
	send                      chan []byte
	stopCh                    chan struct{}
	registered                bool
	inferenceHandler          InferenceHandler
	streamingInferenceHandler StreamingInferenceHandler
	warmupHandler             WarmupHandler
	modelHealthProvider       func() map[string]string           // Returns current model health for heartbeat
	modelMappingsProvider     func() map[string]ModelMapping     // Returns current model mappings for format/quantization
	mu                        sync.RWMutex
	writeMu                   sync.Mutex // Mutex for WebSocket writes to prevent concurrent writes

	// Cached hardware info (detected once at registration)
	hardware *HardwareInfo

	// Metrics tracking
	metrics      *InferenceMetrics
	gpuCollector *GPUMetricsCollector
}

// NewInferenceClient creates a new Inference client
func NewInferenceClient(nodeID, workerAddr, ownerAddr string) *InferenceClient {
	config := conf.GetConfig()

	// Allow env var override for dev mode
	wsURL := config.Inference.WebSocketURL
	if envURL := os.Getenv("INFERENCE_WS_URL"); envURL != "" {
		wsURL = envURL
		logs.GetLogger().Infof("Using INFERENCE_WS_URL env override: %s", wsURL)
	}

	// Get service URL for HTTP API calls (status checks)
	serviceURL := config.Inference.ServiceURL
	if serviceURL == "" {
		// Derive from WebSocket URL if not configured
		// e.g., wss://inference-ws-dev.swanchain.io -> https://inference-dev.swanchain.io
		serviceURL = wsURL
		serviceURL = strings.Replace(serviceURL, "wss://", "https://", 1)
		serviceURL = strings.Replace(serviceURL, "ws://", "http://", 1)
		serviceURL = strings.TrimSuffix(serviceURL, "/ws")
		serviceURL = strings.Replace(serviceURL, "-ws", "", 1) // Remove -ws from hostname
	}

	// Allow env var override for API key
	apiKey := config.Inference.ApiKey
	if envKey := os.Getenv("INFERENCE_API_KEY"); envKey != "" {
		apiKey = envKey
		logs.GetLogger().Infof("Using INFERENCE_API_KEY env override")
	}

	return &InferenceClient{
		nodeID:       nodeID,
		workerAddr:   workerAddr,
		ownerAddr:    ownerAddr,
		apiKey:       apiKey,
		models:       config.Inference.Models,
		wsURL:        wsURL,
		serviceURL:   serviceURL,
		send:         make(chan []byte, 256),
		stopCh:       make(chan struct{}),
		metrics:      NewInferenceMetrics(),
		gpuCollector: NewGPUMetricsCollector(),
	}
}

// SetInferenceHandler sets the handler for non-streaming inference requests
func (c *InferenceClient) SetInferenceHandler(handler InferenceHandler) {
	c.inferenceHandler = handler
}

// SetStreamingInferenceHandler sets the handler for streaming inference requests
func (c *InferenceClient) SetStreamingInferenceHandler(handler StreamingInferenceHandler) {
	c.streamingInferenceHandler = handler
}

// SetWarmupHandler sets the handler for model warmup requests
func (c *InferenceClient) SetWarmupHandler(handler WarmupHandler) {
	c.warmupHandler = handler
}

// SetModelHealthProvider sets the function that provides current model health for heartbeats
func (c *InferenceClient) SetModelHealthProvider(provider func() map[string]string) {
	c.modelHealthProvider = provider
}

// SetModelMappingsProvider sets the function that returns model mappings for format/quantization
func (c *InferenceClient) SetModelMappingsProvider(provider func() map[string]ModelMapping) {
	c.modelMappingsProvider = provider
}

// ProviderStatusResponse represents the status check response from Swan Inference
type ProviderStatusResponse struct {
	ProviderID      string   `json:"provider_id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	CanConnect      bool     `json:"can_connect"`
	APIKeyValid     bool     `json:"api_key_valid"`
	Message         string   `json:"message"`
	Warning         string   `json:"warning,omitempty"`
	NextSteps       []string `json:"next_steps,omitempty"`
	Step            int      `json:"step"`
	TotalSteps      int      `json:"total_steps"`
	StepLabel       string   `json:"step_label"`
	EarningsEnabled bool     `json:"earnings_enabled"`
}

// checkProviderStatus verifies the provider is approved before connecting
func (c *InferenceClient) checkProviderStatus() (*ProviderStatusResponse, error) {
	if c.apiKey == "" {
		return &ProviderStatusResponse{
			APIKeyValid: false,
			CanConnect:  false,
			Message:     "No API key configured",
			NextSteps: []string{
				"1. Sign up at https://inference.swanchain.io or via API",
				"2. Add your API key to config.toml [Inference] section",
				"3. Or set INFERENCE_API_KEY environment variable",
			},
		}, nil
	}

	// Use serviceURL for HTTP API calls
	statusURL := c.serviceURL + "/api/v1/provider/status"

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", statusURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := client.Do(req)
	if err != nil {
		// If we can't reach the status endpoint, log a warning but allow connection attempt
		logs.GetLogger().Warnf("Could not check provider status (will attempt connection anyway): %v", err)
		return nil, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read status response: %w", err)
	}

	var status ProviderStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("failed to parse status response: %w", err)
	}

	return &status, nil
}

// Start connects to Swan Inference and starts the client
func (c *InferenceClient) Start() error {
	logs.GetLogger().Infof("Connecting to Swan Inference at %s", c.wsURL)

	// Check provider status before connecting
	status, err := c.checkProviderStatus()
	if err != nil {
		logs.GetLogger().Warnf("Status check failed: %v (continuing anyway)", err)
	} else if status != nil {
		if !status.APIKeyValid {
			logs.GetLogger().Error("===========================================")
			logs.GetLogger().Error("PROVIDER AUTHENTICATION ERROR")
			logs.GetLogger().Error("===========================================")
			logs.GetLogger().Errorf("Message: %s", status.Message)
			if len(status.NextSteps) > 0 {
				logs.GetLogger().Error("Next steps:")
				for _, step := range status.NextSteps {
					logs.GetLogger().Errorf("  %s", step)
				}
			}
			logs.GetLogger().Error("===========================================")
			return fmt.Errorf("invalid API key: %s", status.Message)
		}

		if !status.CanConnect {
			logs.GetLogger().Warn("===========================================")
			logs.GetLogger().Warn("PROVIDER CANNOT CONNECT")
			logs.GetLogger().Warn("===========================================")
			logs.GetLogger().Warnf("Status: %s", status.Status)
			logs.GetLogger().Warnf("Message: %s", status.Message)
			if len(status.NextSteps) > 0 {
				logs.GetLogger().Warn("Next steps:")
				for _, step := range status.NextSteps {
					logs.GetLogger().Warnf("  %s", step)
				}
			}
			logs.GetLogger().Warn("===========================================")
			logs.GetLogger().Warn("Run 'computing-provider inference status' to check approval status")
			return fmt.Errorf("provider cannot connect: %s (status: %s)", status.Message, status.Status)
		}

		// Display informational warning for non-active providers that can connect
		if status.Status != "active" && status.CanConnect {
			logs.GetLogger().Warn("===========================================")
			logs.GetLogger().Warnf("PROVIDER STATUS: %s", strings.ToUpper(status.Status))
			logs.GetLogger().Warn("Earnings are DISABLED until admin approval.")
			if status.Status == "pending" {
				logs.GetLogger().Warn("Run 'computing-provider inference request-approval' to request approval.")
			}
			logs.GetLogger().Warn("===========================================")
		}

		logs.GetLogger().Infof("Provider status: %s (can_connect: %v, earnings: %v)", status.Status, status.CanConnect, status.EarningsEnabled)
	}

	if err := c.connect(); err != nil {
		return err
	}

	// Start read/write pumps
	go c.readPump()
	go c.writePump()
	go c.heartbeatPump()

	// Send registration
	if err := c.register(); err != nil {
		return err
	}

	return nil
}

// Stop gracefully shuts down the client
func (c *InferenceClient) Stop() {
	close(c.stopCh)
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *InferenceClient) connect() error {
	c.metrics.RecordConnectionState("connecting")

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			NextProtos: []string{"http/1.1"}, // Force HTTP/1.1 to enable WebSocket upgrade through Cloudflare
		},
	}

	conn, _, err := dialer.Dial(c.wsURL+"/ws", nil)
	if err != nil {
		c.metrics.RecordConnectionState("disconnected")
		return fmt.Errorf("failed to connect to Swan Inference: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.registered = false
	c.mu.Unlock()

	c.metrics.RecordConnectionState("connected")
	logs.GetLogger().Info("Connected to Swan Inference")
	return nil
}

func (c *InferenceClient) reconnect() {
	c.metrics.RecordConnectionState("reconnecting")
	c.metrics.RecordReconnect()

	for {
		select {
		case <-c.stopCh:
			return
		default:
			logs.GetLogger().Info("Attempting to reconnect to Swan Inference...")
			if err := c.connect(); err != nil {
				logs.GetLogger().Errorf("Reconnection failed: %v", err)
				time.Sleep(reconnectDelay)
				continue
			}

			// Re-register after reconnection
			if err := c.register(); err != nil {
				logs.GetLogger().Errorf("Re-registration failed: %v", err)
				time.Sleep(reconnectDelay)
				continue
			}

			// Restart pumps
			go c.readPump()
			go c.writePump()
			return
		}
	}
}

// detectGPUHardware detects GPU hardware information
func detectGPUHardware() *HardwareInfo {
	if runtime.GOOS == "darwin" {
		return detectAppleSiliconHardware()
	}
	return detectNvidiaHardware()
}

// detectNvidiaHardware detects NVIDIA GPU hardware using nvidia-smi
func detectNvidiaHardware() *HardwareInfo {
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total,driver_version", "--format=csv,noheader,nounits")
	output, err := cmd.Output()
	if err != nil {
		logs.GetLogger().Warnf("Failed to detect GPU hardware: %v", err)
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return nil
	}

	// Parse first GPU (primary)
	parts := strings.Split(lines[0], ", ")
	if len(parts) < 3 {
		logs.GetLogger().Warnf("Unexpected nvidia-smi output format: %s", lines[0])
		return nil
	}

	gpuModel := strings.TrimSpace(parts[0])
	vramStr := strings.TrimSpace(parts[1])
	driverVersion := strings.TrimSpace(parts[2])

	// Parse VRAM (in MiB from nvidia-smi)
	vramMiB, _ := strconv.Atoi(vramStr)
	vramGB := vramMiB / 1024
	if vramGB == 0 {
		vramGB = 1 // Minimum 1GB
	}

	// Convert GPU model to type (e.g., "NVIDIA GeForce RTX 3070" -> "RTX 3070")
	gpuType := gpuModel
	if strings.Contains(gpuModel, "GeForce") {
		gpuType = strings.Replace(gpuModel, "NVIDIA GeForce ", "", 1)
	} else if strings.Contains(gpuModel, "Tesla") {
		gpuType = strings.Replace(gpuModel, "NVIDIA ", "", 1)
		gpuType = strings.Replace(gpuType, "Tesla ", "", 1)
	} else if strings.Contains(gpuModel, "NVIDIA") {
		gpuType = strings.Replace(gpuModel, "NVIDIA ", "", 1)
	}

	// Get compute capability
	computeCap := ""
	cudaCmd := exec.Command("nvidia-smi", "--query-gpu=compute_cap", "--format=csv,noheader")
	if cudaOutput, err := cudaCmd.Output(); err == nil {
		computeCap = strings.TrimSpace(string(cudaOutput))
	}

	hardware := &HardwareInfo{
		GPUType:           gpuType,
		GPUModel:          gpuModel,
		VRAMGB:            vramGB,
		GPUCount:          len(lines),
		ComputeCapability: computeCap,
		DriverVersion:     driverVersion,
	}

	logs.GetLogger().Infof("Detected GPU hardware: %s (%dGB VRAM x%d)", gpuType, vramGB, len(lines))
	return hardware
}

// detectAppleSiliconHardware detects Apple Silicon GPU hardware
func detectAppleSiliconHardware() *HardwareInfo {
	// Detect chip model via sysctl
	gpuModel := "Apple Silicon"
	gpuType := "apple_silicon"

	chipCmd := exec.Command("sysctl", "-n", "machdep.cpu.brand_string")
	if chipOutput, err := chipCmd.Output(); err == nil {
		chip := strings.TrimSpace(string(chipOutput))
		if chip != "" {
			gpuModel = chip
			// Extract chip type for gpu_type (e.g., "Apple M3 Max" -> "m3_max")
			chipLower := strings.ToLower(chip)
			chipLower = strings.ReplaceAll(chipLower, "apple ", "")
			chipLower = strings.ReplaceAll(chipLower, " ", "_")
			gpuType = chipLower
		}
	}

	// Get total system memory (unified memory on Apple Silicon)
	vramGB := 0
	memCmd := exec.Command("sysctl", "-n", "hw.memsize")
	if memOutput, err := memCmd.Output(); err == nil {
		memBytes, _ := strconv.ParseInt(strings.TrimSpace(string(memOutput)), 10, 64)
		if memBytes > 0 {
			vramGB = int(memBytes / (1024 * 1024 * 1024))
		}
	}
	if vramGB == 0 {
		vramGB = 8 // Conservative fallback
	}

	hardware := &HardwareInfo{
		GPUType:  gpuType,
		GPUModel: gpuModel,
		VRAMGB:   vramGB,
		GPUCount: 1,
	}

	logs.GetLogger().Infof("Detected Apple Silicon hardware: %s (%dGB unified memory)", gpuModel, vramGB)
	return hardware
}

// detectServingEngine probes model endpoints to identify the inference engine.
// Returns one of: "sglang", "vllm", "ollama", "llamacpp", "tgi", "unknown".
func (c *InferenceClient) detectServingEngine() string {
	var mappings map[string]ModelMapping
	if c.modelMappingsProvider != nil {
		mappings = c.modelMappingsProvider()
	}
	if len(mappings) == 0 {
		return "unknown"
	}

	// Collect unique endpoints
	endpoints := make(map[string]bool)
	for _, m := range mappings {
		if m.Endpoint != "" {
			endpoints[m.Endpoint] = true
		}
	}

	httpClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Count detected engine types across endpoints
	engineCounts := make(map[string]int)
	for endpoint := range endpoints {
		engine := probeEndpointEngine(httpClient, endpoint)
		engineCounts[engine]++
	}

	// Return the most common engine
	bestEngine := "unknown"
	bestCount := 0
	for engine, count := range engineCounts {
		if count > bestCount && engine != "unknown" {
			bestEngine = engine
			bestCount = count
		}
	}

	logs.GetLogger().Infof("Detected serving engine: %s", bestEngine)
	return bestEngine
}

// probeEndpointEngine detects the inference engine type for a given endpoint.
func probeEndpointEngine(client *http.Client, endpoint string) string {
	// Try SGLang-specific endpoint
	if resp, err := client.Get(endpoint + "/get_server_args"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return "sglang"
		}
	}

	// Try vLLM version endpoint
	if resp, err := client.Get(endpoint + "/version"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var version struct {
				Version string `json:"version"`
			}
			if json.NewDecoder(resp.Body).Decode(&version) == nil {
				if strings.Contains(strings.ToLower(version.Version), "vllm") {
					return "vllm"
				}
			}
		}
	}

	// Try Ollama API
	if resp, err := client.Get(endpoint + "/api/tags"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return "ollama"
		}
	}

	return "unknown"
}

func (c *InferenceClient) register() error {
	// Detect GPU hardware and cache it for heartbeat messages
	hardware := detectGPUHardware()
	c.hardware = hardware

	// Detect serving engine from model endpoints
	if hardware != nil {
		hardware.ServingEngine = c.detectServingEngine()
	}

	// Load hash manifests for each model
	modelHashes := c.loadModelHashes()

	payload := RegisterPayload{
		NodeID:       c.nodeID,   // Local node ID for routing
		ProviderID:   c.nodeID,   // Deprecated: kept for backward compatibility
		WorkerAddr:   c.workerAddr,
		OwnerAddr:    c.ownerAddr,
		Token:        c.apiKey,   // API key for authentication (provider ID resolved from this)
		Models:       c.models,
		ModelHashes:  modelHashes,
		Capabilities: []string{"inference", "verification"},
		Hardware:     hardware,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal register payload: %w", err)
	}

	msg := Message{
		Type:      MsgTypeRegister,
		RequestID: uuid.New().String(),
		Payload:   payloadBytes,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	c.send <- msgBytes
	if len(c.apiKey) > 0 {
		displayLen := 15
		if len(c.apiKey) < 15 {
			displayLen = len(c.apiKey)
		}
		logs.GetLogger().Infof("Sent registration for provider %s (owner: %s) with models: %v and token: %s...", c.nodeID, c.ownerAddr, c.models, c.apiKey[:displayLen])
	} else {
		logs.GetLogger().Warnf("Sent registration for provider %s (owner: %s) with models: %v - NO TOKEN SET!", c.nodeID, c.ownerAddr, c.models)
	}
	return nil
}

func (c *InferenceClient) readPump() {
	defer func() {
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
		}
		c.mu.Unlock()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		select {
		case <-c.stopCh:
			return
		default:
			_, message, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					logs.GetLogger().Errorf("WebSocket read error: %v", err)
				}
				go c.reconnect()
				return
			}

			var msg Message
			if err := json.Unmarshal(message, &msg); err != nil {
				logs.GetLogger().Errorf("Failed to parse message: %v", err)
				continue
			}

			c.handleMessage(msg)
		}
	}
}

func (c *InferenceClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case message := <-c.send:
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()

			if conn == nil {
				continue
			}

			c.writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.WriteMessage(websocket.TextMessage, message)
			c.writeMu.Unlock()
			if err != nil {
				logs.GetLogger().Errorf("WebSocket write error: %v", err)
				return
			}
		case <-ticker.C:
			c.mu.RLock()
			conn := c.conn
			c.mu.RUnlock()

			if conn == nil {
				continue
			}

			c.writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			c.writeMu.Unlock()
			if err != nil {
				logs.GetLogger().Errorf("WebSocket ping error: %v", err)
				return
			}
		}
	}
}

func (c *InferenceClient) heartbeatPump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.RLock()
			registered := c.registered
			c.mu.RUnlock()

			if !registered {
				continue
			}

			c.sendHeartbeat()
		}
	}
}

func (c *InferenceClient) sendHeartbeat() {
	payload := HeartbeatPayload{
		NodeID:     c.nodeID,   // Local node ID for routing
		ProviderID: c.nodeID,   // Deprecated: kept for backward compatibility
		Timestamp:  time.Now().Unix(),
		Metrics:    c.collectMetrics(),
		Hardware:   c.hardware, // Include cached hardware info for periodic updates
	}

	// Include model health in heartbeat as backup for health update messages
	if c.modelHealthProvider != nil {
		payload.ModelHealth = c.modelHealthProvider()
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logs.GetLogger().Errorf("Failed to marshal heartbeat: %v", err)
		return
	}

	msg := Message{
		Type:      MsgTypeHeartbeat,
		RequestID: uuid.New().String(),
		Payload:   payloadBytes,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		logs.GetLogger().Errorf("Failed to marshal message: %v", err)
		return
	}

	c.send <- msgBytes
}

// SendModelHealthUpdate sends a model health update to Swan Inference
// This is called when model health status changes (healthy/degraded/unhealthy)
func (c *InferenceClient) SendModelHealthUpdate(modelHealth map[string]string) {
	c.mu.RLock()
	registered := c.registered
	c.mu.RUnlock()

	if !registered {
		logs.GetLogger().Debug("Not sending health update: not registered")
		return
	}

	if len(modelHealth) == 0 {
		return
	}

	payload := ModelHealthUpdatePayload{
		NodeID:      c.nodeID,   // Local node ID for routing
		ProviderID:  c.nodeID,   // Deprecated: kept for backward compatibility
		ModelHealth: modelHealth,
		Timestamp:   time.Now().Unix(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logs.GetLogger().Errorf("Failed to marshal health update: %v", err)
		return
	}

	msg := Message{
		Type:      MsgTypeModelHealthUpdate,
		RequestID: uuid.New().String(),
		Payload:   payloadBytes,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		logs.GetLogger().Errorf("Failed to marshal message: %v", err)
		return
	}

	select {
	case c.send <- msgBytes:
		logs.GetLogger().Infof("Sent model health update: %v", modelHealth)
	default:
		logs.GetLogger().Warn("Send buffer full, dropping health update")
	}
}

func (c *InferenceClient) collectMetrics() map[string]float64 {
	metrics := make(map[string]float64)

	// Collect real GPU metrics
	gpuMetrics := c.gpuCollector.CollectGPUMetrics()
	if len(gpuMetrics) > 0 {
		// Calculate average GPU utilization across all GPUs
		var totalUtilization, totalMemoryUsage float64
		for _, gpu := range gpuMetrics {
			totalUtilization += gpu.UtilizationPct
			totalMemoryUsage += gpu.MemoryUsagePct
		}
		metrics["gpu_utilization"] = totalUtilization / float64(len(gpuMetrics))
		metrics["memory_utilization"] = totalMemoryUsage / float64(len(gpuMetrics))

		// Also include per-GPU metrics
		for _, gpu := range gpuMetrics {
			prefix := fmt.Sprintf("gpu_%d_", gpu.Index)
			metrics[prefix+"utilization"] = gpu.UtilizationPct
			metrics[prefix+"memory_used_mb"] = gpu.MemoryUsedMB
			metrics[prefix+"memory_total_mb"] = gpu.MemoryTotalMB
			metrics[prefix+"temperature_c"] = gpu.TemperatureC
			metrics[prefix+"power_draw_w"] = gpu.PowerDrawW
		}

		// Update the internal metrics tracker
		c.metrics.UpdateGPUMetrics(gpuMetrics)
	} else {
		metrics["gpu_utilization"] = 0.0
		metrics["memory_utilization"] = 0.0
	}

	// Add request-level metrics from internal tracker
	snapshot := c.metrics.GetSnapshot()
	metrics["active_requests"] = float64(snapshot.ActiveRequests)
	metrics["total_requests"] = float64(snapshot.TotalRequests)
	metrics["requests_per_minute"] = snapshot.RequestsPerMinute
	metrics["avg_latency_ms"] = snapshot.AvgLatencyMs
	metrics["tokens_per_second"] = snapshot.TokensPerSecond

	return metrics
}

func (c *InferenceClient) handleMessage(msg Message) {
	switch msg.Type {
	case MsgTypeAck:
		var payload AckPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			logs.GetLogger().Errorf("Failed to parse ack payload: %v", err)
			return
		}
		if payload.Success {
			c.mu.Lock()
			c.registered = true
			c.mu.Unlock()
			logs.GetLogger().Infof("Registration successful: %s", payload.Message)
		} else {
			logs.GetLogger().Warnf("Received failed ack: %s", payload.Message)
		}

	case MsgTypeInference:
		var payload InferencePayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			logs.GetLogger().Errorf("Failed to parse inference payload: %v", err)
			c.sendError(msg.RequestID, 400, "invalid inference payload")
			return
		}
		go c.handleInference(msg.RequestID, payload)

	case MsgTypeVerify:
		var payload VerifyPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			logs.GetLogger().Errorf("Failed to parse verify payload: %v", err)
			c.sendError(msg.RequestID, 400, "invalid verify payload")
			return
		}
		go c.handleVerification(msg.RequestID, payload)

	case MsgTypeError:
		var payload ErrorPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			logs.GetLogger().Errorf("Failed to parse error payload: %v", err)
			return
		}
		logs.GetLogger().Errorf("Received error from Swan Inference: [%d] %s", payload.Code, payload.Message)

	case MsgTypeWarmup:
		var payload WarmupPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			logs.GetLogger().Errorf("Failed to parse warmup payload: %v", err)
			c.sendError(msg.RequestID, 400, "invalid warmup payload")
			return
		}
		go c.handleWarmup(msg.RequestID, payload)

	default:
		logs.GetLogger().Warnf("Unknown message type: %s", msg.Type)
	}
}

func (c *InferenceClient) handleInference(requestID string, payload InferencePayload) {
	startTime := time.Now()
	logs.GetLogger().Infof("Processing inference request %s for model %s (stream=%v)", requestID, payload.ModelID, payload.Stream)

	// Record request start for metrics
	c.metrics.RecordRequestStart(payload.ModelID, payload.Stream)

	// Handle streaming inference
	if payload.Stream {
		c.handleStreamingInference(requestID, payload, startTime)
		return
	}

	// Handle non-streaming inference
	var response *InferenceResponse
	var err error

	if c.inferenceHandler != nil {
		response, err = c.inferenceHandler(payload)
	} else {
		err = fmt.Errorf("no inference handler configured")
	}

	latency := time.Since(startTime).Milliseconds()
	latencyFloat := float64(latency)

	if err != nil {
		// Extract HTTP status code from ModelServerError if available
		statusCode := 502 // Default to Bad Gateway
		var modelErr *ModelServerError
		if errors.As(err, &modelErr) {
			statusCode = modelErr.StatusCode
		} else if strings.Contains(err.Error(), "not deployed") {
			statusCode = 404
		}

		// Record failed request
		c.metrics.RecordRequestEnd(payload.ModelID, latencyFloat, 0, 0, false, err.Error())
		response = &InferenceResponse{
			RequestID:  requestID,
			Error:      err.Error(),
			StatusCode: statusCode,
			Latency:    latency,
		}
	} else {
		// Extract token counts from response if available
		tokensIn, tokensOut := extractTokenCounts(response.Response)
		c.metrics.RecordRequestEnd(payload.ModelID, latencyFloat, tokensIn, tokensOut, true, "")
		response.RequestID = requestID
		response.Latency = latency
	}

	c.sendInferenceResponse(response)
}

func (c *InferenceClient) handleStreamingInference(requestID string, payload InferencePayload, startTime time.Time) {
	if c.streamingInferenceHandler == nil {
		logs.GetLogger().Errorf("No streaming inference handler configured")
		c.metrics.RecordRequestEnd(payload.ModelID, 0, 0, 0, false, "streaming not supported")
		c.sendError(requestID, 501, "streaming not supported")
		return
	}

	// Create a callback for sending chunks
	sendChunk := func(chunk []byte, done bool) error {
		return c.sendStreamChunk(requestID, chunk, done)
	}

	// Execute streaming inference
	result := c.streamingInferenceHandler(requestID, payload, sendChunk)

	latency := time.Since(startTime).Milliseconds()
	latencyFloat := float64(latency)

	// Send stream end message with token usage
	var tokensIn, tokensOut int64
	var err error
	if result != nil {
		tokensIn = result.TokensInput
		tokensOut = result.TokensOutput
		err = result.Error
	}

	// Extract HTTP status code from ModelServerError if available
	statusCode := 0
	if err != nil {
		statusCode = 502 // Default to Bad Gateway for errors
		var modelErr *ModelServerError
		if errors.As(err, &modelErr) {
			statusCode = modelErr.StatusCode
		} else if strings.Contains(err.Error(), "not deployed") {
			statusCode = 404
		}
	}

	// Record metrics for streaming request
	if err != nil {
		c.metrics.RecordRequestEnd(payload.ModelID, latencyFloat, int(tokensIn), int(tokensOut), false, err.Error())
	} else {
		c.metrics.RecordRequestEnd(payload.ModelID, latencyFloat, int(tokensIn), int(tokensOut), true, "")
	}

	c.sendStreamEnd(requestID, latency, tokensIn, tokensOut, statusCode, err)
}

// sendStreamChunk sends a streaming chunk to Swan Inference
func (c *InferenceClient) sendStreamChunk(requestID string, chunk []byte, done bool) error {
	payload := StreamChunkPayload{
		RequestID: requestID,
		Chunk:     chunk,
		Done:      done,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal stream chunk: %w", err)
	}

	msg := Message{
		Type:      MsgTypeStreamChunk,
		RequestID: requestID,
		Payload:   payloadBytes,
	}

	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	select {
	case c.send <- msgBytes:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("send buffer full: timed out after 5s")
	case <-c.stopCh:
		return fmt.Errorf("client stopped")
	}
}

// sendStreamEnd sends the end of stream message with token usage
func (c *InferenceClient) sendStreamEnd(requestID string, latencyMs int64, tokensIn, tokensOut int64, statusCode int, err error) {
	payload := StreamEndPayload{
		RequestID:    requestID,
		Latency:      latencyMs,
		TokensInput:  tokensIn,
		TokensOutput: tokensOut,
		StatusCode:   statusCode,
	}
	if err != nil {
		payload.Error = err.Error()
	}

	payloadBytes, _ := json.Marshal(payload)
	msg := Message{
		Type:      MsgTypeStreamEnd,
		RequestID: requestID,
		Payload:   payloadBytes,
	}

	msgBytes, _ := json.Marshal(msg)
	select {
	case c.send <- msgBytes:
	case <-time.After(10 * time.Second):
		logs.GetLogger().Errorf("Failed to send stream_end for %s: timed out after 10s", requestID)
	case <-c.stopCh:
		logs.GetLogger().Warnf("Client stopped while sending stream_end for %s", requestID)
	}
}

func (c *InferenceClient) handleVerification(requestID string, payload VerifyPayload) {
	logs.GetLogger().Infof("Processing verification request %s for model %s (type: %s)", requestID, payload.ModelID, payload.ChallengeType)

	switch payload.ChallengeType {
	case "fingerprint":
		c.handleFingerprintChallenge(requestID, payload)
	case "deterministic":
		c.handleDeterministicChallenge(requestID, payload)
	default:
		logs.GetLogger().Warnf("Unsupported challenge type: %s", payload.ChallengeType)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "unsupported challenge type: "+payload.ChallengeType)
	}
}

// FingerprintChallengeData represents the fingerprint challenge from the server
type FingerprintChallengeData struct {
	Files []FingerprintChallengeFile `json:"files"`
}

// FingerprintChallengeFile is a single file in a fingerprint challenge
type FingerprintChallengeFile struct {
	Filename     string `json:"filename"`
	ExpectedHash string `json:"expected_hash"`
}

// FingerprintResponseData is the response sent back for a fingerprint challenge
type FingerprintResponseData struct {
	Files []FingerprintResponseFile `json:"files"`
}

// FingerprintResponseFile is a single file result in a fingerprint response
type FingerprintResponseFile struct {
	Filename string `json:"filename"`
	Hash     string `json:"hash"`
	Status   string `json:"status"` // "pass", "fail", "missing"
}

// DeterministicChallengeData represents a deterministic inference challenge from the server
type DeterministicChallengeData struct {
	Prompt    string `json:"prompt"`
	Seed      int    `json:"seed"`
	MaxTokens int    `json:"max_tokens"`
}

// DeterministicResponseData is the response sent back for a deterministic challenge
type DeterministicResponseData struct {
	Tokens []string `json:"tokens"`
	Text   string   `json:"text"`
}

func (c *InferenceClient) handleFingerprintChallenge(requestID string, payload VerifyPayload) {
	// Parse the challenge
	var challenge FingerprintChallengeData
	if err := json.Unmarshal(payload.Challenge, &challenge); err != nil {
		logs.GetLogger().Errorf("Failed to parse fingerprint challenge: %v", err)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "failed to parse challenge")
		return
	}

	// Determine the local model directory
	modelDir := c.getModelDir(payload.ModelID)

	// Try to load existing hash manifest
	manifest, err := models.LoadHashManifest(modelDir)
	if err != nil {
		logs.GetLogger().Warnf("Failed to load hash manifest for %s: %v", payload.ModelID, err)
	}

	// Build a hash lookup from manifest
	manifestHashes := make(map[string]string)
	if manifest != nil {
		for _, f := range manifest.Files {
			manifestHashes[f.Filename] = f.Hash
		}
	}

	// Verify each challenged file
	response := FingerprintResponseData{
		Files: make([]FingerprintResponseFile, 0, len(challenge.Files)),
	}

	for _, cf := range challenge.Files {
		result := FingerprintResponseFile{
			Filename: cf.Filename,
		}

		// First try manifest lookup (fast)
		if hash, ok := manifestHashes[cf.Filename]; ok {
			result.Hash = hash
			if hash == cf.ExpectedHash {
				result.Status = "pass"
			} else {
				result.Status = "fail"
			}
		} else {
			// No manifest entry — file may not be locally accessible
			result.Status = "missing"
		}

		response.Files = append(response.Files, result)
	}

	// Determine overall success
	allPass := true
	for _, f := range response.Files {
		if f.Status != "pass" {
			allPass = false
			break
		}
	}

	responseData, _ := json.Marshal(response)
	c.sendVerifyResponse(requestID, payload.ChallengeID, allPass, responseData, "")
	logs.GetLogger().Infof("Fingerprint verification %s completed: success=%v", requestID, allPass)
}

func (c *InferenceClient) handleDeterministicChallenge(requestID string, payload VerifyPayload) {
	// Parse the challenge
	var challenge DeterministicChallengeData
	if err := json.Unmarshal(payload.Challenge, &challenge); err != nil {
		logs.GetLogger().Errorf("Failed to parse deterministic challenge: %v", err)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "failed to parse challenge")
		return
	}

	if c.inferenceHandler == nil {
		logs.GetLogger().Errorf("No inference handler configured for deterministic challenge %s", requestID)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "no inference handler configured")
		return
	}

	// Build OpenAI-compatible chat completion request with deterministic settings
	reqBody := map[string]interface{}{
		"model": payload.ModelID,
		"messages": []map[string]string{
			{"role": "user", "content": challenge.Prompt},
		},
		"temperature":  0,
		"seed":         challenge.Seed,
		"max_tokens":   challenge.MaxTokens,
		"logprobs":     true,
		"top_logprobs": 1,
		"stream":       false,
	}

	requestJSON, err := json.Marshal(reqBody)
	if err != nil {
		logs.GetLogger().Errorf("Failed to marshal deterministic request: %v", err)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "failed to build request")
		return
	}

	// Reuse the existing inference path
	inferPayload := InferencePayload{
		ModelID: payload.ModelID,
		Request: json.RawMessage(requestJSON),
		Stream:  false,
	}

	resp, err := c.inferenceHandler(inferPayload)
	if err != nil {
		logs.GetLogger().Errorf("Deterministic inference failed for %s: %v", requestID, err)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "inference failed: "+err.Error())
		return
	}

	// Parse the OpenAI response to extract tokens and text
	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Logprobs *struct {
				Content []struct {
					Token string `json:"token"`
				} `json:"content"`
			} `json:"logprobs"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(resp.Response, &openAIResp); err != nil {
		logs.GetLogger().Errorf("Failed to parse inference response for deterministic challenge %s: %v", requestID, err)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "failed to parse inference response")
		return
	}

	if len(openAIResp.Choices) == 0 {
		logs.GetLogger().Errorf("No choices in inference response for deterministic challenge %s", requestID)
		c.sendVerifyResponse(requestID, payload.ChallengeID, false, nil, "no choices in inference response")
		return
	}

	text := openAIResp.Choices[0].Message.Content

	// Extract tokens: prefer logprobs for exact token boundaries, fall back to word-split
	var tokens []string
	if openAIResp.Choices[0].Logprobs != nil && len(openAIResp.Choices[0].Logprobs.Content) > 0 {
		for _, lp := range openAIResp.Choices[0].Logprobs.Content {
			tokens = append(tokens, lp.Token)
		}
	} else {
		tokens = strings.Fields(text)
	}

	result := DeterministicResponseData{
		Tokens: tokens,
		Text:   text,
	}

	responseData, _ := json.Marshal(result)
	c.sendVerifyResponse(requestID, payload.ChallengeID, true, responseData, "")
	logs.GetLogger().Infof("Deterministic verification %s completed: %d tokens extracted", requestID, len(tokens))
}

func (c *InferenceClient) sendVerifyResponse(requestID, challengeID string, success bool, responseData json.RawMessage, errMsg string) {
	payload := VerifyResponsePayload{
		ChallengeID: challengeID,
		Success:     success,
		Response:    responseData,
		Error:       errMsg,
	}

	payloadBytes, _ := json.Marshal(payload)
	msg := Message{
		Type:      MsgTypeVerify,
		RequestID: requestID,
		Payload:   payloadBytes,
	}

	msgBytes, _ := json.Marshal(msg)
	select {
	case c.send <- msgBytes:
	default:
		logs.GetLogger().Warn("Send buffer full, dropping verify response")
	}
}

// loadModelHashes loads hash manifests for all configured models
func (c *InferenceClient) loadModelHashes() []ModelInfo {
	hashes := make([]ModelInfo, 0, len(c.models))

	// Get model mappings for format/quantization
	var mappings map[string]ModelMapping
	if c.modelMappingsProvider != nil {
		mappings = c.modelMappingsProvider()
	}

	for _, modelID := range c.models {
		info := ModelInfo{
			ModelID: modelID,
		}

		// Populate format/quantization from model mappings
		if mapping, ok := mappings[modelID]; ok {
			info.Format = mapping.Format
			info.Quantization = mapping.Quantization
		}

		modelDir := c.getModelDir(modelID)
		manifest, err := models.LoadHashManifest(modelDir)
		if err != nil {
			logs.GetLogger().Warnf("Failed to load hash manifest for %s: %v", modelID, err)
			hashes = append(hashes, info)
			continue
		}

		if manifest != nil {
			info.WeightHash = manifest.CompositeHash
			info.HashAlgo = manifest.Algorithm
			logs.GetLogger().Infof("Loaded hash manifest for %s: %s", modelID, manifest.CompositeHash[:16]+"...")
		}

		hashes = append(hashes, info)
	}

	return hashes
}

// getModelDir returns the local directory for a model's weight files
func (c *InferenceClient) getModelDir(modelID string) string {
	home, err := homedir.Dir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".swan", "models", modelID)
}

func (c *InferenceClient) handleWarmup(requestID string, payload WarmupPayload) {
	startTime := time.Now()
	logs.GetLogger().Infof("Processing warmup request %s for model %s (type: %s)", requestID, payload.ModelID, payload.WarmupType)

	var response *WarmupResponse
	var err error

	if c.warmupHandler != nil {
		response, err = c.warmupHandler(payload)
	} else {
		err = fmt.Errorf("no warmup handler configured")
	}

	loadTime := time.Since(startTime).Milliseconds()

	if err != nil {
		logs.GetLogger().Warnf("Warmup failed for model %s: %v", payload.ModelID, err)
		response = &WarmupResponse{
			RequestID:  requestID,
			ModelID:    payload.ModelID,
			Success:    false,
			Error:      err.Error(),
			LoadTimeMs: loadTime,
		}
	} else {
		response.RequestID = requestID
		response.LoadTimeMs = loadTime
		logs.GetLogger().Infof("Warmup successful for model %s in %dms", payload.ModelID, loadTime)
	}

	c.sendWarmupResponse(response)
}

func (c *InferenceClient) sendWarmupResponse(response *WarmupResponse) {
	payloadBytes, err := json.Marshal(response)
	if err != nil {
		logs.GetLogger().Errorf("Failed to marshal warmup response: %v", err)
		return
	}

	msg := Message{
		Type:      MsgTypeAck,
		RequestID: response.RequestID,
		Payload:   payloadBytes,
	}

	msgBytes, _ := json.Marshal(msg)
	c.send <- msgBytes
}

func (c *InferenceClient) sendAck(requestID string, success bool, message string) {
	payload := AckPayload{
		RequestID: requestID,
		Success:   success,
		Message:   message,
	}

	payloadBytes, _ := json.Marshal(payload)
	msg := Message{
		Type:      MsgTypeAck,
		RequestID: requestID,
		Payload:   payloadBytes,
	}

	msgBytes, _ := json.Marshal(msg)
	c.send <- msgBytes
}

func (c *InferenceClient) sendError(requestID string, code int, message string) {
	payload := ErrorPayload{
		RequestID: requestID,
		Code:      code,
		Message:   message,
	}

	payloadBytes, _ := json.Marshal(payload)
	msg := Message{
		Type:      MsgTypeError,
		RequestID: requestID,
		Payload:   payloadBytes,
	}

	msgBytes, _ := json.Marshal(msg)
	c.send <- msgBytes
}

func (c *InferenceClient) sendInferenceResponse(response *InferenceResponse) {
	payloadBytes, err := json.Marshal(response)
	if err != nil {
		logs.GetLogger().Errorf("Failed to marshal inference response: %v", err)
		return
	}

	msg := Message{
		Type:      MsgTypeAck,
		RequestID: response.RequestID,
		Payload:   payloadBytes,
	}

	msgBytes, _ := json.Marshal(msg)
	c.send <- msgBytes
}

// IsConnected returns whether the client is connected and registered
func (c *InferenceClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil && c.registered
}

// GetNodeID returns the local node ID
func (c *InferenceClient) GetNodeID() string {
	return c.nodeID
}

// GetMetrics returns a snapshot of the current metrics
func (c *InferenceClient) GetMetrics() InferenceMetrics {
	return c.metrics.GetSnapshot()
}

// GetMetricsPrometheus returns metrics in Prometheus text format
func (c *InferenceClient) GetMetricsPrometheus() string {
	return c.metrics.GetPrometheusMetrics()
}

// extractTokenCounts attempts to extract token usage from inference response
// Returns (tokensIn, tokensOut) - defaults to 0 if not found
func extractTokenCounts(response json.RawMessage) (int, int) {
	if len(response) == 0 {
		return 0, 0
	}

	// Try to extract from OpenAI-compatible response format
	var openAIResponse struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(response, &openAIResponse); err == nil {
		if openAIResponse.Usage.PromptTokens > 0 || openAIResponse.Usage.CompletionTokens > 0 {
			return openAIResponse.Usage.PromptTokens, openAIResponse.Usage.CompletionTokens
		}
	}

	// Try vLLM/SGLang format
	var vllmResponse struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(response, &vllmResponse); err == nil {
		if vllmResponse.Usage.PromptTokens > 0 || vllmResponse.Usage.CompletionTokens > 0 {
			return vllmResponse.Usage.PromptTokens, vllmResponse.Usage.CompletionTokens
		}
	}

	return 0, 0
}
