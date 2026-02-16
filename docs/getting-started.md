# Getting Started

This guide will walk you through the complete setup process for your Computing Provider.

## Quick Start (Inference Mode)

Inference mode is the simplest way to get started. No wallet, no blockchain, no public IP required.

```bash
# 1. Install
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider
make clean && make mainnet && sudo make install

# 2. Initialize
export CP_PATH=~/.swan/computing
computing-provider init --node-name=my-provider

# 3. Get your Provider API Key
#    Sign up at https://inference.swanchain.io and get your API key (starts with sk-prov-)
#    Add it to ~/.swan/computing/config.toml under [Inference]:
#    ApiKey = "sk-prov-your-key-here"

# 4. Configure models.json (see below)

# 5. Enable your models in config.toml
#    Add model names to the Models array under [Inference]:
#    Models = ["llama-3.2-3b"]

# 6. Start your inference backend (Ollama, vLLM, etc.)

# 7. Run
computing-provider run
```

That's it! Your provider will connect to Swan Inference and start receiving requests.

## Choose Provider Mode

| Mode | Use Case | Requirements | Blockchain |
|------|----------|--------------|------------|
| **Inference** (Default) | AI model inference | Ollama/Docker | Not required |
| **ECP** (ZK Proofs) | FIL-C2 proofs | NVIDIA GPU, v28 params | Required |

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

### 2. Initialize

```bash
export CP_PATH=~/.swan/computing
computing-provider init --node-name=my-provider
```

### 3. Set Up Provider API Key

Sign up at https://inference.swanchain.io to get your provider API key (starts with `sk-prov-`).

Add it to `$CP_PATH/config.toml`:

```toml
[Inference]
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"
```

Or set via environment variable:
```bash
export INFERENCE_API_KEY=sk-prov-xxxxxxxxxxxxxxxxxxxx
```

### 4. Configure Models

Create `$CP_PATH/models.json`:

```json
{
  "llama-3.1-8b": {
    "endpoint": "http://localhost:8000",
    "gpu_memory": 8000,
    "category": "text-generation"
  }
}
```

Then enable the model in `$CP_PATH/config.toml`:

```toml
[Inference]
Models = ["llama-3.1-8b"]
```

### 5. Start Provider

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

### 4. Initialize and Configure

```bash
# Initialize (no public IP required)
export CP_PATH=~/.swan/computing
computing-provider init --node-name=my-mac-provider
```

Add your provider API key to `$CP_PATH/config.toml`:

```toml
[Inference]
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"
```

Create `$CP_PATH/models.json`:

```json
{
  "llama-3.2-3b": {
    "endpoint": "http://localhost:11434",
    "gpu_memory": 4000,
    "category": "text-generation"
  }
}
```

Enable the model in `$CP_PATH/config.toml`:

```toml
[Inference]
Models = ["llama-3.2-3b"]
```

### 5. Start Provider

```bash
# Ensure Ollama is running
ollama serve &

# Start provider
export CP_PATH=~/.swan/computing
nohup computing-provider run >> cp.log 2>&1 &
```

For detailed macOS instructions, see [Apple Silicon Support](apple-silicon-support.md).

## ECP Setup (ZK Proofs)

ECP mode requires blockchain registration for proof submission and rewards.

### 1. Prerequisites

Download v28 parameters (~200GB):

```bash
export FIL_PROOFS_PARAMETER_CACHE=/path/to/v28/params
# Download parameters (see Filecoin documentation)
```

### 2. Initialize with Public IP

```bash
computing-provider init --multi-address=/ip4/<PUBLIC_IP>/tcp/<PORT> --node-name=<NAME>
```

### 3. Set Up Wallet

```bash
# Create new wallet (can be done offline)
computing-provider wallet new

# List wallets
computing-provider wallet list
```

### 4. Create Account (On-Chain)

```bash
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 1,2,4
```

### 5. Add Collateral

```bash
computing-provider collateral add --ecp --from <OWNER_ADDRESS> <AMOUNT>
```

### 6. Configure Sequencer

Edit `$CP_PATH/config.toml`:

```toml
[UBI]
EnableSequencer = true
AutoChainProof = false
```

Add sequencer deposit:

```bash
computing-provider sequencer add --from <OWNER_ADDRESS> <AMOUNT>
```

### 7. Start ECP Provider

```bash
export CP_PATH=~/.swan/computing
export FIL_PROOFS_PARAMETER_CACHE=/path/to/v28/params
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"

nohup computing-provider run >> cp.log 2>&1 &
```

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

## Monitoring Tasks

### View Task List

```bash
# List all tasks
computing-provider task list

# Show recent tasks
computing-provider task list --tail 10
```

### View Task Details

```bash
# Get task details
computing-provider task get <job_uuid>
```

### View UBI Tasks (ZK Proofs)

```bash
# List UBI tasks
computing-provider ubi list

# Show recent UBI tasks
computing-provider ubi list --tail 10
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

- [Discord Community](https://discord.gg/swanchain)
- [GitHub Issues](https://github.com/swanchain/computing-provider/issues)
- [Troubleshooting Guide](troubleshooting.md)

## Next Steps

After successfully starting your provider:

1. **Monitor Performance**: Track task completion rates and earnings
2. **Optimize Resources**: Adjust resource allocation based on usage
3. **Scale Up**: Consider adding more hardware for increased capacity
4. **Join Community**: Connect with other providers on Discord
