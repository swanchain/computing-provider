# Prerequisites

Before setting up your Computing Provider, ensure your system meets all the necessary requirements.

## System Requirements

### Operating System
- **Linux**: Ubuntu 20.04+ (recommended)
- **Architecture**: x86_64/amd64
- **Kernel**: Linux kernel 5.4+

### Hardware Requirements

#### Minimum Requirements (ECP2)
- **CPU**: 4 cores, 2.0 GHz
- **RAM**: 8GB
- **Storage**: 100GB available space
- **Network**: 100 Mbps internet connection
- **GPU**: NVIDIA GPU with 8GB+ VRAM

#### Minimum Requirements (ECP - ZK Proofs)
- **CPU**: 8 cores, 3.0 GHz
- **RAM**: 16GB
- **Storage**: 300GB available space (200GB for v28 parameters)
- **Network**: 100 Mbps internet connection
- **GPU**: NVIDIA GPU with 8GB+ VRAM and CUDA support

#### Recommended Requirements
- **CPU**: 8+ cores, 3.0 GHz+
- **RAM**: 32GB+
- **Storage**: 500GB+ SSD
- **Network**: 1 Gbps internet connection
- **GPU**: NVIDIA RTX 3090/4090 or A100

### Software Dependencies

#### Required Software
- **Go**: Version 1.22+
- **Docker**: Version 20.10+
- **NVIDIA Drivers**: Latest stable (for GPU support)
- **NVIDIA Container Toolkit**: For Docker GPU access

#### Optional Software
- **Nginx**: For reverse proxy
- **Certbot**: For SSL certificate management

## Network Requirements

### Public IP Address
- A static public IP address is required
- Port forwarding: Map internal port 8085 to public IP

### Domain Name (ECP2)
- A wildcard domain (e.g., `*.example.com`) for ECP2 services
- DNS records properly configured

### Ports
- **8085**: Provider API port
- **40000-40050**: Container port range (configurable)

## Docker Setup

### Install Docker

```bash
# Install Docker
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# Add user to docker group
sudo usermod -aG docker $USER

# Start Docker service
sudo systemctl enable docker
sudo systemctl start docker
```

### Install NVIDIA Container Toolkit

```bash
# Add NVIDIA repository
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

# Install toolkit
sudo apt-get update
sudo apt-get install -y nvidia-container-toolkit

# Configure Docker runtime
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker
```

### Verify GPU Access in Docker

```bash
# Test GPU access
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi
```

## ECP Prerequisites (ZK Proofs)

### Download v28 Parameters

The v28 parameters are required for ZK proof generation (~200GB):

```bash
# Set parameter path
export FIL_PROOFS_PARAMETER_CACHE=/path/to/v28/params
mkdir -p $FIL_PROOFS_PARAMETER_CACHE

# Download parameters (see Filecoin documentation for download instructions)
```

### Configure GPU for ZK Proofs

```bash
# Set GPU configuration
export RUST_GPU_TOOLS_CUSTOM_GPU="<GPU_MODEL>:<CORES>"
# Example: "GeForce RTX 4090:16384"

# Add to shell profile
echo 'export RUST_GPU_TOOLS_CUSTOM_GPU="GeForce RTX 4090:16384"' >> ~/.bashrc
```

## Environment Setup

### Environment Variables

```bash
# Set the computing provider path
export CP_PATH=~/.swan/computing

# Add to your shell profile
echo "export CP_PATH=~/.swan/computing" >> ~/.bashrc
```

### Directory Structure

```bash
# Create necessary directories
mkdir -p ~/.swan/computing
```

## Verification Commands

### Check System Requirements

```bash
# Check Go version
go version

# Check Docker
docker --version

# Check Docker GPU access
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi

# Check available memory
free -h

# Check available disk space
df -h

# Check network connectivity
ping -c 3 google.com
```

### Check GPU Support

```bash
# Check NVIDIA drivers
nvidia-smi

# Check CUDA installation
nvcc --version

# Verify GPU in Docker
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi
```

## Pre-installation Checklist

- [ ] Go 1.22+ installed and in PATH
- [ ] Docker installed and running
- [ ] NVIDIA drivers installed
- [ ] NVIDIA Container Toolkit installed
- [ ] Docker GPU access verified
- [ ] Public IP address available
- [ ] Domain name configured (for ECP2)
- [ ] Environment variables configured
- [ ] Required directories created
- [ ] v28 parameters downloaded (for ECP ZK proofs)

## Next Steps

Once all prerequisites are met:

1. [Install the Computing Provider](installation.md)
2. [Configure your environment](configuration.md)
3. [Get started](getting-started.md)
