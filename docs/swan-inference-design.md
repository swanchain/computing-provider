# Swan Inference Integration Design Document

## Executive Summary

This document outlines the design for integrating the Computing Provider (CP) with Swan Inference, the decentralized inference marketplace. Swan Inference is a centralized coordination layer that connects AI model consumers with GPU providers through WebSocket-based real-time communication.

The integration enables Computing Providers to:
1. Register as inference providers in the Swan Inference marketplace
2. Receive and execute inference requests via WebSocket
3. Report usage metrics for billing and settlement
4. Participate in model verification challenges

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                       Swan Inference                                │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                  │
│  │   REST API  │  │  WebSocket  │  │   MySQL DB  │                  │
│  │   :8080     │  │    Hub      │  │             │                  │
│  └──────┬──────┘  └──────┬──────┘  └─────────────┘                  │
└─────────┼────────────────┼──────────────────────────────────────────┘
          │                │
          │ HTTP           │ WS (persistent)
          │                │
┌─────────┴────────────────┴──────────────────────────────────────────┐
│                    Computing Provider (Inference Client)             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                  │
│  │ Inference   │  │   Model     │  │   Docker    │                  │
│  │  Service    │  │  Executor   │  │   Service   │                  │
│  └─────────────┘  └─────────────┘  └─────────────┘                  │
└─────────────────────────────────────────────────────────────────────┘
```

## Swan Inference Components (Backend)

### 1. Data Entities

| Entity | Purpose | Key Fields |
|--------|---------|------------|
| `ModelEntity` | AI models in catalog | id, slug, category (llm/image/audio/embedding/multimodal), specs, requirements |
| `SKUEntity` | Hardware/pricing configs | id, gpu_type, vram, pricing |
| `ProviderEntity` | Registered GPU providers | id, owner_address, worker_address, hardware, status, online |
| `EndpointEntity` | Consumer inference endpoints | id, user_id, model_id, type (dedicated/shared/serverless), status |
| `UsageRecordEntity` | Metered billing records | endpoint_id, provider_id, tokens, gpu_seconds, cost |
| `SettlementBatchEntity` | On-chain settlement batches | id, provider_id, amount, tx_hash |

### 2. WebSocket Protocol

Swan Inference uses JSON messages over WebSocket for real-time communication:

**Message Types:**
- `register` - Provider announces availability with signed worker address
- `inference` - Service routes inference requests to providers
- `warmup` - Pre-load model into GPU memory (reduces cold start latency)
- `verify` - Model verification challenges (fingerprint, logprob, timing)
- `heartbeat` - Liveness checks with optional metrics
- `ack` - Acknowledgment responses
- `error` - Error responses
- `stream_chunk` - Streaming chunk from provider (for streaming inference)
- `stream_end` - End of stream marker with latency stats

**Register Payload (Provider → Swan Inference):**
```json
{
  "type": "register",
  "payload": {
    "provider_id": "uuid",
    "worker_addr": "0x...",
    "token": "sk-prov-xxx",
    "models": ["model_id_1", "model_id_2"],
    "capabilities": ["llm", "embedding"],
    "hardware": {
      "gpu_type": "rtx_4090",
      "vram_gb": 24,
      "gpu_count": 1
    }
  }
}
```

**Inference Payload (Swan Inference → Provider):**
```json
{
  "type": "inference",
  "request_id": "uuid",
  "payload": {
    "endpoint_id": "uuid",
    "model_id": "model_id",
    "request": { /* OpenAI-compatible request */ },
    "stream": true  // Whether to stream the response
  }
}
```

**Stream Chunk Payload (Provider → Swan Inference):**
```json
{
  "type": "stream_chunk",
  "request_id": "uuid",
  "payload": {
    "request_id": "uuid",
    "chunk": { /* OpenAI-compatible SSE chunk data */ },
    "done": false  // True when stream is complete
  }
}
```

**Stream End Payload (Provider → Swan Inference):**
```json
{
  "type": "stream_end",
  "request_id": "uuid",
  "payload": {
    "request_id": "uuid",
    "latency_ms": 1234,
    "error": ""  // Empty if successful
  }
}
```

**Warmup Payload (Swan Inference → Provider):**
```json
{
  "type": "warmup",
  "request_id": "uuid",
  "payload": {
    "model_id": "llama-3.2-3b",
    "warmup_type": "load"
  }
}
```

**Warmup Response (Provider → Swan Inference):**
```json
{
  "type": "ack",
  "request_id": "uuid",
  "payload": {
    "request_id": "uuid",
    "model_id": "llama-3.2-3b",
    "success": true,
    "load_time_ms": 1500
  }
}
```

**Heartbeat Payload (Provider → Swan Inference):**
```json
{
  "type": "heartbeat",
  "payload": {
    "provider_id": "uuid",
    "timestamp": 1234567890,
    "metrics": {
      "gpu_utilization": 0.75,
      "memory_used": 0.60
    }
  }
}
```

## Computing Provider Integration

### 1. New Components Required

#### `internal/computing/inference_client.go`
WebSocket client for Swan Inference communication:

```go
type InferenceClient struct {
    conn            *websocket.Conn
    providerID      string
    workerAddr      string
    supportedModels []string
    send            chan []byte
    done            chan struct{}
}

