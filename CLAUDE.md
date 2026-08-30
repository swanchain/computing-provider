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

Raise `DeepCheckEvery`, or set it to `-1`, when a model is backed by a metered
upstream rather than a local GPU — the probe is one request per model per
`DeepCheckEvery` intervals.

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
| `ack` | Both | Acknowledgment |

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
