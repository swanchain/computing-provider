# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Git Commit Policy

- Do NOT include `Co-Authored-By` lines in commit messages
- Keep commit messages concise and descriptive

## Project Overview

Computing Provider v2 is a CLI tool for the Swan Chain decentralized computing network. It enables operators to run computing providers that offer computational resources (CPU, GPU, memory, storage) to the network.

**ECP2 (Edge Computing Provider 2) is the default and primary mode** for Computing Provider v2, allowing operators to deploy AI inference containers with GPU support.

### Provider Modes

| Mode | Task Type | Description | Command |
|------|-----------|-------------|---------|
| **ECP2** (Default) | 4 | Deploy AI inference containers | `computing-provider ubi daemon` |
| ECP (ZK-Proof) | 1, 2 | FIL-C2 and mining proofs | `computing-provider ubi daemon` |
| FCP | 3 | AI training via Kubernetes | `computing-provider run` |

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

# Run specific test file
go test ./test/k8s_service_test.go

# Run specific test
go test -run TestNewK8sService ./test/
```

Note: Most tests require a running Kubernetes cluster and proper configuration.

## Key CLI Commands

```bash
# Initialize repo (default: ~/.swan/computing, override with CP_PATH env)
computing-provider init --multi-address=/ip4/<IP>/tcp/<PORT> --node-name=<NAME>

# Wallet management
computing-provider wallet new
computing-provider wallet list
computing-provider wallet import <private_key_file>

# Account/collateral management (ECP2 mode - task-types 4)
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 4
computing-provider collateral add --ecp --from <addr> <amount>

# Task management
computing-provider task list --ecp    # ECP2/ECP tasks
computing-provider task list --fcp    # FCP tasks
computing-provider ubi list           # ZK proof tasks
```

## Running ECP2 Mode (Default)

ECP2 (Edge Computing Provider 2) runs AI inference containers via Docker. Does NOT require Kubernetes.

**Prerequisites:**
- Docker with NVIDIA Container Toolkit installed
- Map port to public network: `<Intranet_IP>:8085 <--> <Public_IP>:<PORT>`

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

**ECP2 account setup (task-types 4):**
```bash
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 4
computing-provider collateral add --ecp --from <addr> <amount>
```

**Configure ECP2 in `$CP_PATH/config.toml`:**
```toml
[API]
Domain = "*.example.com"               # Domain for single-port services
PortRange = ["40000-40050", "40060"]   # Ports for multi-port containers
```

**Start ECP2 daemon:**
```bash
nohup ./computing-provider ubi daemon >> cp.log 2>&1 &
```

**Common ECP2 issues:**
- `permission denied...docker.sock`: Add user to docker group or use `sg docker -c "computing-provider ubi daemon"`
- `could not select device driver "nvidia"`: Install NVIDIA Container Toolkit (see above)
- `container name "/resource-exporter" is already in use`: Run `docker rm -f resource-exporter`
- `CP Account is empty`: Create account with `computing-provider account create ...`

## Running ECP Mode (ZK-Proof)

ECP (Edge Computing Provider) handles ZK-Snark proof generation (FIL-C2, Aleo, etc.). Requires v28 parameters (~200GB).

**Additional prerequisites:**
- Download v28 parameters (at least 200GB storage needed)

**Required environment variables:**
```bash
export FIL_PROOFS_PARAMETER_CACHE=<path_to_v28_params>
export RUST_GPU_TOOLS_CUSTOM_GPU="<GPU_MODEL>:<CORES>"  # e.g., "GeForce RTX 4090:16384"
```

**ECP account setup (task-types 1,2,4):**
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

## Running FCP (Fog Computing Provider)

FCP runs AI model training/deployment and requires a Kubernetes cluster.

**Start FCP:**
```bash
export CP_PATH=<path>
computing-provider run
```

**FCP account setup (task-types 3):**
```bash
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 3
computing-provider collateral add --fcp --from <addr> <amount>
```

## Architecture

### Directory Structure

- `cmd/computing-provider/`: CLI entry point and command definitions (main.go defines all subcommands)
- `internal/computing/`: Core services (K8s, Docker, UBI tasks, space deployment)
- `internal/contract/`: Swan Chain smart contract bindings (auto-generated + stub wrappers)
  - `account/`: CP account registration contract
  - `ecp/`: Edge computing contracts (collateral, sequencer, tasks)
  - `fcp/`: Fog computing contracts (collateral, job manager)
  - `token/`: SWAN token contract
- `internal/models/`: Data models (jobs, resources, UBI tasks)
- `internal/db/`: SQLite database via GORM
- `internal/yaml/`: YAML parsing for deployment manifests
- `conf/`: Configuration loading and validation
- `build/`: Version info and embedded network parameters (`parameters.json`)
- `wallet/`: Keystore management and transaction signing

### Core Services in `internal/computing/`

- `k8s_service.go`: Kubernetes cluster operations (deployments, pods, services, ingress)
- `space_service.go`: Space/job deployment and lifecycle management
- `ubi_service.go`: UBI (Universal Basic Income) ZK proof task handling
- `docker_service.go`: Docker image building and registry operations
- `cron_task.go`: Background scheduled tasks (health checks, cleanup, status updates)
- `sequence_service.go`: Sequencer service for ZK proof submission
- `ecp_image_service.go`: ECP2 container image deployment for inference tasks
- `provider.go`: Main provider service orchestration

### Configuration

Config file: `$CP_PATH/config.toml` (see `config.toml.sample`)

Key sections:
- `[API]`: Server port, multi-address, domain, pricing settings, port ranges for ECP2
- `[UBI]`: ZK engine settings, sequencer configuration (`EnableSequencer`, `AutoChainProof`)
- `[HUB]`: Orchestrator settings for FCP tasks
- `[RPC]`: Swan Chain RPC endpoint
- `[Registry]`: Docker registry for multi-node K8s clusters

Pricing config: `$CP_PATH/price.toml` (resource pricing per hour)

### Network Parameters

Network-specific contract addresses and parameters are embedded in `build/parameters.json` and selected at build time via the `NetWorkTag` ldflags variable (mainnet/testnet). The build system uses `-ldflags` to set `build.NetWorkTag`.

### Wire Dependency Injection

The project uses Google Wire for dependency injection. See `internal/computing/wire.go` and the generated `wire_gen.go`. When modifying services, regenerate with `wire ./internal/computing/`.

## Important Patterns

- The `CP_PATH` environment variable controls the repo directory location (default: `~/.swan/computing`)
- Contract stubs in `internal/contract/*/` wrap auto-generated Ethereum contract bindings
- K8s operations use client-go; the service auto-detects in-cluster or kubeconfig mode
- FCP job deployments create Kubernetes Deployments + Services + Ingress resources
- ECP2 and ECP tasks run as Docker containers with GPU resources via NVIDIA Container Toolkit
- Task types: 1=FIL-C2, 2=Mining, 3=AI, 4=ECP2/Inference (default), 5=NodePort, 100=Exit

## Contract Interaction

Smart contracts are accessed via stubs in `internal/contract/`. Each contract has:
- `*_contract.go`: Auto-generated Go bindings from Solidity ABI
- `*_stub.go`: Wrapper providing higher-level operations

Key contracts:
- Account contract: CP registration and management
- Collateral contracts: ECP and FCP collateral deposits/withdrawals
- Sequencer contract: Batch proof submission to reduce gas costs
- Task contracts: Task registration and proof verification
