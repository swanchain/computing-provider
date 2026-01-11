# Apple Silicon (M1/M2/M3/M4) Support

This document outlines how to run Swan Chain Computing Provider on Apple Silicon Macs.

## Overview

Apple Silicon Macs (M1, M2, M3, M4) can serve as **ECP (Edge Computing Provider)** nodes for Swan Chain. Due to Docker's limitations with GPU passthrough on macOS, the recommended approach is native inference using llama.cpp with Metal acceleration.

## Current Limitations

### Docker GPU Access

Docker Desktop on macOS does **not** expose the Apple GPU to containers. Containers only have access to the ARM CPU (or emulated x86 CPU via Rosetta).

| Approach | GPU Access | Performance | Recommended |
|----------|-----------|-------------|-------------|
| Docker Desktop | No | CPU only (~5-10 tok/s) | No |
| Podman + Vulkan | Partial | ~60% of native | Complex setup |
| Native llama.cpp | Full Metal | 100% (~50-150 tok/s) | **Yes** |

### Why Native is Better

- **Native Metal**: 50-150 tokens/second on M3/M4
- **Docker CPU**: 5-10 tokens/second
- **Performance gap**: 10-15x faster with native execution

## Hardware Requirements

### Minimum

- Apple Silicon Mac (M1 or later)
- 8GB unified memory
- 20GB free storage
- macOS 13.0 (Ventura) or later

### Recommended

- M2 Pro/Max or M3/M4
- 16GB+ unified memory
- 50GB+ free storage
- macOS 14.0 (Sonoma) or later

## Installation

### Step 1: Install Dependencies

```bash
# Install Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Install Go
brew install go

# Install llama.cpp
brew install llama.cpp

# Or build from source for latest features
git clone https://github.com/ggml-org/llama.cpp
cd llama.cpp
make GGML_METAL=1
```

### Step 2: Build Computing Provider

```bash
# Clone repository
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider

# Build for Apple Silicon
make darwin-arm64

# Or build directly
GOOS=darwin GOARCH=arm64 go build -o computing-provider ./cmd/computing-provider
```

### Step 3: Initialize Configuration

```bash
# Set config path
export CP_PATH=~/.swan/computing

# Initialize
./computing-provider init

# Edit configuration
vim $CP_PATH/config.toml
```

### Step 4: Download Models

```bash
# Create models directory
mkdir -p $CP_PATH/models

# Download a model (example: Llama 3.1 8B)
# Use Hugging Face CLI or direct download
huggingface-cli download TheBloke/Llama-2-7B-GGUF llama-2-7b.Q4_K_M.gguf \
    --local-dir $CP_PATH/models
```

## Configuration

### config.toml for Apple Silicon

```toml
[API]
Port = 8085
MultiAddress = "/ip4/0.0.0.0/tcp/8085"
Domain = ""
NodeName = "my-m1-provider"

[ECP2]
Enable = true
ServiceURL = "https://ecp2.swanchain.io"
WebSocketURL = "wss://ecp2.swanchain.io/ws"
Models = ["llama-3.1-8b", "mistral-7b"]

[Inference]
# Native inference settings for Apple Silicon
Engine = "llama.cpp"
ModelsPath = "~/.swan/computing/models"
MetalEnabled = true
ContextSize = 4096
GPULayers = 99

[RPC]
ChainRPC = "https://mainnet-rpc.swanchain.io"
```

## Running the Provider

### Option 1: ECP Daemon with Native Inference

```bash
# Start the ECP daemon
./computing-provider ubi daemon

# The provider will:
# 1. Start llama-server with Metal acceleration
# 2. Connect to ECP2 service via WebSocket
# 3. Register available models
# 4. Handle inference requests
```

### Option 2: Manual llama-server + Provider

```bash
# Terminal 1: Start llama-server
llama-server \
    -m ~/.swan/computing/models/llama-2-7b.Q4_K_M.gguf \
    --port 8080 \
    -ngl 99 \
    -c 4096

# Terminal 2: Start provider (connects to local llama-server)
./computing-provider ubi daemon --inference-url http://localhost:8080
```

## Performance Tuning

### Optimal Settings by Model Size

| Model Size | GPU Layers (-ngl) | Context (-c) | Memory Required |
|------------|-------------------|--------------|-----------------|
| 7B Q4 | 99 | 4096 | 6GB |
| 7B Q8 | 99 | 4096 | 10GB |
| 13B Q4 | 99 | 4096 | 10GB |
| 13B Q8 | 99 | 2048 | 16GB |
| 70B Q4 | 40-60 | 2048 | 48GB+ |

