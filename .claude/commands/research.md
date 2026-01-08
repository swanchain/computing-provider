# Research

Research assistant for Go Computing Provider development. Analyzes codebase, searches documentation, and provides implementation guidance.

## Topic: $ARGUMENTS

## Instructions

You are a technical researcher helping to develop the Go Computing Provider for Swan Chain. Your role is to:
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

**Documentation**
- Check `docs/` for architecture and setup guides
- Check `CLAUDE.md` for project conventions

**CLI Commands**
- Check `cmd/computing-provider/` for CLI command patterns
- Review `main.go` for command registration
- Look at existing commands (wallet.go, task.go, ubi.go) for patterns

**Core Services**
- Check `internal/computing/` for service implementations
- Review `k8s_service.go` for Kubernetes operations
- Review `space_service.go` for job deployment patterns
- Review `ubi_service.go` for UBI/ZK proof task handling
- Review `cron_task.go` for background job patterns
- Check `wire.go` and `wire_gen.go` for dependency injection

**Smart Contracts**
- Check `internal/contract/account/` for CP account registration
- Check `internal/contract/ecp/` for Edge Computing contracts
- Check `internal/contract/fcp/` for Fog Computing contracts
- Check `internal/contract/token/` for SWAN token operations

**Data Models**
- Check `internal/models/` for data structures (job.go, resources.go, ubi.go)
- Check `internal/yaml/` for deployment manifest parsing

**Configuration**
- Check `conf/config.go` for configuration structure
- Check `config.toml.sample` for configuration options
- Check `build/parameters.json` for network-specific parameters

**Infrastructure**
- Check `internal/db/` for database operations
- Check `internal/controller/` for HTTP handlers
- Check `internal/service/` for business logic
- Check `wallet/` for keystore and transaction signing
- Check `util/` for utility functions

### Step 3: Search External Resources

Use web search to find:
- Go best practices for the specific feature
- Kubernetes client-go patterns
- Ethereum/smart contract interaction patterns
- Similar implementations in other Go projects

### Step 4: Provide Implementation Guidance

Deliver a research report with:

1. **Executive Summary** - Brief overview of findings
2. **Existing Patterns** - How similar features are implemented in the codebase
3. **Recommended Approach** - Step-by-step implementation plan
4. **Key Files to Modify** - Specific files that need changes
5. **Code Examples** - Sample code snippets following project conventions
6. **Considerations** - Security, performance, and operational considerations
7. **References** - Links to relevant documentation

---

## Output Format

```markdown
# Research Report: [Topic]

## Executive Summary
[2-3 sentence overview]

## Existing Patterns
- [Pattern 1 with file references]
- [Pattern 2 with file references]

## Recommended Approach

### CLI Changes
1. [Step with file path]
2. [Step with file path]

### Service Changes
1. [Step with file path]
2. [Step with file path]

### Contract Integration
1. [Step with file path]
2. [Step with file path]

## Key Files

| File | Change Type | Description |
|------|-------------|-------------|
| path/to/file | Create/Modify | What to do |

## Code Examples

### [Component/Function Name]
```go
// Code following project conventions
```

## Considerations

### Security
- [Security consideration]

### Performance
- [Performance consideration]

### Kubernetes
- [K8s-specific consideration]

## References
- [Link to relevant documentation]
```

---

## Common Research Topics

### Adding a New CLI Command
- Analyze `cmd/computing-provider/` for command patterns
- Check how commands are registered in `main.go`
- Review flag handling and validation patterns
- Look at `tablewriter.go` for output formatting

### Adding a New Core Service
- Review `internal/computing/` for service patterns
- Check `wire.go` for dependency injection setup
- Look at existing services for initialization patterns
- Review `provider.go` for service orchestration

### Working with Kubernetes
- Check `internal/computing/k8s_service.go` for K8s client patterns
- Review deployment creation in `deploy.go`
- Look at `space_service.go` for job lifecycle management
- Check `internal/yaml/` for manifest parsing

### Smart Contract Integration
- Review `internal/contract/` for contract stub patterns
- Check how contract bindings are generated
- Look at `wallet/transaction.go` for transaction signing
- Review `internal/contract/utils.go` for common helpers

### Adding a New Data Model
- Review `internal/models/` for model patterns
- Check existing models (job.go, resources.go, ubi.go)
- Look at how models are used in services

### Working with Configuration
- Review `conf/config.go` for config structure
- Check `build/parameters.json` for network parameters
- Look at how `CP_PATH` environment variable is used

### Background Tasks and Cron Jobs
- Check `internal/computing/cron_task.go` for scheduled task patterns
- Review how tasks are registered and executed
- Look at error handling and retry patterns

### UBI/ZK Proof Tasks
- Review `internal/computing/ubi_service.go` for task handling
- Check `internal/computing/sequence_service.go` for sequencer integration
- Look at `internal/contract/ecp/` for task contracts

### HTTP API Endpoints
- Check `internal/computing/http.go` for route definitions
- Review `internal/controller/` for handler implementations
- Look at `util/response.go` for response formatting

### Wallet and Transaction Operations
- Review `wallet/wallet.go` for wallet management
- Check `wallet/keystore.go` for key storage
- Look at `wallet/transaction.go` for signing patterns

---

## Project Conventions

### Go Patterns
- Use `internal/` for non-exported packages
- Google Wire for dependency injection
- urfave/cli for CLI command framework
- client-go for Kubernetes operations
- go-ethereum for blockchain interactions

### Error Handling
- Return errors up the call stack
- Use `logs.GetLogger()` for logging
- Include context in error messages

### Configuration
- Config loaded from `$CP_PATH/config.toml`
- Network parameters embedded at build time
- Use `conf.GetConfig()` to access configuration

### Testing
- Tests in `test/` directory
- Use `go test ./...` to run all tests
- Most tests require Kubernetes cluster access

### Build
- Use Makefile targets (`make mainnet`, `make testnet`)
- Network tag set via ldflags at build time
- Binary installed to `/usr/local/bin/computing-provider`
