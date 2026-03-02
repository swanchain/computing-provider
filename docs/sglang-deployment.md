# SGLang Deployment Guide for Inference Providers

This guide covers deploying SGLang as the inference engine for Inference providers on Swan Chain.

## Overview

SGLang is the recommended inference engine for Inference providers due to:
- **29% higher throughput** than vLLM (16,215 vs 12,553 tok/s)
- **Stable concurrency** handling (30-31 tok/s under load)
- **RadixAttention** for 10% boost in multi-turn conversations
- **OpenAI-compatible API** for seamless Swan Inference integration
- **Streaming support** for real-time token generation via SSE

## Streaming Support

Swan Inference fully supports streaming chat completions. When a client sends `"stream": true`:

1. Swan Inference forwards the request to the provider via WebSocket
2. The provider calls SGLang with streaming enabled
3. SGLang returns Server-Sent Events (SSE)
4. Provider parses SSE and forwards chunks via WebSocket
5. Swan Inference converts chunks to SSE for the client

**No additional configuration required** - streaming works automatically with SGLang.

### Streaming Flow Diagram

```
Client ←(SSE)→ Swan Inference ←(WebSocket)→ Provider ←(SSE)→ SGLang
       stream: true                stream_chunk          stream: true
```

### Testing Streaming

```bash
# Test streaming with curl
curl -N http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen-2.5-7b",
    "messages": [{"role": "user", "content": "Count to 10 slowly"}],
    "stream": true
  }'
```

## Prerequisites

### Hardware Requirements

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| GPU | NVIDIA Volta V100+ (CC 7.0+) | Ampere A100 / Ada RTX 4090 |
| VRAM | 16GB | 24GB+ (48-80GB for 70B+ models) |
| RAM | 32GB | 64GB+ |
| Storage | 100GB SSD | 500GB+ NVMe |

### Software Requirements

- Docker with NVIDIA Container Toolkit
- CUDA 12.1+ drivers
- Internet connection for model downloads

### Install NVIDIA Container Toolkit

```bash
# Add NVIDIA GPG key
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | \
  sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

# Add repository
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

# Install
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit

# Configure Docker runtime
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker

# Verify
docker run --rm --gpus all nvidia/cuda:12.1-base nvidia-smi
```

## Quick Start

### 1. Pull SGLang Docker Image

```bash
# Official SGLang image
docker pull lmsysorg/sglang:latest

# Or NVIDIA NGC image (optimized)
docker pull nvcr.io/nvidia/sglang:25.04
```

### 2. Get Model Weights

**Option A: Swan Model Repository (Recommended)**

Download verified weights from Swan's repository. See [Using Swan Model Repository](#using-swan-model-repository-recommended) below for the full workflow.

```bash
computing-provider models catalog                        # Browse available models
computing-provider models download Qwen/Qwen2.5-7B-Instruct  # Download with hash verification
```

**Option B: Direct from HuggingFace**

SGLang can download directly from HuggingFace (slower, no hash verification, may require auth for gated models).

### 3. Start SGLang Server

```bash
# With Swan Model Repository weights (recommended):
docker run -d \
  --name sglang-qwen \
  --gpus all \
  --shm-size 32g \
  --ipc=host \
  -p 30000:30000 \
  -v ~/.swan/models/Qwen/Qwen2.5-7B-Instruct:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name Qwen/Qwen2.5-7B-Instruct

# Or with direct HuggingFace download:
docker run -d \
  --name sglang-qwen \
  --gpus all \
  --shm-size 32g \
  --ipc=host \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-7B-Instruct \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name Qwen/Qwen2.5-7B-Instruct
```

### 3. Verify Server

```bash
# Health check
curl http://localhost:30000/health

# List models
curl http://localhost:30000/v1/models

# Test inference
curl http://localhost:30000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen-2.5-7b",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 100
  }'
```

## Model Deployment Examples

