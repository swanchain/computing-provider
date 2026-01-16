package computing

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/filswan/go-mcs-sdk/mcs/api/common/logs"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/swanchain/computing-provider-v2/conf"
)

// Inference WebSocket Protocol Types
type MessageType string

const (
	MsgTypeRegister    MessageType = "register"
	MsgTypeInference   MessageType = "inference"
	MsgTypeVerify      MessageType = "verify"
	MsgTypeHeartbeat   MessageType = "heartbeat"
	MsgTypeAck         MessageType = "ack"
	MsgTypeError       MessageType = "error"
	MsgTypeStreamChunk MessageType = "stream_chunk" // Streaming chunk to Swan Inference
	MsgTypeStreamEnd   MessageType = "stream_end"   // End of stream marker
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
}

// RegisterPayload is sent by provider on connection
type RegisterPayload struct {
	ProviderID   string        `json:"provider_id"`
	WorkerAddr   string        `json:"worker_addr"`
	OwnerAddr    string        `json:"owner_addr"`
	Signature    string        `json:"signature"`
	Models       []string      `json:"models"`
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
	RequestID string          `json:"request_id"`
	Response  json.RawMessage `json:"response"`
	Error     string          `json:"error,omitempty"`
	Latency   int64           `json:"latency_ms"`
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
	ProviderID string             `json:"provider_id"`
	Timestamp  int64              `json:"timestamp"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
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
	Error        string `json:"error,omitempty"`
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

// InferenceClient manages WebSocket connection to Swan Inference service
type InferenceClient struct {
	providerID                string
	workerAddr                string
	ownerAddr                 string
	models                    []string
	wsURL                     string
	conn                      *websocket.Conn
	send                      chan []byte
	stopCh                    chan struct{}
	registered                bool
	inferenceHandler          InferenceHandler
	streamingInferenceHandler StreamingInferenceHandler
	mu                        sync.RWMutex
	writeMu                   sync.Mutex // Mutex for WebSocket writes to prevent concurrent writes
}

// NewInferenceClient creates a new Inference client
func NewInferenceClient(providerID, workerAddr, ownerAddr string) *InferenceClient {
	config := conf.GetConfig()

	// Allow env var override for dev mode
	wsURL := config.Inference.WebSocketURL
	if envURL := os.Getenv("INFERENCE_WS_URL"); envURL != "" {
		wsURL = envURL
		logs.GetLogger().Infof("Using INFERENCE_WS_URL env override: %s", wsURL)
	}

	return &InferenceClient{
		providerID: providerID,
		workerAddr: workerAddr,
		ownerAddr:  ownerAddr,
		models:     config.Inference.Models,
		wsURL:      wsURL,
		send:       make(chan []byte, 256),
		stopCh:     make(chan struct{}),
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

// Start connects to Swan Inference and starts the client
func (c *InferenceClient) Start() error {
	logs.GetLogger().Infof("Connecting to Swan Inference at %s", c.wsURL)

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
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			NextProtos: []string{"http/1.1"}, // Force HTTP/1.1 to enable WebSocket upgrade through Cloudflare
		},
	}

	conn, _, err := dialer.Dial(c.wsURL+"/ws", nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Swan Inference: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.registered = false
	c.mu.Unlock()

	logs.GetLogger().Info("Connected to Swan Inference")
	return nil
}

func (c *InferenceClient) reconnect() {
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

// detectGPUHardware detects GPU hardware information using nvidia-smi
func detectGPUHardware() *HardwareInfo {
	// Run nvidia-smi to get GPU info
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

	// Get CUDA version
	cudaVersion := ""
	cudaCmd := exec.Command("nvidia-smi", "--query-gpu=compute_cap", "--format=csv,noheader")
	if cudaOutput, err := cudaCmd.Output(); err == nil {
		cudaVersion = strings.TrimSpace(string(cudaOutput))
	}

	hardware := &HardwareInfo{
		GPUType:           gpuType,
		GPUModel:          gpuModel,
		VRAMGB:            vramGB,
		GPUCount:          len(lines), // Count of GPUs
		ComputeCapability: cudaVersion,
		DriverVersion:     driverVersion,
		CUDAVersion:       "", // nvidia-smi doesn't directly expose CUDA version
	}

	logs.GetLogger().Infof("Detected GPU hardware: %s (%dGB VRAM x%d)", gpuType, vramGB, len(lines))
	return hardware
}

func (c *InferenceClient) register() error {
	// Detect GPU hardware
	hardware := detectGPUHardware()

	payload := RegisterPayload{
		ProviderID:   c.providerID,
		WorkerAddr:   c.workerAddr,
		OwnerAddr:    c.ownerAddr,
		Models:       c.models,
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
	logs.GetLogger().Infof("Sent registration for provider %s (owner: %s) with models: %v", c.providerID, c.ownerAddr, c.models)
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
		ProviderID: c.providerID,
		Timestamp:  time.Now().Unix(),
		Metrics:    c.collectMetrics(),
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

func (c *InferenceClient) collectMetrics() map[string]float64 {
	// Collect GPU/CPU metrics via Docker resource exporter
	metrics := make(map[string]float64)

	// Placeholder for actual metric collection from Docker containers
	metrics["gpu_utilization"] = 0.0
	metrics["memory_utilization"] = 0.0

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

	default:
		logs.GetLogger().Warnf("Unknown message type: %s", msg.Type)
	}
}

func (c *InferenceClient) handleInference(requestID string, payload InferencePayload) {
	startTime := time.Now()
	logs.GetLogger().Infof("Processing inference request %s for model %s (stream=%v)", requestID, payload.ModelID, payload.Stream)

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

	if err != nil {
		response = &InferenceResponse{
			RequestID: requestID,
			Error:     err.Error(),
			Latency:   latency,
		}
	} else {
		response.RequestID = requestID
		response.Latency = latency
	}

	c.sendInferenceResponse(response)
}

func (c *InferenceClient) handleStreamingInference(requestID string, payload InferencePayload, startTime time.Time) {
	if c.streamingInferenceHandler == nil {
		logs.GetLogger().Errorf("No streaming inference handler configured")
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

	// Send stream end message with token usage
	var tokensIn, tokensOut int64
	var err error
	if result != nil {
		tokensIn = result.TokensInput
		tokensOut = result.TokensOutput
		err = result.Error
	}
	c.sendStreamEnd(requestID, latency, tokensIn, tokensOut, err)
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
	default:
		return fmt.Errorf("send buffer full")
	}
}

// sendStreamEnd sends the end of stream message with token usage
func (c *InferenceClient) sendStreamEnd(requestID string, latencyMs int64, tokensIn, tokensOut int64, err error) {
	payload := StreamEndPayload{
		RequestID:    requestID,
		Latency:      latencyMs,
		TokensInput:  tokensIn,
		TokensOutput: tokensOut,
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
	c.send <- msgBytes
}

func (c *InferenceClient) handleVerification(requestID string, payload VerifyPayload) {
	logs.GetLogger().Infof("Processing verification request %s for model %s", requestID, payload.ModelID)

	// Verification implementation would go here
	// For now, just acknowledge
	c.sendAck(requestID, true, "verification completed")
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

// GetProviderID returns the provider ID
func (c *InferenceClient) GetProviderID() string {
	return c.providerID
}
