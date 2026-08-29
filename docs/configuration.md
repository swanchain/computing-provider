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
Models = ["meta-llama/Llama-3.2-3B-Instruct"]         # Models this provider serves (must match models.json keys)
ServiceURL = ""                                      # Optional: HTTP API URL (auto-derived from WebSocketURL if empty)
```

| Field | Required | Description |
|-------|----------|-------------|
| `Enable` | No | Enable inference mode (default: true) |
| `WebSocketURL` | No | Swan Inference WebSocket endpoint |
| `ApiKey` | Yes | Provider API key (starts with `sk-prov-`). Get one at https://inference.swanchain.io |
| `Models` | Yes | List of model names to serve (must match keys in `models.json`) |
| `ServiceURL` | No | HTTP API URL for status checks. Auto-derived from WebSocketURL if empty |

### Logging

Every field is optional — omit the whole section and the provider writes rotated logs to `$CP_PATH/logs`.

```toml
[Log]
Dir = "/mnt/data/logs/cp"   # Log directory; relative paths resolve against $CP_PATH
Level = "info"              # trace | debug | info | warn | error
MaxSizeMB = 100             # Rotate a file once it exceeds this size
MaxBackups = 5              # Rotated files to keep per level
MaxAgeDays = 30             # Delete rotated files older than this; -1 disables the age limit
Compress = true             # gzip rotated files
Stdout = true               # Also write to stdout
```

| Field | Default | Description |
|-------|---------|-------------|
| `Dir` | `$CP_PATH/logs` | Where `info.log`, `warn.log` and `error.log` are written. Set an absolute path to put logs on a different disk |
| `Level` | `info` | Minimum level to record |
| `MaxSizeMB` | `100` | Size at which a file rotates |
| `MaxBackups` | `5` | Rotated files kept per level, so disk use is bounded by roughly `MaxSizeMB × (MaxBackups + 1) × 3` |
| `MaxAgeDays` | `30` | Age-based deletion of rotated files; `-1` keeps them until `MaxBackups` evicts them |
| `Compress` | `true` | gzip rotated files |
| `Stdout` | `true` | Also write to stdout, for `journald`/`docker logs` setups |

> **Why this matters:** without rotation a provider that loses its connection to Swan Inference logs reconnect attempts continuously and can fill the disk. Rotation caps that, and `Dir` lets you keep logs off a small root volume.

### Context windows

`context_length` in `models.json` is the real window the backend accepts. It is auto-detected from `max_model_len` in `/v1/models`, which **only vLLM and SGLang expose** — for Ollama, llama.cpp, LiteLLM or any OpenAI-compatible proxy, detection yields nothing and Swan Inference assumes the catalog value instead, so long prompts are rejected with `400 exceeds the available context size`.

| Source | Meaning |
|--------|---------|
| `override` | `context_length` set in `models.json` — always wins |
| `detected` | Read from the backend's `max_model_len` |
| `not reported` | Neither available; the catalog value is assumed |
| `health check pending` | The first health check has not completed yet |

`computing-provider inference status` prints the window and source per model, and the provider logs a warning once per model when nothing is reported.

### Alerts

Optional. Set a `WebhookURL` and the provider POSTs a JSON event when something goes wrong; leave it empty and alerting is off.

```toml
[Alerts]
WebhookURL = "https://hooks.example.com/provider"
CooldownMinutes = 15        # Suppress repeats of the same event
DisconnectAfterMin = 5      # Grace period before alerting on a lost connection
ErrorRateThreshold = 0.5    # Alert when a model fails this fraction of requests
ErrorRateMinRequests = 10   # Ignore the ratio below this many requests per minute
```

Events:

| Event | Severity | Fires when |
|-------|----------|------------|
| `model_unhealthy` | critical | A model failed its health checks and was dropped from the models registered with Swan Inference |
| `model_recovered` | info | That model is answering health checks again |
| `model_error_rate` | critical | A model passes health checks but is failing most of its actual requests |
| `model_error_rate_normal` | info | That model's error rate returned to normal |
| `disconnected` | critical | The WebSocket to Swan Inference has been down longer than `DisconnectAfterMin` |
| `reconnected` | info | The connection came back |

Payload:

```json
{
  "event": "model_error_rate",
  "severity": "critical",
  "node_id": "04b089...",
  "node_name": "my-provider",
  "model_id": "openai/gpt-5.5",
  "message": "openai/gpt-5.5 failed 6 of 6 requests (100%) — health checks pass but requests are failing",
  "details": {"failed": "6", "total": "6", "ratio": "1.00"},
  "timestamp": "2026-08-27T00:15:00Z"
}
```

> **Why `model_error_rate` exists:** health checks probe `GET /v1/models`, which many backends answer without touching the inference engine. A vLLM server whose engine has died, or a proxy with expired credentials, looks healthy while failing every request. Only the request outcomes reveal it.

#### Email

Alerts can go to a mailbox instead of, or as well as, a webhook — no receiver to run:

```toml
[Alerts]
CooldownMinutes = 15

  [Alerts.Email]
  Host = "smtp.gmail.com"
  Port = 587                 # 587 = STARTTLS, 465 = implicit TLS
  Username = "you@example.com"
  From = "you@example.com"   # defaults to Username
  To = ["you@example.com"]
