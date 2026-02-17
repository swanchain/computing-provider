# Research

Research assistant for Computing Provider v2 development. Analyzes codebase, searches documentation, and provides implementation guidance.

## Topic: $ARGUMENTS

## Instructions

You are a technical researcher helping develop the Computing Provider v2 for Swan Chain. This is a Go CLI that turns GPUs into AI inference endpoints via WebSocket connection to Swan Inference.

1. Understand the feature or topic being researched
2. Analyze existing codebase patterns
3. Search for relevant best practices
4. Provide actionable implementation guidance

---

## Research Process

### Step 1: Understand the Request

Parse the research topic from `$ARGUMENTS`. Identify:
- What feature or component is being researched
- What specific questions need answering
- What context is needed from the codebase

### Step 2: Analyze Existing Codebase

Search the codebase to understand current patterns:

**Architecture Overview**
```
cmd/computing-provider/     # CLI commands (urfave/cli)
internal/computing/         # Core services
internal/contract/          # Smart contract bindings
internal/setup/             # Setup wizard & model discovery
internal/dashboard/         # Web dashboard
conf/                       # Configuration
```

**CLI Commands** (`cmd/computing-provider/`)
- `main.go` - Command registration
- `ubi.go` - `run` command, REST API routes, inference startup
- `setup.go` - Setup wizard (auth, model discovery, config generation)
- `inference.go` - Swan Inference status/config commands
- `wallet.go` - Wallet management
- `task.go` - Task listing

**Inference Core** (`internal/computing/`)
- `inference_client.go` - WebSocket client for Swan Inference (connect, register, heartbeat, message handling)
- `inference_service.go` - Request forwarding to model servers, streaming, warmup
- `model_registry.go` - Model config management, hot-reload from models.json
- `model_health_checker.go` - Health monitoring with circuit breaker
- `docker_service.go` - Docker container management
- `metrics_collector.go` - Request/latency/token metrics

**Setup & Discovery** (`internal/setup/`)
- `discovery.go` - Auto-discover model servers (SGLang, vLLM, Ollama), match to Swan Inference model IDs
- `auth_client.go` - Swan Inference API auth (signup, login, API key management)
- `prompter.go` - Interactive CLI prompts

**Configuration** (`conf/`)
- `config.go` - Config structs, defaults, environment overrides
- Config file: `$CP_PATH/config.toml`
- Model endpoints: `$CP_PATH/models.json`

**Smart Contracts** (`internal/contract/`)
- `ecp/` - Edge Computing Provider contracts
- `account/` - CP account registration
- `token/` - SWAN token operations

**Dashboard** (`internal/dashboard/`)
- `server.go` - Dashboard HTTP server (port 3005)
- `ui/` - React frontend (Vite + Tailwind)

### Step 3: Search External Resources

Use web search to find:
- Go best practices for the specific feature
- WebSocket patterns (gorilla/websocket)
- Docker SDK patterns
- OpenAI-compatible API patterns
- Similar implementations in other Go projects

### Step 4: Provide Implementation Guidance

Deliver a research report with:

1. **Executive Summary** - Brief overview of findings
2. **Existing Patterns** - How similar features are implemented in the codebase
3. **Recommended Approach** - Step-by-step implementation plan
4. **Key Files to Modify** - Specific files that need changes
5. **Code Examples** - Sample code following project conventions
6. **Considerations** - Security, performance, and operational notes
7. **References** - Links to relevant documentation

---

## Common Research Topics

### WebSocket & Swan Inference Integration
- `internal/computing/inference_client.go` - Connection, registration, heartbeat, message types
- Message types: register, inference, stream_chunk, stream_end, warmup, heartbeat, ack, error
- Auth: Bearer token in status check, API key in register payload
- Config: `WebSocketURL`, `ApiKey`, `Models` in `[Inference]` section

### Model Server Integration
- `internal/computing/inference_service.go` - Forward requests to `/v1/chat/completions`
- `internal/computing/model_registry.go` - Load models.json, hot-reload
- Supports: SGLang, vLLM, Ollama (OpenAI-compatible API)
- `local_model` field maps Swan model IDs to server model names

### Adding a New CLI Command
- Pattern: `cmd/computing-provider/*.go` using `urfave/cli`
- Register in `main.go` app.Commands
- Use `conf.GetConfig()` for configuration access
- Use `logs.GetLogger()` for logging

### REST API Endpoints
- Routes defined in `cmd/computing-provider/ubi.go` (gin router)
- Base path: `/api/v1/computing/`
- Inference endpoints: `/api/v1/computing/inference/*`
- Add new routes in the `startAPIServer()` function

### Docker Container Management
- `internal/computing/docker_service.go` - Container lifecycle
- Uses Docker SDK (`github.com/docker/docker/client`)
- NVIDIA GPU support via container toolkit

### Model Health & Monitoring
- `internal/computing/model_health_checker.go` - Periodic health checks
- Circuit breaker pattern for failing models
- Health updates sent to Swan Inference via WebSocket

### Setup Wizard & Model Discovery
- `internal/setup/discovery.go` - Probe endpoints for model servers
- `internal/setup/auth_client.go` - Swan Inference API (signup/login)
- Auto-match local models to Swan Inference model catalog

### Dashboard
- `internal/dashboard/server.go` - Go HTTP server
- `internal/dashboard/ui/` - React + Vite + Tailwind
- Proxies to inference API endpoints

---

## Project Conventions

### Go Patterns
- `internal/` for non-exported packages
- `urfave/cli` for CLI framework
- `gin` for HTTP router
- `gorilla/websocket` for WebSocket client
- Docker SDK for container management

### Error Handling
- Return errors up the call stack
- Use `logs.GetLogger()` for logging
- Include context in error messages

### Configuration
- Config loaded from `$CP_PATH/config.toml`
- Environment overrides: `CP_PATH`, `INFERENCE_API_KEY`, `INFERENCE_WS_URL`
- Use `conf.GetConfig()` to access configuration

### Development
- `go run ./cmd/computing-provider run` (always runs latest code)
- `make clean && make mainnet && make install` for binary
- Dashboard: `computing-provider dashboard` (port 3005)

### Build
- Use Makefile targets (`make mainnet`, `make testnet`)
- Binary installed to `/usr/local/bin/computing-provider`
