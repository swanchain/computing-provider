# Troubleshooting Guide

This guide helps you resolve common issues when running the Go Computing Provider.

## Quick Diagnostic Commands

```bash
# Check provider status
computing-provider state

# Check configuration
computing-provider info

# Check inference status (API key, registration)
computing-provider inference status

# Check inference config
computing-provider inference config

# Check wallet status
computing-provider wallet list

# Check system resources
htop
free -h
df -h
nvidia-smi  # if using GPU
```

## Common Issues

### 1. Provider Won't Start

#### Symptoms
- Provider fails to start
- Error messages about missing configuration
- Permission denied errors

#### Solutions

**Check Repository Path**
```bash
# Verify CP_PATH is set
echo $CP_PATH

# Check if repository exists
ls -la ~/.swan/computing/

# Initialize if missing
computing-provider init --multi-address=/ip4/<PUBLIC_IP>/tcp/<PORT> --node-name=<NAME>
```

**Check Configuration**
```bash
# Check configuration file
cat ~/.swan/computing/config.toml

# Reinitialize if config is corrupted
computing-provider init --multi-address=/ip4/<PUBLIC_IP>/tcp/<PORT> --node-name=<NAME>
```

**Check Permissions**
```bash
# Fix repository permissions
chmod -R 755 ~/.swan/computing/

# Check file ownership
ls -la ~/.swan/computing/
```

### 2. Docker Issues

#### Symptoms
- "permission denied...docker.sock" errors
- Container startup failures
- GPU not accessible in containers

#### Solutions

**Docker Permission Error**
```bash
# Add user to docker group
sudo usermod -aG docker $USER

# Apply group changes without logout
newgrp docker

# Or run with sg
sg docker -c "computing-provider run"
```

**Container Already Exists**
```bash
# Remove existing container
docker rm -f resource-exporter
```

**GPU Not Available in Docker**
```bash
# Check NVIDIA Container Toolkit installation
nvidia-container-cli info

# Reinstall NVIDIA Container Toolkit
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
sudo apt-get update && sudo apt-get install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker
sudo systemctl restart docker

# Test GPU access
docker run --rm --gpus all nvidia/cuda:12.0-base-ubuntu22.04 nvidia-smi
```

### 3. Wallet Issues

#### Symptoms
- "Wallet not found" errors
- "Invalid private key" errors
- Balance showing as zero

#### Solutions

**Check Wallet Status**
```bash
# List wallets
computing-provider wallet list

# Verify addresses
computing-provider info
```

**Reinitialize Wallet**
```bash
# Backup existing wallet (if needed)
cp -r ~/.swan/computing/keystore ~/.swan/computing/keystore.backup

# Create new wallet
computing-provider wallet new

# Or import existing wallet
computing-provider wallet import <private_key_file>
```

**Check Network Configuration**
```bash
# Test RPC endpoint
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  https://mainnet-rpc.swanchain.io
```

### 4. Inference Mode Issues

#### Symptoms
- "authentication required" or "invalid provider API key" errors
- WebSocket connection failures
- Provider not receiving inference requests

#### Solutions

**API Key Not Configured**
```bash
# Check your current config
computing-provider inference config

# Check status on Swan Inference
computing-provider inference status

# Set API key in config.toml
# [Inference]
# ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"

# Or set via environment variable
export INFERENCE_API_KEY=sk-prov-xxxxxxxxxxxxxxxxxxxx
```

**WebSocket Connection Failed**
```bash
# Check that WebSocketURL is correct in config.toml
computing-provider inference config

# Test network connectivity to Swan Inference
curl -s https://inference.swanchain.io/api/v1/health

# For local development, override the WebSocket URL
export INFERENCE_WS_URL=ws://localhost:8081
```

**Provider Not Receiving Requests**
```bash
# Verify models are configured in both models.json AND config.toml
# models.json defines endpoints, config.toml Models array enables them
computing-provider inference config

# Check model health via REST API
curl http://localhost:8085/api/v1/computing/inference/models

# Force reload models.json
curl -X POST http://localhost:8085/api/v1/computing/inference/models/reload
```

### 5. Network Connectivity Issues

#### Symptoms
- "Connection refused" errors
- Timeout errors
- RPC endpoint not responding

#### Solutions

**Check Network Configuration**
```bash
# Test internet connectivity
ping -c 3 google.com

# Test RPC endpoint
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  https://mainnet-rpc.swanchain.io

# Check firewall settings
sudo ufw status
```

**Update RPC Endpoint**

Edit `~/.swan/computing/config.toml`:
```toml
[RPC]
SWAN_CHAIN_RPC = "https://mainnet-rpc.swanchain.io"
```

### 6. Task Execution Issues

#### ECP2/ECP Issues

**CP Account Empty**
```bash
# Create account first
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 4
```

**GPU Not Detected**
```bash
# Check NVIDIA drivers
nvidia-smi

# Check CUDA installation
nvcc --version

# Check GPU availability
lspci | grep -i nvidia
```

