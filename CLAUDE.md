# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## ⚠️ This repository is PUBLIC

Anything committed here is world-readable the moment it is pushed, and stays
reachable in git history and in forks even after it is deleted. A previous
commit put an internal competitive-strategy document here and it had to be
removed after the fact — deletion did not unpublish it.

**Do not commit to this repo:**

- Anything marked "Internal", "Confidential", or "not for external distribution"
- Marketplace economics: pool settlement figures, subscription revenue, payout
  ratios, margins, unit costs
- Competitor analysis or strategic positioning
- Verification internals: sampling rates, detection thresholds, which requests
  are probes, or how probe traffic is selected — publishing these tells a
  dishonest provider exactly how to stay under them
- Credentials, API keys, private keys, or customer data
- **Cross-repo issue links into the private repo** — `swanchain/swan-inference#NNN`
  or the bare `swan-inference#NNN` short form. This covers code comments, docs,
  commit messages, and issue and PR descriptions and comments alike. The link
  resolves for nobody outside the org, yet still discloses that the issue
  exists, its number, and by implication the work going on there. Say what was
  found instead: "the server stores a zero context length for most
  provider-model pairs" tells a reader everything useful without pointing at an
  internal tracker.

**Where that material belongs:** the private `swanchain/swan-inference`
repository. This repo is the provider *client*; if a document is about the
marketplace rather than about running a node, it almost certainly belongs there.

Naming the service in operational context is fine — a dev-setup guide may say
"confirm swan-inference is running on port 8081". The rule is about *links into
the private tracker*, not the word.

**What is fine here:** anything a provider operator needs in order to run a
node — protocol shapes, configuration, model setup, declared capability formats,
and the behavioural consequences of platform rules (for example, that failing
repeatedly removes you from routing).

CI enforces a keyword scan on changed files; see
`.github/workflows/internal-docs-guard.yaml`. It is a safety net for the obvious
cases, not a substitute for judgement.

## Git Commit Policy

- Do NOT include `Co-Authored-By` lines in commit messages
- Keep commit messages concise and descriptive

## Project Overview

Computing Provider v2 is a CLI tool that turns GPUs into AI inference endpoints on the Swan Chain network. It connects outbound to Swan Inference via WebSocket (no inbound ports needed), registers available models, and forwards inference requests to local model servers (SGLang/Ollama).

