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
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider
make clean && make testnet && sudo make install

# 2. Download model weights from HuggingFace (e.g., Qwen 2.5 7B)
computing-provider models download Qwen/Qwen2.5-7B-Instruct

# 3. Start SGLang with the downloaded model
docker run -d --gpus all -p 30000:30000 --ipc=host --name sglang \
  -v ~/.swan/models/Qwen/Qwen2.5-7B-Instruct:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server --model-path /models \
    --host 0.0.0.0 --port 30000 \
    --served-model-name Qwen/Qwen2.5-7B-Instruct

# 4. Run the setup wizard (handles auth, config, and model discovery)
computing-provider setup

# 5. Run the provider
computing-provider run

# 7. Verify your provider is connected
computing-provider inference status
```

The `models download` command downloads model weights directly from HuggingFace. Large weight files (LFS) are verified with SHA256 hashes. The setup wizard will:
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
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider
make clean && make testnet && sudo make install

# 3. Run the setup wizard
computing-provider setup

# 4. Run the provider
computing-provider run

# 5. Verify your provider is connected
computing-provider inference status
```

The setup wizard auto-discovers Ollama models and matches them to Swan Inference model IDs (e.g., `qwen2.5:7b` → `qwen-2.5-7b`).

> **Want to maximize earnings?** The quickstart uses Qwen 2.5 7B as an example, but UBI rewards are based on real token traffic. Serving other in-demand models means less competition and more requests routed to you. See the [Switching Models](#switching-models) section to get started.

---

## What Happens After Setup

Once your provider is running, it goes through these stages automatically. Most providers are fully active within a day.

```
Connect ──▶ Benchmark ──▶ Approval ──▶ Collateral ──▶ Active
(instant)   (automatic)   (< 24 hrs)   (USD or crypto) (earning)
```

| Stage | What happens | Time |
|-------|-------------|------|
| **Connect** | Provider connects to the network and registers its models | Immediate |
| **Benchmark** | Automated benchmarks verify your GPU can serve the registered models | Minutes (automatic) |
| **Approval** | Admin reviews your provider | < 24 hours |
| **Collateral** | Deposit collateral to secure your position and unlock earnings (Stripe/PayPal or USDC/USDT on-chain) | Instant |
| **Active** | Start receiving inference requests and earning rewards | Ongoing |

> **Grace period:** New providers get a 7-day grace period after activation. During this period, benchmark failures and low uptime won't affect your routing priority, giving you time to stabilize your setup.

Check your current stage at any time:

```bash
computing-provider inference status
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

### Linux (NVIDIA GPU)

| Category | Requirement |
|----------|-------------|
| **GPU** | NVIDIA RTX 3090, 4090, A100, H100, or equivalent |
| **VRAM** | Minimum 16GB (24GB+ recommended) |
| **RAM** | Minimum 32GB system memory |
| **Storage** | 500GB+ SSD for model weights |
| **OS** | Ubuntu 22.04+ or Debian 11+ |
| **NVIDIA Driver** | 535.x or newer |
| **CUDA** | 12.1 or newer |
| **Docker** | 24.0+ with [NVIDIA Container Toolkit](#install-nvidia-container-toolkit) |
| **Network** | 100 Mbps minimum (1 Gbps recommended), stable connection with low latency |

### macOS (Apple Silicon)

| Category | Requirement |
|----------|-------------|
| **Chip** | Apple Silicon M1, M2, M3, or M4 |
| **Memory** | 16GB+ unified memory (32GB+ recommended) |
| **Storage** | 500GB+ SSD for model weights |
| **OS** | macOS 13 Ventura or newer |
| **Software** | [Ollama](https://ollama.ai) (latest version) |
| **Network** | 100 Mbps minimum, stable connection with low latency |

> **Ports:** Only outbound WebSocket connections are needed — no port forwarding or public IP required.

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
WebSocketURL = "ws://inference-ws-dev.swanchain.io"
ServiceURL = "https://api-dev.swanchain.io"
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"  # Required - get from https://inference-dev.swanchain.io
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

## Available Models

Run `computing-provider models catalog` to see all supported models:

```
$ computing-provider models catalog
Available models in Swan Model Repository (6):

+--------------------------------------------------------+----------+-------+----------+----------------+
|                        MODEL ID                        | CATEGORY | FILES |   SIZE   |     STATUS     |
+--------------------------------------------------------+----------+-------+----------+----------------+
| Qwen/Qwen2.5-0.5B                                      |   llm    |     1 | 942.3 MB |   downloaded   |
| Qwen/Qwen3-8B                                          |   llm    |     5 |  15.3 GB | partial (3/5)  |
| Sinensis/L3.3-MS-Nevoria-70b-AWQ                       |   llm    |     8 |  13.7 GB | not downloaded |
| TheDrummer/Cydonia-24B-v4.1                            |   llm    |    19 |  43.9 GB | not downloaded |
| jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym |   llm    |     7 |   9.3 GB | not downloaded |
| meganovaai/MN-Violet-Lotus-12B-AWQ                     |   llm    |    12 |   7.8 GB | not downloaded |
+--------------------------------------------------------+----------+-------+----------+----------------+
```

### Hardware Requirements

| Model | Size | Min VRAM | Recommended GPU | Notes |
|-------|------|----------|-----------------|-------|
| Qwen/Qwen2.5-0.5B | 942 MB | 2 GB | Any GPU | Tiny model, good for testing |
| meganovaai/MN-Violet-Lotus-12B-AWQ | 7.8 GB | 12 GB | RTX 3090, RTX 4090 | AWQ quantized, fits single 24GB GPU |
| jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym | 9.3 GB | 16 GB | RTX 3090, RTX 4090 | AWQ quantized, fits single 24GB GPU |
| Sinensis/L3.3-MS-Nevoria-70b-AWQ | 13.7 GB | 20 GB | RTX 3090, RTX 4090 | AWQ quantized 70B — fits single 24GB GPU |
| Qwen/Qwen3-8B | 15.3 GB | 20 GB | RTX 3090, RTX 4090 | Full precision, needs 24GB GPU |
| TheDrummer/Cydonia-24B-v4.1 | 43.9 GB | 48 GB | 2× RTX 3090/4090 or A100 | Full precision, requires multi-GPU (`--tp 2`) |

---

## Switching Models

You can add, remove, or swap models without restarting the provider.

### 1. Start the new model server

```bash
# Example: switch from Qwen 2.5 7B to Mistral Small 24B (AWQ)

# Stop the old server (optional — you can run multiple models)
docker stop sglang && docker rm sglang

# Download the new model weights
computing-provider models download jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym

# Start the new model server
docker run -d --gpus all -p 30000:30000 --ipc=host --name sglang \
  -v ~/.swan/models/jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server --model-path /models \
    --host 0.0.0.0 --port 30000 \
    --served-model-name jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym

# Verify the server is healthy
curl http://localhost:30000/v1/models
```

### 2. Update `models.json`

Edit `~/.swan/computing/models.json` to point to the new model:

```json
{
  "jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 16000,
    "category": "text-generation"
  }
}
```

The provider watches `models.json` and **hot-reloads automatically** — no restart needed. You can also trigger a manual reload:

```bash
curl -X POST http://localhost:8085/api/v1/computing/inference/models/reload
```

### 3. Verify

```bash
# Check the provider picked up the new model
curl http://localhost:8085/api/v1/computing/inference/models

