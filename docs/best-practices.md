# SGLang Performance Tuning Best Practices

A practical guide for inference providers on Swan Chain to get the best latency and throughput from SGLang.

## Golden Rules

1. **Never disable CUDA graphs** unless you're debugging OOM crashes. CUDA graphs alone can cut p50 latency by 100x.
2. **Always use `--served-model-name`** matching the HuggingFace repo ID (e.g., `meta-llama/Llama-3.2-3B-Instruct`). Swan Inference routes by model name.
3. **Always use `--ipc=host` and `--shm-size`** for multi-GPU setups. Without these, NCCL communication fails silently.
4. **Start conservative, then tune up.** Begin with `--mem-fraction-static 0.70`, confirm stability, then increase.
5. **Benchmark before going live.** Test locally with `curl` before connecting to Swan Inference.

## Single GPU Configurations

### RTX 3080 / 3080 Ti (10-12 GB VRAM)

Best for 3B-7B models or GPTQ/AWQ quantized models up to 14B.

```bash
docker run -d --gpus all \
  -p 30000:30000 \
  --name sglang \
  --shm-size 4g \
  --ipc=host \
  -v /path/to/model:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --mem-fraction-static 0.85 \
    --served-model-name "org/Model-Name"
```

| Model Size | Quantization | `--mem-fraction-static` | Notes |
|-----------|--------------|------------------------|-------|
| 3B | None (FP16) | 0.90 | Fits easily |
| 7B | None (FP16) | 0.85 | Tight, may need 0.80 |
| 7B | GPTQ 4-bit | 0.90 | Comfortable fit |
| 14B | GPTQ 4-bit | 0.85 | Tight fit, monitor OOM |

### RTX 3090 / 3090 Ti (24 GB VRAM)

Best for 7B-14B models unquantized, or GPTQ models up to 24B.

```bash
docker run -d --gpus all \
  -p 30000:30000 \
  --name sglang \
  --shm-size 4g \
  --ipc=host \
  -v /path/to/model:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --mem-fraction-static 0.90 \
    --served-model-name "org/Model-Name"
```

| Model Size | Quantization | `--mem-fraction-static` | Notes |
|-----------|--------------|------------------------|-------|
| 7B | None (FP16) | 0.90 | Large KV cache, great throughput |
| 14B | None (FP16) | 0.85 | Good fit |
| 14B | GPTQ 4-bit | 0.90 | Lots of headroom |
| 24B | GPTQ 4-bit | 0.85 | Good fit |

### RTX 4090 (24 GB VRAM)

Same VRAM as 3090 but faster compute. Best single-GPU option for inference.

```bash
docker run -d --gpus all \
  -p 30000:30000 \
  --name sglang \
  --shm-size 4g \
  --ipc=host \
  -v /path/to/model:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --mem-fraction-static 0.90 \
    --served-model-name "org/Model-Name"
```

### A100 / H100 (40-80 GB VRAM)

Can run 70B+ models on a single GPU with quantization, or unquantized up to 34B.

```bash
docker run -d --gpus all \
  -p 30000:30000 \
  --name sglang \
  --shm-size 16g \
  --ipc=host \
  -v /path/to/model:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --mem-fraction-static 0.90 \
    --chunked-prefill-size 8192 \
    --served-model-name "org/Model-Name"
```

## Multi-GPU (Tensor Parallelism)

Tensor parallelism splits a model across multiple GPUs. This is required when a model doesn't fit on a single GPU.

### Key Concepts

- **`--tp-size N`** splits the model across N GPUs
- GPUs communicate via NCCL (NVIDIA Collective Communications Library)
- **NVLink** (datacenter GPUs, some 3090s) is fast inter-GPU communication
- **PCIe** (most consumer GPUs) is slower and requires special tuning

### Consumer GPUs over PCIe (3080, 3090, 4090)

Consumer GPUs typically don't have NVLink. The PCIe bus is the bottleneck.

```bash
docker run -d --gpus all \
  -p 30000:30000 \
  --name sglang \
  --shm-size 4g \
  -e NCCL_P2P_DISABLE=1 \
  -v /path/to/model:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --tp-size 4 \
    --mem-fraction-static 0.70 \
    --disable-custom-all-reduce \
    --chunked-prefill-size 2048 \
    --watchdog-timeout 600 \
    --served-model-name "org/Model-Name"
```