**Inference-only.** The legacy ZK-Proof/UBI-task mode, wallets, and smart-contract bindings were removed (see commit `677e37d`); that workload lives in the legacy [go-computing-provider](https://github.com/swanchain/go-computing-provider) repo. "UBI rewards" in this repo refers to the server-side inference rewards program (token throughput/uptime-based) — no client-side blockchain code is involved. No wallet is required (a beneficiary address is optional, for rewards).

## Prerequisites

**Build requirements:**
```bash
# Ubuntu/Debian
sudo apt-get update && sudo apt-get install -y git make

# Install Go 1.21+ (https://go.dev/dl/)
wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc

# macOS (via Homebrew)
brew install go git make
```

**Runtime requirements (at least one):**
- Docker + NVIDIA Container Toolkit (Linux with NVIDIA GPU)
- Ollama (macOS or Linux)

## Build & Run

```bash
# Build for mainnet (or testnet)
make clean && make mainnet && make install
make clean && make testnet && make install

# Cross-platform builds
make darwin-arm64                # macOS Apple Silicon (mainnet)
make darwin-arm64-testnet        # macOS Apple Silicon (testnet)
make linux-arm64                 # Linux ARM64 (mainnet)
make linux-arm64-testnet         # Linux ARM64 (testnet)

# Setup (recommended - handles auth, config, and model discovery)
computing-provider setup

# Run
computing-provider run

# Development - always use go run to pick up code changes
go run ./cmd/computing-provider run

# If using binary, rebuild after changes
go build -o computing-provider ./cmd/computing-provider && ./computing-provider run
```

**Build injects via ldflags:** `build.NetWorkTag` (mainnet/testnet) and `build.CurrentCommit` (git hash).

## Inference Quick Reference

**Prerequisites:** Docker + NVIDIA Container Toolkit (Linux) or Ollama (macOS)

**Recommended: Use the Setup Wizard**
```bash
# Start your model server first (SGLang, vLLM, or Ollama)
# Then run the setup wizard:
computing-provider setup
```

The wizard handles:
- Prerequisites check
- Account creation/login
- Auto-discovery of running model servers
- Auto-matching local models to Swan Inference model IDs
- Config file generation

**Config files (generated by setup):**
- `$CP_PATH/config.toml` - Provider settings
- `$CP_PATH/models.json` - Model endpoints mapping

**Example models.json:**
```json
{
  "meta-llama/Llama-3.2-3B-Instruct": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 8000,
    "category": "text-generation"
  },
  "Qwen/Qwen2.5-7B-Instruct": {
    "endpoint": "http://localhost:11434",
    "gpu_memory": 14000,
    "category": "text-generation",
    "local_model": "qwen2.5:7b"
  }
}
```

> **Note:** Model IDs use HuggingFace repo IDs (e.g., `meta-llama/Llama-3.2-3B-Instruct`). `local_model` is used when your server uses different model names (e.g., Ollama's `qwen2.5:7b`)

**Start SGLang server:**
```bash
docker run -d --gpus all -p 30000:30000 --name sglang \
  --shm-size 4g --ipc=host \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path meta-llama/Llama-3.2-3B-Instruct \
    --host 0.0.0.0 --port 30000 \
    --served-model-name meta-llama/Llama-3.2-3B-Instruct
```

> **Performance:** See [SGLang Performance Tuning Best Practices](docs/sglang-best-practices.md) for GPU-specific configs, memory tuning, and multi-GPU TP settings.

## Testing

```bash
go test ./...                           # All tests
go test ./internal/setup/...            # Single package
go test -run TestFunctionName ./path/   # Single test
```

Test coverage is minimal (`internal/setup` wizard tests, `internal/computing` enforcement tests).

## Architecture

### Startup Flow

`main.go` → urfave/cli v2 app → `Before` hook runs `db.InitDb()` → `run` command → `runDaemon()` in `cmd/computing-provider/daemon.go`:
1. `conf.InitConfig()` — loads `$CP_PATH/config.toml` (default `~/.swan/computing`)
2. `computing.CheckMachineIdentity()` — detects private_key copied from another machine
3. `computing.NewInferenceService(nodeID, cpRepoPath).Start()` — connects WebSocket, registers models
4. gin HTTP API on `API.Port` — all `/api/v1/computing/inference/*` routes are registered inline in `runDaemon`
5. `util.MonitorShutdown` — graceful shutdown on SIGTERM/SIGINT (stops HTTP server, then `InferenceService.Stop()` which cascades to all subsystems)

### Core Package: `internal/computing/`

Key components and their relationships:

```
HTTP API (routes in cmd/computing-provider/daemon.go)
    ↕
InferenceService (inference_service.go)  ← orchestrates everything
    ├── ModelRegistry (model_registry.go)       ← tracks models from models.json, fsnotify hot-reload
    ├── ModelHealthChecker (model_health_checker.go) ← polls model endpoints
    ├── RateLimiter (rate_limiter.go)            ← GPU-aware token-bucket rate limiting
    ├── ConcurrencyLimiter (concurrency_limiter.go) ← global + per-model concurrency slots
    ├── RetryPolicy (retry_policy.go)            ← exponential backoff with jitter
    ├── InferenceMetrics (inference_metrics.go)  ← metrics tracking
    ├── MetricsHistory (metrics_history.go)      ← periodic metric snapshots to SQLite
    ├── GPUMetricsCollector (gpu_metrics_collector.go)
    └── InferenceClient (inference_client.go)    ← WebSocket protocol to Swan Inference
```

Rate limiting and concurrency limits are enforced in `handleInference`/`handleStreamingInference` (rejected requests return HTTP 429 to Swan Inference). Transient upstream failures (connection refused/reset, 502/503/504, timeouts) are retried with exponential backoff via RetryPolicy on the non-streaming path.

**ModelHealthChecker** runs two probes. The cheap one (`GET /v1/models`, falling
back to `/health`) runs every `Interval`; because most backends serve it from a
static registry, it stays 200 after the inference engine behind it has died. So
every `DeepCheckEvery`-th check also sends a real one-token completion, and only
that probe can tell a live HTTP server from a backend that can actually serve
(#70). Failures are classified: a 400/422/429 is the prompt or load, not the
backend, and never counts against health; a 5xx, a 401/403/404 or no response at
all does. Non-chat models (embeddings, image, audio) are skipped, since they
answer 404 at `/v1/chat/completions`.

```toml
[HealthCheck]
DeepCheckEvery = 10    # engine probe every Nth check (default 10; -1 disables)
DeepCheckTimeout = 30  # seconds to wait for that completion
```

The cadence is **per endpoint, not per model**, and models sharing an endpoint
are probed one at a time in rotation. A deep probe cannot be shared the way the
cheap `/v1/models` probe is — each model needs its own completion under its own
name — so probing them together would send a burst of N simultaneous requests to
a single host. Against a proxy fronting a metered upstream that is what earns
`server_is_overloaded`, and the burst is self-inflicted: those models are not
independent backends, they are one server.

So a proxy serving six models sees one engine probe per `DeepCheckEvery`
intervals, and each of its models is checked every sixth one. A dedicated
single-model backend is unaffected. The trade is slower detection on shared
endpoints, which is the same trade the cheap probe already makes by
deduplicating per endpoint.

Raise `DeepCheckEvery`, or set it to `-1`, when a model is backed by a metered
upstream rather than a local GPU.

**ModelRegistry** uses callback pattern (`onModelAdded`, `onModelRemoved`, `onHealthUpdate`) to notify InferenceService of changes.

**Dashboard:** `internal/dashboard/` — embedded React/Vite frontend served by Go (port 3060). Proxies API requests to the main provider.

**Other packages:** `internal/setup/` (setup wizard: auth, prerequisites, model discovery), `internal/models/` (HuggingFace catalog/download/verify helpers), `internal/db/` (SQLite), `conf/` (config parsing), `util/` (shutdown, HTTP serve).

### Database: `internal/db/`

SQLite with GORM, WAL mode, max 1 open connection (avoids lock contention). The only table is `metrics_history`, auto-migrated by `MetricsHistory.migrate()` on first use.

## Configuration

**Config files** (in `$CP_PATH`, default `~/.swan/computing`):
- `config.toml` — main config (parsed by `conf/config.go` using BurntSushi/toml)
- `models.json` — model-to-endpoint mapping (watched by fsnotify for hot-reload)

**config.toml example:**
```toml
[API]
Port = 8085
NodeName = "my-provider"

[Inference]
Enable = true
WebSocketURL = "wss://inference-ws.swanchain.io"
ApiKey = "sk-prov-xxxxxxxxxxxxxxxxxxxx"  # Your provider API key
Models = ["meta-llama/Llama-3.2-3B-Instruct"]
```

**Environment overrides:**
- `CP_PATH` — config directory
- `INFERENCE_API_KEY` — provider API key (overrides config)
- `INFERENCE_WS_URL` — WebSocket URL (useful for local dev)

## WebSocket Protocol

Provider communicates with Swan Inference using typed JSON messages:

| Message | Direction | Purpose |
|---------|-----------|---------|
| `register` | → Server | Register with model list and auth token |
| `inference` | ← Server | Incoming inference request |
| `stream_chunk` / `stream_end` | → Server | Streaming response |
| `warmup` | ← Server | Pre-load model (sends `max_tokens: 1` request) |
| `heartbeat` | → Server | Liveness with metrics |
| `notice` | ← Server | Operational notice, forwarded to the operator's configured `[Alerts]` transports |
| `ack` | Both | Acknowledgment |

### Alert email

Alerts are sent as `multipart/alternative`: a styled HTML part and a plain-text
part carrying the same information. Both, because alerts are read on phones, in
terminals, and by spam filters — the text part is the one that must always make
sense.

Audits pass structured rows (`alerts.CheckRow`, `alerts.ModelRow`) rather than
formatting results into the message, so the mail renders a table listing each
check and each model with its own status.

Two rules for anything added here:

- **Escape everything interpolated into the HTML part.** A `notice` from Swan
  Inference carries a message and details chosen remotely; rendering those
  unescaped puts attacker-chosen markup in the operator's mail client. The
  notice sanitiser is the first line of defence, this is the second.
- **Never signal with colour alone.** Every row carries a glyph as well —
  colour means nothing to a colour-blind reader and nothing at all in the text
  part.

The subject keeps its `[node] SEVERITY: event` form with a glyph prepended.
Operators filter on the severity word, so it must not be replaced.

### Earnings

`GET /inference/earnings` values served tokens at the provider payout rates from
`ModelPriceCatalog`, and the dashboard shows the total in **USD**.

It is computed from this node's own aggregate token counters, which cover routed
work only: probes go through `RecordRequest` (history) and never through
`RecordRequestEnd` (aggregates), so the node cannot bill itself for checking
itself. A model with traffic but no available rate is shown as **unpriced**
rather than `$0.00` — those are different statements, and rounding one into the
other makes a working model look idle.

This is the provider's own arithmetic and the UI says so. The platform's figure
is authoritative; the local one is worth showing beside it because the two can
disagree, and the disagreement is the useful part.

Show the operator their own rates and totals. The marketplace-economics material
listed at the top of this file stays out of this repo.

### Request sources

Every recorded request carries a `source` saying where it entered the node, and
the dashboard shows it as a column:

| source | meaning |
|---|---|
| `hub` | an inference request routed over the WebSocket |
| `health` | this node's engine probe — a one-token completion per endpoint per `DeepCheckEvery` cycles |
| `selfcheck` | the periodic audit's inference probe |

Probes are real completions and consume the same backend capacity as routed
work, so they belong in the history rather than being load the operator cannot
account for.

`source` says where a request *entered*, not who *originated* it. A hub request
carries no marker distinguishing customer traffic from the marketplace's own
verification — `InferencePayload` has only `endpoint_id`, `model_id`, `request`
and `stream` — and a provider client must not try to infer that. Guessing which
routed requests are probes is exactly the verification internal the top of this
file rules out publishing; it belongs server-side.

### Self-check alerting

The audit emails on a *change* of state, not on state, and a problem must
persist across `AlertAfterFailures` consecutive audits before it is announced:

```toml
[SelfCheck]
IntervalMinutes = 10
AlertAfterFailures = 2   # consecutive audits agreeing before an email
FailuresBeforeDisable = 2
```

A recovery is only sent when it closes a failure that was actually announced.
Without that gate a transient error — an upstream answering 502 once, ten
minutes before answering normally — sends no actionable failure mail but does
send an all-clear, so the operator receives "recovered" notices for alarms that
never arrived.

Keep `AlertAfterFailures` at or below `FailuresBeforeDisable`: alerting later
than the node acts means a model is deregistered before anyone is told why.
### Hub notices

Swan Inference can see things a node cannot observe about itself — that its
reputation dropped, that traffic is being withheld, that a model declaration was
rejected. A `notice` message carries one of those down the existing WebSocket,
and the provider forwards it to whatever the operator configured under
`[Alerts]` (webhook, email, or both):

```json
{
  "type": "notice",
  "payload": {
    "event": "provider_suspended",
    "severity": "critical",
    "model_id": "Qwen/Qwen3.8-27B",
    "message": "Routing paused: three consecutive failed verifications.",
    "details": {"until": "2026-08-30T14:00:00Z"}
  }
}
```

Forwarding rather than having the server mail the operator directly keeps the
operator's address on their own machine, reuses the cooldown and ordering the
Notifier already applies, and reaches the webhook as well as email.

The server is a remote party, so `internal/computing/hub_notice.go` treats every
field as untrusted before it can reach a transport:

- `event` is whitelisted to `[a-z0-9_]`, max 64 chars. It is interpolated into
  an SMTP `Subject:` header, so an embedded CRLF would let a notice append
  headers of its own — this check is what closes that.
- `event` is always prefixed `hub_` on delivery, so a notice can never arrive
  impersonating a local event such as `model_auto_disabled`.
- `severity` is clamped to `info`/`warning`/`critical`; anything else becomes
  `warning`.
- `message` and `details` values have control characters stripped and are
  truncated visibly; `details` is capped at 16 entries.
- Delivery is rate-limited to 20 notices per hour, so a server-side bug cannot
  drain an operator's mail quota or bury a real local alert.

Notices are dropped with a log line when `[Alerts]` is unconfigured — nothing is
queued for later.

## CLI Structure (urfave/cli v2)

Commands are defined in `cmd/computing-provider/`:
- `daemon.go` — `run` command (`runDaemon`: starts InferenceService + HTTP API + graceful shutdown)
- `run.go` — `init`, `info`, `state` commands
- `setup.go` — `setup` wizard (recommended for new providers), subcommands: `discover`, `login`, `signup`
- `inference.go` — `inference` subcommands: `status`, `config`, `deposit`, `set-beneficiary`, `keygen`, `request-approval`, `recommend-models`, `select-model`
- `models.go` — `models` subcommands: `catalog`, `download`, `verify`, `list`, `rm`
- `research.go` — `research` subcommands: `hardware`, `gpu-info`, `gpu-benchmark`
- `dashboard.go` — web UI (port 3060)
- `auth.go` — shared auth/login helpers used by setup and inference commands
- `tablewriter.go` — CLI table output helpers

Global flags: `--repo` sets `CP_PATH` (default `~/.swan/computing`), `--help`/`-h` shows help for any command/subcommand, `--version` shows version info.

## REST API

Base: `/api/v1/computing/` (routes registered in `cmd/computing-provider/daemon.go` `runDaemon`)

**Inference endpoints** (`/inference/`):
- `GET /metrics` — JSON metrics summary
- `GET /metrics/prometheus` — Prometheus format
- `GET /metrics/history` — historical metrics (`duration`, `resolution` query params)
- `GET /status` — connection and active models status
- `GET /models` — list all models with status
- `GET /models/:model_id` — single model status
- `GET /models/:model_id/health` — model health details
- `GET /models/:model_id/metrics` — per-model detailed metrics
- `GET /health` — all models health
- `POST /models/:model_id/enable` — enable a model
- `POST /models/:model_id/disable` — disable a model
- `POST /models/:model_id/healthcheck` — force health check
- `POST /models/reload` — hot-reload models.json

**Request management** (`/inference/`):
- `GET /ratelimit`, `GET /concurrency`, `GET /retries` — metrics
- `GET /request-management` — combined status
- `GET /requests` — request queue/inflight status
- `POST /ratelimit/global`, `POST /ratelimit/model/:model_id` — set rate limits
- `POST /concurrency/global`, `POST /concurrency/model/:model_id` — set concurrency limits

> Rate-limit/concurrency settings are enforced on the inference request path; over-limit requests are rejected with HTTP 429.

## Common Issues

| Error | Solution |
|-------|----------|
| `go: command not found` | Install Go 1.21+ (see Prerequisites section above) |
| `permission denied...docker.sock` | `sudo usermod -aG docker $USER` |
| `could not select device driver "nvidia"` | Install NVIDIA Container Toolkit |
| `authentication required` / `invalid provider API key` | Set valid `sk-prov-*` key in config.toml or `INFERENCE_API_KEY` |
| Config changes not taking effect | Rebuild binary or use `go run ./cmd/computing-provider run` |
