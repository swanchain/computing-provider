# Models

Which models to serve, how to get their weights, and how to swap one for another
without restarting the provider.

## Choosing what to serve

You are paid per token actually served, so the model you pick decides your
income far more than your hardware does. A popular model with many providers may
route you less traffic than a less contested one your GPU can handle.

```bash
computing-provider inference recommend-models
```

That ranks catalog models by current demand against your hardware, and is the
fastest way to find traffic.

## The catalog

`models catalog` lists every model Swan Inference supports, with what you have
already downloaded:

```
$ computing-provider models catalog
Available models in Swan Model Repository (6):

+--------------------------------------------------------+----------+-------+----------+----------------+
|                        MODEL ID                        | CATEGORY | FILES |   SIZE   |     STATUS     |
+--------------------------------------------------------+----------+-------+----------+----------------+
| Qwen/Qwen2.5-0.5B                                      |   llm    |     1 | 942.3 MB |   downloaded   |
| Qwen/Qwen3-8B                                          |   llm    |     5 |  15.3 GB | partial (3/5)  |
| Sinensis/L3.3-MS-Nevoria-70b-AWQ                       |   llm    |     8 |  13.7 GB | not downloaded |
| TheDrummer/Cydonia-24B-v4.1                            |   llm    |    19 |  43.9 GB | not downloaded |
| jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym |   llm    |     7 |   9.3 GB | not downloaded |
| meganovaai/MN-Violet-Lotus-12B-AWQ                     |   llm    |    12 |   7.8 GB | not downloaded |
+--------------------------------------------------------+----------+-------+----------+----------------+
```

Model IDs are HuggingFace repo IDs. Whatever you put in `models.json` must match
one of these exactly, and must also match your inference server's
`--served-model-name` — a mismatch anywhere in that chain is the most common
reason a healthy-looking provider receives no requests.

Related commands:

```bash
computing-provider models list      # what is on disk
computing-provider models download  # fetch weights from HuggingFace
computing-provider models verify    # re-check SHA256 of downloaded weights
computing-provider models rm        # delete local weights
```

## VRAM per model

Budget roughly **2× the HuggingFace file size** in VRAM: the weights have to fit
alongside the KV cache and runtime overhead. Longer context windows push this
higher still.

| Model | HF size | Recommended VRAM | Example GPU |
|-------|---------|------------------|-------------|
| Qwen/Qwen2.5-0.5B | 1 GB | 2 GB+ | Any GPU |
| meganovaai/MN-Violet-Lotus-12B-AWQ | 8.3 GB | 16 GB+ | RTX 4090, RTX 3090 |
| jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym | 15.1 GB | 32 GB+ | 2× RTX 3090/4090 or A100 |
| Qwen/Qwen3-8B | 16.4 GB | 32 GB+ | 2× RTX 3090/4090 or A100 |
| Sinensis/L3.3-MS-Nevoria-70b-AWQ | 39.8 GB | 80 GB+ | A100 80GB or 4× RTX 3090/4090 |
| TheDrummer/Cydonia-24B-v4.1 | 47.2 GB | 96 GB+ | 2× A100 or 4× RTX 3090/4090 |

## Downloading weights

```bash
computing-provider models download Qwen/Qwen2.5-7B-Instruct
```

Weights come straight from HuggingFace into `~/.swan/models/`, and large LFS
files are verified against their SHA256 as they land.

Gated repositories (most Llama models, among others) need you to accept the
licence on the model's HuggingFace page first, then supply a token:

```bash
export HF_TOKEN=hf_xxxxxxxxxxxxxxxxxxxxx
computing-provider models download meta-llama/Llama-3.3-70B-Instruct
```

## Switching models

Adding, removing, or swapping a model needs no provider restart.

### 1. Start the new model server

```bash
# Example: switch from Qwen 2.5 7B to Mistral Small 24B (AWQ)

# Stop the old server — optional, you can run several at once
docker stop sglang && docker rm sglang

computing-provider models download jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym

docker run -d --gpus all -p 30000:30000 --ipc=host --name sglang \
  -v ~/.swan/models/jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server --model-path /models \
    --host 0.0.0.0 --port 30000 \
    --served-model-name jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym

# Confirm the server is up before touching models.json
curl http://localhost:30000/v1/models
```

### 2. Update `models.json`

Edit `~/.swan/computing/models.json`:

```json
{
  "jeffcookio/Mistral-Small-3.2-24B-Instruct-2506-awq-sym": {
    "endpoint": "http://localhost:30000",
    "gpu_memory": 16000,
    "category": "text-generation"
  }
}
```

The provider watches this file and hot-reloads on change. To force it:

```bash
curl -X POST http://localhost:8085/api/v1/computing/inference/models/reload
```

Every field is documented in
[configuration.md](configuration.md#modelsjson-field-reference). Two are worth
knowing about up front:

- **`local_model`** — set it when your server's name for the model differs from
  the marketplace ID (Ollama's `qwen2.5:7b` for `Qwen/Qwen2.5-7B-Instruct`).
- **`context_length`** — set it for **any backend other than vLLM or SGLang**.
  Only those two expose `max_model_len`, so for Ollama, llama.cpp, LiteLLM and
  other OpenAI-compatible proxies nothing is detected and the marketplace
  advertises the catalog's theoretical window instead of yours. Clients then
  size prompts your backend will reject. See
  [configuration.md](configuration.md#context-windows).

### 3. Verify

```bash
curl http://localhost:8085/api/v1/computing/inference/models  # local view
computing-provider inference status                           # upstream view
computing-provider selfcheck                                  # catches silent mismatches
```

`inference status` also prints the context window reported for each model:

```
Reported context windows
----------------------------------------
  TheDrummer/Cydonia-24B-v4.3                 45056  (detected)
  openai/gpt-5.5                             128000  (override)
  Qwen/Qwen3.8-27B                                -  (not reported)
```

Anything showing `not reported` is being advertised at its catalog value rather
than yours.

## Running several models at once

Start each on its own port and list them all in `models.json`. Pin each to
specific GPUs rather than using `--gpus all`, so one server does not claim
memory another needs:

```bash
docker run -d --gpus '"device=0"' -p 30000:30000 ... # model A
docker run -d --gpus '"device=1"' -p 30001:30001 ... # model B
```

For splitting one large model across several GPUs, use tensor parallelism
(`--tp 2`, `--tp 4`) — see
[SGLang deployment](sglang-deployment.md) and
[SGLang performance tuning](sglang-best-practices.md).