```

| Field | Default | Notes |
|-------|---------|-------|
| `Host` | — | Empty disables email |
| `Port` | `587` | `465` switches to implicit TLS; anything else upgrades with STARTTLS when the server offers it |
| `Username` | — | Omit for an unauthenticated relay, e.g. on localhost |
| `Password` | — | The SMTP credential — for most providers an **app password**, not your login password. **Prefer the `SMTP_PASSWORD` environment variable**; it overrides the file |
| `From` | `Username` | Envelope sender |
| `To` | — | One or more recipients |

**App password, not your account password.** Gmail, Outlook, Yahoo and most other consumer providers reject a login password over SMTP once 2FA is on, and Google removed plain-password access entirely — you need a generated app password (Google: *Account → Security → App passwords*). A company or self-hosted relay usually takes the account password, or no authentication at all on localhost.

`computing-provider alerts test` tells you which case you are in: a wrong credential comes back as `smtp auth: 535 ...` rather than failing silently later.

Keep the password out of `config.toml` — that file is often world-readable and ends up pasted into support threads. The provider reads `$CP_PATH/.env` at startup, so the secret lives in one file that only it needs:

```bash
umask 077
printf "SMTP_PASSWORD='your-app-password'\n" > $CP_PATH/.env
chmod 600 $CP_PATH/.env
```

`.env` takes `KEY=value` lines, ignores blanks and `#` comments, tolerates a leading `export`, and honours single or double quotes — which you need if the password contains a space or a `#`. A variable already set in the real environment wins, so you can override the file for one run without editing it. The provider warns if the file is readable by anyone but its owner.

An exported `SMTP_PASSWORD` works too, if you prefer to manage it yourself.

Subjects are `[node-name] SEVERITY: event — model`, so they can be filtered.

#### Testing delivery

```bash
computing-provider alerts test
```

Sends one message through every configured transport and reports what failed. Worth running right after configuring SMTP — the alternative is discovering the password is wrong during your first real incident.

Delivery is asynchronous and never blocks inference: events are queued and sent in order by a single worker, and are dropped (with a log line) if a transport is too slow to keep up. A failing transport never stops the other.

### models.json field reference

Each key in `models.json` is the marketplace model ID (must match a value in `Models` above).

| Field | Required | Description |
|-------|----------|-------------|
| `endpoint` | Yes | Base URL of the local model server (`http://localhost:PORT`) |
| `api_key` | No | API key sent to the local server (Bearer token) |
| `category` | No | Model category — `text-generation`, `image-generation`, `embedding` |
| `local_model` | No | Model name to use when forwarding to the local server. Set this when the marketplace ID differs from what the server expects — e.g. marketplace has `openai/gpt-5.5` but the server only accepts `gpt-5.5`, or Ollama uses `llama3.2:3b` for `meta-llama/Llama-3.2-3B-Instruct` |
| `gpu_memory` | No | VRAM used by this model in MB (used for load scheduling) |
| `format` | No | Weight format hint: `fp16`, `awq`, `gptq`, `gguf` |
| `quantization` | No | Quantization detail: `q4_k_m`, `q8_0`, `w4a16`, etc. |

`models.json` is watched by fsnotify — changes are hot-reloaded without a provider restart.

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

Resource pricing is configured in `$CP_PATH/price.toml`. Generate and manage with CLI commands:

```bash
computing-provider price generate   # Generate default pricing config
computing-provider price view       # View current pricing
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
Models = ["meta-llama/Llama-3.2-3B-Instruct"]         # Models this provider serves
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

1. [Start the provider](getting-started.md)
2. [Monitor tasks](cli/task.md)
