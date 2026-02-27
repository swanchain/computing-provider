# Go Computing Provider Documentation

Welcome to the comprehensive documentation for the Go Computing Provider, a decentralized computing network client for Swan Chain.

## Overview

The Go Computing Provider is a command-line tool that enables individuals and organizations to participate in Swan Chain's decentralized computing network by offering computational resources such as processing power (CPU and GPU), memory, storage, and bandwidth.

**Key Features:**
- Docker-based deployment (no Kubernetes required)
- NVIDIA GPU support via Container Toolkit
- AI inference and ZK proof generation
- Cross-platform support (Linux, macOS including Apple Silicon)

## Provider Modes

| Mode | Task Type | Description |
|------|-----------|-------------|
| **Inference** (Default) | 4 | Deploy AI inference containers via Docker |
| ZK-Proof | 1, 2 | FIL-C2 and mining ZK-SNARK proofs |

### Inference Mode (Default)
The primary mode for Computing Provider v2, allowing operators to deploy AI inference containers with GPU support. Inference mode connects to **Swan Inference**, the decentralized inference marketplace.

### ZK-Proof Mode
Specializes in ZK-SNARK proof generation (FIL-C2, Aleo, etc.) using GPU acceleration. Ideal for real-time proof generation applications.

## Quick Start

1. [Installation Guide](installation.md)
2. [Configuration](configuration.md)
3. [Getting Started](getting-started.md)

## Documentation Sections

### Setup & Installation
- [Installation Guide](installation.md) - Complete setup instructions
- [Prerequisites](prerequisites.md) - System requirements and dependencies
- [Configuration](configuration.md) - Configuration files and settings

### Provider Modes
- [Edge Computing Provider](ecp/README.md) - Inference and ZK-Proof mode setup
- [UBI Tasks](ubi/architecture.md) - ZK proof task management

### Operations
- [Command Line Interface](cli/README.md) - Complete CLI reference
- [Task Management](cli/task.md) - Managing computing tasks
- Wallet Management - `computing-provider wallet --help`
- `computing-provider inference status` - Check provider status on Swan Inference
- `computing-provider inference config` - Show inference configuration
- `computing-provider dashboard` - Web-based monitoring UI (port 3005)

### Hardware & Research
- `computing-provider research hardware` - Display system hardware info
- `computing-provider research gpu-info` - Display GPU information
- `computing-provider research gpu-benchmark` - Run GPU benchmark tests

### Advanced Topics
- [SGLang Deployment](sglang-deployment.md) - Deploy SGLang for inference
- [SGLang Performance Tuning](sglang-best-practices.md) - GPU configs, memory tuning, latency optimization
- [Swan Inference Design](swan-inference-design.md) - Architecture overview
- [Troubleshooting](troubleshooting.md) - Common issues and solutions


## Getting Help

- [Discord Community](https://discord.gg/swanchain)
- [GitHub Issues](https://github.com/swanchain/computing-provider/issues)
- [Swan Chain Documentation](https://docs.swanchain.io)

## License

Apache License 2.0 - see [LICENSE](../LICENSE) file for details.
