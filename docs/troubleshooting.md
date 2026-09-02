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

## Error reference

| Error | Solution |
|-------|----------|
| `go: command not found` | Install Go 1.22+, see [go.dev/dl](https://go.dev/dl/) — or skip the toolchain and install the [published binary](installation.md#installing-the-binary) |
| `permission denied...docker.sock` | `sudo usermod -aG docker $USER`, then start a new shell |
| `could not select device driver "nvidia"` | Install the [NVIDIA Container Toolkit](installation.md#install-nvidia-container-toolkit), then restart Docker |
| `authentication required` | Set `ApiKey` in `config.toml` or the `INFERENCE_API_KEY` env var |
| `invalid provider API key` | Key must start with `sk-prov-` and not be revoked. Consumer keys (`sk-swan-*`) do not work |
| `WebSocket connection failed` | Check `WebSocketURL` and outbound connectivity on port 443 |
| `cuda>=12.x unsatisfied condition` | Driver too old for the latest SGLang image — use an older tag, e.g. `lmsysorg/sglang:v0.4.7.post1-cu124` |
| Provider receives no requests | `models.json` keys must match both your server's `--served-model-name` and a catalog model ID |

### Check logs

```bash
tail -f cp.log        # provider
docker logs sglang    # inference server
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

## FAQ

### Setup and installation

**`make mainnet` fails with `go: command not found`**
Install Go 1.22+ — on Linux download from [go.dev/dl](https://go.dev/dl/) and add
it to `PATH`; on macOS `brew install go`. Restart your shell or `source ~/.bashrc`
afterwards. You can also skip building entirely and install the
[published binary](installation.md#installing-the-binary).

**SGLang container fails with `cuda>=12.x unsatisfied condition`**
Your NVIDIA driver is too old for the latest SGLang image:

```
nvidia-container-cli: requirement error: unsatisfied condition: cuda>=12.9, please update your driver to a newer version, or use an earlier cuda container
```

Check what your driver supports — `nvidia-smi` shows the maximum CUDA version in
the top-right corner — then either update the driver
(`sudo apt install nvidia-driver-550`) or pin an image matching what you have:

```bash
docker run -d --gpus all -p 30000:30000 --ipc=host --name sglang \
  -v ~/.swan/models/Qwen/Qwen2.5-7B-Instruct:/models \
  lmsysorg/sglang:v0.4.7.post1-cu124 \
  python3 -m sglang.launch_server --model-path /models \
    --host 0.0.0.0 --port 30000 \
    --served-model-name Qwen/Qwen2.5-7B-Instruct
```

**`computing-provider setup` does not detect my running model server**
The wizard scans ports 30000, 8080 and 11434, and it only finds servers that are
already running — start yours first. Verify by hand:

```bash
curl http://localhost:30000/v1/models   # SGLang/vLLM
curl http://localhost:11434/api/tags    # Ollama
```

On a non-standard port the wizard may miss it; edit
`~/.swan/computing/models.json` afterwards.

### Models

**My provider is online but receives no inference requests**
Almost always a name mismatch. The `--served-model-name` on your SGLang/vLLM
command must match the key in `models.json` exactly, and that key must match a
model ID registered on Swan Inference. `computing-provider models catalog` lists
the valid IDs, and `computing-provider selfcheck` reports the mismatch directly.

**SGLang container starts but immediately exits**
Check `docker logs sglang`. Usual causes:

- **Out of VRAM** — split across GPUs with `--tp 2` (or `--tp 4`). A 12B model in
  bf16 needs ~23 GB, too much for one 24 GB card once the KV cache is included,
  but comfortable across two.
- **Unbalanced GPU memory** — if another server already holds one of your GPUs,
  SGLang fails with `memory capacity is unbalanced`. Pin it to free GPUs instead
  of `--gpus all`:

  ```bash
  nvidia-smi   # see which GPUs are free
  docker run -d --gpus '"device=0,2"' -p 30000:30000 --ipc=host \
    -v ~/.swan/models/YourModel:/models \
    lmsysorg/sglang:v0.4.7.post1-cu124 \
    python3 -m sglang.launch_server --model-path /models \
      --host 0.0.0.0 --port 30000 --tp 2 \
      --served-model-name YourModel
  ```

- **Shared memory** — add `--shm-size 4g`.
- **Port conflict** — check `docker ps` or `lsof -i :30000`.

**`models download` fails for Llama or other gated models**
Accept the licence on the model's HuggingFace page, then set a token:

```bash
export HF_TOKEN=hf_xxxxxxxxxxxxxxxxxxxxx
computing-provider models download meta-llama/Llama-3.3-70B-Instruct
```

### Connection and authentication

**`WebSocket connection failed`, or the provider cannot connect**

- `WebSocketURL` in `~/.swan/computing/config.toml` must be
  `wss://inference-ws.swanchain.io` — not `http://` or `https://`
- Outbound port 443 must not be blocked by a firewall or cloud security group
- Corporate proxies often block WebSocket upgrades; check with your network admin

**`invalid provider API key` or `authentication required`**

- The key must start with `sk-prov-`. Consumer keys (`sk-swan-*`) will not work
- It lives under `[Inference].ApiKey` in `~/.swan/computing/config.toml`
- Or set `INFERENCE_API_KEY` in the environment

**Provider is stuck in `pending`**
Activation is automatic once collateral is deposited, the GPU meets the minimum
hardware requirements, and the registration benchmark passes. Check with
`computing-provider inference status`. If you are only testing, ask on
[Discord](https://discord.gg/3uQUWzaS7U) about dev mode, which skips these.

### Earnings and collateral

**How do I earn?**
Per token — input and output tokens of every request you serve, times that
model's published payout price. There is no UBI and no allocation for idle
hardware; only served traffic earns. `computing-provider inference status` shows
your current stage and earnings. Request a payout from the dashboard once your
withdrawable balance reaches $10 (flat $1 fee), having set a beneficiary first
with `computing-provider inference set-beneficiary 0x...`.

**What are the collateral deposit options?**
Collateral is required before activation, by card (Stripe, through the Provider
Dashboard) or on-chain (USDC on Ethereum or Base, SWAN on Swan Chain). Run
`computing-provider inference deposit` for chains, contract addresses and
minimums. It is refundable with a 7-day waiting period.

**What happens if I fail benchmarks?**
Periodic benchmarks (math, code, reasoning, latency) verify provider quality, and
passing resets your failure counter. Consecutive failures can result in
collateral slashing — by default 10% after 2 consecutive failures.

### Configuration

**I edited `config.toml` but nothing changed**
Check which file you edited. The provider reads
`~/.swan/computing/config.toml` (or wherever `$CP_PATH` points), **not** the
`config.toml` in the git checkout.

**How do I change models without restarting?**
Edit `~/.swan/computing/models.json` — it is watched and hot-reloaded. To force
it:

```bash
curl -X POST http://localhost:8085/api/v1/computing/inference/models/reload
```

**Port 8085 or 30000 is already in use**

```bash
lsof -i :30000        # what is holding it
docker ps             # leftover containers
docker rm -f sglang   # remove an old one
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
