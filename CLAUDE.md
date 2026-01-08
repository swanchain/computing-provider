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

# Start the provider
export CP_PATH=<path>
computing-provider run

# Wallet management
computing-provider wallet new
computing-provider wallet list
computing-provider wallet import <private_key_file>

# Account/collateral management
computing-provider account create --ownerAddress <addr> --workerAddress <addr> --beneficiaryAddress <addr> --task-types 3
computing-provider collateral add --fcp --from <addr> <amount>

# Task management
computing-provider task list --fcp
computing-provider ubi list
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
