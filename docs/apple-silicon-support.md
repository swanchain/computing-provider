# Apple Silicon (M1/M2/M3/M4) Support

This document outlines how to run Swan Chain Computing Provider on Apple Silicon Macs.

## Overview

Apple Silicon Macs (M1, M2, M3, M4) can serve as **Inference Provider** nodes for Swan Chain. Due to Docker's limitations with GPU passthrough on macOS, the recommended approach is native inference using **Ollama** with Metal acceleration.

## Recommended Setup: Ollama

[Ollama](https://ollama.com) is the recommended inference backend for macOS. It provides:
- Native Metal GPU acceleration
- Easy model management
- OpenAI-compatible API
- Simple installation via Homebrew

## Current Limitations

### Docker GPU Access

Docker Desktop on macOS does **not** expose the Apple GPU to containers. Containers only have access to the ARM CPU (or emulated x86 CPU via Rosetta).

| Approach | GPU Access | Performance | Recommended |
|----------|-----------|-------------|-------------|
| Docker Desktop | No | CPU only (~5-10 tok/s) | No |
| **Ollama** | Full Metal | 100% (~50-150 tok/s) | **Yes** |
| Native llama.cpp | Full Metal | 100% (~50-150 tok/s) | Yes |

### Why Native is Better

- **Ollama/Native Metal**: 50-150 tokens/second on M3/M4
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

## Quick Start (Ollama)

### Step 1: Install Ollama

```bash
# Install via Homebrew
brew install ollama

# Or download from https://ollama.com/download
```

### Step 2: Start Ollama and Pull Models

```bash
# Start Ollama service
ollama serve &

# Pull a model (example: Llama 3.2 3B)
ollama pull llama3.2:3b

# Or pull larger models based on your hardware
ollama pull llama3.1:8b    # Requires 8GB+ RAM
ollama pull llama3.3:70b   # Requires 48GB+ RAM
```

### Step 3: Install Computing Provider

```bash
# Install Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Install Go
brew install go

# Clone repository
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider

# Build for Apple Silicon
make clean && make mainnet
sudo make install
```

### Step 4: Run the Setup Wizard

The setup wizard handles configuration, authentication, and model discovery automatically:

```bash
computing-provider setup
```

The wizard will:
1. Check prerequisites (Ollama, Docker)
2. Create or login to your Swan Inference account
3. Auto-discover your Ollama models
4. Auto-match them to Swan Inference model IDs (e.g., `llama3.2:3b` → `meta-llama/Llama-3.2-3B-Instruct`)
5. Generate `config.toml` and `models.json`

### Step 5: Start the Provider

```bash
computing-provider run

# Or run in background
nohup computing-provider run >> cp.log 2>&1 &
```

### Step 6: Verify It's Working

```bash
# Check provider logs
tail -f cp.log

# You should see:
# - "Connected to Swan Inference"
# - "Registration successful"
# - Heartbeat messages
```

### Manual Configuration (Alternative)

If you prefer manual setup instead of the wizard:

```bash
# Initialize
computing-provider init --node-name=my-mac-provider

# Create models.json manually
cat > ~/.swan/computing/models.json << 'EOF'
{
  "meta-llama/Llama-3.2-3B-Instruct": {
    "endpoint": "http://localhost:11434",
    "gpu_memory": 4000,
    "category": "text-generation",
    "local_model": "llama3.2:3b"
  }
}
EOF

# Edit config.toml to add your API key and models
```

> **Note:** Model IDs use HuggingFace repo IDs (e.g., `meta-llama/Llama-3.2-3B-Instruct`). The `local_model` field maps to Ollama's local model name.

## Alternative: llama.cpp

If you prefer llama.cpp over Ollama:

### Install llama.cpp

```bash
# Install via Homebrew
brew install llama.cpp

# Or build from source for latest features
git clone https://github.com/ggml-org/llama.cpp
cd llama.cpp
make GGML_METAL=1
```

### Download Models

```bash
# Create models directory
mkdir -p $CP_PATH/models

# Download a model (example: Llama 3.1 8B)
huggingface-cli download TheBloke/Llama-2-7B-GGUF llama-2-7b.Q4_K_M.gguf \
    --local-dir $CP_PATH/models
```

## Configuration

### config.toml for Apple Silicon

```toml
[API]
Port = 9085
MultiAddress = "/ip4/127.0.0.1/tcp/9085"
NodeName = "my-mac-provider"

[Inference]
Enable = true
WebSocketURL = "wss://inference-ws.swanchain.io"

[RPC]
SwanChainRPC = "https://mainnet-rpc.swanchain.io"
```

### models.json for Ollama

```json
{
  "meta-llama/Llama-3.2-3B-Instruct": {
    "endpoint": "http://localhost:11434",
    "gpu_memory": 4000,
    "category": "text-generation",
    "local_model": "llama3.2:3b"
  }
}
```

## Running the Provider

### Option 1: Ollama (Recommended)

```bash
# Terminal 1: Ensure Ollama is running
ollama serve

# Terminal 2: Start the computing provider
export CP_PATH=~/.swan/computing
computing-provider run

# The provider will:
# 1. Connect to Swan Inference via WebSocket
# 2. Register available models from models.json
# 3. Forward inference requests to Ollama
```

### Option 2: Manual llama-server + Provider

```bash
# Terminal 1: Start llama-server
llama-server \
    -m ~/.swan/computing/models/llama-2-7b.Q4_K_M.gguf \
    --port 8080 \
    -ngl 99 \
    -c 4096

# Terminal 2: Update models.json to point to llama-server
# Then start provider
computing-provider run
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

### Ollama Not Running

```bash
# Check if Ollama is running
pgrep -l ollama

# Start Ollama service
ollama serve

# Or restart via brew services
brew services restart ollama
```

### Model Not Found

```bash
# List available models
ollama list

# Pull the required model
ollama pull llama3.2:3b

# Create an alias if model name doesn't match
ollama cp llama3.1:latest llama-3.2-3b
```

### Metal Not Detected

```bash
# Verify Metal support
system_profiler SPDisplaysDataType | grep Metal

# Check Ollama is using Metal (check GPU usage in Activity Monitor)
```

### Out of Memory

```bash
# Use a smaller model
ollama pull llama3.2:1b

# Or use quantized versions
ollama pull llama3.1:8b-q4_0
```

### Slow Performance

1. Verify Ollama is using Metal (check Activity Monitor > GPU)
2. Close other GPU-intensive apps
3. Use smaller quantized models
4. Ensure using ARM64 binary (not Rosetta)

```bash
# Verify binary architecture
file $(which computing-provider)
# Should show: Mach-O 64-bit executable arm64
```

### Connection Issues

```bash
# Test WebSocket connection
curl -I https://inference-ws.swanchain.io

# Check provider logs
tail -f cp.log | grep -E "error|warning"
```

### Provider Not Receiving Requests

```bash
# Verify models.json is correctly configured
cat $CP_PATH/models.json

# Test Ollama endpoint directly
curl http://localhost:11434/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "llama-3.2-3b", "messages": [{"role": "user", "content": "Hello"}]}'

# Check provider registration
tail -f cp.log | grep -E "Registration|connected"
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

- [Ollama](https://ollama.com) - Recommended inference backend for macOS
- [Ollama GitHub](https://github.com/ollama/ollama)
- [Ollama Model Library](https://ollama.com/library)
- [llama.cpp GitHub](https://github.com/ggml-org/llama.cpp)
- [llama.cpp Performance on Apple Silicon](https://github.com/ggml-org/llama.cpp/discussions/4167)
- [Apple Silicon vs NVIDIA CUDA](https://scalastic.io/en/apple-silicon-vs-nvidia-cuda-ai-2025/)
