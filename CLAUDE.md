# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Go Computing Provider is a CLI tool for the Swan Chain decentralized computing network. It enables operators to run computing providers that offer computational resources (CPU, GPU, memory, storage) to the network.

Two provider types are supported:
- **ECP (Edge Computing Provider)**: Handles ZK-Snark proof generation tasks (Filecoin, Aleo, etc.)
- **FCP (Fog Computing Provider)**: Runs AI model training/deployment tasks via Kubernetes

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

Go version 1.22+ is required.

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

# Account/collateral management
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 3
computing-provider collateral add --fcp --from <addr> <amount>

# Task management
computing-provider task list --fcp
computing-provider task list --ecp
computing-provider ubi list
```

## Running ECP (Edge Computing Provider)

ECP handles ZK-Snark proof generation (FIL-C2, Aleo, etc.) and does NOT require Kubernetes.

**Prerequisites:**
- Docker with NVIDIA Container Toolkit installed
- Download v28 parameters (at least 200GB storage needed)
- Map port 9085 to public network: `<Intranet_IP>:9085 <--> <Public_IP>:<PORT>`

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

**Required environment variables:**
```bash
export FIL_PROOFS_PARAMETER_CACHE=<path_to_v28_params>
export RUST_GPU_TOOLS_CUSTOM_GPU="<GPU_MODEL>:<CORES>"  # e.g., "GeForce RTX 4090:16384"
```

**Start ECP daemon:**
```bash
nohup ./computing-provider ubi daemon >> cp.log 2>&1 &
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

**Common ECP issues:**
- `permission denied...docker.sock`: Add user to docker group or use `sg docker -c "computing-provider ubi daemon"`
- `could not select device driver "nvidia"`: Install NVIDIA Container Toolkit (see above)
- `container name "/resource-exporter" is already in use`: Run `docker rm -f resource-exporter`
- `CP Account is empty`: Create account with `computing-provider account create ...`

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

- `cmd/computing-provider/`: CLI entry point and command definitions
- `internal/computing/`: Core services (K8s, Docker, UBI tasks, space deployment)
- `internal/contract/`: Swan Chain smart contract bindings
  - `account/`: CP account registration contract
  - `ecp/`: Edge computing contracts (collateral, sequencer, tasks)
  - `fcp/`: Fog computing contracts (collateral, job manager)
  - `token/`: SWAN token contract
- `internal/models/`: Data models (jobs, resources, UBI tasks)
- `conf/`: Configuration loading and validation
- `build/`: Version info and embedded network parameters
- `wallet/`: Keystore management and transaction signing

### Core Services in `internal/computing/`

- `k8s_service.go`: Kubernetes cluster operations (deployments, pods, services, ingress)
- `space_service.go`: Space/job deployment and lifecycle management
- `ubi_service.go`: UBI (Universal Basic Income) ZK proof task handling
- `docker_service.go`: Docker image building and registry operations
- `cron_task.go`: Background scheduled tasks (health checks, cleanup, status updates)
- `sequence_service.go`: Sequencer service for ZK proof submission

### Configuration

Config file: `$CP_PATH/config.toml` (see `config.toml.sample`)

Key sections:
- `[API]`: Server port, multi-address, domain, pricing settings
- `[UBI]`: ZK engine settings, sequencer configuration
- `[HUB]`: Orchestrator settings for FCP tasks
- `[RPC]`: Swan Chain RPC endpoint

### Network Parameters

Network-specific contract addresses and parameters are embedded in `build/parameters.json` and selected at build time via the `NetWorkTag` ldflags variable (mainnet/testnet).

### Wire Dependency Injection

The project uses Google Wire for dependency injection. See `internal/computing/wire.go` and the generated `wire_gen.go`.

## Important Patterns

- The `CP_PATH` environment variable controls the repo directory location
- Contract stubs in `internal/contract/*/` wrap auto-generated contract bindings
- K8s operations use client-go; the service auto-detects in-cluster or kubeconfig
- Job deployments create Kubernetes Deployments + Services + Ingress resources
- UBI tasks run as Kubernetes Jobs with GPU resources when available
