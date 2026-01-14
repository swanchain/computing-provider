# Deploy

Deploy and connect Computing Provider to Swan Inference server in development mode.

## Arguments: $ARGUMENTS

## Instructions

This command helps deploy and configure the Computing Provider to connect to a local Swan Inference server running in development mode.

---

## Testnet Information

### Base Sepolia (Swan Inference Chain)

| Property | Value |
|----------|-------|
| Network | Base Sepolia |
| Chain ID | 84532 |
| RPC | https://sepolia.base.org |
| Explorer | https://sepolia.basescan.org |

### Deployed Contracts

| Contract | Address |
|----------|---------|
| Collateral | `0x5EBc65E856ad97532354565560ccC6FAB51b255a` |
| Task | `0x6c1f6ad2b4Cb8A7ba4027b348D7f20A14706d3C2` |

### Authentication Mode

**Development:** Node ID based authentication (no on-chain registration required)
- Provider authenticates via wallet signature
- No gas fees required for testing
- Suitable for local development

**Production:** On-chain CP account registration on Swan Chain required

---

## Development Server Configuration

### Swan Inference Dev Server

Connect to the local Swan Inference server:
- **HTTP API**: http://localhost:8080
- **WebSocket**: ws://localhost:8081 (used by ECP2 client)

---

## Deployment Steps

### Step 1: Verify Prerequisites

Ensure the following are running:
1. Swan Inference server on `localhost:8080`
2. Docker daemon with NVIDIA Container Toolkit (for GPU support)
3. Computing Provider built for testnet (`make testnet && make install`)

### Step 2: Configure ECP2 for Dev Mode

Update `$CP_PATH/config.toml` to point to the local dev server:

```toml
[API]
Port = 8085
MultiAddress = "/ip4/127.0.0.1/tcp/8085"
Domain = "localhost"
PortRange = ["40000-40050"]

[RPC]
# Use local or testnet RPC
SWAN_CHAIN_RPC = "https://rpc-testnet.swanchain.io"

[ECP2]
Enable = true
ServiceURL = "http://localhost:8080"      # HTTP API (not currently used by client)
WebSocketURL = "ws://localhost:8081"      # WebSocket connection to Swan Inference
Models = ["llama-3.1-8b", "qwen2.5-7b"]   # Models this provider serves

# Base Sepolia (Swan Inference chain)
ChainRPC = "https://sepolia.base.org"
CollateralContract = "0x5EBc65E856ad97532354565560ccC6FAB51b255a"
TaskContract = "0x6c1f6ad2b4Cb8A7ba4027b348D7f20A14706d3C2"
```

**Note:** The ECP2 client connects to Swan Inference via WebSocket (`WebSocketURL`), not HTTP.
Contracts are deployed on Base Sepolia (chainId: 84532).

### Step 3: Set Environment Variables

```bash
export CP_PATH=~/.swan/computing

# Optional: Override WebSocket URL for dev (takes precedence over config.toml)
export ECP2_WS_URL=ws://localhost:8081
```

### Step 4: Start Computing Provider in Dev Mode

```bash
# Start the UBI daemon connecting to local swan-inference
computing-provider ubi daemon
```

---

## Dev Server Endpoints

When connected to Swan Inference dev server:

**HTTP API (localhost:8080)**
| Endpoint | Description |
|----------|-------------|
| `GET /health` | Health check |
| `POST /v1/inference` | Submit inference request |
| `GET /v1/models` | List available models |

**WebSocket (ws://localhost:8081)**
| Message Type | Description |
|--------------|-------------|
| `register` | Provider registration with models |
| `inference` | Inference request from server |
| `heartbeat` | Provider liveness check |
| `stream_chunk` | Streaming response chunk |

---

## Troubleshooting Dev Mode

### Connection Refused
```bash
# Verify swan-inference HTTP API is running
curl http://localhost:8080/health

# Check WebSocket port is listening
nc -zv localhost 8081
```

### Port Already in Use
```bash
# Check what's using port 8080
lsof -i :8080
```

### Docker Permission Issues
```bash
# Run with docker group
sg docker -c "computing-provider ubi daemon"
```

---

## Quick Start Commands

```bash
# 1. Build for testnet
make clean && make testnet && make install

# 2. Initialize (if not already done)
computing-provider init --multi-address=/ip4/127.0.0.1/tcp/8085 --node-name=dev-provider

# 3. Edit config to enable ECP2
# $CP_PATH/config.toml:
#   [ECP2]
#   Enable = true

# 4. Start daemon with dev WebSocket URL override
ECP2_WS_URL=ws://localhost:8081 computing-provider ubi daemon
```
