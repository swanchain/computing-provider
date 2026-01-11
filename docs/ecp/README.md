# Edge Computing Provider (ECP)

Edge Computing Provider (ECP) specializes in processing data at the source of data generation, using minimal latency setups ideal for real-time applications. Currently supports ZK-SNARK proof generation for the Filecoin network.

**Important:** ECP does **NOT** require Kubernetes. It runs as a standalone daemon using Docker containers.

## Overview

ECP handles specific, localized tasks directly on devices at the network's edge, such as IoT devices. It's designed for low-latency, real-time processing requirements.

### Current Supported Tasks

- **Filecoin C2 ZK-SNARK Proofs**: Generation of zero-knowledge proofs for Filecoin network
- **Mining Tasks**: Cryptocurrency mining workloads
- **Inference Tasks**: AI model inference
- **Future Support**: Aleo, Scroll, StarkNet, and other ZK proof types

### Task Types

| Type | ID | Description |
|------|-----|-------------|
| Fil-C2 | 1 | Filecoin C2 ZK-SNARK proofs |
| Mining | 2 | Mining workloads |
| Inference | 4 | AI inference (ECP2) |
| Exit | 100 | Exit provider status |

For ECP (ZK proofs), set task-types to `1,2,4`.
For ECP2 (inference only), set task-types to `4`.

## Prerequisites

### Hardware Requirements

- **CPU**: 4+ cores, 2.0 GHz+
- **RAM**: 8GB+ (16GB recommended)
- **Storage**: 200GB+ available space (for v28 parameters)
- **GPU**: NVIDIA GPU with 8GB+ VRAM (for ZK proof generation)
- **Network**: Stable internet connection with public IP

### Network Requirements

- **Port 9085** must be mapped to public network:
  ```
  <Intranet_IP>:9085 <--> <Public_IP>:<PORT>
  ```
- This allows the Swan network to assign ZK proof tasks to your provider

### Software Requirements

- **Operating System**: Linux (Ubuntu 20.04+ recommended)
- **Go**: Version 1.22+
- **Docker**: Latest stable version (required for ECP)
- **NVIDIA Drivers**: Latest stable version (470.xx+)
- **NVIDIA Container Toolkit**: Required for GPU access in Docker containers
- **CUDA**: Version 11.0+ (for GPU acceleration)

## Installation

### 1. Install Dependencies

```bash
# Install Go 1.22+
wget -c https://golang.org/dl/go1.22.0.linux-amd64.tar.gz -O - | sudo tar -xz -C /usr/local
echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc && source ~/.bashrc

# Install Docker
curl -fsSL https://get.docker.com | bash
sudo usermod -aG docker $USER
# Log out and back in for group changes to take effect

# Install NVIDIA drivers and CUDA
# Follow NVIDIA's official installation guide for your GPU
```

### 2. Install NVIDIA Container Toolkit

**Required** for ECP to access GPU in Docker containers:

```bash
# Add NVIDIA GPG key
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

# Add repository
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

# Install
sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit

# Configure Docker runtime
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

Verify installation:
```bash
nvidia-ctk --version
docker info | grep -i nvidia
```

### 3. Run Setup Script

```bash
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/setup.sh | bash
```

### 4. Download v28 Parameters (for ZK-FIL tasks)

```bash
# At least 200GB storage needed
export PARENT_PATH="<V28_PARAMS_PATH>"

# 512MiB parameters
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/fetch-param-512.sh | bash

# 32GiB parameters
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/fetch-param-32.sh | bash
```

### 5. Build Computing Provider

```bash
# Clone repository
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider
git checkout releases

# Build for mainnet
make clean && make mainnet
make install

# Or build for testnet
# make clean && make testnet
# make install
```

### 6. Initialize ECP

```bash
# Initialize repository with public IP and port
computing-provider init --multi-address=/ip4/<YOUR_PUBLIC_IP>/tcp/<YOUR_PORT> --node-name=<YOUR_NODE_NAME>
```

**Note:** By default, the CP repo is `~/.swan/computing`. Override with `export CP_PATH="<YOUR_CP_PATH>"`

## Configuration

### config.toml

Edit `$CP_PATH/config.toml`:

```toml
[API]
Port = 9085                                    # ECP service port (default: 9085)
MultiAddress = "/ip4/<PUBLIC_IP>/tcp/<PORT>"   # Public multiAddress for libp2p
Domain = ""                                    # Domain name for inference tasks
NodeName = ""                                  # Your provider node name
Pricing = "true"                               # Accept smart pricing orders
AutoDeleteImage = false                        # Auto-delete unused images
PortRange = ["40000-40050","40060"]            # Port range for multi-port inference tasks

