# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Git Commit Policy

- Do NOT include `Co-Authored-By` lines in commit messages
- Keep commit messages concise and descriptive

## Project Overview

Computing Provider v2 is a CLI tool for the Swan Chain decentralized computing network. It enables operators to run computing providers that offer computational resources (CPU, GPU, memory, storage) to the network.

**Key characteristics:**
- **Docker-based architecture** - All workloads run as Docker containers (no Kubernetes required)
- **NVIDIA GPU support** - Uses NVIDIA Container Toolkit for GPU access
- **Cross-platform** - Supports Linux and macOS (including Apple Silicon)

**Inference Mode is the default and primary mode** for Computing Provider v2, allowing operators to deploy AI inference containers with GPU support. It connects to **Swan Inference**, the decentralized inference marketplace.

### Provider Modes

| Mode | Task Type | Description | Command |
|------|-----------|-------------|---------|
| **Inference** (Default) | 4 | Deploy AI inference containers | `computing-provider ubi daemon` |
| ZK-Proof | 1, 2 | FIL-C2 and mining proofs | `computing-provider ubi daemon` |

## Build Commands

```bash
# Build for mainnet
make clean && make mainnet
make install

# Build for testnet
make clean && make testnet
make install

# The binary is installed to /usr/local/bin/computing-provider
```

Go version 1.22+ is required (see go.mod).

## Running Tests

```bash
# Run all tests
go test ./...

# Run specific test
go test -run TestSequencer ./test/
```

## Key CLI Commands

```bash
# Initialize repo (default: ~/.swan/computing, override with CP_PATH env)
computing-provider init --multi-address=/ip4/<IP>/tcp/<PORT> --node-name=<NAME>

# Wallet management
computing-provider wallet new
computing-provider wallet list
computing-provider wallet import <private_key_file>

# Account/collateral management (Inference mode - task-types 4)
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 4
computing-provider collateral add --ecp --from <addr> <amount>

# Task management
computing-provider task list          # List tasks
computing-provider ubi list           # ZK proof tasks

# Hardware research and benchmarking
computing-provider research hardware       # Display all hardware info
computing-provider research gpu-info       # Display GPU information
computing-provider research gpu-benchmark  # Run GPU benchmark tests
```

## Running Inference Mode (Default)

Inference Mode runs AI inference containers via Docker. Does NOT require Kubernetes.

**Prerequisites:**
- Docker with NVIDIA Container Toolkit installed
- Local inference server (SGLang or vLLM)

> **Note:** Inference mode does NOT require a public IP address. The provider connects outbound to Swan Inference via WebSocket.

**Install NVIDIA Container Toolkit (required for GPU access in Docker):**
```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

**Inference account setup (task-types 4):**
```bash
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 4
computing-provider collateral add --ecp --from <addr> <amount>
```

**Configure Inference mode in `$CP_PATH/config.toml`:**
```toml
[Inference]
Enable = true
WebSocketURL = "wss://inference-ws.swanchain.io"  # Swan Inference WebSocket
Models = ["llama-3.2-3b"]                         # Models this provider serves
```

**Configure model mappings in `$CP_PATH/models.json`:**
```json
{
  "llama-3.2-3b": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 8000,
    "category": "text-generation"
  }
}
```

**Start SGLang inference server:**
```bash
docker run -d --gpus all -p 30000:30000 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  --shm-size 32g --ipc=host \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path meta-llama/Llama-3.2-3B-Instruct \
    --host 0.0.0.0 --port 30000 --served-model-name llama-3.2-3b
```

**Environment variable overrides (for dev):**
```bash
export INFERENCE_WS_URL=ws://localhost:8081  # Override WebSocket URL for local dev
```

**Start Inference daemon:**
```bash
nohup ./computing-provider ubi daemon >> cp.log 2>&1 &
```

**Common Inference mode issues:**
- `permission denied...docker.sock`: Add user to docker group or use `sg docker -c "computing-provider ubi daemon"`
- `could not select device driver "nvidia"`: Install NVIDIA Container Toolkit (see above)
- `container name "/resource-exporter" is already in use`: Run `docker rm -f resource-exporter`
- `CP Account is empty`: Create account with `computing-provider account create ...`

## Running ZK-Proof Mode

ZK-Proof mode handles ZK-Snark proof generation (FIL-C2, Aleo, etc.). Requires v28 parameters (~200GB).

**Additional prerequisites:**
- Download v28 parameters (at least 200GB storage needed)

**Required environment variables:**
```bash
export FIL_PROOFS_PARAMETER_CACHE=<path_to_v28_params>
export RUST_GPU_TOOLS_CUSTOM_GPU="<GPU_MODEL>:<CORES>"  # e.g., "GeForce RTX 4090:16384"
```

**ZK-Proof account setup (task-types 1,2,4):**
```bash
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 1,2,4
computing-provider collateral add --ecp --from <addr> <amount>
computing-provider sequencer add --from <addr> <amount>
```

**Sequencer config** (`$CP_PATH/config.toml`):
```toml
[UBI]
EnableSequencer = true    # Submit proofs to Sequencer (reduces gas costs)
AutoChainProof = false    # Fallback to chain when sequencer unavailable
```

## Inference Development Mode (Base Sepolia)

For development and testing, Inference mode uses Base Sepolia testnet for smart contracts.

**Base Sepolia Contracts:**
| Contract | Address |
|----------|---------|
| Collateral | `0x5EBc65E856ad97532354565560ccC6FAB51b255a` |
| Task | `0x6c1f6ad2b4Cb8A7ba4027b348D7f20A14706d3C2` |

**Network:** Base Sepolia (chainId: 84532)
**RPC:** `https://sepolia.base.org`
**Explorer:** https://sepolia.basescan.org