// Connect establishes WebSocket connection to Swan Inference
func (c *InferenceClient) Connect(wsURL string) error

// Register sends registration message with signature
func (c *InferenceClient) Register() error

// SendHeartbeat sends periodic heartbeat with metrics
func (c *InferenceClient) SendHeartbeat(metrics map[string]float64) error

// HandleMessage processes incoming messages from Swan Inference
func (c *InferenceClient) HandleMessage(msg Message)
```

#### `internal/computing/inference_service.go`
Handles inference execution using Docker containers:

```go
type InferenceService struct {
    dockerService *DockerService
    modelRegistry map[string]ModelConfig
}

// ExecuteInference runs inference request and returns response
func (e *InferenceService) ExecuteInference(req InferencePayload) (*InferenceResponse, error)

// HandleVerification responds to model verification challenges
func (e *InferenceService) HandleVerification(req VerifyPayload) error
```

#### `internal/computing/inference_client.go`
Data models for Inference integration:

```go
// Inference WebSocket message types
type MessageType string

const (
    MsgTypeRegister    MessageType = "register"
    MsgTypeInference   MessageType = "inference"
    MsgTypeWarmup      MessageType = "warmup"
    MsgTypeVerify      MessageType = "verify"
    MsgTypeHeartbeat   MessageType = "heartbeat"
    MsgTypeAck         MessageType = "ack"
    MsgTypeError       MessageType = "error"
    MsgTypeStreamChunk MessageType = "stream_chunk"
    MsgTypeStreamEnd   MessageType = "stream_end"
)

type Message struct {
    Type      MessageType     `json:"type"`
    RequestID string          `json:"request_id,omitempty"`
    Payload   json.RawMessage `json:"payload"`
}

type RegisterPayload struct {
    ProviderID   string        `json:"provider_id"`
    WorkerAddr   string        `json:"worker_addr"`
    Token        string        `json:"token,omitempty"`     // API key for authentication (sk-prov-*)
    Signature    string        `json:"signature,omitempty"` // Alternative: wallet signature auth
    Models       []string      `json:"models"`
    Capabilities []string      `json:"capabilities"`
    Hardware     *HardwareInfo `json:"hardware,omitempty"`
}

type InferencePayload struct {
    EndpointID string          `json:"endpoint_id"`
    ModelID    string          `json:"model_id"`
    Request    json.RawMessage `json:"request"`
}

type InferenceResponse struct {
    RequestID string          `json:"request_id"`
    Response  json.RawMessage `json:"response"`
    Error     string          `json:"error,omitempty"`
    Latency   int64           `json:"latency_ms"`
}
```

### 2. Configuration Changes

Add to `conf/config.go`:

```go
type Inference struct {
    Enable             bool     `toml:"Enable"`
    ServiceURL         string   `toml:"ServiceURL"`         // HTTP API URL
    WebSocketURL       string   `toml:"WebSocketURL"`       // WebSocket URL
    ApiKey             string   `toml:"ApiKey"`             // Provider API key (sk-prov-*)
    Models             []string `toml:"Models"`             // Models this provider serves
    ChainRPC           string   `toml:"ChainRPC"`           // RPC for Swan Inference chain
    CollateralContract string   `toml:"CollateralContract"` // Collateral contract address
    TaskContract       string   `toml:"TaskContract"`       // Task contract address
}
```

Add to `config.toml`:

```toml
[Inference]
Enable = true
WebSocketURL = "wss://inference.swanchain.io/ws"
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"  # From provider signup
Models = ["llama-3.2-3b"]  # Models this provider serves
```

**Getting an API Key:**

Register as a provider via the signup endpoint:
```bash
curl -X POST https://inference.swanchain.io/api/v1/provider/signup \
  -H "Content-Type: application/json" \
  -d '{
    "name": "My GPU Provider",
    "owner_address": "0x...",
    "worker_address": "0x..."
  }'
```

The response includes your API key (`sk-prov-*`). Save it - it won't be shown again.

**Environment Variable Override:**
```bash
export INFERENCE_API_KEY="sk-prov-xxxxxxxxxxxxxxxxxxxx"
```

> **Note:** Inference mode does NOT require a public IP. The provider connects outbound to Swan Inference via WebSocket.

### 3. CLI Commands

The Inference mode is integrated into the main daemon. No separate CLI commands are needed.

```bash
# Start daemon (Inference mode is enabled by default)
computing-provider run
```

### 4. Integration Flow

#### Provider Registration Flow
```
1. CP calls POST /api/v1/providers with:
   - name, description
   - owner_address, worker_address, beneficiary_address
   - hardware info (GPU type, VRAM, count)
   - location info

