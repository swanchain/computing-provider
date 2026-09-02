# Getting Started

This guide will walk you through the complete setup process for your Computing Provider.

## Quick Start (Inference Mode)

Inference mode is the simplest way to get started. No wallet, no blockchain, no public IP required.

### Prerequisites

```bash
# Ubuntu/Debian - install build tools
sudo apt-get update && sudo apt-get install -y git make

# Install Go 1.21+ (https://go.dev/dl/)
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# Verify installation
go version  # Should show go1.22.0 or higher
```

### Install & Run

```bash
# 1. Clone and build
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider
make clean && make mainnet && sudo make install

# 2. Start your inference backend (Ollama, SGLang, vLLM, etc.)
# See examples below

# 3. Run the setup wizard (handles auth, config, and model discovery)
computing-provider setup

# 4. Run the provider
computing-provider run
```

That's it! The setup wizard will:
- Check prerequisites (Docker, GPU, Ollama)
- Create or login to your Swan Inference account
- Auto-discover running model servers
- Auto-match local models to Swan Inference model IDs
- Generate `config.toml` and `models.json`

## Inference Mode Setup (Linux)

### 1. Install Docker with NVIDIA Container Toolkit

```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

### 2. Start an Inference Server

```bash
# Example: SGLang with Qwen 2.5 3B (no HuggingFace auth required)
docker run -d --gpus all -p 30000:30000 --name sglang \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --shm-size 32g --ipc=host \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path Qwen/Qwen2.5-3B-Instruct \
    --host 0.0.0.0 --port 30000 --served-model-name Qwen/Qwen2.5-3B-Instruct
```

### 3. Run Setup Wizard

```bash
computing-provider setup
```

The wizard will auto-discover your running model server and configure everything.

### 4. Start Provider

```bash
# Start the provider
nohup computing-provider run >> cp.log 2>&1 &

# Check if running
tail -f cp.log
```

## macOS Setup (Apple Silicon with Ollama)

For Apple Silicon Macs (M1/M2/M3/M4), use Ollama instead of Docker for native Metal GPU acceleration.

### 1. Install Ollama

```bash
# Install via Homebrew
brew install ollama

# Or download from https://ollama.com/download
```

### 2. Pull Models

```bash
# Start Ollama service
ollama serve &

# Pull a model
ollama pull llama3.2:3b
```

### 3. Install Computing Provider

```bash
# Install Go
brew install go

# Clone and build
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider
make clean && make mainnet
sudo make install
```

### 4. Run Setup Wizard

```bash
computing-provider setup
```

The wizard will:
- Detect Ollama and your pulled models
- Auto-match models to Swan Inference IDs (e.g., `llama3.2:3b` → `meta-llama/Llama-3.2-3B-Instruct`)
- Create your Swan Inference account
- Generate all config files

### 5. Start Provider

```bash
# Ensure Ollama is running
ollama serve &

# Start provider
nohup computing-provider run >> cp.log 2>&1 &
```

For detailed macOS instructions, see [Apple Silicon Support](apple-silicon-support.md).

## Start Your Provider

### 1. Verify Configuration

```bash
# Check provider information
computing-provider info

# Check provider state
computing-provider state
```

### 2. Start the Service

```bash
# Set environment variable
export CP_PATH=~/.swan/computing

# Start the provider
nohup computing-provider run >> cp.log 2>&1 &

# Check if it's running
ps aux | grep computing-provider
```

### 3. Monitor Your Provider

```bash
# Check logs
tail -f cp.log

# Check status
computing-provider state

# List tasks
computing-provider task list
```

## Verification and Monitoring

### Check Provider Status

```bash
# Provider information
computing-provider info

# Provider state
computing-provider state
```

### Monitor Performance

```bash
# System resources
htop
nvidia-smi  # GPU monitoring

# Docker containers
docker ps -a
```

### Check Logs

```bash
# Provider logs
tail -f cp.log

# Docker container logs
docker logs <container_name>
```

## Troubleshooting

### Common Startup Issues

1. **Docker Permission Error** (Linux only)
   ```bash
   # Add user to docker group
   sudo usermod -aG docker $USER
   # Or run with sg
   sg docker -c "computing-provider run"
   ```

2. **NVIDIA Container Toolkit Not Found**
   ```bash
   # Reinstall nvidia-container-toolkit
   sudo apt-get install -y nvidia-container-toolkit
   sudo nvidia-ctk runtime configure --runtime=docker
   sudo systemctl restart docker
   ```

3. **Container Already Exists**
   ```bash
   # Remove existing container
   docker rm -f resource-exporter
   ```

4. **Ollama Connection Failed** (macOS)
   ```bash
   # Ensure Ollama is running
   ollama serve

   # Test endpoint
   curl http://localhost:11434/v1/models
   ```

### Getting Help

```bash
# Show all available commands
computing-provider --help

# Help for a specific command
computing-provider setup --help
computing-provider inference --help

# Help for a subcommand
computing-provider task list --help
```

- [CLI Reference](cli/README.md)
- [Discord Community](https://discord.gg/swanchain)
- [GitHub Issues](https://github.com/swanchain/computing-provider/issues)
- [Troubleshooting Guide](troubleshooting.md)

## Next Steps

After successfully starting your provider:

1. **Monitor Performance**: Track task completion rates and earnings
2. **Optimize Resources**: Adjust resource allocation based on usage
3. **Scale Up**: Consider adding more hardware for increased capacity
4. **Join Community**: Connect with other providers on Discord