**Task Failures**
```bash
# Check task details
computing-provider task get <job_uuid>

# Check system logs
tail -f cp.log

# Check resource usage
htop
nvidia-smi
```

**UBI/ZK Parameters Missing (ECP Mode)**
```bash
# Set parameter path
export FIL_PROOFS_PARAMETER_CACHE=/path/to/v28/params

# Verify parameters exist
ls -la $FIL_PROOFS_PARAMETER_CACHE
```

### 7. Resource Exhaustion

#### Symptoms
- Tasks stuck in pending
- System becomes unresponsive
- Out of memory errors

#### Solutions

**Check Resource Usage**
```bash
# Monitor system resources
htop
free -h
df -h
nvidia-smi
```

**Clean Up Docker Resources**
```bash
# Remove stopped containers
docker container prune

# Remove unused images
docker image prune

# Remove all unused resources
docker system prune
```

### 8. Collateral Issues

#### Symptoms
- "Insufficient collateral" errors
- Cannot add collateral
- Withdrawal failures

#### Solutions

**Check Collateral Status**
```bash
# Check provider info (includes collateral)
computing-provider info

# Check account balance
computing-provider wallet list
```

**Add Collateral**
```bash
# Add collateral for ECP/ECP2
computing-provider collateral add --ecp --from <OWNER_ADDRESS> <AMOUNT>

# Verify addition
computing-provider info
```

**Withdrawal Issues**
```bash
# Request withdrawal (7-day waiting period)
computing-provider collateral withdraw-request --ecp --owner <OWNER_ADDRESS> <AMOUNT>

# Confirm withdrawal after 7 days
computing-provider collateral withdraw-confirm --ecp --owner <OWNER_ADDRESS>
```

## Performance Issues

### Slow Task Execution

**Check System Performance**
```bash
# Monitor CPU usage
top -p $(pgrep computing-provider)

# Monitor memory usage
free -h

# Monitor disk I/O
iotop

# Monitor GPU usage
nvidia-smi -l 1
```

### High Resource Usage

**Identify Resource Hogs**
```bash
# Find processes using most CPU
ps aux --sort=-%cpu | head -10

# Find processes using most memory
ps aux --sort=-%mem | head -10

# Check disk usage
du -sh /* | sort -hr | head -10
```

## Log Analysis

### Understanding Logs

**Provider Logs**
```bash
# View recent logs
tail -f cp.log

# Search for errors
grep -i error cp.log

# Search for warnings
grep -i warning cp.log

# Search for specific task
grep <job_uuid> cp.log
```

**System Logs**
```bash
# Check system logs
journalctl -f

# Check kernel logs
dmesg | tail -20

# Check Docker logs
docker logs <container_name>
```

## Recovery Procedures

### Complete Reset

**Backup Important Data**
```bash
# Backup configuration
cp -r ~/.swan/computing ~/.swan/computing.backup

# Backup wallet
cp -r ~/.swan/computing/keystore ~/.swan/computing/keystore.backup
```

**Reset Provider**
```bash
# Stop provider
pkill computing-provider

# Remove repository
rm -rf ~/.swan/computing

# Reinitialize
computing-provider init --multi-address=/ip4/<PUBLIC_IP>/tcp/<PORT> --node-name=<NAME>

# Restore wallet
cp -r ~/.swan/computing.backup/keystore ~/.swan/computing/

# Restore configuration
cp ~/.swan/computing.backup/config.toml ~/.swan/computing/
```

## Getting Help

### Before Asking for Help

1. **Collect Information**
   ```bash
   # System information
   uname -a
   cat /etc/os-release

   # Provider information
   computing-provider info
   computing-provider state

   # Logs
   tail -100 cp.log
   ```

2. **Document the Issue**
   - What were you trying to do?
   - What error messages did you see?
   - What steps did you take?
   - What is your system configuration?

### Support Channels

- **Discord**: [Swan Chain Community](https://discord.gg/swanchain)
- **GitHub**: [Issue Tracker](https://github.com/swanchain/computing-provider/issues)
- **Documentation**: [Swan Chain Docs](https://docs.swanchain.io)

### Useful Commands for Support

```bash
# Generate debug information
computing-provider info > debug_info.txt
computing-provider state >> debug_info.txt
tail -100 cp.log >> debug_info.txt
nvidia-smi >> debug_info.txt
docker ps -a >> debug_info.txt
```

## Prevention

### Regular Maintenance

1. **Monitor System Resources**
   ```bash
   # Set up monitoring
   watch -n 30 'nvidia-smi'
   ```

2. **Regular Backups**
   ```bash
   # Backup configuration
   cp -r ~/.swan/computing ~/.swan/computing.backup.$(date +%Y%m%d)
   ```

3. **Update Software**
   ```bash
   # Update system
   sudo apt update && sudo apt upgrade

   # Update provider
   git pull
   make clean && make mainnet
   make install
   ```

4. **Check Logs Regularly**
   ```bash
   # Monitor logs
   tail -f cp.log
   ```
