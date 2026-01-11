---
name: computing-provider
description: Helps build, test, deploy, and troubleshoot the Swan Chain Computing Provider CLI. Use when working with ECP (Edge Computing Provider) for ZK proofs, FCP (Fog Computing Provider) for AI tasks, or managing wallets, accounts, and collateral.
allowed-tools: Bash, Read, Grep, Glob
---

# Computing Provider Development Guide

## Quick Reference

### Build Commands
```bash
# Mainnet build
make clean && make mainnet && make install

# Testnet build
make clean && make testnet && make install

# Run tests
go test ./...
```

### Provider Types

| Type | Purpose | Requires K8s | Start Command |
|------|---------|--------------|---------------|
| ECP | ZK-Snark proofs (FIL-C2, Aleo) | No | `computing-provider ubi daemon` |
| FCP | AI model training/deployment | Yes | `computing-provider run` |

### Task Types
- 1 = FIL-C2 (ECP)
- 2 = Mining (ECP)
- 3 = AI (FCP)
- 4 = Inference (ECP)
- 5 = NodePort
- 100 = Exit

## ECP Setup Checklist

1. Install NVIDIA Container Toolkit for GPU access in Docker
2. Download v28 parameters (200GB+ storage needed)
3. Set environment variables:
   - `FIL_PROOFS_PARAMETER_CACHE=<path_to_v28_params>`
   - `RUST_GPU_TOOLS_CUSTOM_GPU="<GPU_MODEL>:<CORES>"`
4. Map port 9085 to public network
5. Create account with task-types 1,2,4
6. Add ECP collateral and sequencer funds

## Common ECP Errors

| Error | Solution |
|-------|----------|
| `permission denied...docker.sock` | Add user to docker group or use `sg docker -c "..."` |
| `could not select device driver "nvidia"` | Install NVIDIA Container Toolkit |
| `container name "/resource-exporter" is already in use` | Run `docker rm -f resource-exporter` |
| `CP Account is empty` | Create account with `computing-provider account create ...` |

## Key Directories

- `cmd/computing-provider/`: CLI commands
- `internal/computing/`: Core services (K8s, Docker, UBI)
- `internal/contract/`: Smart contract bindings
- `conf/`: Configuration loading
- `build/`: Network parameters (mainnet/testnet)

## Configuration Files

- `$CP_PATH/config.toml`: Main configuration
- `$CP_PATH/price.toml`: Resource pricing
- Default CP_PATH: `~/.swan/computing`

## Sequencer Configuration

In `config.toml`:
```toml
[UBI]
EnableSequencer = true    # Use sequencer for lower gas costs
AutoChainProof = false    # Fallback when sequencer unavailable
```
