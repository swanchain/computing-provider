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

### ECP2 Configuration (Inference)

```toml
[ECP2]
Enable = false                  # Enable ECP2/Swan Inference mode
ServiceURL = "http://localhost:8080"      # HTTP API (not used currently)
WebSocketURL = "ws://localhost:8081"      # WebSocket connection to Swan Inference
Models = []                               # Models this provider serves

# Base Sepolia contracts (for development/testing)
ChainRPC = "https://sepolia.base.org"
CollateralContract = "0x5EBc65E856ad97532354565560ccC6FAB51b255a"
TaskContract = "0x6c1f6ad2b4Cb8A7ba4027b348D7f20A14706d3C2"
```

**Environment variable override:**
```bash
export ECP2_WS_URL=ws://localhost:8081  # Override WebSocket URL
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
# For ECP2 (inference) - task type 4
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 4

# For ECP (ZK proofs) - task types 1,2,4
computing-provider account create \
  --ownerAddress <OWNER_ADDRESS> \
  --workerAddress <WORKER_ADDRESS> \
  --beneficiaryAddress <BENEFICIARY_ADDRESS> \
  --task-types 1,2,4
```

### Add Collateral

```bash
# Add collateral for ECP/ECP2
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

## ECP2 Mode Configuration

For AI inference with Swan Inference:

```toml
[API]
Domain = "*.example.com"        # Wildcard domain for services
PortRange = ["40000-40050", "40060"]

[ECP2]
Enable = true
WebSocketURL = "wss://inference.swanchain.io/ws"  # Production
Models = ["your-model-name"]

# For development (Base Sepolia testnet)
# WebSocketURL = "ws://localhost:8081"
# ChainRPC = "https://sepolia.base.org"
# CollateralContract = "0x5EBc65E856ad97532354565560ccC6FAB51b255a"
# TaskContract = "0x6c1f6ad2b4Cb8A7ba4027b348D7f20A14706d3C2"
```

### Base Sepolia Development

For testing ECP2 with Swan Inference on Base Sepolia:

| Contract | Address |
|----------|---------|
| Collateral | `0x5EBc65E856ad97532354565560ccC6FAB51b255a` |
| Task | `0x6c1f6ad2b4Cb8A7ba4027b348D7f20A14706d3C2` |

**Network:** Base Sepolia (chainId: 84532)
**RPC:** `https://sepolia.base.org`
**Explorer:** https://sepolia.basescan.org

### Development Mode (No On-Chain Registration)

For local development, ECP2 supports Node ID based authentication without requiring on-chain account registration:

```bash
# Build for testnet
make clean && make testnet

# Start with local Swan Inference
ECP2_WS_URL=ws://localhost:8081 ./computing-provider ubi daemon
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
