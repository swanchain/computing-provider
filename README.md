# Computing Provider v2

[![Discord](https://img.shields.io/discord/770382203782692945?label=Discord&logo=Discord)](https://discord.gg/Jd2BFSVCKw)
[![Twitter Follow](https://img.shields.io/twitter/follow/swan_chain)](https://twitter.com/swan_chain)

Turn your GPU into an AI inference endpoint and join the Swan Chain decentralized computing network.

## Quick Start (5 minutes)

**No wallet needed. No blockchain registration. No public IP required.**

### Linux (NVIDIA GPU)

```bash
# 0. Install build tools (skip if already installed)
sudo apt-get update && sudo apt-get install -y git make
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc && source ~/.bashrc

# 1. Clone and build
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider && git checkout releases
make clean && make mainnet && sudo make install

# 2. Start an inference server (example: Qwen 2.5 7B with SGLang)
docker run -d --gpus all -p 30000:30000 --name sglang \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --shm-size 32g --ipc=host \
  lmsysorg/sglang:v0.4.7.post1-cu124 \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-7B \
    --host 0.0.0.0 --port 30000 --served-model-name qwen-2.5-7b

# 3. Run the setup wizard (handles auth, config, and model discovery)
computing-provider setup

# 4. Run the provider
computing-provider run
```

That's it! The setup wizard will:
- Check prerequisites (Docker, GPU)
- Create/login to your Swan Inference account
- Auto-discover your running model servers
- Auto-match local models to Swan Inference model IDs
- Generate `config.toml` and `models.json`

### macOS (Apple Silicon)

```bash
# 1. Install Ollama and pull a model
brew install ollama
ollama serve &
ollama pull qwen2.5:7b

# 2. Install Computing Provider
brew install go
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider && git checkout releases
make clean && make mainnet && sudo make install

# 3. Run the setup wizard
computing-provider setup

# 4. Run the provider
computing-provider run
```

The setup wizard auto-discovers Ollama models and matches them to Swan Inference model IDs (e.g., `qwen2.5:7b` → `qwen-2.5-7b`).

---

## How It Works

```
Swan Inference (Cloud)
        │
        │ WebSocket (outbound connection - works behind NAT)
        ▼
┌───────────────────────┐
│  Computing Provider   │
│  ┌─────────────────┐  │
│  │ Your GPU Server │  │
│  │ (SGLang/Ollama) │  │
│  └─────────────────┘  │
└───────────────────────┘
```

1. Provider connects **outbound** to Swan Inference (no inbound ports needed)
2. Registers available models
3. Receives inference requests via WebSocket
4. Forwards to local model server, returns response
5. **Earn rewards** for completed requests (optional wallet setup)

---

## Prerequisites

| Platform | Requirements |
|----------|-------------|
| **Linux** | Docker, NVIDIA GPU, [NVIDIA Container Toolkit](#install-nvidia-container-toolkit) |
| **macOS** | [Ollama](https://ollama.ai), Apple Silicon (M1/M2/M3/M4) |

### Install NVIDIA Container Toolkit (Linux only)

```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker

# Verify
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi
```

---

## Configuration

### Model Configuration (`models.json`)

Map Swan Inference model IDs to your local inference endpoints:

```json
{
  "qwen-2.5-7b": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 16000,
    "category": "text-generation"
  }
}
```

| Field | Description |
|-------|-------------|
| `endpoint` | URL of your local inference server |
| `gpu_memory` | GPU memory required (MB) |
| `category` | Model category (`text-generation`, `image-generation`, etc.) |
| `local_model` | (Optional) Actual model name for local server (e.g., Ollama model name) |

> **Note:** The `local_model` field is used when your local server uses different model names than Swan Inference. For example, Ollama uses `qwen2.5:7b` while Swan Inference expects `qwen-2.5-7b`. The setup wizard handles this mapping automatically.

### Provider Configuration (`config.toml`)

Located at `~/.swan/computing/config.toml`:

```toml
[API]
Port = 8085
NodeName = "my-provider"

[Inference]
Enable = true
WebSocketURL = "wss://inference-ws.swanchain.io"
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"  # Required - get from https://inference.swanchain.io
Models = ["qwen-2.5-7b"]
```

---

## Monitoring

### Web Dashboard

```bash
computing-provider dashboard
# Open http://localhost:3005
```

Features: Real-time metrics, GPU status, model management, request controls.

### REST API

```bash
# View metrics
curl http://localhost:8085/api/v1/computing/inference/metrics

# List models
curl http://localhost:8085/api/v1/computing/inference/models

# Check health
curl http://localhost:8085/api/v1/computing/inference/health
```

### Useful Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /inference/metrics` | Request counts, latency, GPU stats |
| `GET /inference/metrics/prometheus` | Prometheus format for Grafana |
| `GET /inference/models` | List all models with status |
| `POST /inference/models/:id/enable` | Enable a model |
| `POST /inference/models/:id/disable` | Disable a model |
| `POST /inference/models/reload` | Hot-reload models.json |

---

## Earning Rewards (Optional)

To receive SWAN token rewards for completed inference requests, set up a wallet:

```bash
# 1. Create a wallet
computing-provider wallet new

# 2. Note your wallet address
computing-provider wallet list

# 3. Register on Swan Chain (requires small amount of SwanETH for gas)
computing-provider account create \
  --ownerAddress <your-wallet> \
  --workerAddress <your-wallet> \
  --beneficiaryAddress <your-wallet> \
  --task-types 4

# 4. Add collateral (determines your reward tier)
computing-provider collateral add --ecp --from <your-wallet> <amount>
```

Get SwanETH from the [Swan Chain Faucet](https://docs.swanchain.io).

> **Note:** You can run the provider without a wallet - it will still serve inference requests, but you won't receive on-chain rewards.

---

## CLI Reference

### Basic Commands

```bash
computing-provider setup                     # Interactive setup wizard (recommended)
computing-provider run                       # Start provider
computing-provider dashboard                 # Web UI
computing-provider task list --ecp           # List tasks
```

### Setup Wizard

The setup wizard is the recommended way to configure a new provider:

```bash
computing-provider setup                     # Full interactive setup
computing-provider setup --skip-discovery    # Skip model discovery
computing-provider setup --api-key=sk-prov-xxx  # Use existing API key

# Subcommands
computing-provider setup discover            # Just discover model servers
computing-provider setup login               # Login to existing account
computing-provider setup signup              # Create new account
```

### Wallet Commands (for rewards)

```bash
computing-provider wallet new                # Create wallet
computing-provider wallet list               # List wallets
computing-provider wallet import <file>      # Import private key
```

### Hardware Info

```bash
computing-provider research hardware         # All hardware info
computing-provider research gpu-info         # GPU details
computing-provider research gpu-benchmark    # Run benchmark
```

---

## Troubleshooting

| Error | Solution |
|-------|----------|
| `go: command not found` | Install Go 1.21+: see [go.dev/dl](https://go.dev/dl/) |
| `permission denied...docker.sock` | Add user to docker group: `sudo usermod -aG docker $USER` |
| `could not select device driver "nvidia"` | Install [NVIDIA Container Toolkit](#install-nvidia-container-toolkit) |
| `container "/resource-exporter" already in use` | Run `docker rm -f resource-exporter` |
| `authentication required` | Set ApiKey in config.toml or INFERENCE_API_KEY env var |
| `invalid provider API key` | Verify key starts with `sk-prov-` and is not revoked |
| `WebSocket connection failed` | Check WebSocketURL and network connectivity |
| Provider not receiving requests | Check `models.json` matches your inference server |

### Check Logs

```bash
# Provider logs
tail -f cp.log

# Inference server logs
docker logs sglang
```

---

## Advanced: ZK-Proof Mode

For generating ZK-Snark proofs (Filecoin, Aleo). Requires additional setup.

See [ZK-Proof Documentation](docs/ubi/README.md).

```bash
# Initialize with public IP (required for ZK mode)
computing-provider init --multi-address=/ip4/<PUBLIC_IP>/tcp/<PORT> --node-name=<name>

# Requires: wallet, account registration, v28 parameters (~200GB)
```

---

## Getting Help

- [Discord](https://discord.gg/3uQUWzaS7U) - Community support
- [GitHub Issues](https://github.com/swanchain/go-computing-provider/issues) - Bug reports
- [Documentation](https://docs.swanchain.io) - Full docs

## License

Apache 2.0
