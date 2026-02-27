# Installation Guide

This guide will walk you through the installation process for the Computing Provider.

## Prerequisites

Before installing the Computing Provider, ensure you have the following:

### System Requirements
- **Operating System**: Linux (Ubuntu 20.04+) or macOS (Apple Silicon recommended)
- **Go Version**: 1.22+
- **Public IP**: Optional (not required for Inference mode)

### Hardware Requirements

**Linux (NVIDIA GPU)**:
- Multi-core CPU (4+ cores recommended)
- NVIDIA GPU with 8GB+ VRAM
- 16GB+ RAM
- 100GB+ storage
- Docker with NVIDIA Container Toolkit

**macOS (Apple Silicon)**:
- Apple Silicon Mac (M1/M2/M3/M4)
- 8GB+ unified memory (16GB+ recommended)
- 20GB+ storage
- Ollama for inference

## Installing on Linux

### Install Go

```bash
# Download and install Go 1.22+
wget -c https://golang.org/dl/go1.22.0.linux-amd64.tar.gz -O - | sudo tar -xz -C /usr/local

# Add Go to your PATH
echo "export PATH=$PATH:/usr/local/go/bin" >> ~/.bashrc && source ~/.bashrc

# Verify installation
go version
```

## Installing on macOS

### Install Dependencies

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

## Building from Source

### 1. Clone the Repository

```bash
git clone https://github.com/swanchain/computing-provider.git
cd computing-provider
```

### 2. Build the Binary

```bash
# Build for mainnet
make clean && make mainnet

# Or build for testnet
make clean && make testnet

# Install system-wide
sudo make install
```

### 3. Verify Installation

```bash
# Verify installation
computing-provider --version
computing-provider --help
```

## Using the Install Script

The project includes an automated installation script:

```bash
# Make the script executable
chmod +x install.sh

# Run the installation
./install.sh
```

## Docker Installation (Alternative)

If you prefer using Docker:

```bash
# Build the Docker image
docker build -t computing-provider .

# Run the container
docker run -it --rm computing-provider --help
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

1. [Configure your environment](configuration.md)
2. [Choose your provider type](getting-started.md)

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