[UBI]
UbiEnginePk = "0x594A4c5cF8e98E1aA5e9266F913dC74a24Eae0e9"   # UBI Engine public key
EnableSequencer = true                         # Submit proofs to Sequencer (reduces gas costs)
AutoChainProof = false                         # Fallback to chain when sequencer unavailable
SequencerUrl = "https://sequencer.swanchain.io"
EdgeUrl = "https://edge-api.swanchain.io/v1"
VerifySign = true

[RPC]
SWAN_CHAIN_RPC = "https://mainnet-rpc-01.swanchain.org"     # Swan chain RPC
```

### price.toml (Optional)

Generate and customize pricing:

```bash
# Generate default pricing config
computing-provider --repo <YOUR_CP_PATH> price generate

# View current pricing
computing-provider --repo <YOUR_CP_PATH> price view

# Edit pricing
vi $CP_PATH/price.toml
```

Example `price.toml`:
```toml
[API]
Pricing = "true"

TARGET_CPU = "0.2"            # SWAN/thread-hour
TARGET_MEMORY = "0.1"         # SWAN/GB-hour
TARGET_HD_EPHEMERAL = "0.005" # SWAN/GB-hour
TARGET_GPU_DEFAULT = "1.6"    # SWAN/GPU-hour
```

## Setup Process

### 1. Wallet Setup

```bash
# Generate a new wallet
computing-provider wallet new

# Or import existing wallet
computing-provider wallet import <private_key_file>

# List wallets
computing-provider wallet list
```

Deposit `SwanETH` to your wallet address. See [Swan Chain guide](https://docs.swanchain.io/swan-mainnet/getting-started-guide).

### 2. Create CP Account

```bash
computing-provider account create \
    --ownerAddress <YOUR_OWNER_ADDRESS> \
    --workerAddress <YOUR_WORKER_ADDRESS> \
    --beneficiaryAddress <YOUR_BENEFICIARY_ADDRESS> \
    --task-types 1,2,4
```

**Note:** For ECP, set `--task-types` to `1,2,4` (Fil-C2, Mining, Inference).

### 3. Add Collateral

```bash
# Add SWAN collateral for ECP
computing-provider collateral add --ecp --from <YOUR_WALLET_ADDRESS> <AMOUNT>
```

To withdraw collateral:
```bash
computing-provider collateral withdraw --ecp --owner <YOUR_WALLET_ADDRESS> --account <YOUR_CP_ACCOUNT> <AMOUNT>
```

### 4. Deposit to Sequencer Account

The Sequencer batches proof submissions to reduce gas costs:

```bash
computing-provider sequencer add --from <YOUR_WALLET_ADDRESS> <AMOUNT>
```

To withdraw from Sequencer:
```bash
computing-provider sequencer withdraw --owner <YOUR_OWNER_WALLET_ADDRESS> <AMOUNT>
```

## Running ECP

### Required Environment Variables

```bash
# Path to v28 parameters
export FIL_PROOFS_PARAMETER_CACHE=<path_to_v28_params>

# GPU model and CUDA cores (update for your GPU)
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"
```

Common GPU configurations:
| GPU Model | CUDA Cores | Configuration |
|-----------|------------|---------------|
| RTX 4090 | 16384 | `"GeForce RTX 4090:16384"` |
| RTX 3090 | 10496 | `"GeForce RTX 3090:10496"` |
| RTX 3080 | 8704 | `"GeForce RTX 3080:8704"` |

See [bellperson](https://github.com/filecoin-project/bellperson) for more GPU options.

### Start ECP Daemon

```bash
# Start ECP service (foreground)
computing-provider ubi daemon

# Or run in background
nohup computing-provider ubi daemon >> cp.log 2>&1 &
```

**Important:** Use `computing-provider ubi daemon` to start ECP/ECP2.

### Monitor ECP

```bash
# List ECP tasks
computing-provider task list --ecp

# List UBI (ZK proof) tasks
computing-provider ubi list

# Show failed tasks
computing-provider ubi list --show-failed

