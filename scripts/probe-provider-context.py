#!/usr/bin/env python3
"""Probe a Swan Inference provider's real context window.

Pins requests to one provider via X-Swan-Target-Provider (the benchmark
header) and detects silent truncation, which Ollama backends do instead of
rejecting over-length prompts like vLLM:

  1. usage.prompt_tokens < tokens sent  -> backend truncated the prompt
  2. a secret placed at the START of the prompt can't be recalled -> the
     beginning was dropped (Ollama truncates oldest-first)

Usage:
  SWAN_API_KEY=sk-... ./probe-provider-context.py \
      --provider 53e045d4-5947-4f8d-adb1-55f7409c4cc8 \
      --model TheDrummer/Cydonia-24B-v4.3 \
      --sizes 4000,8000,16000,32000,64000

Cost: roughly $0.3 per million input tokens; the default ladder is ~124k
tokens total, i.e. ~$0.04.
"""
import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request

SECRET = "SWAN-7741"


def build_prompt(target_tokens):
    # ~4 chars/token heuristic; numbered filler lines resist compression and
    # keep the tokenizer honest.
    header = f"The secret code is {SECRET}. Remember it.\n"
    line = "Log entry %d: routine telemetry, all systems nominal.\n"
    body = []
    approx = len(header) // 4
    i = 0
    while approx < target_tokens - 60:
        entry = line % i
        body.append(entry)
        approx += len(entry) // 4
        i += 1
    question = ("\nWhat is the secret code stated at the very beginning of "
                "this message? Reply with the code only.")
    return header + "".join(body) + question


def probe(base, key, provider, model, target_tokens, timeout):
    prompt = build_prompt(target_tokens)
    body = json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": 30,
        "temperature": 0,
    }).encode()
    req = urllib.request.Request(
        base + "/v1/chat/completions", data=body,
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer " + key,
            "X-Swan-Target-Provider": provider,
            # Cloudflare in front of the gateway blocks urllib's default UA (1010)
            "User-Agent": "swan-context-probe/1.0 (curl-compatible)",
        })
    t0 = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            data = json.load(r)
            served_by = r.headers.get("X-Swan-Provider-ID", "?")
    except urllib.error.HTTPError as e:
        detail = e.read().decode(errors="replace")[:300]
        return {"sent_target": target_tokens, "error": f"HTTP {e.code}: {detail}"}
    except Exception as e:  # timeouts etc.
        return {"sent_target": target_tokens, "error": str(e)}

    usage = data.get("usage", {})
    answer = (data.get("choices") or [{}])[0].get("message", {}).get("content", "")
    return {
        "sent_target": target_tokens,
        "prompt_tokens_reported": usage.get("prompt_tokens"),
        "recalled_secret": SECRET in answer,
        "answer_snippet": answer.strip()[:60],
        "served_by": served_by,
        "latency_s": round(time.time() - t0, 1),
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--provider", required=True)
    ap.add_argument("--model", required=True)
    ap.add_argument("--base", default="https://inference.swanchain.io/api")
    ap.add_argument("--sizes", default="4000,8000,16000,32000,64000")
    ap.add_argument("--timeout", type=int, default=300)
    args = ap.parse_args()

    key = os.environ.get("SWAN_API_KEY", "")
    if not key:
        sys.exit("Set SWAN_API_KEY to a client API key (sk-...)")

    est_window = None
    for size in [int(s) for s in args.sizes.split(",")]:
        r = probe(args.base, key, args.provider, args.model, size, args.timeout)
        print(json.dumps(r))
        if "error" in r:
            # vLLM-style backends 400 with "maximum context length is N";
            # that N is the answer. Stop climbing either way.
            break
        sent = r["prompt_tokens_reported"]
        # Reported far below target => backend truncated (Ollama-style).
        truncated = (sent is not None and sent < size * 0.8) or not r["recalled_secret"]
        if truncated:
            print(f"--> truncation detected at ~{size} tokens; "
                  f"real window is between the previous size and {size}")
            break
        est_window = size
    if est_window:
        print(f"--> provider handled {est_window} tokens intact "
              f"(window >= {est_window})")


if __name__ == "__main__":
    main()
