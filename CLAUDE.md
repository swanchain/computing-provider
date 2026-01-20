# CLAUDE.md

## Git Commit Policy

- Do NOT include `Co-Authored-By` lines in commit messages
- Keep commit messages concise and descriptive

## Project Overview

Computing Provider v2 is a CLI tool that turns GPUs into AI inference endpoints on the Swan Chain network.

**Key points:**
- **Inference Mode is default** - No wallet, no blockchain, no public IP needed to start
- **Docker-based** - All workloads run as containers
- **Cross-platform** - Linux (NVIDIA) and macOS (Apple Silicon via Ollama)

## Build & Run

```bash
# Build
make clean && make mainnet && make install

# Initialize (Inference mode - no public IP needed)
computing-provider init --node-name=my-provider

# Run
computing-provider run
```

## Provider Modes

| Mode | Description | Wallet Required |
|------|-------------|-----------------|
| **Inference** (Default) | AI inference via WebSocket | No (optional for rewards) |
| ZK-Proof | ZK-Snark proof generation | Yes |

## Inference Mode Quick Reference

**Prerequisites:** Docker + NVIDIA Container Toolkit (Linux) or Ollama (macOS)

**Getting a Provider API Key:**
1. Sign up at https://inference.swanchain.io or via API:
   ```bash
   # Create user account
   curl -X POST https://inference.swanchain.io/api/v1/user/signup \
     -H "Content-Type: application/json" \
     -d '{"email":"you@example.com","password":"YourPassword123","display_name":"My Provider"}'

   # Upgrade to provider (use token from signup response)
   curl -X POST https://inference.swanchain.io/api/v1/user/upgrade-to-provider \
     -H "Authorization: Bearer <your-token>" \
     -H "Content-Type: application/json" \
     -d '{"name":"My GPU Provider","wallet_address":"0x..."}'
   ```
2. Save the returned `provider_api_key` (starts with `sk-prov-`)

**Config files:**
- `$CP_PATH/config.toml` - Provider settings
- `$CP_PATH/models.json` - Model endpoints mapping

**Example models.json:**
```json
{
  "llama-3.2-3b": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 8000,
    "category": "text-generation"
  }
}
```

**Start SGLang server:**
```bash
docker run -d --gpus all -p 30000:30000 --name sglang \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path meta-llama/Llama-3.2-3B-Instruct \
    --host 0.0.0.0 --port 30000 --served-model-name llama-3.2-3b
```

## Key CLI Commands

```bash
computing-provider init --node-name=<NAME>    # Initialize
computing-provider run                         # Start provider
computing-provider task list --ecp            # List tasks
computing-provider dashboard                   # Web UI (port 3005)

# Hardware
computing-provider research hardware           # Hardware info
computing-provider research gpu-info           # GPU info

# Wallet (optional - only for receiving rewards)
computing-provider wallet new
computing-provider wallet list
```

## Wallet & Rewards (Optional)

Wallet setup is **only needed if you want to receive SWAN token rewards**:

```bash
computing-provider wallet new
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 4
computing-provider collateral add --ecp --from <addr> <amount>
```

## ZK-Proof Mode (Advanced)

Requires wallet, public IP, and v28 parameters (~200GB).

```bash
# Initialize with public IP
computing-provider init --multi-address=/ip4/<IP>/tcp/<PORT> --node-name=<NAME>

# Required env vars
export FIL_PROOFS_PARAMETER_CACHE=<path>
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"
```

## REST API Endpoints

Base: `/api/v1/computing/inference/`

| Endpoint | Description |
|----------|-------------|
| `GET /metrics` | JSON metrics |
| `GET /models` | List models |
| `GET /health` | Health status |
| `POST /models/:id/enable` | Enable model |
| `POST /models/:id/disable` | Disable model |
| `POST /models/reload` | Hot-reload config |

## Architecture

```
cmd/computing-provider/     # CLI commands
internal/computing/         # Core services
  inference_service.go      # Inference WebSocket client
  inference_client.go       # Swan Inference connection
  model_registry.go         # Model management
  model_health_checker.go   # Model health monitoring
  docker_service.go         # Container management
internal/contract/          # Smart contract bindings
internal/db/                # SQLite database
conf/                       # Configuration
```

## WebSocket Message Types

The provider communicates with Swan Inference via WebSocket using these message types:

| Message | Direction | Description |
|---------|-----------|-------------|
| `register` | Provider → Server | Register with model list and auth |
| `inference` | Server → Provider | Inference request |
| `stream_chunk` | Provider → Server | Streaming response chunk |
| `stream_end` | Provider → Server | End of streaming response |
| `warmup` | Server → Provider | Pre-load model into GPU memory |
| `heartbeat` | Provider → Server | Liveness check with metrics |
| `ack` | Both | Acknowledgment/response |

## Model Warmup

When Swan Inference sends a `warmup` message, the provider:
1. Looks up the model endpoint from `models.json`
2. Sends a minimal inference request (`max_tokens: 1`) to load the model
3. Returns success/failure with load time

This reduces cold start latency for first requests.

## Configuration

**config.toml:**
```toml
[API]
Port = 8085
NodeName = "my-provider"

[Inference]
Enable = true
WebSocketURL = "wss://inference.swanchain.io/ws"
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"  # Your provider API key
Models = ["llama-3.2-3b"]
```

**Environment overrides:**
```bash
export CP_PATH=~/.swan/computing              # Config directory
export INFERENCE_API_KEY=sk-prov-xxx          # Provider API key (overrides config)
export INFERENCE_WS_URL=ws://localhost:8081   # Dev WebSocket URL
```

## Common Issues

| Error | Solution |
|-------|----------|
| `permission denied...docker.sock` | `sudo usermod -aG docker $USER` |
| `could not select device driver "nvidia"` | Install NVIDIA Container Toolkit |
| `container "/resource-exporter" already in use` | `docker rm -f resource-exporter` |
| `authentication required` | Set ApiKey in config.toml or INFERENCE_API_KEY env var |
| `invalid provider API key` | Verify key starts with `sk-prov-` and is not revoked |
| `WebSocket connection failed` | Check WebSocketURL and network connectivity |
