# Model VRAM estimation for local inference

Research note for the `cp_agent` design. The question it answers: **can this node
actually run a given model, and can it find that out without downloading 600 GB
to discover the answer is no?**

This matters because the marketplace does not tell us. `GET
/api/v1/stats/model-demand` publishes `vram_known: false` for **26 of 26**
models, with `min_vram_gb: 0` alongside. A provider client that wants to choose
models automatically has to work the requirement out for itself.

## Summary

| question | answer |
|---|---|
| Is there a hosted API that returns a model's VRAM requirement? | **No.** Every calculator found is a web UI with no programmatic endpoint. |
| Can exact parameter counts be fetched without downloading weights? | **Yes** — HuggingFace's API, free, unauthenticated, works on gated repos. |
| Can KV-cache dimensions be fetched? | **Yes for open repos** via `config.json`; gated repos return 401 without a token. |
| Can GGUF files be measured without downloading? | **Yes** — `gguf-parser-go` reads the header over HTTP range requests. |
| Is a generic formula good enough? | **No.** Architecture breaks it — see [Where the formula fails](#where-the-formula-fails). |

The recommendation is to compute locally from primary sources, and to treat any
figure the node cannot derive as *unknown* rather than guessing — which is what
the `vram_not_known_to_fit` guardrail already enforces.

## What exists publicly

### Calculators (all web UIs, no API)

- [APXML VRAM Calculator](https://apxml.com/tools/vram-calculator) — the most
  thorough; multi-GPU, quantisation, throughput estimates.
- [SelfHostLLM](https://selfhostllm.org/) — GPU memory and max concurrent
  requests for self-hosted inference.
- [LLM VRAM Calculator](https://llmvramcalculator.com/),
  [LocalLLM.in](https://localllm.in/blog/interactive-vram-calculator),
  [DevTk.AI](https://devtk.ai/en/tools/vram-calculator/) — variations on the
  same arithmetic.
- [BentoML's write-up](https://bentoml.com/llm/getting-started/calculating-gpu-memory-for-llms)
  — the clearest explanation of the formula itself.

None of these expose a REST endpoint. They are useful for checking our own
arithmetic by hand, and useless to an agent.

### Libraries that are usable programmatically

- **[`gguf-parser-go`](https://github.com/gpustack/gguf-parser-go)** (GPUStack,
  MIT, `github.com/gpustack/gguf-parser-go`). The strongest option for the
  llama.cpp path, and it is Go, so it drops straight into this repo.
  - `ParseGGUFFileRemote()` / `ParseFromHuggingFace()` read the metadata over
    **chunked HTTP range requests** — no full download.
  - `EstimateLLaMACppRun()` reproduces llama.cpp's own allocation, including
    `--tensor-split` across several GPUs and the context size.
  - Because it reads real tensor shapes rather than assuming an architecture, it
    is right about the cases a formula gets wrong.
- **[`accelerate estimate-memory`](https://huggingface.co/docs/accelerate/usage_guides/model_size_estimator)**
  (HuggingFace, Python). Downloads `config.json`, instantiates on the meta
  device, reports size. Their own docs say it covers **loading only** and that
  inference adds roughly 20% — which matches what was measured on this box
  (18%). Python, so a subprocess dependency for a Go binary.

### Primary sources — verified against the live API

**Exact parameter counts by dtype**, no auth, works on gated repos:

```
GET https://huggingface.co/api/models/{id}?expand=safetensors
```

```
Qwen/Qwen2.5-7B-Instruct        7,615,616,512   {BF16: 7,615,616,512}
meta-llama/Llama-3.3-70B        70,553,706,496  {BF16: 70,553,706,496}     (gated — still works)
deepseek-ai/DeepSeek-V3-0324    684,531,386,000 {F8_E4M3: 680B, BF16: 3.9B, F32: 41M}
```

The dtype breakdown is the useful part: it gives the byte count directly rather
than requiring an assumption about precision.

**KV-cache dimensions** from `config.json`:

```
GET https://huggingface.co/{id}/resolve/main/config.json   (follow redirects)
```

Returns `num_hidden_layers`, `num_key_value_heads`, `head_dim` (or
`hidden_size` / `num_attention_heads`), `max_position_embeddings`,
`torch_dtype`.

> **Gated repos return HTTP 401 here**, unlike the API endpoint above. So for a
> gated model the node can get the weight size but not the KV dimensions without
> an HF token. `meta-llama/Llama-3.3-70B-Instruct` is the case to test against.

## The formula

```
VRAM  =  weights  +  KV cache  +  runtime overhead

weights   = Σ (params_of_dtype × bytes_of_dtype)
KV cache  = 2 × layers_with_kv × kv_heads × head_dim × bytes_per_element
              × context × concurrent_sequences
overhead  ≈ 0.5–1.5 GiB   (CUDA context, cuBLAS workspace, compute buffers)
```

Bytes per element: BF16/FP16 2, FP8 1, Q8_0 ~1.06 (1 byte plus a block scale),
Q4_K_M ~0.57, INT4 0.5.

`concurrent_sequences` is llama.cpp's `--parallel` or vLLM's `--max-num-seqs`.
It is easy to forget and it multiplies the KV term.

### Validated against this node

`Qwen3.8-27B` on GPUs 2–3, llama.cpp, `-c 65536`, `-ctk q8_0 -ctv q8_0`:

| term | GiB |
|---|---|
| weights (actual GGUF file size) | 14.30 |
| KV cache, naive — all 64 layers | 8.50 |
| KV cache, corrected — 16 full-attention layers | 2.12 |
| **predicted total (corrected)** | **16.43** |
| **measured (`nvidia-smi`)** | **17.42** |
| residual = CUDA context + compute buffers | 0.99 |

The corrected estimate lands within 1 GiB of measured, and the residual is
exactly the overhead term the formula expects.

**Weights alone under-predicted real usage by 18%.** Capacity must never be
planned on weight size.

## Where the formula fails

The naive KV term over-predicted by 5.38 GiB above — a 32% error on the total —
and the reason generalises. `Qwen3.8-27B`'s config says:

```json
"layer_types": [48 × "linear_attention", 16 × "full_attention"],
"full_attention_interval": 4
```

Only 16 of 64 layers hold a KV cache at all. Three architecture families break a
generic formula:

- **Hybrid / linear attention** (Qwen3-Next and Qwen3.5 generation): most layers
  carry no KV cache. Read `layer_types` or `full_attention_interval`.
- **Multi-head Latent Attention** (DeepSeek V2/V3): KV is compressed to a small
  latent per token, so the classic formula is wildly high. Look for
  `kv_lora_rank`.
- **Sliding-window attention** (Gemma, some Mistral): KV is bounded by the
  window, not the context. Look for `sliding_window` and whether it is enabled.

Here the error was in the safe direction — over-predicting only means declining a
model that would have fitted. **The dangerous direction is under-predicting**,
which is an OOM partway through a load, after the weights have been pulled.

This is the argument for `gguf-parser-go` over hand-rolled arithmetic on the
GGUF path: it reads the tensors that are actually there.

## What this means for the marketplace models

Applying the above to the models the demand table currently ranks highest, on a
4× RTX 3080 node (40 GiB total), 32k context:

| model | precision | weights | KV | total | fits 40 GiB? |
|---|---|---|---|---|---|
| `deepseek-ai/DeepSeek-V3-0324` | FP8 | 637.5 | (MLA, ≪ naive) | **≥ 640** | no |
| `meta-llama/Llama-3.3-70B` | Q4 | 32.9 | 10.0 | 42.9 | no |
| `Qwen/Qwen2.5-7B-Instruct` | BF16 | 14.2 | 1.8 | 15.9 | **yes** |

The models with the best per-provider earnings estimates are the ones this
hardware cannot run, and nothing in the marketplace data says so. That is the
gap `cp_agent` has to close locally.

## Design recommendation for `cp_agent`

Three tiers, most trustworthy first, and **never** promote a lower tier's answer
to a higher tier's confidence:

1. **Measured.** The model has run here before; use what it actually consumed.
   Store it. This is the only figure that needs no caveat.
2. **Derived.** GGUF → `gguf-parser-go` with the node's real `--tensor-split` and
   context. Safetensors → HF API for weights, `config.json` for KV, with the
   architecture corrections above.
3. **Unknown.** Anything else — a gated repo with no token, an architecture not
   recognised, a missing config. Report `unknown` and let the guardrail refuse
   it.

Three properties the implementation needs:

- **Unknown must stay unknown.** This is the same rule
  `internal/market.VRAMFit` already applies, and for the same reason: the
  previous code read a missing requirement as "fits" and passed 685 GB models
  onto 10 GB cards.
- **Estimate against the node's real serving configuration**, not a default. The
  context length, KV quantisation and parallelism the node would actually use
  change the answer by multiples.
- **Keep headroom.** Fitting a model to 39.8 of 40 GiB is not fitting it.
  Reserve the overhead term plus a margin, and prefer the measured figure the
  moment one exists.

A useful side effect: once the node can derive requirements, it can report them
upstream. The marketplace's `vram_known: false` is a gap that providers are
better placed to fill than the platform is — they are the ones who find out what
actually loads.

## Sources

- [gguf-parser-go](https://github.com/gpustack/gguf-parser-go) — MIT, Go, remote GGUF metadata parsing and llama.cpp memory estimation
- [HuggingFace Accelerate model memory estimator](https://huggingface.co/docs/accelerate/usage_guides/model_size_estimator)
- [Model Memory Utility space](https://huggingface.co/spaces/hf-accelerate/model-memory-usage)
- [BentoML — calculating GPU memory for serving LLMs](https://bentoml.com/llm/getting-started/calculating-gpu-memory-for-llms)
- [APXML VRAM calculator](https://apxml.com/tools/vram-calculator)
- [SelfHostLLM](https://selfhostllm.org/)
- [Predict peak VRAM before downloading a model](https://genmind.ch/posts/Predict-Peak-VRAM-Before-Downloading-A-Model/)
