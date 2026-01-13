# Computing Provider v2

[![Discord](https://img.shields.io/discord/770382203782692945?label=Discord&logo=Discord)](https://discord.gg/Jd2BFSVCKw)
[![Twitter Follow](https://img.shields.io/twitter/follow/swan_chain)](https://twitter.com/swan_chain)
[![standard-readme compliant](https://img.shields.io/badge/readme%20style-standard-brightgreen.svg)](https://github.com/RichardLitt/standard-readme)

Computing Provider v2 is a CLI tool for the Swan Chain decentralized computing network. It enables operators to provide computational resources (CPU, GPU, memory, storage) to the network and earn rewards.

**ECP2 (Edge Computing Provider 2)** is the default and recommended mode for Computing Provider v2, allowing you to deploy and run AI inference containers with GPU support. ECP2 mode connects to **Swan Inference**, the decentralized inference marketplace.

## Provider Modes

| Mode | Description | Requirements | Command |
|------|-------------|--------------|---------|
| **ECP2** (Default) | Deploy AI inference containers | Docker + NVIDIA Container Toolkit | `computing-provider ubi daemon` |
| ECP (ZK-Proof) | Generate ZK-Snark proofs (FIL-C2, Aleo) | Docker + NVIDIA + v28 params | `computing-provider ubi daemon` |

# Table of Contents

- [Quick Start: ECP2 Mode](#quick-start-ecp2-mode)
  - [Prerequisites](#prerequisites)
  - [Install NVIDIA Container Toolkit](#install-nvidia-container-toolkit)
  - [Build Computing Provider](#build-computing-provider)
  - [Initialize and Configure](#initialize-and-configure)
  - [Setup Wallet and Account](#setup-wallet-and-account)
  - [Start ECP2 Provider](#start-ecp2-provider)
- [Configuration Reference](#configuration-reference)
- [ECP Mode (ZK-Proof)](#ecp-mode-zk-proof)
- [CLI Reference](#cli-reference)
- [Getting Help](#getting-help)

---

# Quick Start: ECP2 Mode

ECP2 (Edge Computing Provider 2) allows you to run AI inference containers on your GPU hardware and earn rewards from the Swan Chain network.

## Prerequisites

- Linux server with NVIDIA GPU
- Docker installed ([install guide](https://docs.docker.com/engine/install/))
- Public IP address
- Go 1.22+ for building from source

```bash
# Install Go if needed
wget -c https://golang.org/dl/go1.22.7.linux-amd64.tar.gz -O - | sudo tar -xz -C /usr/local
echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc && source ~/.bashrc
```

## Install NVIDIA Container Toolkit

Required for GPU access in Docker containers:

```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

Verify installation:
```bash
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi
```

## Build Computing Provider

```bash
git clone https://github.com/swanchain/go-computing-provider.git
cd go-computing-provider
git checkout releases

# Build for mainnet
make clean && make mainnet
make install

# Or for testnet
# make clean && make testnet
# make install
```

## Initialize and Configure

1. **Initialize the repository:**
```bash
computing-provider init --multi-address=/ip4/<YOUR_PUBLIC_IP>/tcp/<YOUR_PORT> --node-name=<YOUR_NODE_NAME>
```

> **Note:** Default repo location is `~/.swan/computing`. Override with `export CP_PATH="<YOUR_CP_PATH>"`

2. **Configure for ECP2** in `$CP_PATH/config.toml`:

```toml
[API]
Port = 8085                                    # Web server port
MultiAddress = "/ip4/<public_ip>/tcp/<port>"   # Your public address
Domain = "*.example.com"                       # Domain for single-port services (optional)
NodeName = "my-inference-node"                 # Your node name
PortRange = ["40000-40050", "40060"]           # Ports for multi-port containers

[RPC]
SWAN_CHAIN_RPC = "https://mainnet-rpc-01.swanchain.org"

[ECP2]
Enable = true
WebSocketURL = "wss://inference.swanchain.io/ws"  # Swan Inference WebSocket
Models = ["your-model-name"]                       # Models this provider serves

# For development/testnet (Base Sepolia)
# ChainRPC = "https://sepolia.base.org"
# CollateralContract = "0x5EBc65E856ad97532354565560ccC6FAB51b255a"
# TaskContract = "0x6c1f6ad2b4Cb8A7ba4027b348D7f20A14706d3C2"
```

**Environment variable overrides:**
```bash
export ECP2_WS_URL=ws://localhost:8081  # Override WebSocket URL for dev
```

**Port Configuration:**
- Single-port containers: Use `traefik` with domain resolution (port 9000)
- Multi-port containers: Use `PortRange` with direct IP + port mapping

## Setup Wallet and Account

1. **Create or import wallet:**
```bash
# Create new wallet
computing-provider wallet new

# Or import existing wallet
computing-provider wallet import <YOUR_PRIVATE_KEY_FILE>
```

2. **Deposit SwanETH** to your wallet address. See the [getting started guide](https://docs.swanchain.io/swan-mainnet/getting-started-guide).

3. **Create CP account with ECP2 task type:**
```bash
computing-provider account create \
    --ownerAddress <YOUR_OWNER_ADDRESS> \
    --workerAddress <YOUR_WORKER_ADDRESS> \
    --beneficiaryAddress <YOUR_BENEFICIARY_ADDRESS> \
    --task-types 4
```

> **Task Type 4** = ECP2 (Inference)

4. **Add collateral:**
```bash
computing-provider collateral add --ecp --from <YOUR_WALLET_ADDRESS> <AMOUNT>
```

## Start ECP2 Provider

```bash
export CP_PATH=<YOUR_CP_PATH>
nohup computing-provider ubi daemon >> cp.log 2>&1 &
```

**Check running tasks:**
```bash
computing-provider task list --ecp
```

**Example output:**
```
TASK UUID                               TASK NAME       IMAGE NAME                              CONTAINER STATUS   REWARD    CREATE TIME
75f9df4e-b6a5-40b0-b7ac-02fb1840dafa    inference-01    mymodel/inference:latest                running            1.2500    2024-11-24 10:23:32
```

---

# Configuration Reference

## Resource Pricing

Configure pricing in `$CP_PATH/price.toml`:

```bash
# Generate default pricing config
computing-provider price generate

# View current prices
computing-provider price view
```

Example `price.toml`:
```toml
TARGET_CPU="0.2"            # SWAN/thread-hour
TARGET_MEMORY="0.1"         # SWAN/GB-hour
TARGET_HD_EPHEMERAL="0.005" # SWAN/GB-hour
TARGET_GPU_DEFAULT="1.6"    # SWAN/GPU-hour
TARGET_GPU_3080="2.0"       # SWAN/3080 GPU-hour
```

## Full config.toml Reference

```toml
[API]
Port = 8085                                    # Web server port
MultiAddress = "/ip4/<public_ip>/tcp/<port>"   # Public multiaddress
Domain = ""                                    # Domain for traefik routing
NodeName = ""                                  # Display name
Pricing = "true"                               # Accept smart pricing orders
AutoDeleteImage = false                        # Auto-delete unused images
PortRange = ["40000-40050"]                    # Ports for multi-port containers

[RPC]
SWAN_CHAIN_RPC = "https://mainnet-rpc-01.swanchain.org"

[UBI]
EnableSequencer = false                        # Enable sequencer for ZK proofs
AutoChainProof = false                         # Fallback to chain when sequencer unavailable

[Registry]
ServerAddress = ""                             # Docker registry (optional)
UserName = ""
Password = ""
```

---

# ECP Mode (ZK-Proof)

ECP (Edge Computing Provider) generates ZK-Snark proofs (Filecoin FIL-C2, Aleo, etc.). Requires additional v28 parameters (~200GB).

See [ECP/UBI Documentation](docs/ubi/README.md) for full setup.

**Quick overview:**
```bash
# Download v28 parameters
export PARENT_PATH="<V28_PARAMS_PATH>"
curl -fsSL https://raw.githubusercontent.com/swanchain/go-computing-provider/releases/ubi/fetch-param-512.sh | bash

# Set environment
export FIL_PROOFS_PARAMETER_CACHE=$PARENT_PATH
export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"

# Create account with ZK task types
computing-provider account create \
    --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> \
    --task-types 1,2,4

# Enable sequencer (reduces gas costs)
# In config.toml:
# [UBI]
# EnableSequencer = true
# AutoChainProof = false

# Deposit to sequencer
computing-provider sequencer add --from <addr> <amount>

# Start daemon
nohup computing-provider ubi daemon >> cp.log 2>&1 &
```

---

# CLI Reference

## Task Management
```bash
# List ECP2/ECP tasks
computing-provider task list --ecp

# Get task details
computing-provider task get --ecp <task_uuid>

# Delete task
computing-provider task delete --ecp <task_uuid>
```

## Wallet Commands
```bash
computing-provider wallet new              # Create new wallet
computing-provider wallet list             # List wallets
computing-provider wallet import <file>    # Import from private key file
computing-provider wallet send --from <addr> <to_addr> <amount>
```

## Account Commands
```bash
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types <types>
computing-provider account changeTaskTypes --ownerAddress <addr> <new_types>
computing-provider account changeMultiAddress --ownerAddress <addr> /ip4/<ip>/tcp/<port>
```

## Collateral Commands
```bash
# Add collateral
computing-provider collateral add --ecp --from <addr> <amount>

# Withdraw collateral
computing-provider collateral withdraw --ecp --owner <addr> --account <cp_account> <amount>
```

## ZK/Sequencer Commands
```bash
computing-provider ubi list                           # List ZK tasks
computing-provider ubi list --show-failed             # Include failed tasks
computing-provider sequencer add --from <addr> <amt>  # Deposit to sequencer
computing-provider sequencer withdraw --owner <addr> <amt>
```

---

# Getting Help

For usage questions or issues reach out to the Swan team either in the [Discord channel](https://discord.gg/3uQUWzaS7U) or open a new issue here on GitHub.

## License

Apache
