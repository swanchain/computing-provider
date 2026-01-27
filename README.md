# Computing Provider v2

[![Discord](https://img.shields.io/discord/770382203782692945?label=Discord&logo=Discord)](https://discord.gg/Jd2BFSVCKw)
[![Twitter Follow](https://img.shields.io/twitter/follow/swan_chain)](https://twitter.com/swan_chain)

Turn your GPU into an AI inference endpoint and join the Swan Chain decentralized computing network.

## Quick Start (5 minutes)

**No wallet needed. No blockchain registration. No public IP required.**

### Linux (NVIDIA GPU)

```bash
# 1. Install
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider && git checkout releases
make clean && make mainnet && sudo make install

# 2. Initialize
computing-provider init --node-name=my-provider

# 3. Get your Provider API Key
#    Sign up at https://inference.swanchain.io and get your API key (starts with sk-prov-)
#    Add it to ~/.swan/computing/config.toml under [Inference]:
#    ApiKey = "sk-prov-your-key-here"

# 4. Start an inference server (example: Llama 3.2 with SGLang)
docker run -d --gpus all -p 30000:30000 --name sglang \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --shm-size 32g --ipc=host \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path meta-llama/Llama-3.2-3B-Instruct \
    --host 0.0.0.0 --port 30000 --served-model-name llama-3.2-3b

# 5. Configure your model
cat > ~/.swan/computing/models.json << 'EOF'
{
  "llama-3.2-3b": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 8000,
    "category": "text-generation"
  }
}
EOF

# 6. Run the provider
computing-provider run
```

That's it! Your provider connects to Swan Inference and starts receiving requests.

### macOS (Apple Silicon)

```bash
# 1. Install Ollama
brew install ollama
ollama serve &
ollama pull llama3.2:3b

# 2. Install Computing Provider
brew install go
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider && git checkout releases
make clean && make mainnet && sudo make install

# 3. Initialize
computing-provider init --node-name=my-mac-provider

# 4. Get your Provider API Key
#    Sign up at https://inference.swanchain.io and get your API key (starts with sk-prov-)
#    Add it to ~/.swan/computing/config.toml under [Inference]:
#    ApiKey = "sk-prov-your-key-here"

# 5. Configure your model
cat > ~/.swan/computing/models.json << 'EOF'
{
  "llama-3.2-3b": {
    "endpoint": "http://localhost:11434",
    "gpu_memory": 4000,
    "category": "text-generation"
  }
}
EOF

# 6. Run
computing-provider run
```

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

Map model names to your local inference endpoints:

```json
{
  "llama-3.2-3b": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 8000,
    "category": "text-generation"
  },
  "mistral-7b": {
    "endpoint": "http://localhost:30001",
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
Models = ["llama-3.2-3b"]
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
computing-provider init --node-name=<name>  # Initialize
computing-provider run                       # Start provider
computing-provider task list --ecp           # List tasks
computing-provider dashboard                 # Web UI
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