**Dev setup (no on-chain registration required):**
```bash
# Build for testnet
make clean && make testnet

# Set environment for dev
export INFERENCE_WS_URL=ws://localhost:8081

# Run daemon (uses Node ID auth, skips on-chain account)
./computing-provider ubi daemon
```

**Authentication:**
- **Production**: On-chain CP account registration on Swan Chain
- **Development**: Node ID based authentication (wallet signature)
  - No on-chain account required
  - Provider authenticates via signed messages
  - Suitable for local testing with Swan Inference

**Inference Config Fields:**
- `Enable`: Enable/disable Inference service
- `WebSocketURL`: WebSocket connection to Swan Inference
- `Models`: List of models this provider serves

**Model Configuration:**
Create `$CP_PATH/models.json` to map models to local endpoints:
```json
{
  "llama-3.1-8b": {
    "container": "lmsysorg/sglang:latest",
    "endpoint": "http://localhost:30000",
    "gpu_memory": 16000,
    "category": "text-generation"
  }
}
```

## Architecture

### Directory Structure

- `cmd/computing-provider/`: CLI entry point and command definitions (main.go defines all subcommands)
- `internal/computing/`: Core services (Docker, UBI tasks, Inference deployment)
- `internal/contract/`: Swan Chain smart contract bindings (auto-generated + stub wrappers)
  - `account/`: CP account registration contract
  - `ecp/`: Edge computing contracts (collateral, sequencer, tasks)
  - `token/`: SWAN token contract
- `internal/models/`: Data models (jobs, resources, UBI tasks)
- `internal/db/`: SQLite database via GORM
- `conf/`: Configuration loading and validation
- `build/`: Version info and embedded network parameters (`parameters.json`)
- `wallet/`: Keystore management and transaction signing

### Core Services in `internal/computing/`

- `ubi_service.go`: UBI (Universal Basic Income) ZK proof task handling
- `docker_service.go`: Docker container management and operations
- `cron_task.go`: Background scheduled tasks (health checks, cleanup, status updates)
- `sequence_service.go`: Sequencer service for ZK proof submission
- `inference_service.go`: Inference service for Swan Inference marketplace
- `inference_client.go`: WebSocket client for Swan Inference connection
- `provider.go`: Main provider service orchestration

### Configuration

Config file: `$CP_PATH/config.toml` (see `config.toml.sample`)

Key sections:
- `[API]`: Server port, multi-address, domain, pricing settings
- `[Inference]`: Inference mode settings (Enable, WebSocketURL, Models)
- `[UBI]`: ZK engine settings, sequencer configuration (`EnableSequencer`, `AutoChainProof`)
- `[RPC]`: Swan Chain RPC endpoint
- `[Registry]`: Docker registry for container image storage

Pricing config: `$CP_PATH/price.toml` (resource pricing per hour)

### Network Parameters

Network-specific contract addresses and parameters are embedded in `build/parameters.json` and selected at build time via the `NetWorkTag` ldflags variable (mainnet/testnet). The build system uses `-ldflags` to set `build.NetWorkTag`.

### Wire Dependency Injection

The project uses Google Wire for dependency injection. See `internal/computing/wire.go` and the generated `wire_gen.go`. When modifying services, regenerate with `wire ./internal/computing/`.

## Important Patterns

- The `CP_PATH` environment variable controls the repo directory location (default: `~/.swan/computing`)
- Contract stubs in `internal/contract/*/` wrap auto-generated Ethereum contract bindings
- Inference and ZK-Proof tasks run as Docker containers with GPU resources via NVIDIA Container Toolkit
- Task types: 1=FIL-C2, 2=Mining, 4=Inference (default), 5=NodePort, 100=Exit

## Contract Interaction

Smart contracts are accessed via stubs in `internal/contract/`. Each contract has:
- `*_contract.go`: Auto-generated Go bindings from Solidity ABI
- `*_stub.go`: Wrapper providing higher-level operations

Key contracts:
- Account contract: CP registration and management
- Collateral contracts: Collateral deposits/withdrawals
- Sequencer contract: Batch proof submission to reduce gas costs
- Task contracts: Task registration and proof verification