2. Swan Inference creates ProviderEntity (status: pending)

3. Provider may need on-chain collateral verification

4. Swan Inference activates provider (status: active)

5. CP receives provider_id, stores in config
```

#### WebSocket Connection Flow
```
1. CP connects to wss://inference.swanchain.io/ws

2. CP sends register message with API key:
   {
     "type": "register",
     "payload": {
       "provider_id": "uuid",
       "worker_addr": "0x...",
       "token": "sk-prov-xxxxxxxxxxxx",
       "models": ["llama-3-70b", "mistral-7b"],
       "hardware": {
         "gpu_type": "rtx_4090",
         "vram_gb": 24,
         "gpu_count": 1
       }
     }
   }

3. Swan Inference validates API key, registers client in Hub

4. Swan Inference responds with ack:
   {
     "type": "ack",
     "payload": {"success": true, "message": "registered successfully"}
   }

5. CP starts heartbeat loop (every 30s)
```

#### Inference Request Flow
```
1. Consumer creates endpoint via POST /api/v1/endpoints

2. Swan Inference assigns provider to endpoint based on:
   - Model availability
   - Provider capacity
   - Provider performance/reputation

3. Consumer sends inference request to Swan Inference

4. Swan Inference routes to CP via WebSocket:
   {
     "type": "inference",
     "request_id": "uuid",
     "payload": {
       "endpoint_id": "endpoint_uuid",
       "model_id": "llama-3-70b",
       "request": {
         "model": "llama-3-70b",
         "messages": [{"role": "user", "content": "Hello"}],
         "max_tokens": 100
       }
     }
   }

5. CP executes inference in Docker container

6. CP responds with result:
   {
     "type": "ack",
     "request_id": "uuid",
     "payload": {
       "response": { /* OpenAI-compatible response */ },
       "latency_ms": 250
     }
   }

7. Swan Inference records usage, returns response to consumer
```

### 5. Model Execution

ECP2 inference uses the same Docker-based execution as existing ECP inference tasks, but with WebSocket-based task dispatch instead of HTTP.

**Model Container Requirements:**
- Expose OpenAI-compatible API endpoint (e.g., `/v1/chat/completions`)
- Support environment variables for model configuration
- Report token usage in response for billing

**Execution Flow:**
```go
func (s *InferenceService) ExecuteInference(req InferencePayload) (*InferenceResponse, error) {
    // 1. Look up model configuration
    modelConfig := s.modelRegistry[req.ModelID]

    // 2. Find or start model container
    containerName := fmt.Sprintf("inference-model-%s", req.ModelID)
    if !s.dockerService.IsExistContainer(containerName) {
        if err := s.startModelContainer(modelConfig, containerName); err != nil {
            return nil, err
        }
    }

    // 3. Forward request to model container
    start := time.Now()
    resp, err := s.forwardToModel(containerName, req.Request)
    latency := time.Since(start).Milliseconds()

    // 4. Return response
    return &InferenceResponse{
        RequestID: req.RequestID,
        Response:  resp,
        Latency:   latency,
    }, nil
}
```

## Key Files

| File | Type | Description |
|------|------|-------------|
| `internal/computing/inference_client.go` | Created | WebSocket client for Swan Inference |
| `internal/computing/inference_service.go` | Created | Inference execution handler |
| `conf/config.go` | Modified | Added Inference config struct |
| `config.toml.sample` | Modified | Added [Inference] section |

## Integration Considerations

### Security
- Provider authentication via API key (`sk-prov-*`) or wallet signature
- API keys are hashed in database (bcrypt) - original key shown only once at creation
- TLS required for production (wss://)
- Verify Swan Inference service identity before connecting
- Rate limit inference requests to prevent abuse
- API keys can be rotated/revoked via provider portal

### Performance
- Keep WebSocket connection alive with heartbeats
- Pre-warm model containers for low latency
- Use connection pooling for model inference
- Implement request queuing for high load

### Reliability
- Auto-reconnect on WebSocket disconnect
- Exponential backoff for reconnection attempts
- Graceful degradation if Swan Inference service unavailable
- Local request logging for audit trail

### Monitoring
- Track inference latency metrics
- Monitor WebSocket connection health
- Log all inference requests/responses
- Report GPU utilization in heartbeats

## Implementation Status

Inference mode has been implemented with the following components:

1. **Inference config and parsing** - Complete
2. **WebSocket client with heartbeat** - Complete
3. **Inference message handling** - Complete
4. **Docker service integration** - Complete
5. **Streaming support** - Complete
6. **API key authentication** - Complete (supports `sk-prov-*` tokens and env var override)
7. **Model warmup support** - Complete (handles `warmup` messages to pre-load models)

## References

- Swan Inference Repository: `../swan-inference`
- Swan Inference API Documentation: `../swan-inference/README.md`