> **Note:** Qwen and DeepSeek models are fully open and download automatically without authentication.

### Qwen 2.5 3B (8GB VRAM) - No Auth Required

```bash
docker run -d \
  --name sglang-qwen-3b \
  --gpus all \
  --shm-size 16g \
  --ipc=host \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-3B-Instruct \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name qwen-2.5-3b \
    --mem-fraction-static 0.9
```

### Qwen 2.5 7B (16GB VRAM) - No Auth Required

```bash
docker run -d \
  --name sglang-qwen-7b \
  --gpus all \
  --shm-size 32g \
  --ipc=host \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-7B-Instruct \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name qwen-2.5-7b \
    --mem-fraction-static 0.85
```

### Qwen 2.5 14B (24GB VRAM) - No Auth Required

```bash
docker run -d \
  --name sglang-qwen-14b \
  --gpus all \
  --shm-size 32g \
  --ipc=host \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-14B-Instruct \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name qwen-2.5-14b \
    --mem-fraction-static 0.9
```

### Qwen 2.5 72B (Multi-GPU with Tensor Parallelism) - No Auth Required

```bash
docker run -d \
  --name sglang-qwen-72b \
  --gpus all \
  --shm-size 64g \
  --ipc=host \
  -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-72B-Instruct \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name qwen-2.5-72b \
    --tp 4 \
    --mem-fraction-static 0.9
```

### DeepSeek V3 (FP8 Quantization)

```bash
docker run -d \
  --name sglang-deepseek \
  --gpus all \
  --shm-size 64g \
  --ipc=host \
  -p 30003:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path deepseek-ai/DeepSeek-V3 \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name deepseek-v3 \
    --tp 8 \
    --quantization fp8
```

## Swan Inference Integration

### Architecture (No Public IP Required)

Inference providers connect **outbound** to Swan Inference via WebSocket. This means:
- No public IP address required
- Works behind NAT/firewalls
- No port forwarding needed

```
Swan Inference (wss://inference-ws.swanchain.io)
         │
         │ Outbound WebSocket
         ▼
┌─────────────────────────────────────┐
│  Provider (can be behind NAT)       │
│  InferenceClient ──► SGLang (localhost)  │
└─────────────────────────────────────┘
```

### Configure Computing Provider

Edit `~/.swan/computing/config.toml`:

```toml
[Inference]
Enable = true
WebSocketURL = "wss://inference-ws.swanchain.io"
Models = ["Qwen/Qwen2.5-7B-Instruct", "Qwen/Qwen2.5-72B-Instruct"]
```

### Model-to-Container Mapping

Create a mapping file `~/.swan/computing/models.json`:

```json
{
  "Qwen/Qwen2.5-3B-Instruct": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 8000,
    "category": "text-generation",
    "local_model": "qwen-2.5-3b"
  },
  "Qwen/Qwen2.5-7B-Instruct": {
    "endpoint": "http://localhost:30001",
    "gpu_memory": 16000,
    "category": "text-generation",
    "local_model": "qwen-2.5-7b"
  },
  "Qwen/Qwen2.5-72B-Instruct": {
    "endpoint": "http://localhost:30002",
    "gpu_memory": 160000,
    "category": "text-generation",
    "local_model": "qwen-2.5-72b"
  }
}
```

> **Note:** Model IDs use HuggingFace repo IDs as keys. The `local_model` field maps to the `--served-model-name` used in your SGLang server. The `endpoint` URL points to your local SGLang server.

### Start Inference Daemon

```bash
# Start SGLang containers first
docker start sglang-qwen-7b sglang-qwen-72b

# Then start computing provider
export CP_PATH=~/.swan/computing
computing-provider run
```

## Performance Tuning

### Memory Optimization

```bash
# Adjust memory fraction (default 0.9)
--mem-fraction-static 0.85    # Leave more for KV cache

# Enable chunked prefill for long contexts
--chunked-prefill-size 8192

# Set max concurrent requests
--max-running-requests 64
```

