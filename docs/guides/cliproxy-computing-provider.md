# Guide: CLIProxyAPI — Serve GPT-5.x as a Computing Provider

*Turn a ChatGPT account into a Swan Chain computing provider in five minutes — no GPU required.*

---

## The idea

The computing-provider forwards inference requests to any OpenAI-compatible HTTP endpoint listed in `models.json`. [CLIProxyAPI](https://github.com/swanchain/CLIProxyAPI) turns a ChatGPT OAuth session into exactly that — a local OpenAI-compatible server serving `gpt-5.5`, `gpt-5.4`, and `gpt-5.4-mini`.

Neither binary needs modification. The integration is two config files.

```
swan-inference
      │  WebSocket dispatch
      ▼
computing-provider
      │  POST /v1/chat/completions
      ▼
CLIProxyAPI :8317           ← no GPU, no weights
      │  ChatGPT OAuth
      ▼
gpt-5.5 / gpt-5.4 / gpt-5.4-mini
```

---

## Step 1 — Run CLIProxyAPI

Clone and build [swanchain/CLIProxyAPI](https://github.com/swanchain/CLIProxyAPI), authenticate with your ChatGPT account, then start the server:

```yaml
# config.yaml
port: 8317
auth-dir: ~/.cli-proxy-api
api-keys:
  - sk-swan-local
```

```bash
./CLIProxyAPI serve
```

Verify it lists models:

```bash
curl http://localhost:8317/v1/models \
  -H "Authorization: Bearer sk-swan-local"
```

You should see `gpt-5.5`, `gpt-5.4`, `gpt-5.4-mini` in the response.

---

## Step 2 — Create a provider account

```bash
# Sign up
curl -X POST http://localhost:8100/api/v1/user/signup \
  -H "Content-Type: application/json" \
  -d '{"email":"cliproxy@example.com","password":"YourPass1","display_name":"CLIProxy Provider"}'

# Upgrade — note the sk-prov-* key in the response
TOKEN="<token from signup>"
curl -X POST http://localhost:8100/api/v1/user/upgrade-to-provider \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"CLIProxy Provider"}'
```

---

## Step 3 — Configure the computing-provider

Initialize a new config directory:

```bash
computing-provider --repo ./cp-cliproxy init --node-name cliproxy-provider --port 9086
```

Edit `cp-cliproxy/config.toml` — update the `[Inference]` block:

```toml
[Inference]
  Enable = true
  WebSocketURL = "ws://localhost:8081"   # no /ws suffix
  ApiKey = "sk-prov-<your-key>"
  Models = ["gpt-5.5", "gpt-5.4", "gpt-5.4-mini"]
```

Create `cp-cliproxy/models.json`:

```json
{
  "gpt-5.5": {
    "endpoint": "http://localhost:8317",
    "api_key": "sk-swan-local",
    "category": "text-generation"
  },
  "gpt-5.4": {
    "endpoint": "http://localhost:8317",
    "api_key": "sk-swan-local",
    "category": "text-generation"
  },
  "gpt-5.4-mini": {
    "endpoint": "http://localhost:8317",
    "api_key": "sk-swan-local",
    "category": "text-generation"
  }
}
```

---

## Step 4 — Start and verify

```bash
computing-provider --repo ./cp-cliproxy run
```

Expected log output:

```
Connected to Swan Inference
Registration successful: registered successfully
Model gpt-5.5 health changed: unknown -> healthy
Model gpt-5.4 health changed: unknown -> healthy
Model gpt-5.4-mini health changed: unknown -> healthy
```

End-to-end test through the marketplace:

```bash
curl http://localhost:8100/v1/chat/completions \
  -H "Authorization: Bearer sk-swan-localtest" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.4-mini",
    "messages": [{"role": "user", "content": "Hello"}],
    "max_tokens": 20
  }'
```

---

## What you get

| Feature | Detail |
|---|---|
| Models in marketplace | `gpt-5.5`, `gpt-5.4`, `gpt-5.4-mini` |
| Health monitoring | computing-provider polls `GET /v1/models` every 30s |
| Streaming | SSE streaming works end-to-end |
| Hot-reload | Edit `models.json` to add/remove models without restart |
| Dashboard | `http://localhost:9086` — provider metrics UI |

---

## Limitations

**Rate limits** — a single ChatGPT account has per-minute caps. For higher throughput, run multiple CLIProxyAPI instances (each with its own account) and register them as separate providers.

**Model verification** — swan-inference's deterministic/logprob challenges expect a local model checkpoint. Disable verification in `dev_mode` or use the external endpoint registration path if your deployment enforces it.

**Latency** — ChatGPT adds ~200–800 ms round-trip vs a local server. Suitable for development and low-volume production.

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Registration failed: invalid API key` | Check `ApiKey` in `config.toml` matches the `sk-prov-*` from your provider signup |
| Models stay `unknown` health | Confirm CLIProxyAPI is running and `GET /v1/models` returns 200 |
| `WebSocketURL` connection refused | Confirm swan-inference is running on port 8081; URL must not have `/ws` suffix |
| Requests return wrong model | Verify `models.json` keys match the model IDs consumers request |
