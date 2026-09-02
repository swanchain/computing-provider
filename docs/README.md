# Computing Provider Documentation

Computing Provider v2 turns a GPU into an AI inference endpoint on the Swan Chain
network. It connects outbound to Swan Inference over a WebSocket — no inbound
ports, no public IP — registers the models you serve, and forwards inference
requests to your local model server (SGLang, vLLM or Ollama).

**Inference only.** The legacy ZK-proof and UBI-task workloads, wallets and
smart-contract bindings were removed from this client; that code now lives in the
legacy [go-computing-provider](https://github.com/swanchain/go-computing-provider)
repository. No wallet is required here — a beneficiary address is optional, for
payouts.

**Key features**

- Docker-based deployment, no Kubernetes
- NVIDIA GPU support via the Container Toolkit
- Cross-platform: Linux, and macOS on Apple Silicon
- Continuous health checking with automatic deregistration and recovery
- Built-in dashboard, alerting and self-audit

## Quick start

1. [Installation](installation.md)
2. [Getting Started](getting-started.md)
3. [Configuration](configuration.md)
4. [Models](models.md)

## Documentation

### Setup and installation
- [Installation](installation.md) — requirements, binary install, building from source, updates
- [Getting Started](getting-started.md) — first run, Linux and macOS walkthroughs
- [Configuration](configuration.md) — `config.toml`, `models.json`, alerts, self-check, dashboard
- [Models](models.md) — catalog, VRAM sizing, downloading weights, switching models

### Operations
- [Command Line Interface](cli/README.md) — complete CLI reference
- [Troubleshooting](troubleshooting.md) — error reference and FAQ

Useful commands:

```bash
computing-provider inference status   # stage, models, earnings, context windows
computing-provider selfcheck          # audit this node
computing-provider dashboard          # web UI on port 3060
computing-provider research hardware  # CPU, memory, disk, GPUs
```

### Inference backends
- [SGLang Deployment](sglang-deployment.md) — deploying SGLang for inference
- [SGLang Performance Tuning](sglang-best-practices.md) — GPU configs, memory tuning, latency
- [Apple Silicon Support](apple-silicon-support.md) — Ollama on M-series Macs
- [CLIProxy + Computing Provider](guides/cliproxy-computing-provider.md) — serving through a proxy

### Development
- [Testing Plan](testing-plan.md) — end-to-end manual test checklist

## Getting help

- [Discord](https://discord.gg/3uQUWzaS7U)
- [GitHub Issues](https://github.com/swanchain/computing-provider/issues)
- [Swan Chain Documentation](https://docs.swanchain.io)

## License

Apache License 2.0 — see [LICENSE](../LICENSE).