# Monitor logs
tail -f cp.log
```

## Task Management

### List Tasks

```bash
# List ECP tasks (inference/mining)
computing-provider task list --ecp

# List UBI tasks (ZK proofs)
computing-provider ubi list

# Show failed UBI tasks
computing-provider ubi list --show-failed
```

Example UBI task output:
```
TASK ID  TASK CONTRACT                                 TASK TYPE  ZK TYPE  STATUS    SEQUENCER  CREATE TIME
1114203  0x89580E512915cB33bB5Ac419196835fC19affaEe   GPU        fil-c2   verified  YES        2024-11-12 01:52:47
```

### Task Status

- **verified**: Proof verified on-chain
- **submitted**: Submitted to sequencer
- **failed**: Task failed (use `--show-failed` to see)

## Troubleshooting

### Common Issues

1. **Docker Permission Denied**
   ```
   Error: permission denied while trying to connect to the Docker daemon socket
   ```
   ```bash
   # Add user to docker group
   sudo usermod -aG docker $USER
   # Log out and back in for group changes to take effect

   # Or use sg to run with docker group (temporary fix)
   sg docker -c "computing-provider ubi daemon"
   ```

2. **NVIDIA Container Toolkit Not Installed**
   ```
   Error: could not select device driver "nvidia" with capabilities: [[gpu]]
   ```
   The resource-exporter container requires GPU access. Install NVIDIA Container Toolkit:
   ```bash
   curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
   curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
     sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
     sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
   sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
   sudo nvidia-ctk runtime configure --runtime=docker
   sudo systemctl restart docker
   ```

3. **GPU Not Detected**
   ```bash
   # Check NVIDIA driver
   nvidia-smi

   # Check CUDA
   nvcc --version

   # Check Docker can access GPU
   docker run --rm --gpus all nvidia/cuda:11.0-base nvidia-smi
   ```

4. **v28 Parameters Missing**
   ```bash
   export PARENT_PATH="<path>"
   curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/fetch-param-512.sh | bash
   ```

5. **CP Account Not Created**
   ```
   Error: CP Account is empty. Please create an account first
   ```
   Solution: Create CP account with:
   ```bash
   computing-provider account create \
       --ownerAddress <OWNER> --workerAddress <WORKER> \
       --beneficiaryAddress <BENEFICIARY> --task-types 1,2,4
   ```

6. **resource-exporter Container Conflict**
   ```
   Error: The container name "/resource-exporter" is already in use
   ```
   Remove the old container:
   ```bash
   docker rm -f resource-exporter
   ```
   The ECP daemon will recreate it automatically on the next cycle (every 3 minutes).

7. **Verify ECP is Running Correctly**
   ```bash
   # Check resource-exporter container
   docker ps | grep resource-exporter

   # Check ECP service port
   curl http://localhost:9085/health

   # Check logs
   tail -f cp.log
   ```

## Sequencer

### Why Use Sequencer?

The Sequencer is a Layer 3 solution that batches proof submissions from all ECPs and submits them in a single transaction every 24 hours. This significantly reduces gas costs.

### Enable Sequencer

1. Edit `$CP_PATH/config.toml`:
   ```toml
   [UBI]
   EnableSequencer = true
   AutoChainProof = false
   ```

2. Deposit funds:
   ```bash
   computing-provider sequencer add --from <YOUR_WALLET_ADDRESS> <AMOUNT>
   ```

3. Restart ECP daemon

## Exit Procedure

### Step 1: Set Exit Status

```bash
computing-provider account changeTaskTypes --ownerAddress <OWNER_ADDRESS> 100
```

### Step 2: Withdraw Collateral

```bash
# Withdraw from Collateral account
computing-provider collateral withdraw --ecp --owner <OWNER_ADDRESS> --account <CP_ACCOUNT> <AMOUNT>
```

## Support

- [Discord Community](https://discord.gg/Jd2BFSVCKw)
- [GitHub Issues](https://github.com/swanchain/go-computing-provider/issues)
- [Swan Chain Documentation](https://docs.swanchain.io)
- [Sequencer Documentation](https://docs.swanchain.io/bulders/market-provider/web3-zk-computing-market/sequencer)

## Related Documentation

- [Installation Guide](../installation.md)
- [Configuration](../configuration.md)
- [CLI Reference](../cli/README.md)
- [Troubleshooting](../troubleshooting.md) 