# SWE-bench Lite Harness

A self-contained Go harness for running the [SWE-bench Lite](https://www.swebench.com/lite.html)
benchmark against spore.

## Baseline result

**12 / 23 resolved (52.2% raw, 66.7% adjusted)** on SWE-bench Lite dev split (2026-06-22)

```
Instances submitted:  23
Resolved:             12   ← official harness verdict
Unresolved:           10   ← includes 5 upstream-broken pvlib instances
Empty patches:         1
Errors:                0
```

**Adjusted score: 12 / 18 = 66.7%** — excluding 5 broken pvlib instances
where the conftest.py imports fail under numpy 2.0 (`np.Inf` was removed
in numpy 2.0; the prebuilt eval images ship numpy ≥2.0 but pvlib 0.7-0.9
`conftest.py` still uses `np.Inf`). Confirmed via gold-patch sanity check:
running the official answer on `pvlib__pvlib-python-1606` produces the
same `PASS_TO_PASS: 0/10 success` failure, so this is a SWE-bench
upstream image incompatibility, not an agent miss.

By repository (raw):

| Repo | Resolved |
|---|---|
| marshmallow-code | 2/2 (100%) |
| pylint-dev/astroid | 4/5 (80%) |
| sqlfluff | 3/5 (60%) — incl. 1 empty patch |
| pydicom | 3/5 (60%) |
| pvlib-python | 0/5 — **broken upstream**, conftest.py / numpy 2.0 |
| pyvista | 0/1 (0%) |

Per-instance breakdown lives in [`baseline-lite-dev.json`](./baseline-lite-dev.json)
(includes `broken_instance: true` marker on the affected instances).

Configuration:
- Agent: spore with the Claude Code ACP runtime + plan-execute-verify loop
- Patch generation: single-shot per instance, 12-minute wall clock
- Grading: official `swebench.harness.run_evaluation` in Docker, `--max_workers 2`, 30-minute timeout
- Dataset: `SWE-bench/SWE-bench_Lite`, dev split, 23 instances

## How it works

Two-stage pipeline driven by `cmd/swe-bench-runner`:

```
Stage 1 — patch generation (in-Go)
  per instance:
    git clone <repo> ; git checkout <base_commit>
    spore ACP runtime executes:
      "<problem_statement> — apply your fix to the working tree"
    git diff  →  candidate patch
    write to   results.json[i].patch

Stage 2 — official grading (delegated to swebench Python harness)
  predictions.json  ← export Stage 1 patches in official schema
  python -m swebench.harness.run_evaluation \
      --predictions_path predictions.json \
      --dataset_name SWE-bench/SWE-bench_Lite \
      --split dev --run_id <id>
  →  per-instance Docker container, applies patch, runs FAIL_TO_PASS + PASS_TO_PASS
  →  logs/run_evaluation/<run_id>/spore/<instance>/report.json

Merge: results.json[i].resolved + patch_successfully_applied + tests_status
       are populated from each report.

CLI summary: === resolved: X/Y (Z%) ===
```

The agent/swarm layer is deliberately bypassed. SWE-bench is a single-shot
patch task; multi-agent coordination, memory, evolution, and the
stigmergic market would only complicate failure attribution. The runtime
+ plan loop alone is the minimum viable surface to measure raw fix
capability.

## Layout

```
bench/swe-bench/
  dataset.go          # Instance struct + JSONL loader + FAIL_TO_PASS / PASS_TO_PASS accessors
  repo.go             # `git clone` + `git checkout base_commit` + reset-on-rerun
  runner.go           # prompt formatting + ACP runtime invocation
  evaluate.go         # `git diff` capture + EvalResult shape
  predictions.go      # EvalResult[] → official predictions.json schema
  grade.go            # swebench harness wrapper + report loader + merger
  baseline-lite-dev.json  # frozen 12/23 baseline (this PR)
  *_test.go           # parser + classifier + predictions + grader unit tests

cmd/swe-bench-runner/
  main.go             # CLI: Stage 1 + Stage 2 flags
```

## Quickstart

### 1. Prepare the dataset

The harness reads JSONL — one `Instance` per line. Convert the HF parquet
once:

```bash
mkdir -p /tmp/swe-bench-data && cd /tmp/swe-bench-data

# dev split (23 instances) — used for the baseline above
curl -fsSL -o lite-dev.parquet \
  https://huggingface.co/datasets/SWE-bench/SWE-bench_Lite/resolve/main/data/dev-00000-of-00001.parquet

# test split (300 instances) — official leaderboard
curl -fsSL -o lite-test.parquet \
  https://huggingface.co/datasets/SWE-bench/SWE-bench_Lite/resolve/main/data/test-00000-of-00001.parquet

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

### 2. Build

```bash
go build -o bin/swe-bench-runner ./cmd/swe-bench-runner
```

### 3. Stage 1 only (just generate patches)

```bash
./bin/swe-bench-runner \
  --dataset /tmp/swe-bench-data/lite-dev.jsonl \
  --work    /tmp/swe-bench-work \
  --out     ./results.json \
  --ids     sqlfluff__sqlfluff-1625 \
  --timeout 15m
```

### 4. Stage 1 + Stage 2 (generate + grade)

You need the swebench Python package and Docker:

```bash
# one-time setup
python3 -m venv /tmp/swebench-venv
/tmp/swebench-venv/bin/pip install swebench
# Docker daemon must be running (Docker Desktop, OrbStack, …)
```

Then add `--evaluate` and the grading flags:

```bash
./bin/swe-bench-runner \
  --dataset /tmp/swe-bench-data/lite-dev.jsonl \
  --work    /tmp/swe-bench-work \
  --out     ./results.json \
  --timeout 12m \
  --evaluate \
  --python  /tmp/swebench-venv/bin/python \
  --split   dev \
  --max-workers 2 \
  --grader-timeout 1800 \
  --report-dir /tmp/swe-bench-grade
```

Output ends with:

```
=== resolved: 12/23 (52.2%) ===
```

`results.json` then carries `resolved`, `patch_successfully_applied`, and
the FAIL_TO_PASS / PASS_TO_PASS counts merged in from each instance's
`report.json`.

## CLI flags

Stage 1:
- `--dataset PATH` — JSONL dataset
- `--work DIR` — per-instance worktrees (clone + patches)
- `--out PATH` — results.json
- `--ids a,b,c` — comma-separated instance subset
- `--limit N` — first N instances after filtering
- `--timeout` — per-instance wall clock (default 20m)

Stage 2 (with `--evaluate`):
- `--python PATH` — interpreter with `swebench` installed
- `--split {dev,test}` — must match the predictions
- `--run-id ID` — defaults to `spore-<unix>`
- `--max-workers N` — concurrent Docker containers (default 2)
- `--grader-timeout SECONDS` — per-instance harness timeout (default 1800)
- `--report-dir DIR` — where harness logs land
  (the swebench harness silently ignores its own `--report_dir`; we set
  `cmd.Dir` to redirect logs into the dir you ask for)
- `--predictions PATH` — override predictions output path
- `--model-tag NAME` — defaults to `spore`
- `--dataset-name NAME` — defaults to `SWE-bench/SWE-bench_Lite`

## Requirements

- `git` on PATH
- `claude-agent-acp` on PATH — the ACP runtime spore drives for code edits.
  See [Anthropic's claude-agent-sdk-js](https://github.com/anthropics/claude-agent-sdk-js)
  or `npm i -g @anthropic-ai/claude-agent-acp`.
- Python 3 venv with `swebench` (Stage 2 only)
- Docker daemon (Stage 2 only) — each instance pulls a ~1–2 GB prebuilt
  eval image. **Budget ≥ 50 GB free disk for the full 23-instance dev
  split** (we hit OOM at 6 GB).
- Disk for clones: per-instance the full repo at `base_commit`
  (sqlfluff ≈ 50 MB; django much larger).

## Pitfalls we hit

- **Docker daemon hangs at 99% disk** — symptom is `error creating
  temporary lease: write …meta.db: no space left on device` and pulled
  images all 404. Solution: clean caches + `docker image prune -af`,
  optionally `orb stop && orb start`.
- **`swebench --report_dir` is silently ignored** — the harness writes
  to `<cwd>/logs/run_evaluation/...` regardless. Our wrapper sets
  `cmd.Dir` to work around this.
- **swebench harness exits non-zero** when *any* instance errors, even
  with successful reports landed for others. Our wrapper degrades:
  merges whatever reports exist and surfaces the rest as
  "no harness report".
- **LLM nondeterminism** — the same instance can be `resolved=true` on
  one run (string-edit patch) and `resolved=false` on another (logic
  patch that breaks an oracle test). Variance is real; baseline above is
  one full pass.

## Next

1. **Run the test split** (300 instances) for an official leaderboard
   data point. Estimated wall clock ≈ 1 day at `--max-workers 4`.
2. **Report pvlib brokenness upstream** to SWE-bench — confirmed via
   gold-patch sanity check; affects 5/23 dev instances.
3. **Plan loop tuning** — currently single-attempt. Adding a
   verifier-driven retry pass should help the marginal failures.
4. **Grade-only mode** — `--grade-only --predictions PATH` to re-grade
   without regenerating patches (useful when Stage 2 fails for
   environmental reasons).
