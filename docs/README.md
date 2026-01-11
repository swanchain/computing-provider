# Go Computing Provider Documentation

Welcome to the comprehensive documentation for the Go Computing Provider, a decentralized computing network client for Swan Chain.

## Overview

The Go Computing Provider is a command-line tool that enables individuals and organizations to participate in Swan Chain's decentralized computing network by offering computational resources such as processing power (CPU and GPU), memory, storage, and bandwidth.

## Provider Modes

### ECP2 - Edge Computing Provider 2 (Default)
The primary mode for Computing Provider v2, allowing operators to deploy AI inference containers with GPU support. ECP2 connects to **Swan Inference**, the decentralized inference marketplace.

### ECP - Edge Computing Provider (ZK-Proof)
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
- [Edge Computing Provider (ECP)](ecp/README.md) - ECP/ECP2 setup and operation
- [UBI Tasks](ubi/README.md) - ZK proof task management

### Operations
- [Command Line Interface](cli/README.md) - Complete CLI reference
- [Task Management](cli/task.md) - Managing computing tasks
- [Wallet Management](cli/wallet.md) - Wallet operations and security

### Advanced Topics
- [SGLang Deployment](sglang-deployment.md) - Deploy SGLang for inference
- [Swan Inference Design](swan-inference-design.md) - Architecture overview
- [Troubleshooting](troubleshooting.md) - Common issues and solutions
- [Security Best Practices](security.md) - Security guidelines

## Getting Help

- [Discord Community](https://discord.gg/swanchain)
- [GitHub Issues](https://github.com/swanchain/computing-provider-v2/issues)
- [Swan Chain Documentation](https://docs.swanchain.io)

## License

Apache License 2.0 - see [LICENSE](../LICENSE) file for details.