### Throughput Optimization

```bash
# Enable FlashInfer backend (faster attention)
--attention-backend flashinfer

# Enable CUDA graphs (reduces kernel launch overhead)
--disable-cuda-graph false

# Batch size tuning
--max-num-seqs 256
```

### Latency Optimization

```bash
# Reduce first token latency
--schedule-policy lpm    # Longest prefix match

# Enable prefix caching
--enable-prefix-caching

# Speculative decoding (if supported)
--speculative-model Qwen/Qwen2.5-3B-Instruct
```

## Monitoring

### Enable Metrics Endpoint

```bash
docker run -d \
  --name sglang-qwen \
  --gpus all \
  -p 8000:8000 \
  -p 9090:9090 \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-7B-Instruct \
    --host 0.0.0.0 \
    --port 8000 \
    --served-model-name qwen-2.5-7b \
    --enable-metrics \
    --metrics-port 9090
```

### Prometheus Metrics

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'sglang'
    static_configs:
      - targets: ['localhost:9090']
```

### Key Metrics

| Metric | Description |
|--------|-------------|
| `sglang_num_running_requests` | Active inference requests |
| `sglang_num_waiting_requests` | Queued requests |
| `sglang_token_throughput` | Tokens per second |
| `sglang_time_to_first_token_seconds` | TTFT latency |
| `sglang_gpu_memory_used_bytes` | GPU memory usage |

## Docker Compose Setup

Create `docker-compose.yml` for multi-model deployment:

```yaml
version: '3.8'

services:
  sglang-qwen-7b:
    image: lmsysorg/sglang:latest
    container_name: sglang-qwen-7b
    runtime: nvidia
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              device_ids: ['0']
              capabilities: [gpu]
    shm_size: '32g'
    ipc: host
    ports:
      - "8000:8000"
    volumes:
      - ~/.cache/huggingface:/root/.cache/huggingface
    command: >
      python3 -m sglang.launch_server
      --model-path Qwen/Qwen2.5-7B-Instruct
      --host 0.0.0.0 --port 8000
      --served-model-name qwen-2.5-7b
      --mem-fraction-static 0.85
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/health"]
      interval: 30s
      timeout: 10s
      retries: 3

  sglang-qwen-14b:
    image: lmsysorg/sglang:latest
    container_name: sglang-qwen-14b
    runtime: nvidia
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              device_ids: ['1']
              capabilities: [gpu]
    shm_size: '32g'
    ipc: host
    ports:
      - "8001:8000"
    volumes:
      - ~/.cache/huggingface:/root/.cache/huggingface
    command: >
      python3 -m sglang.launch_server
      --model-path Qwen/Qwen2.5-14B-Instruct
      --host 0.0.0.0 --port 8000
      --served-model-name qwen-2.5-14b
      --mem-fraction-static 0.9
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/health"]
      interval: 30s
      timeout: 10s
      retries: 3
```

Start with:

```bash
docker compose up -d
```

## Troubleshooting

### Common Issues

**Out of Memory (OOM)**
```bash
# Reduce memory fraction
--mem-fraction-static 0.8

# Use quantization
--quantization fp8    # Requires Ada/Hopper GPUs
--quantization awq    # Works on all GPUs
```

**Slow First Request**
```bash
# Model loading is slow on first request
# Pre-warm with a dummy request after startup
curl http://localhost:8000/v1/chat/completions \
  -d '{"model":"qwen-2.5-7b","messages":[{"role":"user","content":"hi"}],"max_tokens":1}'
```

**Container Keeps Restarting**
```bash
# Check logs
docker logs sglang-qwen-7b

# Common causes:
# - Insufficient GPU memory
# - CUDA version mismatch
```

**FlashInfer Errors**
```bash
# Use triton backend instead
--attention-backend triton
```

### Health Check Script

```bash
#!/bin/bash
# check_sglang.sh