### Recommended Quantization

- **Best quality/speed**: Q4_K_M or Q5_K_M
- **Faster, lower quality**: Q4_0
- **Higher quality**: Q8_0 (requires more memory)

### Memory Management

```bash
# Check available memory
sysctl hw.memsize

# Monitor during inference
sudo powermetrics --samplers gpu_power -i 1000
```

## Architecture Detection

The computing provider automatically detects Apple Silicon:

```go
// Detection logic
if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
    architecture = "ARM"  // Apple Silicon
    accelerator = "metal"
}
```

## API Compatibility

The native llama-server provides OpenAI-compatible endpoints:

```bash
# Chat completions
curl http://localhost:8080/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d '{
        "model": "llama-3.1-8b",
        "messages": [{"role": "user", "content": "Hello!"}]
    }'

# Completions
curl http://localhost:8080/v1/completions \
    -H "Content-Type: application/json" \
    -d '{
        "model": "llama-3.1-8b",
        "prompt": "Hello, world!"
    }'
```

## Troubleshooting

### Metal Not Detected

```bash
# Verify Metal support
system_profiler SPDisplaysDataType | grep Metal

# Check llama.cpp was built with Metal
llama-server --version
# Should show: Metal support: true
```

### Out of Memory

```bash
# Reduce context size
llama-server -m model.gguf -ngl 99 -c 2048

# Or reduce GPU layers
llama-server -m model.gguf -ngl 40 -c 4096

# Use smaller quantization
# Switch from Q8 to Q4_K_M
```

### Slow Performance

1. Verify Metal is enabled: `-ngl 99`
2. Check Activity Monitor for GPU usage
3. Close other GPU-intensive apps
4. Ensure using ARM64 binary (not Rosetta)

```bash
# Verify binary architecture
file ./computing-provider
# Should show: Mach-O 64-bit executable arm64
```

### Connection Issues

```bash
# Test WebSocket connection
websocat wss://ecp2.swanchain.io/ws

# Check network
curl -I https://ecp2.swanchain.io/health
```

## Docker Alternative (CPU-Only)

If you must use Docker (not recommended for inference):

```bash
# Pull ARM64 image
docker pull --platform linux/arm64 swanhub/ubi-worker-cpu-arm64:latest

# Run container
docker run -d \
    --name swan-provider \
    -p 8085:8085 \
    -v ~/.swan/computing:/root/.swan/computing \
    swanhub/ubi-worker-cpu-arm64:latest
```

**Note**: Docker containers on Apple Silicon cannot access the GPU. Performance will be 10-15x slower than native.

## Benchmarks

### M2 Pro (16GB) - Llama 2 7B Q4_K_M

| Metric | Native Metal | Docker CPU |
|--------|-------------|------------|
| Prompt Processing | 450 tok/s | 45 tok/s |
| Token Generation | 85 tok/s | 8 tok/s |
| Memory Usage | 5.2GB | 6.1GB |

### M3 Max (48GB) - Llama 2 70B Q4_K_M

| Metric | Native Metal |
|--------|-------------|
| Prompt Processing | 180 tok/s |
| Token Generation | 25 tok/s |
| Memory Usage | 42GB |

## Comparison with NVIDIA GPUs

| Hardware | Model | Tokens/sec | Notes |
|----------|-------|------------|-------|
| M2 Pro | 7B Q4 | 85 | Native Metal |
| M3 Max | 7B Q4 | 120 | Native Metal |
| RTX 4090 | 7B Q4 | 150 | CUDA |
| RTX 3080 | 7B Q4 | 90 | CUDA |
| M1 Docker | 7B Q4 | 8 | CPU only |

Apple Silicon is competitive with mid-range NVIDIA GPUs when using native Metal acceleration.

## References

- [llama.cpp GitHub](https://github.com/ggml-org/llama.cpp)
- [llama.cpp Performance on Apple Silicon](https://github.com/ggml-org/llama.cpp/discussions/4167)
- [Apple Silicon vs NVIDIA CUDA 2025](https://scalastic.io/en/apple-silicon-vs-nvidia-cuda-ai-2025/)
- [GPU-Accelerated Containers for M-series Macs](https://medium.com/@andreask_75652/gpu-accelerated-containers-for-m1-m2-m3-macs-237556e5fe0b)
- [LLM Performance: Native vs Docker on Mac](https://www.vchalyi.com/blog/2025/ollama-performance-benchmark-macos/)
