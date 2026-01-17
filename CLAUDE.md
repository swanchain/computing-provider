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
  docker_service.go         # Container management
internal/contract/          # Smart contract bindings
internal/db/                # SQLite database
conf/                       # Configuration
```

## Configuration

**config.toml:**
```toml
[API]
Port = 8085
NodeName = "my-provider"

[Inference]
Enable = true
WebSocketURL = "wss://inference-ws.swanchain.io"
Models = ["llama-3.2-3b"]
```

**Environment overrides:**
```bash
export CP_PATH=~/.swan/computing          # Config directory
export INFERENCE_WS_URL=ws://localhost:8081  # Dev WebSocket
```

## Common Issues

| Error | Solution |
|-------|----------|
| `permission denied...docker.sock` | `sudo usermod -aG docker $USER` |
| `could not select device driver "nvidia"` | Install NVIDIA Container Toolkit |
| `container "/resource-exporter" already in use` | `docker rm -f resource-exporter` |
