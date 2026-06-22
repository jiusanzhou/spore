// swe-bench-runner is the CLI entry point for spore's SWE-bench Lite
// harness. It drives the pipeline end-to-end:
//
//	1. Load the dataset (JSONL produced from the HuggingFace parquet)
//	2. For each selected instance:
//	     a. Prepare a clean clone at the base commit
//	     b. Ask the runtime to fix the bug in-place
//	     c. Capture the diff and score the patch
//	3. Write a results.json file with per-instance verdicts and a
//	   leaderboard-style aggregate.
//
// Usage:
//
//	swe-bench-runner \
//	  --dataset /tmp/swe-bench-data/lite-dev.jsonl \
//	  --work    /tmp/swe-bench-work \
//	  --out     ./results.json \
//	  --ids     sqlfluff__sqlfluff-1625 \      # optional: subset by ID
//	  --limit   3 \                             # optional: cap instance count
//	  --timeout 20m
//
// Test execution is intentionally NOT wired up in this first cut.
// SWE-bench's official grader spins per-repo Python environments (often
// via Docker) and is the bulk of the project's complexity — orthogonal
// to "can spore produce a sensible patch?". The harness records
// candidate patches now; the test-runner plug-in lands in a follow-up
// PR alongside the env-setup scripts.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	swebench "go.zoe.im/spore/bench/swe-bench"
	"go.zoe.im/spore/internal/runtime"
)