# Check status on Swan Inference
computing-provider inference status
```

> **Tip:** To run multiple models simultaneously, start each on a different port and add all of them to `models.json`. Use `--gpus '"device=0"'` and `--gpus '"device=1"'` to pin each model to a specific GPU.

---

## Earning Rewards (Optional)

To receive SWAN token rewards for completed inference requests, set your beneficiary wallet:

```bash
# Set the wallet address where rewards will be sent
computing-provider inference set-beneficiary 0xYourWalletAddress
```

For on-chain collateral (optional, enables staking rewards):

```bash
# 1. Create a wallet
computing-provider wallet new

# 2. Add collateral
computing-provider collateral add --ecp --from <your-wallet> <amount>
```

> **Note:** You can run the provider without a wallet - it will still serve inference requests, but you won't receive on-chain rewards.

### How UBI Rewards Are Calculated

Your daily SWAN reward is based on a **weight** that combines four factors:

```
weight = usage_factor × uptime_factor × success_factor × benchmark_factor
```

| Factor | What it measures | How to maximize |
|--------|-----------------|-----------------|
| **Usage factor** | Your share of real token throughput across the network (sqrt-compressed) | Serve more inference requests — idle providers with zero tokens receive zero UBI |
| **Uptime factor** | How consistently your provider stays online | Keep your provider running 24/7 with stable connectivity |
| **Success factor** | Ratio of successful responses to total requests | Ensure your model server is healthy and responding correctly |
| **Benchmark factor** | Performance on periodic benchmarks (math, code, reasoning, latency) | Use recommended model servers (SGLang) and follow [performance tuning best practices](docs/sglang-best-practices.md) |

**Key points:**
- **Traffic matters most.** Usage factor is based on actual tokens served — registering a powerful GPU without serving traffic won't earn rewards.
- **Output tokens count 3x** compared to input tokens, so models that generate longer responses contribute more to your usage share.
- **Rewards scale sub-linearly.** The usage factor uses square-root compression, so doubling your traffic increases your factor by ~1.4x (not 2x). This keeps rewards accessible to smaller providers.

> **Tip:** Run `computing-provider inference status` to see your current earnings breakdown and check how your provider is performing relative to the network.

---

## CLI Reference

### Basic Commands

```bash
computing-provider setup                     # Interactive setup wizard (recommended)
computing-provider run                       # Start provider
computing-provider inference status          # Check status on Swan Inference
computing-provider inference config          # Show inference config
computing-provider inference deposit         # Get collateral deposit instructions
computing-provider inference deposit --check # Check current collateral status
computing-provider inference set-beneficiary 0x...  # Set reward wallet
computing-provider dashboard                 # Web UI (port 3005)
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
| `cuda>=12.x unsatisfied condition` | Use an older SGLang tag: `lmsysorg/sglang:v0.4.7.post1-cu124` |