**Required flags for PCIe setups:**

| Flag | Why |
|------|-----|
| `NCCL_P2P_DISABLE=1` | PCIe P2P often fails on consumer GPUs without NVLink |
| `--disable-custom-all-reduce` | Custom all-reduce assumes NVLink-speed interconnects |
| `--chunked-prefill-size 2048` | Smaller chunks reduce memory spikes during prefill |
| `--watchdog-timeout 600` | TP over PCIe is slow to start; prevents false timeout kills |

**Flags to test carefully:**

| Flag | Default | Try removing if... |
|------|---------|-------------------|
| `--disable-cuda-graph` | Enabled | You're seeing OOM during CUDA graph capture |
| `--disable-overlap-schedule` | Enabled | You have NVLink or ample VRAM headroom |

> **Real-world example:** On 4x RTX 3080 (10GB each) running a 24B GPTQ model, removing `--disable-cuda-graph` dropped p50 latency from 25,835ms to 212ms (122x improvement) with no OOM issues.

### Datacenter GPUs with NVLink (A100, H100)

NVLink provides 600+ GB/s bandwidth vs PCIe's ~32 GB/s. Most flags can stay at defaults.

```bash
docker run -d --gpus all \
  -p 30000:30000 \
  --name sglang \
  --shm-size 16g \
  --ipc=host \
  -v /path/to/model:/models \
  lmsysorg/sglang:latest \
  python3 -m sglang.launch_server \
    --model-path /models \
    --host 0.0.0.0 \
    --port 30000 \
    --tp-size 4 \
    --mem-fraction-static 0.90 \
    --chunked-prefill-size 8192 \
    --served-model-name "org/Model-Name"
```

No need for `NCCL_P2P_DISABLE`, `--disable-custom-all-reduce`, or `--disable-overlap-schedule` with NVLink.

## GPTQ / AWQ Quantization

Quantization reduces model memory footprint, letting you run larger models on smaller GPUs.

### When to Use Quantization

| Scenario | Recommendation |
|----------|---------------|
| Model fits in VRAM unquantized | Don't quantize (better quality) |
| Model barely fits | Try quantization to free KV cache space |
| Model doesn't fit on 1 GPU | Quantize before adding more GPUs (simpler) |
| Multi-GPU over PCIe | Quantize to reduce TP communication overhead |

### GPTQ with Marlin Kernels

SGLang auto-detects GPTQ models and uses Marlin kernels for acceleration. No extra flags needed.

```bash
# Just point --model-path at a GPTQ model directory
--model-path /path/to/Model-GPTQ
```

Look for this in logs to confirm Marlin is active:
```
The model is convertible to gptq_marlin during runtime. Using gptq_marlin kernel.
```

### AWQ Quantization

```bash
# AWQ models are also auto-detected
--model-path /path/to/Model-AWQ
```

### FP8 Quantization (Ada/Hopper GPUs only)

```bash
# Requires RTX 4090, A100, H100 or newer
--quantization fp8
```

## Memory Tuning

### Understanding `--mem-fraction-static`

This controls how much VRAM is allocated for the KV cache (after model weights are loaded). Higher values = more concurrent requests, but risk OOM.

```
Total VRAM = Model Weights + KV Cache + CUDA Graphs + Overhead
                              ^^^^^^^
                    controlled by --mem-fraction-static
```

### Safe Starting Points

| GPU VRAM | Model Fits Easily | Model Is Tight |
|----------|------------------|----------------|
| 10 GB | 0.85 | 0.70 |
| 12 GB | 0.85 | 0.75 |
| 24 GB | 0.90 | 0.85 |
| 40 GB | 0.90 | 0.85 |
| 80 GB | 0.90 | 0.90 |

### Signs You Need to Lower `--mem-fraction-static`

- Container crashes during CUDA graph capture (startup)
- OOM errors during inference under load
- Container restarts intermittently

### Signs You Can Raise `--mem-fraction-static`

- `nvidia-smi` shows significant free VRAM during inference
- Requests are queuing due to KV cache exhaustion (check SGLang logs for `waiting_requests`)

## Benchmarking Your Setup

Before connecting to Swan Inference, verify your setup performs well locally.

### Quick Latency Test

