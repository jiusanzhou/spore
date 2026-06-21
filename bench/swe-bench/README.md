# SWE-bench Lite Harness

A self-contained Go harness for running the [SWE-bench Lite](https://www.swebench.com/lite.html)
benchmark against spore.

## Status

**Stage 1 (this PR)**: pipeline scaffolding + patch generation.

- ✅ Dataset loader (JSONL converted from HuggingFace parquet)
- ✅ Per-instance repo prep (clone + checkout `base_commit`)
- ✅ Single-shot runtime invocation via spore's ACP runtime (claude-code)
- ✅ Worktree-diff capture as the candidate patch
- ✅ Per-instance JSON report (`results.json`)
- ⏳ Test execution (FAIL_TO_PASS / PASS_TO_PASS verification) — landing in a follow-up alongside the per-repo Python env scripts

The agent/swarm layer is deliberately bypassed here. SWE-bench is a
single-shot patch task; multi-agent coordination, memory, evolution,
and the stigmergic market would only complicate failure attribution.
The runtime alone is the minimum viable surface to measure raw fix
capability.

## Layout

```
bench/swe-bench/
  dataset.go      # Instance struct + JSONL loader + FAIL_TO_PASS / PASS_TO_PASS accessors
  repo.go         # `git clone` + `git checkout base_commit` + reset-on-rerun
  runner.go       # prompt formatting + runtime invocation
  evaluate.go     # `git diff` capture + (pluggable) test runner + resolution verdict
  *_test.go       # parser + classifier unit tests

cmd/swe-bench-runner/
  main.go         # CLI: --dataset, --work, --out, --ids, --limit, --timeout
```

## Quickstart

### 1. Prepare the dataset

The harness reads JSONL — one `Instance` per line. Convert the HF
parquet once:

```bash
mkdir -p /tmp/swe-bench-data && cd /tmp/swe-bench-data

# dev split (23 instances) — good for smoke tests
curl -fsSL -o lite-dev.parquet \
  https://huggingface.co/datasets/princeton-nlp/SWE-bench_Lite/resolve/main/data/dev-00000-of-00001.parquet

# test split (300 instances) — official leaderboard
curl -fsSL -o lite-test.parquet \
  https://huggingface.co/datasets/princeton-nlp/SWE-bench_Lite/resolve/main/data/test-00000-of-00001.parquet

uv run --quiet --with pyarrow python3 - <<'PY'
import json, pyarrow.parquet as pq
for split in ("dev", "test"):
    rows = pq.read_table(f"lite-{split}.parquet").to_pylist()
    with open(f"lite-{split}.jsonl", "w") as f:
        for r in rows:
            f.write(json.dumps(r) + "\n")
    print(f"wrote {len(rows)} rows to lite-{split}.jsonl")
PY
```

### 2. Build the runner

```bash
go build -o bin/swe-bench-runner ./cmd/swe-bench-runner
```

### 3. Run

```bash
./bin/swe-bench-runner \
  --dataset /tmp/swe-bench-data/lite-dev.jsonl \
  --work    /tmp/swe-bench-work \
  --out     ./results.json \
  --ids     sqlfluff__sqlfluff-1625 \
  --timeout 15m
```

Useful flags:
- `--ids a,b,c` — comma-separated subset (omit to run everything)
- `--limit N` — cap to first N instances after filtering
- `--timeout` — per-instance wall-clock limit (default 20m)

### 4. Read results

`results.json` is a JSON array of `EvalResult` (one per instance):

```json
[
  {
    "instance_id": "sqlfluff__sqlfluff-1625",
    "resolved": false,
    "reason": "patch-only mode (test runner not configured)",
    "patch": "diff --git a/src/sqlfluff/rules/L031.py ...",
    "duration": 169000000000
  }
]
```

When the test runner is wired up (next PR), `resolved` will be `true`
exactly when every `FAIL_TO_PASS` flips to pass AND every
`PASS_TO_PASS` stays passing.

## Requirements

- `git` on PATH
- `claude-agent-acp` on PATH — the ACP runtime spore drives for code edits.
  See [Anthropic's claude-agent-sdk-js](https://github.com/anthropics/claude-agent-sdk-js)
  or `npm i -g @anthropic-ai/claude-agent-acp`.
- Disk space — each per-instance clone is the full repo at the requested
  commit (sqlfluff ≈ 50 MB; django much larger).

## Roadmap

1. **Test runner plugin** — pluggable `RunTestsFunc` already in the
   evaluator; needs the per-repo venv setup scripts and a small Python
   shim that translates pytest exit codes into the pass/fail split.
2. **Docker mode** — the official SWE-bench harness uses per-repo
   Docker images. Wrap that as an alternate `RunTestsFunc`.
3. **Agent-mode comparison** — once test execution is solid, add a flag
   to route through the spore agent (plan-execute-verify loop) and
   measure the lift vs raw runtime.
4. **Parallel execution** — instances are independent; running 4–8 at
   a time with bounded concurrency cuts wall clock dramatically.