CONTAINERS=("sglang-qwen-7b" "sglang-qwen-14b")
PORTS=(8000 8001)

for i in "${!CONTAINERS[@]}"; do
  container="${CONTAINERS[$i]}"
  port="${PORTS[$i]}"

  if curl -sf "http://localhost:${port}/health" > /dev/null; then
    echo "[OK] ${container} is healthy"
  else
    echo "[ERROR] ${container} is not responding"
    docker logs --tail 20 "${container}"
  fi
done
```

## Using Swan Model Repository (Recommended)

Swan provides a self-hosted model repository with verified weights on NebulaBlock cloud storage. **This is the recommended way to get model weights** — it ensures all providers use canonical, unmodified weights with SHA256 hash verification.

### Step 1: Browse Available Models

```bash
# See what models are available for download
computing-provider models catalog

# Example output:
# +--------------------------------------+----------+-------+---------+----------------+
# |              MODEL ID                | CATEGORY | FILES |  SIZE   |     STATUS     |
# +--------------------------------------+----------+-------+---------+----------------+
# | Qwen/Qwen2.5-0.5B-Instruct          |   llm    |    10 | 953 MB  | not downloaded |
# | meta-llama/Llama-3.1-8B-Instruct     |   llm    |    20 | 16.1 GB | downloaded     |
# +--------------------------------------+----------+-------+---------+----------------+

# JSON output for scripting
computing-provider models catalog --json
```

### Step 2: Download Model Weights

```bash
# Download a model (files are verified after download)
computing-provider models download Qwen/Qwen2.5-7B-Instruct

# Download to a custom directory
computing-provider models download --dest /data/models Qwen/Qwen2.5-7B-Instruct
```

Default download location: `~/.swan/models/<model-id>`

### Step 3: Verify Weights (Optional)

```bash
# Re-verify integrity of local weights at any time
computing-provider models verify Qwen/Qwen2.5-7B-Instruct
```

### Step 4: Start SGLang with Local Weights

```bash
# Mount the downloaded weights into the SGLang container
docker run -d --gpus all --shm-size 32g --ipc=host \
  -p 30000:30000 \
  -v ~/.swan/models/Qwen/Qwen2.5-7B-Instruct:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --served-model-name Qwen/Qwen2.5-7B-Instruct
```

### Step 5: List Local Models

```bash
# See what you have downloaded
computing-provider models list
```

### Benefits

- **Verified weights**: Every file is SHA256 hash-verified against the Swan registry
- **Resume support**: Interrupted downloads resume automatically (verified files are skipped)
- **No HuggingFace auth**: Downloads from Swan's public S3 bucket, no tokens needed
- **Consistent source**: All providers use the same canonical weights
- **Faster downloads**: NebulaBlock S3 provides reliable, fast downloads without HuggingFace rate limits

## vLLM Alternative

If SGLang doesn't work for your setup, vLLM is a reliable alternative:

```bash
# Qwen 2.5 7B (no HuggingFace auth required)
docker run -d \
  --name vllm-qwen \
  --runtime nvidia \
  --gpus all \
  -p 8000:8000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --ipc=host \
  vllm/vllm-openai:latest \
  --model Qwen/Qwen2.5-7B-Instruct \
  --host 0.0.0.0 \
  --port 8000 \
  --served-model-name qwen-2.5-7b \
  --gpu-memory-utilization 0.9
```

vLLM advantages:
- Simpler setup
- Lower time-to-first-token
- More mature documentation

## References

- [SGLang Documentation](https://docs.sglang.ai/)
- [SGLang GitHub](https://github.com/sgl-project/sglang)
- [SGLang Docker Hub](https://hub.docker.com/r/lmsysorg/sglang)
- [NVIDIA NGC SGLang](https://catalog.ngc.nvidia.com/orgs/nvidia/containers/sglang)
- [HuggingFace Model Hub](https://huggingface.co/models)
