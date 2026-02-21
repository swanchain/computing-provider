# Computing Provider v2 — End-to-End Testing Plan

This document provides a comprehensive, section-by-section checklist for manually testing the Computing Provider CLI. It covers setup, inference mode, model management, WebSocket protocol, health monitoring, verification, wallet/collateral operations, and the local REST API.

**Prerequisites for all tests:**
- Go 1.21+ installed
- Docker + NVIDIA Container Toolkit (Linux) OR Ollama (macOS)
- At least one model server running (SGLang, vLLM, or Ollama)
- Swan Inference service accessible (local or remote)
- Built binary: `make clean && make mainnet && make install` or use `go run ./cmd/computing-provider`

---

## Table of Contents

1. [Build & Installation](#1-build--installation)
2. [Setup Wizard](#2-setup-wizard)
3. [Manual Initialization](#3-manual-initialization)
4. [Configuration](#4-configuration)
5. [Inference — Connection & Registration](#5-inference--connection--registration)
6. [Inference — Non-Streaming Requests](#6-inference--non-streaming-requests)
7. [Inference — Streaming Requests](#7-inference--streaming-requests)
8. [Inference — Status & Config Commands](#8-inference--status--config-commands)
9. [Model Management — Download](#9-model-management--download)
10. [Model Management — Verify & List](#10-model-management--verify--list)
11. [Model Registry & Hot Reload](#11-model-registry--hot-reload)
12. [Model Health Checking](#12-model-health-checking)
13. [Heartbeat & Metrics](#13-heartbeat--metrics)
14. [Warmup Handling](#14-warmup-handling)
15. [Verification — Hash Registration](#15-verification--hash-registration)
16. [Verification — Runtime Challenges](#16-verification--runtime-challenges)
17. [Wallet Management](#17-wallet-management)
18. [Account Management](#18-account-management)
19. [Collateral Management](#19-collateral-management)
20. [Sequencer](#20-sequencer)
21. [Task Management (ECP)](#21-task-management-ecp)
22. [Local REST API](#22-local-rest-api)
23. [Dashboard](#23-dashboard)
24. [Error Handling & Reconnection](#24-error-handling--reconnection)
25. [Rate Limiting & Concurrency](#25-rate-limiting--concurrency)
26. [Hardware Detection](#26-hardware-detection)

---

## 1. Build & Installation

- [ ] `make clean` removes previous build artifacts
- [ ] `make mainnet` builds binary successfully (no compile errors)
- [ ] `make install` installs to `/usr/local/bin/computing-provider`
- [ ] `computing-provider --version` displays version info
- [ ] `computing-provider --help` lists all commands
- [ ] `go build ./cmd/computing-provider` builds from source
- [ ] `go vet ./...` passes without errors related to our code
- [ ] `go test ./...` passes (pre-existing contract test failures excepted)

---

## 2. Setup Wizard

### Step 1: Prerequisites
- [ ] `computing-provider setup` starts the wizard
- [ ] Docker availability is detected correctly
- [ ] Ollama availability is detected correctly
- [ ] At least one backend required — error if neither available
- [ ] GPU detection works (NVIDIA via nvidia-smi, Apple Silicon via sysctl)

### Step 2: Configuration
- [ ] Prompts for node name (defaults to hostname)
- [ ] Creates `~/.swan/computing` directory
- [ ] Generates initial `config.toml` with correct structure
- [ ] Multi-address is set correctly

### Step 3: Authentication
- [ ] `computing-provider setup login` — login with existing email/password
- [ ] `computing-provider setup signup` — create new account
- [ ] API key is generated and validated against Swan Inference
- [ ] API key stored in config.toml
- [ ] Invalid credentials show clear error

### Step 4: Model Discovery
- [ ] `computing-provider setup discover` scans localhost ports
- [ ] Detects vLLM on port 8000
- [ ] Detects SGLang on port 30000
- [ ] Detects Ollama on port 11434
- [ ] Fetches Swan Inference model catalog
- [ ] Matches local models to Swan model IDs with confidence scores
- [ ] Displays matched models in table format
- [ ] Handles collisions (multiple local models matching same Swan ID)
- [ ] Shows unmatched models separately

### Step 5: Finalize
- [ ] `models.json` generated with correct model-endpoint mappings
- [ ] `config.toml` updated with API key and model list
- [ ] Display of model mappings is accurate
- [ ] Next steps instructions are shown

---

## 3. Manual Initialization

- [ ] `computing-provider init --node-name=test-node` creates config directory
- [ ] `computing-provider init --multi-address=/ip4/1.2.3.4/tcp/9085` sets multi-address
- [ ] Config directory defaults to `~/.swan/computing`
- [ ] `CP_PATH` env var overrides config directory
- [ ] Init fails gracefully if config already exists (or prompts to overwrite)

---

## 4. Configuration

### config.toml
- [ ] `[API].Port` is respected (default 8085)
- [ ] `[API].NodeName` is used in registration
- [ ] `[Inference].Enable = true` enables inference mode
- [ ] `[Inference].WebSocketURL` sets WS connection target
- [ ] `[Inference].ApiKey` is sent in registration
- [ ] `[Inference].Models` array lists models to register

### Environment Overrides
- [ ] `CP_PATH=~/custom/path` overrides config directory
- [ ] `INFERENCE_API_KEY=sk-prov-xxx` overrides config ApiKey
- [ ] `INFERENCE_WS_URL=ws://localhost:8081/ws` overrides WebSocket URL

### models.json
- [ ] File is loaded on startup
- [ ] Each model entry has `endpoint`, `gpu_memory`, `category`
- [ ] `local_model` field enables name substitution
- [ ] Invalid JSON shows clear error message
- [ ] Missing file shows appropriate warning

---

## 5. Inference — Connection & Registration

### WebSocket Connection
- [ ] `computing-provider run` connects to Swan Inference WebSocket
- [ ] Connection URL matches config `WebSocketURL`
- [ ] API key is sent in Authorization header
- [ ] Connection success logged

### Registration Message
- [ ] Register message includes NodeID
- [ ] Register message includes all models from config
- [ ] Register message includes hardware info (GPU type, VRAM, count)
- [ ] Register message includes `ModelHashes` from local manifests
- [ ] Server acknowledges registration
- [ ] Provider appears online in Swan Inference dashboard

### Authentication
- [ ] Valid `sk-prov-*` key authenticates successfully
- [ ] Invalid key shows clear error and retries
- [ ] Missing key shows configuration guidance
- [ ] Expired/revoked key shows appropriate error

---

## 6. Inference — Non-Streaming Requests

- [ ] Receive `inference` message from Swan Inference
- [ ] Model ID is looked up in registry
- [ ] Request is forwarded to correct local endpoint (from models.json)
- [ ] `local_model` substitution works (e.g., `meta-llama/Llama-3.2-3B` → `llama3.2:3b`)
- [ ] Response sent back as `ack` with full response body
- [ ] Token counts extracted from response (prompt_tokens, completion_tokens)
- [ ] Latency measured and included in response
- [ ] Error response sent if model endpoint is down
- [ ] Error response sent if model endpoint returns error
- [ ] Metrics updated (request count, latency, tokens)

---

## 7. Inference — Streaming Requests

- [ ] Receive `inference` message with `stream: true`
- [ ] Request forwarded with `stream: true` and `stream_options.include_usage: true`
- [ ] SSE stream parsed correctly (line-by-line `data: {...}` format)
- [ ] Each chunk sent as `stream_chunk` message
- [ ] `[DONE]` marker detected as stream end
- [ ] Final `stream_end` message includes token counts
- [ ] Streaming errors send error chunk
- [ ] Partial stream failure handled gracefully
- [ ] Multiple concurrent streams work correctly

---

## 8. Inference — Status & Config Commands

- [ ] `computing-provider inference status` shows provider status
- [ ] Status includes: provider ID, status (pending/active), can_connect
- [ ] `computing-provider inference status --json` outputs JSON
- [ ] `computing-provider inference config` shows current config
- [ ] Config shows: WebSocket URL, API key (masked), models list
- [ ] `computing-provider inference keygen` generates new API key
- [ ] `computing-provider inference request-approval --hardware="..." --email="..."` sends request
- [ ] `computing-provider inference deposit --check` shows collateral requirements

---

## 9. Model Management — Download

### Catalog
- [ ] `computing-provider models catalog` lists available models
- [ ] Output shows: model ID, category, file count, size, download status
- [ ] `--json` flag outputs JSON format
- [ ] Already-downloaded models show "downloaded" status
- [ ] Partially downloaded models show "partial (X/Y)" status
- [ ] `--service-url` flag overrides API URL

### Download
- [ ] `computing-provider models download meta-llama/Llama-3.1-8B-Instruct` downloads model
- [ ] File manifest is fetched from Swan Inference API
- [ ] Each file downloaded with progress indicator
- [ ] SHA256 hash verified after each file download
- [ ] Existing files with matching hash are skipped
- [ ] Hash mismatch causes re-download (file deleted and error reported)
- [ ] Subdirectories created as needed (e.g., `tokenizer/config.json`)
- [ ] `.swan-hash-manifest.json` saved after successful download
- [ ] Manifest contains: model_id, composite_hash, algorithm, files array, created_at
- [ ] `--dest` flag overrides download directory
- [ ] Default directory is `~/.swan/models/<model-id>`
- [ ] Interrupted download can be resumed (skips completed files)
- [ ] Invalid model ID shows clear error
- [ ] Non-existent model returns appropriate error from API

---

## 10. Model Management — Verify & List

### Verify
- [ ] `computing-provider models verify meta-llama/Llama-3.1-8B-Instruct` checks hashes
- [ ] Each file compared against expected SHA256 hash
- [ ] Output shows: filename, status (PASS/FAIL/MISSING), hash prefix
- [ ] Summary shows: total files, passed, failed, missing
- [ ] All pass → green "All files verified successfully!"
- [ ] Any failure → non-zero exit code with error message
- [ ] `--dir` flag overrides model directory
- [ ] `--service-url` flag overrides API URL

### List
- [ ] `computing-provider models list` shows locally downloaded models
- [ ] Output format: `org/model-name (N files)`
- [ ] Nested org/model directory structure handled correctly
- [ ] Empty models directory shows "No models downloaded yet."
- [ ] Non-existent models directory shows appropriate message

---

## 11. Model Registry & Hot Reload

- [ ] Models loaded from `models.json` on startup
- [ ] Each model transitions: Loading → Ready
- [ ] Model state tracked: Loading, Ready, Unhealthy, Disabled
- [ ] File watcher detects `models.json` changes
- [ ] Changes trigger hot-reload (debounced 500ms)
- [ ] New models added without restart
- [ ] Removed models no longer served
- [ ] Updated endpoints take effect immediately
- [ ] `POST /api/v1/computing/inference/models/reload` forces reload
- [ ] Model health update sent to Swan Inference after reload

---

## 12. Model Health Checking

- [ ] Health checks run every 30 seconds for each model
- [ ] Check sends minimal inference request to model endpoint
- [ ] Healthy model: response within timeout, valid result
- [ ] Consecutive failures (3): model becomes Unhealthy
- [ ] Consecutive successes (2): model recovers to Healthy
- [ ] Unhealthy model not offered for inference
- [ ] Health status change triggers `model_health_update` message to Swan Inference
- [ ] Endpoint timeout (10s) handled gracefully
- [ ] Circuit breaker: 60-second cooldown before retesting unhealthy model
- [ ] Health status visible via `GET /api/v1/computing/inference/models/:id/health`
- [ ] `POST /api/v1/computing/inference/models/:id/healthcheck` forces immediate check

---

## 13. Heartbeat & Metrics

### Heartbeat Message (30-second interval)
- [ ] Heartbeat sent every 30 seconds
- [ ] Includes: NodeID, ProviderID, Timestamp
- [ ] Includes GPU metrics: utilization %, memory usage %
- [ ] Includes per-GPU details: name, utilization, memory, temperature, power
- [ ] Includes request metrics: active requests, total requests, req/min, latency
- [ ] Includes model health for all enabled models
- [ ] Includes hardware info (cached at registration)
- [ ] Missing heartbeat causes server to mark provider as potentially offline

### Local Metrics
- [ ] `GET /api/v1/computing/inference/metrics` returns JSON metrics
- [ ] `GET /api/v1/computing/inference/metrics/prometheus` returns Prometheus format
- [ ] Metrics include: request count, latency histogram, token throughput, GPU utilization
- [ ] Metrics update in real-time as requests are processed

---

## 14. Warmup Handling

- [ ] Receive `warmup` message from Swan Inference
- [ ] Model endpoint looked up from registry
- [ ] Minimal inference request sent (`max_tokens: 1`)
- [ ] Success response includes: LoadTimeMs, MemoryMB
- [ ] Failure response includes error message
- [ ] Warmup does not interfere with normal inference requests
- [ ] Model endpoint unreachable → error response with clear message
- [ ] Warmup request honors timeout

---

## 15. Verification — Hash Registration

### Hash Manifest
- [ ] After `models download`, `.swan-hash-manifest.json` exists in model directory
- [ ] Manifest contains correct `composite_hash` (SHA256 of sorted filename:hash pairs)
- [ ] Manifest contains per-file hashes matching downloaded file hashes
- [ ] `algorithm` field is `"sha256"`

### Registration with Hashes
- [ ] On `register`, `ModelHashes` array is populated for models with manifests
- [ ] Each entry has `model_id`, `weight_hash` (composite), `hash_algo`
- [ ] Models without manifests included with empty `weight_hash` (backward compatible)
- [ ] Composite hash matches server-side computation (same algorithm)

### Verification Scenarios
- [ ] Valid hash → server accepts model registration
- [ ] Tampered manifest → server rejects model (in `reject_model` mode)
- [ ] Missing manifest → server allows model (backward compatible)
- [ ] Corrupted manifest file → graceful fallback (empty hash)

---

## 16. Verification — Runtime Challenges

### Fingerprint Challenge
- [ ] Receive `verify` message with `challenge_type: "fingerprint"`
- [ ] Challenge parsed: list of filenames with expected hashes
- [ ] Local hash manifest loaded for the challenged model
- [ ] Each file hash compared against expected value
- [ ] Response sent with per-file results: `"pass"`, `"fail"`, or `"missing"`
- [ ] Overall `success: true` if all files pass
- [ ] Overall `success: false` if any file fails or is missing
- [ ] Response includes `challenge_id` from request

### Edge Cases
- [ ] Model not downloaded locally → all files "missing", success: false
- [ ] Manifest exists but some files deleted → affected files "missing"
- [ ] Manifest missing but files exist → files marked as "missing" (no manifest to compare)
- [ ] Unknown challenge type → error response with "unsupported challenge type"
- [ ] Malformed challenge data → error response

---

## 17. Wallet Management

- [ ] `computing-provider wallet new` generates new wallet address
- [ ] `computing-provider wallet list` shows all wallet addresses
- [ ] `computing-provider wallet list --swan` shows Swan chain wallets
- [ ] `computing-provider wallet export <address>` exports private key (requires confirmation)
- [ ] `computing-provider wallet import <path>` imports private key from file
- [ ] `computing-provider wallet import -` imports from stdin
- [ ] `computing-provider wallet delete <address>` deletes wallet (requires confirmation)
- [ ] `computing-provider wallet sign <address> "message"` signs message
- [ ] `computing-provider wallet verify <address> <signature> "message"` verifies signature
- [ ] `computing-provider wallet send <target> <amount> --from=<addr>` sends funds
- [ ] Send with `--nonce=N` overrides nonce
- [ ] Insufficient balance shows clear error

---

## 18. Account Management

- [ ] `computing-provider account create` creates on-chain account
- [ ] Requires: `--ownerAddress`, `--workerAddress`, `--beneficiaryAddress`, `--task-types`
- [ ] `computing-provider account changeMultiAddress` updates multi-address
- [ ] `computing-provider account changeOwnerAddress` transfers ownership
- [ ] `computing-provider account changeWorkerAddress` updates worker
- [ ] `computing-provider account changeBeneficiaryAddress` updates beneficiary
- [ ] `computing-provider account changeTaskTypes` updates supported task types
- [ ] All account changes require `--ownerAddress` for authorization

---

## 19. Collateral Management

### Add Collateral
- [ ] `computing-provider collateral add <amount> --ecp --from=<addr>` deposits ECP collateral
- [ ] `computing-provider collateral add <amount> --fcp --from=<addr>` deposits FCP collateral
- [ ] `--account=<addr>` specifies target account
- [ ] Transaction hash displayed on success

### Withdraw Collateral
- [ ] `computing-provider collateral withdraw-request <amount> --ecp --owner=<addr>` initiates request
- [ ] `computing-provider collateral withdraw-confirm --ecp --owner=<addr>` completes withdrawal
- [ ] `computing-provider collateral withdraw-view --ecp` shows pending withdrawals
- [ ] Direct `collateral withdraw` also works
- [ ] `--account=<addr>` specifies target account

### Send Collateral
- [ ] `computing-provider collateral send <target> <amount> --from=<addr>` transfers collateral

---

## 20. Sequencer

- [ ] `computing-provider sequencer token` prints sequencer token
- [ ] `computing-provider sequencer add <amount> --from=<addr>` adds to sequencer
- [ ] `computing-provider sequencer withdraw <amount> --owner=<addr>` withdraws from sequencer
- [ ] `--account=<addr>` specifies target account

---

## 21. Task Management (ECP)

- [ ] `computing-provider task list` lists recent ECP tasks
- [ ] `computing-provider task list --tail 20` limits to last 20 tasks
- [ ] `computing-provider task get <job_uuid>` shows job details
- [ ] `computing-provider task delete <job_uuid>` removes job
- [ ] Task list shows: UUID, status, type, created time

---

## 22. Local REST API

### Status & Metrics
- [ ] `GET /api/v1/computing/inference/metrics` returns JSON metrics
- [ ] `GET /api/v1/computing/inference/metrics/prometheus` returns Prometheus format
- [ ] `GET /api/v1/computing/inference/status` returns connection status
- [ ] `GET /api/v1/computing/inference/health` returns all model health

### Model Management
- [ ] `GET /api/v1/computing/inference/models` lists all models with status
- [ ] `GET /api/v1/computing/inference/models/:id` returns specific model
- [ ] `GET /api/v1/computing/inference/models/:id/health` returns model health details
- [ ] `POST /api/v1/computing/inference/models/:id/enable` enables model
- [ ] `POST /api/v1/computing/inference/models/:id/disable` disables model
- [ ] `POST /api/v1/computing/inference/models/:id/healthcheck` forces health check
- [ ] `POST /api/v1/computing/inference/models/reload` reloads models.json

### Request Management
- [ ] `GET /api/v1/computing/inference/ratelimit` shows rate limiter status
- [ ] `GET /api/v1/computing/inference/concurrency` shows concurrency limiter status
- [ ] `POST /api/v1/computing/inference/ratelimit/global` sets global rate limit
- [ ] `POST /api/v1/computing/inference/ratelimit/model/:id` sets per-model rate limit
- [ ] `POST /api/v1/computing/inference/concurrency/global` sets global concurrency
- [ ] `POST /api/v1/computing/inference/concurrency/model/:id` sets per-model concurrency

---

## 23. Dashboard

- [ ] `computing-provider dashboard` starts web UI on port 3005
- [ ] `--port 8080` overrides dashboard port
- [ ] `--api http://localhost:8085` overrides API base URL
- [ ] Dashboard loads in browser
- [ ] Shows connection status (connected/disconnected)
- [ ] Shows model list with health status
- [ ] Shows request metrics
- [ ] Shows GPU utilization

---

## 24. Error Handling & Reconnection

### WebSocket Reconnection
- [ ] Server disconnect triggers automatic reconnect
- [ ] Exponential backoff between reconnect attempts
- [ ] Reconnect logged with attempt number
- [ ] After reconnect, re-registration occurs
- [ ] Models re-announced after reconnection
- [ ] In-flight requests at disconnect time are cleaned up

### Model Server Errors
- [ ] Model endpoint down → health check marks unhealthy
- [ ] Model endpoint returns 500 → error forwarded to Swan Inference
- [ ] Model endpoint times out → timeout error sent
- [ ] Model endpoint returns invalid JSON → parsing error sent

### Configuration Errors
- [ ] Missing config.toml → clear error with guidance
- [ ] Invalid config.toml → parsing error with line info
- [ ] Missing models.json → warning, no models registered
- [ ] Invalid models.json → parsing error
- [ ] Missing API key → clear error with setup instructions

---

## 25. Rate Limiting & Concurrency

### Global Limits
- [ ] Global rate limit enforced (requests/second)
- [ ] Exceeding rate limit → request queued or rejected
- [ ] Global concurrency limit enforced
- [ ] Exceeding concurrency → request queued

### Per-Model Limits
- [ ] Per-model rate limits enforced
- [ ] Per-model concurrency limits enforced
- [ ] GPU-aware adaptive adjustment works
- [ ] Burst handling allows short spikes above limit

### Retry Policy
- [ ] Failed requests retried with exponential backoff
- [ ] Max retries respected
- [ ] Jitter prevents thundering herd
- [ ] Non-retryable errors (400, 401) not retried

---

## 26. Hardware Detection

### NVIDIA GPU (Linux)
- [ ] `nvidia-smi` output parsed correctly
- [ ] GPU name, VRAM, driver version extracted
- [ ] Multi-GPU setup detected (count > 1)
- [ ] Compute capability included if available

### Apple Silicon (macOS)
- [ ] CPU brand string detected via `sysctl`
- [ ] Total memory detected via `hw.memsize`
- [ ] Reported as Apple Silicon GPU in hardware info
- [ ] Unified memory reported as VRAM

### No GPU
- [ ] Missing `nvidia-smi` handled gracefully
- [ ] Clear message that no GPU detected
- [ ] Provider can still run with CPU-only inference (Ollama)

---

## Quick Smoke Test Sequence

For a rapid validation that the provider is working end-to-end:

1. **Build:**
   ```bash
   go build -o computing-provider ./cmd/computing-provider
   ```

2. **Start a model server** (pick one):
   ```bash
   # Ollama
   ollama serve &
   ollama run llama3.2:3b

   # OR SGLang
   docker run -d --gpus all -p 30000:30000 --name sglang \
     lmsysorg/sglang:latest \
     python3 -m sglang.launch_server \
       --model-path meta-llama/Llama-3.2-3B-Instruct \
       --host 0.0.0.0 --port 30000
   ```

3. **Setup:**
   ```bash
   ./computing-provider setup
   ```

4. **Check status:**
   ```bash
   ./computing-provider inference status
   ./computing-provider inference config
   ```

5. **Run provider:**
   ```bash
   ./computing-provider run
   ```

6. **Verify connection:**
   - Check logs for "Connected to Swan Inference"
   - Check logs for "Registered with X models"
   - `curl http://localhost:8085/api/v1/computing/inference/status`

7. **Test inference (from another terminal):**
   ```bash
   # Send test request via Swan Inference
   curl -X POST http://localhost:8100/v1/chat/completions \
     -H "Authorization: Bearer sk-your-consumer-key" \
     -H "Content-Type: application/json" \
     -d '{"model":"meta-llama/Llama-3.2-3B-Instruct","messages":[{"role":"user","content":"Say hello"}],"max_tokens":50}'
   ```

8. **Check metrics:**
   ```bash
   curl http://localhost:8085/api/v1/computing/inference/metrics
   ```

9. **Check model health:**
   ```bash
   curl http://localhost:8085/api/v1/computing/inference/health
   ```

10. **Model download & verify:**
    ```bash
    ./computing-provider models catalog
    ./computing-provider models download meta-llama/Llama-3.2-3B-Instruct
    ./computing-provider models verify meta-llama/Llama-3.2-3B-Instruct
    ```

---

## Cross-Repo Integration Tests

These tests require both swan-inference and computing-provider running together:

### Full Inference Loop
- [ ] Provider connects and registers with Swan Inference
- [ ] Consumer sends inference request to Swan Inference
- [ ] Request routed to provider via WebSocket
- [ ] Provider forwards to local model server
- [ ] Response flows back: model server → provider → Swan Inference → consumer
- [ ] End-to-end latency is reasonable (< 5s for short prompts)

### Streaming Loop
- [ ] Consumer sends streaming request
- [ ] Provider streams chunks back
- [ ] Consumer receives SSE chunks in real-time
- [ ] Stream ends cleanly with usage stats

### Verification Loop
- [ ] Provider downloads model via `models download`
- [ ] Hash manifest saved correctly
- [ ] Provider registers with composite hash
- [ ] Swan Inference verifies hash against database
- [ ] Verification challenge sent to provider
- [ ] Provider responds with correct hashes
- [ ] Challenge marked as passed in database

### Multi-Provider Load Balancing
- [ ] Start 2+ providers with same model
- [ ] Send multiple requests
- [ ] Requests distributed across providers
- [ ] One provider disconnects → requests route to remaining provider
- [ ] Disconnected provider reconnects → resumes receiving requests

### Warmup Loop
- [ ] Provider connects
- [ ] Swan Inference sends warmup for each model (if auto-warmup enabled)
- [ ] Provider processes warmup (minimal inference)
- [ ] Warmup result sent back to Swan Inference
- [ ] First real request has lower latency (model already loaded)
