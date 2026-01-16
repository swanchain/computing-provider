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
│                    Computing Provider (ECP2 Client)                  │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                  │
│  │  ECP2       │  │   Model     │  │   Docker    │                  │
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
    "signature": "signed_message",
    "models": ["model_id_1", "model_id_2"],
    "capabilities": ["llm", "embedding"]
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

#### `internal/computing/ecp2_client.go`
WebSocket client for Swan Inference communication:

```go
type ECP2Client struct {
    conn            *websocket.Conn
    providerID      string
    workerAddr      string
    supportedModels []string
    hub             *ECP2Hub
    send            chan []byte
    done            chan struct{}
}

// Connect establishes WebSocket connection to Swan Inference
func (c *ECP2Client) Connect(wsURL string) error

// Register sends registration message with signature
func (c *ECP2Client) Register() error

// SendHeartbeat sends periodic heartbeat with metrics
func (c *ECP2Client) SendHeartbeat(metrics map[string]float64) error

// HandleMessage processes incoming messages from Swan Inference
func (c *ECP2Client) HandleMessage(msg Message)
```

#### `internal/computing/ecp2_executor.go`
Handles inference execution using Docker containers:

```go
type ECP2Executor struct {
    dockerService *DockerService
    modelRegistry map[string]ModelConfig
}

// ExecuteInference runs inference request and returns response
func (e *ECP2Executor) ExecuteInference(req InferencePayload) (*InferenceResponse, error)

// HandleVerification responds to model verification challenges
func (e *ECP2Executor) HandleVerification(req VerifyPayload) error
```

#### `internal/models/ecp2.go`
Data models for ECP2 integration:

```go
// ECP2 WebSocket message types
type MessageType string

const (
    MsgTypeRegister  MessageType = "register"
    MsgTypeInference MessageType = "inference"
    MsgTypeVerify    MessageType = "verify"
    MsgTypeHeartbeat MessageType = "heartbeat"
    MsgTypeAck       MessageType = "ack"
    MsgTypeError     MessageType = "error"
)

type Message struct {
    Type      MessageType     `json:"type"`
    RequestID string          `json:"request_id,omitempty"`
    Payload   json.RawMessage `json:"payload"`
}

type RegisterPayload struct {
    ProviderID   string   `json:"provider_id"`
    WorkerAddr   string   `json:"worker_addr"`
    Signature    string   `json:"signature"`
    Models       []string `json:"models"`
    Capabilities []string `json:"capabilities"`
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
type ECP2Config struct {
    Enabled       bool   `toml:"enabled"`
    ServiceURL    string `toml:"service_url"`     // HTTP API URL
    WebSocketURL  string `toml:"websocket_url"`   // WebSocket URL
    ProviderID    string `toml:"provider_id"`     // From registration
    HeartbeatSec  int    `toml:"heartbeat_sec"`   // Heartbeat interval
    ReconnectSec  int    `toml:"reconnect_sec"`   // Reconnect interval on disconnect
}
```

Add to `config.toml`:

```toml
[ECP2]
Enable = true
WebSocketURL = "wss://inference-ws.swanchain.io"
Models = ["llama-3.2-3b"]  # Models this provider serves
```

> **Note:** ECP2 does NOT require a public IP. The provider connects outbound to Swan Inference via WebSocket.

### 3. CLI Commands

#### `computing-provider ecp2 register`
Register as an ECP2 provider:

```bash
computing-provider ecp2 register \
    --name "My Provider" \
    --models "llama-3-70b,mistral-7b" \
    --hardware '{"gpu": "RTX 4090", "vram": "24GB", "count": 4}'
```

#### `computing-provider ecp2 daemon`
Start ECP2 daemon service:

```bash
computing-provider ecp2 daemon
```

#### `computing-provider ecp2 models`
List/update supported models:

```bash
# List supported models
computing-provider ecp2 models list

# Add model support
computing-provider ecp2 models add --model-id "llama-3-70b" --sku "gpu-4090-24gb"

# Remove model support
computing-provider ecp2 models remove --model-id "llama-3-70b"
```

#### `computing-provider ecp2 status`
Show ECP2 connection status:

```bash
computing-provider ecp2 status
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
1. CP connects to wss://inference-ws.swanchain.io/ws

2. CP sends register message:
   {
     "type": "register",
     "payload": {
       "provider_id": "uuid",
       "worker_addr": "0x...",
       "signature": "signed(provider_id + timestamp)",
       "models": ["llama-3-70b", "mistral-7b"]
     }
   }

3. Swan Inference verifies signature, registers client in Hub

4. Swan Inference responds with ack:
   {
     "type": "ack",
     "payload": {"success": true, "message": "registered"}
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
func (e *ECP2Executor) ExecuteInference(req InferencePayload) (*InferenceResponse, error) {
    // 1. Look up model configuration
    modelConfig := e.modelRegistry[req.ModelID]

    // 2. Find or start model container
    containerName := fmt.Sprintf("ecp2-model-%s", req.ModelID)
    if !e.dockerService.IsExistContainer(containerName) {
        if err := e.startModelContainer(modelConfig, containerName); err != nil {
            return nil, err
        }
    }

    // 3. Forward request to model container
    start := time.Now()
    resp, err := e.forwardToModel(containerName, req.Request)
    latency := time.Since(start).Milliseconds()

    // 4. Return response
    return &InferenceResponse{
        RequestID: req.RequestID,
        Response:  resp,
        Latency:   latency,
    }, nil
}
```

## Key Files to Create/Modify

| File | Type | Description |
|------|------|-------------|
| `internal/computing/ecp2_client.go` | Create | WebSocket client for ECP2 |
| `internal/computing/ecp2_executor.go` | Create | Inference execution handler |
| `internal/models/ecp2.go` | Create | ECP2 data models |
| `cmd/computing-provider/ecp2.go` | Create | CLI commands for ECP2 |
| `cmd/computing-provider/main.go` | Modify | Register ecp2 command |
| `conf/config.go` | Modify | Add ECP2Config struct |
| `config.toml.sample` | Modify | Add [ECP2] section |

## Integration Considerations

### Security
- All WebSocket messages must be signed with worker key
- TLS required for production (wss://)
- Verify Swan Inference service identity before connecting
- Rate limit inference requests to prevent abuse

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

## Migration Path

### Phase 1: Registration & Connection
1. Implement ECP2Config and config parsing
2. Implement provider registration CLI
3. Implement WebSocket client with heartbeat

### Phase 2: Inference Execution
4. Implement inference message handling
5. Integrate with existing Docker service
6. Implement model container management

### Phase 3: Production Features
7. Add verification challenge handling
8. Implement usage metrics reporting
9. Add monitoring and alerting

## References

- Swan Inference Repository: `../swan-inference`
- Swan Inference API Documentation: `../swan-inference/README.md`
- WebSocket Protocol: `../ecp2-service/api/ws/protocol.go`
- Provider Entity: `../ecp2-service/internal/module/repository/entity/provider.go`
- Endpoint Entity: `../ecp2-service/internal/module/repository/entity/endpoint.go`