```bash
# Short request (measures overhead + TTFT)
time curl -s http://localhost:30000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"org/Model-Name","messages":[{"role":"user","content":"What is 2+2?"}],"max_tokens":20}'

# Medium request (measures generation throughput)
time curl -s http://localhost:30000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"org/Model-Name","messages":[{"role":"user","content":"Explain TCP vs UDP"}],"max_tokens":200}'

# Long request (measures sustained throughput)
time curl -s http://localhost:30000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"org/Model-Name","messages":[{"role":"user","content":"Write a detailed essay about machine learning"}],"max_tokens":500}'
```

### Concurrency Test

```bash
# Fire 5 concurrent requests
for i in $(seq 1 5); do
  curl -s http://localhost:30000/v1/chat/completions \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"org/Model-Name\",\"messages\":[{\"role\":\"user\",\"content\":\"Write a haiku about number $i\"}],\"max_tokens\":50}" &
done
wait
echo "All done"
```

### What Good Latency Looks Like

| Request Type | Good p50 | Acceptable p50 | Needs Tuning |
|-------------|----------|----------------|-------------|
| Short (20 tokens) | < 500ms | < 1s | > 2s |
| Medium (200 tokens) | < 3s | < 5s | > 10s |
| Long (500 tokens) | < 8s | < 15s | > 30s |

These vary by model size and GPU. A 7B on a 4090 will be much faster than a 24B GPTQ on 4x 3080s.

### Monitor Provider Metrics

Once connected to Swan Inference:

```bash
# Check latency percentiles and throughput
curl -s http://localhost:9085/api/v1/computing/inference/metrics | python3 -m json.tool

# Check model health
curl -s http://localhost:9085/api/v1/computing/inference/models | python3 -m json.tool
```

## Common Pitfalls

### 1. Disabling CUDA Graphs Unnecessarily

**Problem:** Adding `--disable-cuda-graph` because of OOM during startup, then forgetting about it.

**Impact:** 10-100x worse latency on repeated requests.

**Fix:** Only disable CUDA graphs as a last resort. First try lowering `--mem-fraction-static` or `--chunked-prefill-size`.

### 2. Wrong `--mem-fraction-static` for Your GPU

**Problem:** Using `0.90` on a 10GB card with a large model causes intermittent OOM.

**Fix:** Start at `0.70` for tight-VRAM setups, increase by 0.05 increments, test under load at each step.

### 3. Missing `NCCL_P2P_DISABLE=1` on Consumer GPUs

**Problem:** NCCL tries P2P transfers over PCIe, fails silently or crashes.

**Fix:** Always set `NCCL_P2P_DISABLE=1` for consumer GPUs without NVLink.

### 4. Missing `--served-model-name`

**Problem:** SGLang uses the local path as model name, which doesn't match what Swan Inference expects.

**Fix:** Always set `--served-model-name` to the HuggingFace repo ID (e.g., `Qwen/Qwen2.5-7B-Instruct`).

### 5. Small `--shm-size` with Tensor Parallelism

**Problem:** Default Docker shared memory (64MB) is too small for NCCL communication.

**Fix:** Use `--shm-size 4g` minimum for TP setups.

### 6. Not Pre-warming After Restart

**Problem:** First request after restart is slow because model weights aren't in GPU cache.

**Fix:** Send a dummy request after startup:
```bash
curl http://localhost:30000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"org/Model-Name","messages":[{"role":"user","content":"hi"}],"max_tokens":1}'
```

### 7. Using Too Many GPUs for TP

**Problem:** TP-8 over PCIe adds more communication overhead than compute benefit.

**Fix:** Use the minimum number of GPUs needed to fit the model. 2-4 GPUs is usually the sweet spot for PCIe. If you need more, consider a smaller or quantized model.

## Tuning Checklist

Before going live, verify each item:

- [ ] CUDA graphs enabled (no `--disable-cuda-graph` flag)
- [ ] `--served-model-name` matches HuggingFace repo ID
- [ ] `--mem-fraction-static` tested under load without OOM
- [ ] `--shm-size 4g+` for multi-GPU setups
- [ ] `NCCL_P2P_DISABLE=1` set for consumer GPUs (if using TP)
- [ ] `--disable-custom-all-reduce` set for PCIe TP setups
- [ ] Short, medium, and long requests tested locally
- [ ] Concurrent requests tested (5+ simultaneous)
- [ ] Provider metrics endpoint responding (`/inference/metrics`)
- [ ] No OOM errors in `docker logs` after 10+ minutes of operation