### Check Logs

```bash
# Provider logs
tail -f cp.log

# Inference server logs
docker logs sglang
```

---

## FAQ

### Setup & Installation

**Q: `make mainnet` fails with `go: command not found`**
Install Go 1.22+. On Linux: download from [go.dev/dl](https://go.dev/dl/) and add to PATH. On macOS: `brew install go`. Make sure to restart your shell or `source ~/.bashrc` after installing.

**Q: SGLang container fails with `cuda>=12.x unsatisfied condition`**
Your NVIDIA driver is too old for the latest SGLang image. The error looks like:
```
nvidia-container-cli: requirement error: unsatisfied condition: cuda>=12.9, please update your driver to a newer version, or use an earlier cuda container
```
First, check what CUDA version your driver supports:
```bash
nvidia-smi   # Max CUDA version is shown in the top-right corner
```
Then either update your driver (`sudo apt install nvidia-driver-550`) or use an older SGLang tag that matches your CUDA version:
```bash
# For CUDA 12.4 compatible drivers
docker run -d --gpus all -p 30000:30000 --ipc=host --name sglang \
  -v ~/.swan/models/Qwen/Qwen2.5-7B-Instruct:/models \
  lmsysorg/sglang:v0.4.7.post1-cu124 \
  python3 -m sglang.launch_server --model-path /models \
    --host 0.0.0.0 --port 30000 \
    --served-model-name Qwen/Qwen2.5-7B-Instruct
```

**Q: `docker: Error response from daemon: could not select device driver "nvidia"`**
The NVIDIA Container Toolkit is not installed. Follow the [NVIDIA Container Toolkit](#install-nvidia-container-toolkit) section, then restart Docker.

**Q: `computing-provider setup` doesn't detect my running model server**
The setup wizard scans common ports (30000, 8080, 11434). Make sure your model server is running *before* you start the wizard. You can verify manually:
```bash
curl http://localhost:30000/v1/models   # SGLang/vLLM
curl http://localhost:11434/api/tags    # Ollama
```
If your server uses a non-standard port, the wizard may not find it — you can manually edit `~/.swan/computing/models.json` afterward.

### Model Issues

**Q: My provider is online but not receiving any inference requests**
The most common cause is a model name mismatch. The `--served-model-name` in your SGLang/vLLM command **must exactly match** the key in `models.json`, and that key must match a model ID registered on Swan Inference. Run `computing-provider models catalog` to see valid model IDs.

**Q: SGLang container starts but immediately exits**
Check logs with `docker logs sglang`. Common causes:
- **Out of VRAM**: The model is too large for a single GPU. Use tensor parallelism to split it across multiple GPUs with `--tp 2` (or `--tp 4`). For example, a 12B model in bf16 needs ~23 GB — too large for a single 24GB GPU once KV cache is included, but fits easily across 2 GPUs.
- **Unbalanced GPU memory**: If another model server (e.g., vLLM) is already using one of your GPUs, SGLang will fail with `memory capacity is unbalanced`. Pin SGLang to specific free GPUs instead of using `--gpus all`:
  ```bash
  # Check which GPUs are free
  nvidia-smi
  # Run on specific GPUs (e.g., GPUs 0 and 2)
  docker run -d --gpus '"device=0,2"' -p 30000:30000 --ipc=host \
    -v ~/.swan/models/YourModel:/models \
    lmsysorg/sglang:v0.4.7.post1-cu124 \
    python3 -m sglang.launch_server --model-path /models \
      --host 0.0.0.0 --port 30000 --tp 2 \
      --served-model-name YourModel
  ```
- **Shared memory**: Add `--shm-size 4g` to your `docker run` command.
- **Port conflict**: Port 30000 is already in use. Check with `docker ps` or `lsof -i :30000`.

**Q: `models download` fails for Llama or other gated models**
Some HuggingFace models require accepting a license agreement. Visit the model page on [huggingface.co](https://huggingface.co), accept the terms, then set your HuggingFace token:
```bash
export HF_TOKEN=hf_xxxxxxxxxxxxxxxxxxxxx
computing-provider models download meta-llama/Llama-3.3-70B-Instruct
```

### Connection & Authentication

**Q: `WebSocket connection failed` or provider can't connect**
- Verify the WebSocket URL in `~/.swan/computing/config.toml` is `wss://inference-ws.swanchain.io` (not `http://` or `https://`)
- Check that outbound port 443 isn't blocked by your firewall or cloud security group
- If behind a corporate proxy, WebSocket connections may be blocked — check with your network admin

**Q: `invalid provider API key` or `authentication required`**
- Your API key must start with `sk-prov-`. Consumer keys (`sk-swan-*`) won't work.
- Verify your key in `~/.swan/computing/config.toml` under `[Inference].ApiKey`
- You can also set it via environment variable: `export INFERENCE_API_KEY=sk-prov-xxx`

**Q: Provider is stuck in `pending` status**
Providers are auto-activated when all conditions are met: collateral deposited, GPU meets minimum hardware requirements, and registration benchmark passes. Check your status:
```bash
computing-provider inference status
```
If you're just testing, ask the Swan team on [Discord](https://discord.gg/3uQUWzaS7U) about dev mode access which skips these requirements.

### Earnings & Collateral

**Q: How do I earn rewards?**
You earn in two ways: **per-request earnings** based on token usage (input + output tokens) multiplied by the model's per-token price, and **daily UBI rewards** (SWAN tokens) distributed based on your provider's weight — a combination of token throughput, uptime, success rate, and benchmark scores. Serving more real inference traffic increases both. See [How UBI Rewards Are Calculated](#how-ubi-rewards-are-calculated) for the full UBI breakdown.
```bash
computing-provider inference status   # Shows current stage and earnings summary
```
Payouts are processed when your balance reaches the minimum threshold ($50). Set a beneficiary wallet to receive payouts:
```bash
computing-provider inference set-beneficiary 0xYourWalletAddress
```

**Q: What are the collateral deposit options?**
After your provider is approved, you can deposit collateral via:
- **USD (off-chain):** Stripe or PayPal — pay through the Provider Dashboard
- **Stablecoin (on-chain):** USDC or USDT on supported chains (Base, Ethereum)

Run `computing-provider inference deposit` to see supported chains, contract addresses, and minimum amounts. Deposit via the [Provider Dashboard](https://inference.swanchain.io/dashboard) or directly to the contract from your wallet.

**Q: What happens if I fail benchmarks?**
The system runs periodic benchmarks (math, code, reasoning, latency) to verify provider quality. Passing resets your failure counter. Consecutive failures may result in collateral slashing (default: 10% after 2 consecutive failures).

### Configuration

**Q: I edited `config.toml` but nothing changed**
Make sure you're editing the right file. The provider reads config from `~/.swan/computing/config.toml` (or wherever `$CP_PATH` points), **not** the `config.toml` in the git repo directory.

**Q: How do I change models without restarting?**
Edit `~/.swan/computing/models.json` — the provider watches this file and hot-reloads automatically. You can also reload via the API:
```bash
curl -X POST http://localhost:8085/api/v1/computing/inference/models/reload
```

**Q: Port 8085 or 30000 is already in use**
Find and stop the conflicting process:
```bash
lsof -i :30000   # Find what's using the port
docker ps         # Check for leftover containers
docker rm -f sglang  # Remove old SGLang container
```

---

## Getting Help

- [Discord](https://discord.gg/3uQUWzaS7U) - Community support
- [GitHub Issues](https://github.com/swanchain/computing-provider/issues) - Bug reports
- [Documentation](https://docs.swanchain.io) - Full docs

## License

Apache 2.0
