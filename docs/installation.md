# Installation Guide

This guide will walk you through the installation process for the Computing Provider.

## Prerequisites

Go is needed only to build from source — if you install the published binary
below, you need no toolchain at all.

### Linux (NVIDIA GPU)

| Category | Requirement |
|----------|-------------|
| **GPU** | NVIDIA RTX 3090, 4090, A100, H100, or equivalent |
| **VRAM** | Minimum 16GB (24GB+ recommended) |
| **RAM** | Minimum 32GB system memory |
| **Storage** | 500GB+ SSD for model weights |
| **OS** | Ubuntu 22.04+ or Debian 11+ |
| **NVIDIA Driver** | 535.x or newer |
| **CUDA** | 12.1 or newer |
| **Docker** | 24.0+ with [NVIDIA Container Toolkit](#install-nvidia-container-toolkit) |
| **Network** | 100 Mbps minimum (1 Gbps recommended), stable and low-latency |

### macOS (Apple Silicon)

| Category | Requirement |
|----------|-------------|
| **Chip** | Apple Silicon M1, M2, M3, or M4 |
| **Memory** | 16GB+ unified memory (32GB+ recommended) |
| **Storage** | 500GB+ SSD for model weights |
| **OS** | macOS 13 Ventura or newer |
| **Software** | [Ollama](https://ollama.ai) (latest version) |
| **Network** | 100 Mbps minimum, stable and low-latency |

> **Ports:** only outbound WebSocket connections are needed. No port forwarding
> and no public IP.

VRAM sizing per model is in [models.md](models.md#vram-per-model).

## Installing the binary

Every release publishes prebuilt binaries, so building from source is optional:

```bash
# Linux x86-64 — also computing-provider-linux-arm64, computing-provider-darwin-arm64
curl -fL -o computing-provider \
  https://github.com/swanchain/computing-provider/releases/latest/download/computing-provider-linux-amd64
chmod +x computing-provider && sudo mv computing-provider /usr/local/bin/

computing-provider version
```

### Install NVIDIA Container Toolkit

Linux only, and required before Docker can hand a GPU to your model server:

```bash
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker

# Verify
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi
```

## Keeping up to date

```bash
computing-provider version         # what you are running
computing-provider update --check  # what is available
sudo computing-provider update     # install it
```

`update` downloads the binary for your platform from the GitHub release, checks
its SHA-256 against the checksums published with that release, and replaces the
executable atomically — a crash mid-update cannot leave a half-written binary
where the agent used to be. `sudo` is needed only because the binary usually
lives in `/usr/local/bin`.

It deliberately does **not** restart the provider; an agent that restarts itself
mid-request drops that request. Restart when it suits you:

```bash
sudo systemctl restart computing-provider   # under systemd
# otherwise stop the process and run `computing-provider run` again
```

If a release has no binary for your platform, `update` says so and prints the
source-build commands instead.

### Verifying a release by hand

Releases publish `checksums.txt` alongside a signature over it. The checksums
prove the download arrived intact; the signature is what proves it came from the
release workflow, since anyone who could publish a release could also rewrite
the checksums to match a binary of their own.

```bash
sha256sum -c checksums.txt --ignore-missing

cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/swanchain/computing-provider/.github/workflows/release.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

Swan Inference may also mention a newer release in its reply when the agent
registers, which `computing-provider run` logs verbatim at startup. That depends
on the server sending it, so treat it as a convenience — `update --check` is
what actually asks.

## Building from source

Only needed if you would rather not use the published binary.

### Toolchain (Linux)

```bash
# Download and install Go 1.22+
wget -c https://golang.org/dl/go1.22.0.linux-amd64.tar.gz -O - | sudo tar -xz -C /usr/local

# Add Go to your PATH
echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc && source ~/.bashrc

# Verify installation
go version
```

### Toolchain (macOS)

```bash
# Install Homebrew (if not installed)
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

# Install Go
brew install go

# Install Ollama (recommended for inference)
brew install ollama

# Verify installation
go version
ollama --version
```

### Clone and build

```bash
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider

# Build for mainnet
make clean && make mainnet

# Or build for testnet
make clean && make testnet

# Install system-wide
sudo make install
```

## Using the install script

The project includes an automated installation script:

```bash
# Make the script executable
chmod +x install.sh

# Run the installation
./install.sh
```

## Verification

After installation, verify that everything is working:

```bash
# Check version
computing-provider --version

# Check help
computing-provider --help

# Initialize a new repository
computing-provider init
```

## Next Steps

After successful installation:

1. [Get started](getting-started.md) — run the setup wizard and start serving
2. [Configure your environment](configuration.md)
3. [Choose which models to serve](models.md)

## Troubleshooting

### Common Issues

**Go not found in PATH**
```bash
export PATH=$PATH:/usr/local/go/bin
```

**Permission denied errors**
```bash
sudo chmod +x computing-provider
```

**Build failures**
- Ensure Go version is 1.21+
- Check that all dependencies are installed
- Verify you have sufficient disk space

For more troubleshooting help, see the [Troubleshooting Guide](troubleshooting.md). 