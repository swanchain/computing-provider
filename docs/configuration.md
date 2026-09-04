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

### Where the provider repo lives

`$CP_PATH` (default `~/.swan/computing`) holds this node's identity, not just its settings:

| File | Why it matters |
|------|----------------|
| `private_key` | **This is the node.** Lose it and the provider identity, its reputation and its collateral association are gone; there is no recovery. |
| `machine_fingerprint` | Pins the key to this machine; a mismatch warns that the key was copied |
| `config.toml` | Contains the `sk-prov-` API key |
| `models.json` | May contain per-backend API keys |
| `.env` | Secrets read into the environment |

Two rules follow.

**Keep it out of any git working tree.** `.gitignore` stops the files being committed, but `git clean -xfd` removes *ignored* files too — one routine command deletes `private_key` and the node's identity with it. The provider warns at startup if `$CP_PATH` sits inside a checkout.

**Keep it owner-only.** The provider creates these `0600` (directories `0700`) and repairs anything looser at startup, reporting what it changed. Early versions created them world-readable, so an existing install will tighten itself on the next start.

Back up `private_key` somewhere safe. It is 32 bytes and irreplaceable.

### Context windows

`context_length` in `models.json` is the real window the backend accepts. It is auto-detected from `max_model_len` in `/v1/models`, which **only vLLM and SGLang expose** — for Ollama, llama.cpp, LiteLLM or any OpenAI-compatible proxy, detection yields nothing and Swan Inference assumes the catalog value instead, so long prompts are rejected with `400 exceeds the available context size`.

| Source | Meaning |
|--------|---------|
| `override` | `context_length` set in `models.json` — always wins |
| `detected` | Read from the backend's `max_model_len` |
| `not reported` | Neither available; the catalog value is assumed |
| `health check pending` | The first health check has not completed yet |

`computing-provider inference status` prints the window and source per model, and the provider logs a warning once per model when nothing is reported.

### Self-check and auto-heal

The provider audits itself on a timer and, when a backend cannot serve, takes the model out of routing until it can again.

```toml
[SelfCheck]
Enable = true
IntervalMinutes = 10
AutoDisable = true
AutoRecover = true
FailuresBeforeDisable = 2
```

| Field | Default | Description |
|-------|---------|-------------|
| `Enable` | `true` | Run the periodic audit |
| `IntervalMinutes` | `10` | How often to audit; re-read each tick, so a change applies without a restart |
| `AutoDisable` | `true` | Deregister a model whose backend cannot serve |
| `AutoRecover` | `true` | Re-register it once the backend works again |
| `FailuresBeforeDisable` | `2` | Consecutive failed probes required, so one blip does not pull a model |

**Why deregister.** Health checks probe `GET /v1/models`, which most backends answer without touching the inference engine. A model can therefore look healthy while failing every request — a dead engine, expired upstream credentials — and the marketplace keeps routing to it. Every failure is attributed to your node. No traffic is better than traffic that fails.

**Which failures count.** Only ones the backend owns: 5xx, 401, 403, 404, and connection errors or timeouts. A `400` is the client's — an over-long prompt against a wider advertised window looks identical to a broken model in a raw failure count, and disabling on it would remove a healthy, earning model. A `429` is your own rate limiter. Neither counts.

**A model you disabled by hand is never re-enabled.** The provider only re-registers models it deregistered itself.

Both transitions raise `model_auto_disabled` and `model_auto_recovered` alerts.

### Dashboard

The web UI served by `computing-provider dashboard`. Both fields are optional.

```toml
[Dashboard]
Host = "127.0.0.1"   # use 0.0.0.0 only on a network you trust
Port = 3060
```

| Field | Default | Notes |
|-------|---------|-------|
| `Host` | `127.0.0.1` | The dashboard proxies to the provider API, which has endpoints that can take models out of service — so exposing it is a deliberate act, not the default |
| `Port` | `3060` | Change it if something else on the host already uses this port |

Command-line flags win over the config, so `--host` and `--port` still override for a one-off run:

```bash
computing-provider dashboard                        # uses [Dashboard], else 127.0.0.1:3060
computing-provider dashboard --host 0.0.0.0         # reachable from other machines
computing-provider dashboard --port 8085            # one-off override
```

To reach the UI from another machine without exposing it, forward the port over SSH instead:

```bash
ssh -L 3060:localhost:3060 <provider-host>   # then open http://localhost:3060
```

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

**A restart always reports.** The first self-check after the provider starts sends its full result whether it passed or failed, so you learn the node came back correctly without going to look. It runs five minutes in, once models have registered and been probed — the report is meaningless before that. Restarts are infrequent, so this cannot become noise.

**Otherwise, alerts fire on state changes, not on state.** A problem that persists is reported once, not on every check — the self-check runs every 10 minutes and the error-rate monitor every 60 seconds, so a standing failure would otherwise arrive around a hundred times a day and teach you to filter the very alert that matters. You get a message when a problem appears, when the set of problems changes, and when it clears. Warnings are logged but never mailed.

Delivery is asynchronous and never blocks inference: events are queued and sent in order by a single worker, and are dropped (with a log line) if a transport is too slow to keep up. A failing transport never stops the other.

### Health checks

```toml
[HealthCheck]
DeepCheckEvery = 10    # engine probe every Nth check (default 10; -1 disables)
DeepCheckTimeout = 30  # seconds to wait for that completion
```

Each model endpoint gets a cheap probe (`GET /v1/models`, falling back to
`/health`) every interval. Most backends answer that from a static registry, so
it stays 200 after the inference engine behind it has died — which is why every
`DeepCheckEvery`-th check also sends a real one-token completion. Only that
probe distinguishes a live HTTP server from a backend that can actually serve.

The cadence is **per endpoint, not per model**, and models sharing an endpoint
are probed one at a time in rotation, so a proxy serving six models sees one
engine probe per cycle rather than a burst of six.

Raise `DeepCheckEvery`, or set it to `-1`, when a model is backed by a metered
upstream rather than a local GPU.

### Request limits

```toml
[RequestLimits]
RequestsPerSecond = 500   # global token-bucket rate limit
MaxConcurrent = 50        # global in-flight request slots
```

Over-limit requests are rejected with HTTP 429 rather than queued onto an
overloaded GPU. Both can also be changed at runtime, globally or per model,
through the REST API — see the endpoint table in the
[main README](../README.md#rest-api).

### Request history

```toml
[RequestLog]
RetentionDays = 7        # how far back the Transactions view reaches (default 7)
MaxRows = 200000         # hard cap on stored rows (default 200000)
```

Every served request is stored, so the Transactions view — and its model and
source filters — survive a restart. Before this existed the list lived only in
a 1000-entry in-memory ring, so each restart emptied it, and a node that
restarts often kept almost no history at all.

Two limits rather than one: `RetentionDays` is how far back the data stays
useful, and `MaxRows` stops a traffic burst filling the disk before the age
limit ever applies. Whichever binds first wins, and pruning runs hourly.

Writes are batched and happen off the request path, so recording never delays
serving; under a burst large enough to fill the queue, records are dropped and
the count is logged rather than blocking inference.

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

## Development mode (local testing)

Point the provider at a local Swan Inference instead of production:

```bash
make clean && make testnet
INFERENCE_WS_URL=ws://localhost:8081 ./computing-provider run
```

The provider authenticates with its node identity and provider API key, so a
local run needs nothing registered anywhere. Collateral is still required before
a production provider is activated.

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
4. **Network connectivity**: check outbound access to `WebSocketURL` on port 443

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
2. [Choose which models to serve](models.md)
3. [CLI reference](cli/README.md)
