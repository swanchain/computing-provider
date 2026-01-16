# Getting Started

This guide will walk you through the complete setup process for your Computing Provider, from installation to running your first tasks.

## Quick Start Overview

1. [Install Dependencies](prerequisites.md)
2. [Install Computing Provider](installation.md)
3. [Configure Your Environment](configuration.md)
4. [Set Up Your Wallet](#set-up-wallet)
5. [Choose Provider Mode](#choose-provider-mode)
6. [Start Your Provider](#start-your-provider)

## Choose Provider Mode

The Computing Provider supports two modes:

### Inference Mode (Default)
- **Best for**: AI model inference, containerized workloads
- **Hardware**: GPU with 8GB+ VRAM, Docker with NVIDIA Container Toolkit
- **Setup**: Simple Docker-based setup, no public IP required
- **Tasks**: AI inference via Swan Inference marketplace

### ECP - ZK Proof Generation
- **Best for**: ZK-SNARK proof generation, FIL-C2 proofs
- **Hardware**: GPU with 8GB+ VRAM, 200GB+ storage for v28 parameters
- **Setup**: Requires v28 parameter files
- **Tasks**: Filecoin C2 proofs, mining proofs

## Initial Setup

### 1. Initialize Repository

```bash
# Initialize for Inference mode (no public IP required)
computing-provider init --node-name=<NAME>

# Or with public IP for ZK-proof mode
computing-provider init --multi-address=/ip4/<PUBLIC_IP>/tcp/<PORT> --node-name=<NAME>

# Verify initialization
ls -la ~/.swan/computing/
```

### 2. Set Up Wallet

```bash
# Create new wallet
computing-provider wallet new

# Or import existing wallet
computing-provider wallet import <private_key_file>

# List wallets
computing-provider wallet list
```

### 3. Create Account

```bash
# For Inference mode - task type 4
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 4

# For ZK proofs (ECP) - task types 1,2,4
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 1,2,4
```

### 4. Add Collateral

```bash
# Add collateral
computing-provider collateral add --ecp --from <OWNER_ADDRESS> <AMOUNT>

# Check collateral status
computing-provider info
```

## Inference Mode Setup

### Prerequisites

1. **Install Docker with NVIDIA Container Toolkit**:

```bash
# Install NVIDIA Container Toolkit
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

2. **Configure domain and ports** in `$CP_PATH/config.toml`:

```toml
[API]
Domain = "*.example.com"
PortRange = ["40000-40050", "40060"]
```

### Start Inference Provider

```bash
# Set environment variable
export CP_PATH=~/.swan/computing

# Start the provider
nohup computing-provider run >> cp.log 2>&1 &

# Check if running
ps aux | grep computing-provider
```

## ECP Setup (ZK Proofs)

### Prerequisites

1. **Download v28 parameters** (~200GB):

```bash
# Set parameter path
export FIL_PROOFS_PARAMETER_CACHE=/path/to/v28/params

# Download parameters (see Filecoin documentation)
```

2. **Configure GPU**:

```bash
export RUST_GPU_TOOLS_CUSTOM_GPU="<GPU_MODEL>:<CORES>"
# Example: "GeForce RTX 4090:16384"
```

### Configure Sequencer

Edit `$CP_PATH/config.toml`:

```toml
[UBI]
EnableSequencer = true
AutoChainProof = false
```

### Add Sequencer Deposit

```bash
computing-provider sequencer add --from <OWNER_ADDRESS> <AMOUNT>
```

### Start ECP Provider

```bash
# Set environment variables
export CP_PATH=~/.swan/computing
export FIL_PROOFS_PARAMETER_CACHE=/path/to/v28/params
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"

# Start the provider
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

1. **Docker Permission Error**
   ```bash
   # Add user to docker group
   sudo usermod -aG docker $USER
   # Or run with sg
   sg docker -c "computing-provider ubi daemon"
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

4. **CP Account Empty**
   ```bash
   # Create account first
   computing-provider account create ...
   ```

### Getting Help

- [Discord Community](https://discord.gg/swanchain)
- [GitHub Issues](https://github.com/swanchain/computing-provider-v2/issues)
- [Troubleshooting Guide](troubleshooting.md)

## Next Steps

After successfully starting your provider:

1. **Monitor Performance**: Track task completion rates and earnings
2. **Optimize Resources**: Adjust resource allocation based on usage
3. **Scale Up**: Consider adding more hardware for increased capacity
4. **Join Community**: Connect with other providers on Discord
