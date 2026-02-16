# Configuration

This guide covers all configuration aspects of the Go Computing Provider.

## Configuration Files

The Computing Provider uses configuration files located in your repository directory (`~/.swan/computing` by default).

### Main Configuration File

The main configuration file is `config.toml`. Initialize it with:

```bash
# Initialize computing provider repository
computing-provider init --multi-address=/ip4/<PUBLIC_IP>/tcp/<PORT> --node-name=<NAME>
```

## Configuration Structure

### API Configuration

```toml
[API]
Port = 8085
MultiAddress = "/ip4/<PUBLIC_IP>/tcp/<PORT>"
Domain = "*.example.com"        # Domain for single-port services
NodeName = "my-computing-provider"
PortRange = ["40000-40050"]     # Ports for multi-port containers
Pricing = true
```

### UBI Configuration (ZK Proofs)

```toml
[UBI]
UbiEnginePk = ""                # ZK engine public key (auto-configured)
EnableSequencer = true          # Submit proofs to Sequencer (reduces gas)
AutoChainProof = false          # Fallback to chain when sequencer unavailable
VerifySign = true
```

### RPC Configuration

```toml
[RPC]
SWAN_CHAIN_RPC = "https://mainnet-rpc.swanchain.io"
```

### Registry Configuration (Optional)

```toml
[Registry]
ServerAddress = ""              # Docker registry for image storage
UserName = ""
Password = ""
```

### Inference Mode Configuration

```toml
[Inference]
Enable = true                                        # Inference mode is enabled by default
WebSocketURL = "wss://inference-ws.swanchain.io"     # Swan Inference WebSocket endpoint
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"               # Required: Provider API key from https://inference.swanchain.io
Models = ["llama-3.2-3b"]                            # Models this provider serves (must match models.json)
ServiceURL = ""                                      # Optional: HTTP API URL (auto-derived from WebSocketURL if empty)
```

| Field | Required | Description |
|-------|----------|-------------|
| `Enable` | No | Enable inference mode (default: true) |
| `WebSocketURL` | No | Swan Inference WebSocket endpoint |
| `ApiKey` | Yes | Provider API key (starts with `sk-prov-`). Get one at https://inference.swanchain.io |
| `Models` | Yes | List of model names to serve (must match keys in `models.json`) |
| `ServiceURL` | No | HTTP API URL for status checks. Auto-derived from WebSocketURL if empty |

**Environment variable overrides:**
```bash
export INFERENCE_WS_URL=ws://localhost:8081      # Override WebSocket URL for dev
export INFERENCE_API_KEY=sk-prov-your-key-here   # Override API key
```

## Environment Variables

The CLI respects the `CP_PATH` environment variable:

```bash
# Set repository path
export CP_PATH=~/.swan/computing

# Or use flag
computing-provider --repo /custom/path init
```

## Wallet Configuration

### Address Types

The Computing Provider uses three different wallet addresses:

1. **Owner Address**: Controls account settings and permissions
2. **Worker Address**: Used for submitting proofs and paying gas fees
3. **Beneficiary Address**: Receives all earnings

### Setting Up Wallets

```bash
# Create new wallet
computing-provider wallet new

# Import existing wallet
computing-provider wallet import <private_key_file>

# List wallets
computing-provider wallet list
```

## Account Configuration

### Create Account

```bash
# For Inference mode - task type 4
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 4

# For ZK proofs - task types 1,2,4
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 1,2,4
```

### Add Collateral

```bash
# Add collateral for Inference/ZK-Proof modes
computing-provider collateral add --ecp --from <OWNER_ADDRESS> <AMOUNT>
```

## Network Configuration

### Supported Networks

- **Mainnet**: Chain ID 254
- **Testnet**: Chain ID 20241133

### RPC Endpoints

```toml
[RPC]
# Mainnet
SWAN_CHAIN_RPC = "https://mainnet-rpc.swanchain.io"

# Testnet
SWAN_CHAIN_RPC = "https://testnet-rpc.swanchain.io"
```

## Pricing Configuration

Resource pricing is configured in `$CP_PATH/price.toml`:

```toml
[pricing]
cpu_per_hour = "0.01"           # Price per CPU core per hour
memory_per_hour = "0.005"       # Price per GB RAM per hour
gpu_per_hour = "0.50"           # Price per GPU per hour
storage_per_hour = "0.001"      # Price per GB storage per hour
```

## Inference Mode Configuration

For AI inference with Swan Inference:

```toml
[API]
Domain = "*.example.com"        # Wildcard domain for services (optional for Inference mode)
PortRange = ["40000-40050", "40060"]

[Inference]
Enable = true                                        # Enabled by default
WebSocketURL = "wss://inference-ws.swanchain.io"     # Production
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"               # Required: your provider API key
Models = ["llama-3.2-3b"]                            # Models this provider serves
```

To verify your configuration:
```bash
computing-provider inference config    # Show current inference config
computing-provider inference status    # Check status on Swan Inference
```

### Development Mode (Local Testing)

For local development, Inference mode supports Node ID based authentication without requiring on-chain account registration:

```bash
# Build for testnet
make clean && make testnet

# Start with local Swan Inference
INFERENCE_WS_URL=ws://localhost:8081 ./computing-provider run
```

**Authentication Flow:**
1. Provider connects to Swan Inference via WebSocket
2. Sends registration with Node ID and wallet signature
3. Swan Inference verifies signature and registers provider
4. No on-chain transaction required

This is suitable for:
- Local development and testing
- Integration testing with Swan Inference
- Rapid iteration without gas costs

For production, on-chain account registration on Swan Chain is required for collateral and rewards.

## ECP Mode Configuration (ZK Proofs)

For ZK proof generation:

```bash
# Required environment variables
export FIL_PROOFS_PARAMETER_CACHE=<path_to_v28_params>
export RUST_GPU_TOOLS_CUSTOM_GPU="<GPU_MODEL>:<CORES>"
```

```toml
[UBI]
EnableSequencer = true
AutoChainProof = false
```

## Validation

Verify your configuration:

```bash
# Check provider information
computing-provider info

# Check provider state
computing-provider state
```

## Troubleshooting Configuration

### Common Issues

1. **Invalid TOML syntax**: Use a TOML validator
2. **Missing required fields**: Check the sample configuration
3. **Permission errors**: Ensure proper file permissions
4. **Network connectivity**: Verify RPC endpoints

### Debug Commands

```bash
# Show provider info
computing-provider info

# Show provider state
computing-provider state
```

## Next Steps

After configuring your Computing Provider:

1. [Set up your wallet](cli/wallet.md)
2. [Start the provider](getting-started.md)
3. [Monitor tasks](cli/task.md)