func main() {
	dataset := flag.String("dataset", "", "path to SWE-bench Lite JSONL")
	workRoot := flag.String("work", "/tmp/swe-bench-work", "directory for per-instance clones")
	outPath := flag.String("out", "results.json", "where to write per-instance results")
	idsArg := flag.String("ids", "", "comma-separated instance_id list (empty = all)")
	limit := flag.Int("limit", 0, "max instances to run (0 = no cap)")
	timeout := flag.Duration("timeout", 20*time.Minute, "per-instance timeout")

	// Stage-2 grading flags. Default off so the harness stays usable
	// in dev / patch-only mode without Docker.
	evaluate := flag.Bool("evaluate", false, "run SWE-bench Docker harness on captured patches")
	gradeOnly := flag.Bool("grade-only", false, "skip Stage 1 — load patches from --out (an existing results.json) and run only the harness grading. Implies --evaluate. Useful when Stage 2 failed for environmental reasons (Docker disk full, registry hiccup) and you want to re-grade without burning another hour on LLM calls.")
	pythonBin := flag.String("python", "python3", "Python interpreter with the `swebench` package installed (for --evaluate)")
	datasetName := flag.String("dataset-name", "SWE-bench/SWE-bench_Lite", "HF dataset name passed to the harness")
	split := flag.String("split", "dev", "dataset split: dev (23 instances) or test (300)")
	runID := flag.String("run-id", "", "harness run id (default: spore-<timestamp>)")
	maxWorkers := flag.Int("max-workers", 2, "harness max_workers — 2 is a safe default for orbstack on a laptop")
	graderTimeout := flag.Int("grader-timeout", 1800, "harness per-instance timeout (seconds)")
	reportDir := flag.String("report-dir", ".", "directory the harness writes logs/reports under")
	predictionsPath := flag.String("predictions", "", "where to write predictions.json (default: <out>.predictions.json)")
	modelTag := flag.String("model-tag", "spore", "model_name_or_path written into predictions.json")

	flag.Parse()

	// --grade-only implies --evaluate. We bail early on conflicting
	// configs so users don't burn 3 hours discovering the typo.
	if *gradeOnly {
		*evaluate = true
	}

	if *dataset == "" && !*gradeOnly {
		fmt.Fprintln(os.Stderr, "usage: swe-bench-runner --dataset <jsonl> [flags]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	// Cancel cleanly on Ctrl-C so a partial run still writes results.
	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// --grade-only: skip Stage 1 entirely. Load patches from the
	// existing results.json and jump straight to harness grading.
	if *gradeOnly {
		results, err := swebench.ReadResults(*outPath)
		if err != nil {
			log.Fatalf("--grade-only: read %s: %v", *outPath, err)
		}
		if len(results) == 0 {
			log.Fatalf("--grade-only: %s contains no results", *outPath)
		}
		// Optional subset filter — same --ids syntax as Stage 1.
		var instanceIDs []string
		if *idsArg != "" {
			for _, id := range strings.Split(*idsArg, ",") {
				if id = strings.TrimSpace(id); id != "" {
					instanceIDs = append(instanceIDs, id)
				}
			}
			log.Printf("--grade-only: subsetting to %d instance(s) via --ids", len(instanceIDs))
		}
		log.Printf("--grade-only: loaded %d result(s) from %s — skipping Stage 1", len(results), *outPath)
		results, resolved := gradeWithHarness(rootCtx, results, gradeArgs{
			outPath:         *outPath,
			predictionsPath: *predictionsPath,
			modelTag:        *modelTag,
			pythonBin:       *pythonBin,
			datasetName:     *datasetName,
			split:           *split,
			runID:           *runID,
			maxWorkers:      *maxWorkers,
			graderTimeout:   *graderTimeout,
			reportDir:       *reportDir,
			instanceIDs:     instanceIDs,
		})
		total := len(results)
		if total > 0 {
			pct := float64(resolved) / float64(total) * 100
			log.Printf("=== resolved: %d/%d (%.1f%%) ===", resolved, total, pct)
		}
		return
	}

	instances, err := swebench.LoadInstances(*dataset)
	if err != nil {
		log.Fatalf("load dataset: %v", err)
	}
	log.Printf("loaded %d instances from %s", len(instances), *dataset)

	if *idsArg != "" {
		ids := strings.Split(*idsArg, ",")
		for i := range ids {
			ids[i] = strings.TrimSpace(ids[i])
		}
		instances = swebench.FilterByIDs(instances, ids)
		log.Printf("filtered to %d by --ids", len(instances))
	}
	if *limit > 0 && len(instances) > *limit {
		instances = instances[:*limit]
		log.Printf("capped to first %d by --limit", *limit)
	}
	if len(instances) == 0 {
		log.Fatal("no instances to run")
	}

	// Use the ACP runtime directly. Bypassing the agent / swarm layer
	// keeps the benchmark a clean signal on the runtime's raw capability
	// and avoids the configuration surface (agent name, manifest,
	// evolution, etc.) entirely.
	rt := runtime.NewACPRuntime()
	if err := rt.Healthy(rootCtx); err != nil {
		log.Fatalf("ACP runtime unhealthy — is claude-agent-acp installed and on PATH? %v", err)
	}
	defer rt.Close()
	log.Printf("runtime: %s", rt.Info().Name)

	if err := os.MkdirAll(*workRoot, 0o755); err != nil {
		log.Fatalf("mkdir work: %v", err)
	}

	runner := swebench.NewRunner(rt, *timeout)
	// First-PR scaffolding: no test runner wired up yet. See evaluate.go
	// docstring for why test execution is split into a follow-up.
	evaluator := swebench.NewEvaluator(nil)

	var results []*swebench.EvalResult
	resolved := 0
	for i, inst := range instances {
		if rootCtx.Err() != nil {
			log.Printf("interrupted after %d instance(s)", i)
			break
		}
		log.Printf("[%d/%d] %s (%s @ %s)",
			i+1, len(instances), inst.InstanceID, inst.Repo, inst.BaseCommit[:12])

		repoDir, err := swebench.PrepareRepo(rootCtx, *workRoot, inst)
		if err != nil {
			log.Printf("  prepare repo: %v", err)
			results = append(results, &swebench.EvalResult{
				InstanceID: inst.InstanceID,
				Error:      fmt.Sprintf("prepare repo: %v", err),
			})
			continue
		}

		solveLog, solveErr := runner.Solve(rootCtx, inst, repoDir)
		if solveErr != nil {
			log.Printf("  solve: %v (evaluating worktree anyway)", solveErr)
		}

		res := evaluator.Evaluate(rootCtx, inst, repoDir)
		res.SolveLog = truncate(solveLog, 4000)
		results = append(results, res)

		if res.Resolved {
			resolved++
			log.Printf("  ✅ resolved (%s)", res.Duration.Round(time.Second))
		} else if res.Error != "" {
			log.Printf("  ⚠️  error: %s", res.Error)
		} else {
			diffLines := strings.Count(res.Patch, "\n")
			log.Printf("  ⏸️  unresolved: %s (patch: %d lines, %s)",
				res.Reason, diffLines, res.Duration.Round(time.Second))
		}
	}

	if err := swebench.WriteResults(*outPath, results); err != nil {
		log.Fatalf("write results: %v", err)
	}
	abs, _ := filepath.Abs(*outPath)
	log.Printf("wrote %d result(s) to %s", len(results), abs)

	// Stage 2: hand the captured patches to the official SWE-bench
	// Docker harness for real FAIL_TO_PASS / PASS_TO_PASS grading.
	if *evaluate {
		results, resolved = gradeWithHarness(rootCtx, results, gradeArgs{
			outPath:         *outPath,
			predictionsPath: *predictionsPath,
			modelTag:        *modelTag,
			pythonBin:       *pythonBin,
			datasetName:     *datasetName,
			split:           *split,
			runID:           *runID,
			maxWorkers:      *maxWorkers,
			graderTimeout:   *graderTimeout,
			reportDir:       *reportDir,
		})
	}

	total := len(results)
	if total > 0 {
		pct := float64(resolved) / float64(total) * 100
		log.Printf("=== resolved: %d/%d (%.1f%%) ===", resolved, total, pct)
	}
}

// gradeArgs bundles the --evaluate flags so gradeWithHarness has a
// clean signature instead of 10 positional parameters.
type gradeArgs struct {
	outPath         string
	predictionsPath string
	modelTag        string
	pythonBin       string
	datasetName     string
	split           string
	runID           string
	maxWorkers      int
	graderTimeout   int
	reportDir       string
	instanceIDs     []string // optional subset (grade-only mode)
}

// gradeWithHarness exports the patches as predictions.json, runs the
// official SWE-bench Docker harness, and merges the per-instance
// reports back into the results slice. Returns the merged slice and
// the new resolved count. The original results.json on disk is
// rewritten so consumers always see the most authoritative verdict.
func gradeWithHarness(ctx context.Context, results []*swebench.EvalResult, args gradeArgs) ([]*swebench.EvalResult, int) {
	predPath := args.predictionsPath
	if predPath == "" {
		predPath = args.outPath + ".predictions.json"
	}
	if err := swebench.WritePredictions(predPath, args.modelTag, results); err != nil {
		log.Printf("warn: write predictions: %v (skipping grading)", err)
		return results, countResolved(results)
	}
	log.Printf("wrote predictions to %s", predPath)

	runID := args.runID
	if runID == "" {
		runID = fmt.Sprintf("spore-%d", time.Now().Unix())
	}
	log.Printf("running SWE-bench harness (run_id=%s, split=%s, workers=%d)",
		runID, args.split, args.maxWorkers)

	merged, err := swebench.Grade(ctx, predPath, results, swebench.GradeOptions{
		PythonBin:      args.pythonBin,
		DatasetName:    args.datasetName,
		Split:          args.split,
		RunID:          runID,
		MaxWorkers:     args.maxWorkers,
		TimeoutSeconds: args.graderTimeout,
		ReportDir:      args.reportDir,
		InstanceIDs:    args.instanceIDs,
	})
	if err != nil {
		log.Printf("warn: grade: %v (keeping pre-grading results)", err)
		return results, countResolved(results)
	}

	// Rewrite results.json with the graded verdicts so a fresh reader
	// sees the same numbers we're about to log.
	if err := swebench.WriteResults(args.outPath, merged); err != nil {
		log.Printf("warn: rewrite results after grading: %v", err)
	} else {
		log.Printf("rewrote graded results to %s", args.outPath)
	}
	return merged, countResolved(merged)
}

func countResolved(results []*swebench.EvalResult) int {
	n := 0
	for _, r := range results {
		if r.Resolved {
			n++
		}
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
