# Command Line Interface

```bash
computing-provider [global-flags] <command> [command-flags] [arguments]
```

`--help` works on every command and subcommand, and is authoritative — this page
is a map, not a substitute.

## Global flags

| Flag | Description |
|------|-------------|
| `--repo <path>` | Provider repository directory (default `~/.swan/computing`) |
| `--version` | Print version information |
| `--help`, `-h` | Help for any command or subcommand |

## Environment variables

| Variable | Effect |
|----------|--------|
| `CP_PATH` | Repository directory; same as `--repo` |
| `INFERENCE_API_KEY` | Provider API key, overriding `config.toml` |
| `INFERENCE_WS_URL` | WebSocket URL, useful for local development |

Secrets belong in `$CP_PATH/.env` rather than `config.toml` — see
[configuration.md](../configuration.md).

## Commands

| Command | Purpose |
|---------|---------|
| `setup` | Interactive setup wizard: auth, config, model discovery |
| `run` | Start the provider |
| `selfcheck` | Audit this node for the failures that earn nothing |
| `dashboard` | Web UI for monitoring and settings |
| `inference` | Status, config, collateral, beneficiary, model selection |
| `models` | Catalog, weight download, verification, local model files |
| `alerts` | Test the configured alert transports |
| `research` | Hardware and GPU information, benchmarks |
| `init` | Create a provider repository without the wizard |
| `info`, `state` | Provider information and current state |
| `version`, `update` | Show the installed build; install a newer release |

### `setup`

The recommended path for a new provider. Start your model server **first** — the
wizard discovers what is already running.

```bash
computing-provider setup                        # full interactive setup
computing-provider setup --skip-discovery       # skip model discovery
computing-provider setup --api-key=sk-prov-xxx  # use an existing key

computing-provider setup discover               # only discover model servers
computing-provider setup login                  # log into an existing account
computing-provider setup signup                 # create an account
```

### `run`

```bash
computing-provider run
computing-provider run --host 0.0.0.0   # only on a trusted network
```

Starts the WebSocket connection to Swan Inference, registers the models in
`models.json`, and serves the local REST API. Shuts down cleanly on
SIGTERM/SIGINT.

### `selfcheck`

```bash
computing-provider selfcheck
computing-provider selfcheck --json           # machine-readable
computing-provider selfcheck --no-inference   # skip the completion probe
computing-provider selfcheck --min-free-gb 50 # disk threshold
```

Exits non-zero when a check fails, so it works directly as a cron or monitoring
check. The daemon runs the same audit on a timer — see
[configuration.md](../configuration.md#self-check-and-auto-heal).

### `dashboard`

```bash
computing-provider dashboard                      # http://localhost:3060
computing-provider dashboard --port 8080
computing-provider dashboard --api http://localhost:8085
computing-provider dashboard --host 0.0.0.0       # trusted networks only
```

Defaults to `127.0.0.1`. Read-only for anyone who can reach the listener; writes
and settings need the token at `$CP_PATH/dashboard.token`, entered through
**Unlock controls**.

### `inference`

```bash
computing-provider inference status                 # stage, models, earnings, context windows
computing-provider inference status --json
computing-provider inference config                 # current config, API key masked
computing-provider inference deposit                # collateral instructions
computing-provider inference deposit --check        # current collateral status
computing-provider inference set-beneficiary 0x...  # wallet that payouts go to
computing-provider inference recommend-models       # rank catalog models by demand
computing-provider inference recommend-models --vram 24 --top 10 --category llm
computing-provider inference keygen                 # generate a provider API key
computing-provider inference request-approval       # request approval to start earning
```

### `models`

```bash
computing-provider models catalog          # supported models and local status
computing-provider models catalog --json
computing-provider models download <id>    # fetch weights from HuggingFace
computing-provider models download <id> --dest /mnt/weights
computing-provider models list             # what is on disk
computing-provider models verify           # re-check SHA256 of local weights
computing-provider models rm <id>          # delete local weights
```

Gated repositories need `HF_TOKEN` set. See [models.md](../models.md).

### `alerts`

```bash
computing-provider alerts test
computing-provider alerts test --message "hello from my node"
```

Sends through every configured transport, so you find out the SMTP password is
wrong now rather than during an outage.

### `research`

```bash
computing-provider research hardware        # CPU, memory, disk, GPUs
computing-provider research gpu-info        # GPU details
computing-provider research gpu-info --json
computing-provider research gpu-benchmark   # run a benchmark
computing-provider research gpu-benchmark --gpu 0 --iterations 5
```

### `init`, `info`, `state`

```bash
computing-provider init --node-name my-provider --port 8085
computing-provider info    # provider information
computing-provider state   # current state
```

`setup` calls `init` for you; run it directly only when configuring by hand.

### `version` and `update`

```bash
computing-provider version
computing-provider update --check   # report only, change nothing
sudo computing-provider update      # download, verify, replace the binary
sudo computing-provider update --yes
```

`update` verifies the download against the release checksums and replaces the
binary atomically. It does **not** restart a running provider. Details and
manual signature verification in
[installation.md](../installation.md#keeping-up-to-date).

## Next steps

- [Configuration](../configuration.md) — every field in `config.toml` and `models.json`
- [Models](../models.md) — choosing, downloading and switching models
- [Troubleshooting](../troubleshooting.md) — error reference and FAQ
