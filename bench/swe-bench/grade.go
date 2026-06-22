package swebench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HarnessReport is the per-instance JSON the official SWE-bench Docker
// harness writes to:
//
//	logs/run_evaluation/<run_id>/<model>/<instance_id>/report.json
//
// The structure is wrapped in a single-key dict where the key is the
// instance_id, e.g.:
//
//	{
//	  "django__django-12345": {
//	    "patch_is_None": false,
//	    "patch_exists": true,
//	    "patch_successfully_applied": true,
//	    "resolved": true,
//	    "tests_status": {
//	      "FAIL_TO_PASS": {"success": [...], "failure": [...]},
//	      "PASS_TO_PASS": {"success": [...], "failure": [...]}
//	    }
//	  }
//	}
//
// We unwrap that into HarnessReport for ergonomic access. The fields
// here mirror what the upstream grading code populates — anything we
// don't currently consume is dropped on decode.
type HarnessReport struct {
	PatchIsNone             bool        `json:"patch_is_None"`
	PatchExists             bool        `json:"patch_exists"`
	PatchSuccessfullyApplied bool       `json:"patch_successfully_applied"`
	Resolved                bool        `json:"resolved"`
	TestsStatus             TestsStatus `json:"tests_status"`
}

// TestsStatus is the per-bucket pass/fail split the harness emits.
type TestsStatus struct {
	FailToPass TestBucket `json:"FAIL_TO_PASS"`
	PassToPass TestBucket `json:"PASS_TO_PASS"`
}

// TestBucket holds the success / failure test names for one bucket.
type TestBucket struct {
	Success []string `json:"success"`
	Failure []string `json:"failure"`
}

// GradeOptions configure a Docker-harness invocation. Defaults match
// the official run_evaluation defaults except for MaxWorkers, which we
// bump down to 2 — orbstack on a laptop chokes at 4+ concurrent docker
// builds.
type GradeOptions struct {
	PythonBin       string   // path to a Python with `swebench` installed; "" → "python3"
	DatasetName     string   // "" → "SWE-bench/SWE-bench_Lite"
	Split           string   // "" → "test" (use "dev" for the 23-instance smoke split)
	RunID           string   // unique tag for this eval run; required
	MaxWorkers      int      // 0 → 2
	TimeoutSeconds  int      // 0 → 1800 (matches harness default)
	InstanceIDs     []string // optional subset; nil → grade everything in predictions.json
	ReportDir       string   // where to look for harness logs ("." by default)
	ExtraArgs       []string // raw flags appended to the harness invocation; for power users / future-proofing
}

// Grade runs `python -m swebench.harness.run_evaluation` on
// predictionsPath and merges per-instance results back into the
// matching EvalResult slots.
//
// Returns a fresh slice — the input results are NOT mutated, so the
// caller can keep the pre-grading record alongside if it wants. This
// matters because the grader may legitimately FAIL to grade an
// instance (Docker build error, network flake) without that being a
// signal about the patch itself; preserving the pre-grading state
// keeps the failure attribution clean.
func Grade(
	ctx context.Context,
	predictionsPath string,
	results []*EvalResult,
	opts GradeOptions,
) ([]*EvalResult, error) {
	if opts.RunID == "" {
		return nil, errors.New("Grade: RunID is required")
	}
	pythonBin := opts.PythonBin
	if pythonBin == "" {
		pythonBin = "python3"
	}
	dataset := opts.DatasetName
	if dataset == "" {
		dataset = "SWE-bench/SWE-bench_Lite"
	}
	split := opts.Split
	if split == "" {
		split = "test"
	}
	maxWorkers := opts.MaxWorkers
	if maxWorkers == 0 {
		maxWorkers = 2
	}
	timeout := opts.TimeoutSeconds
	if timeout == 0 {
		timeout = 1800
	}
	reportDir := opts.ReportDir
	if reportDir == "" {
		reportDir = "."
	}
	// Resolve to absolute path. The swebench harness ignores --report_dir
	// (logs always land under cwd/logs/run_evaluation/...), so we set
	// cmd.Dir = reportDir to redirect them. Predictions path also needs
	// to be absolute since cwd is changing.
	absReportDir, err := filepath.Abs(reportDir)
	if err != nil {
		return nil, fmt.Errorf("resolve report dir: %w", err)
	}
	if err := os.MkdirAll(absReportDir, 0o755); err != nil {
		return nil, fmt.Errorf("create report dir: %w", err)
	}
	reportDir = absReportDir
	absPredictions, err := filepath.Abs(predictionsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve predictions path: %w", err)
	}

	args := []string{
		"-m", "swebench.harness.run_evaluation",
		"--predictions_path", absPredictions,
		"--dataset_name", dataset,
		"--split", split,
		"--run_id", opts.RunID,
		"--max_workers", fmt.Sprintf("%d", maxWorkers),
		"--timeout", fmt.Sprintf("%d", timeout),
		"--report_dir", reportDir,
	}
	if len(opts.InstanceIDs) > 0 {
		args = append(args, "--instance_ids")
		args = append(args, opts.InstanceIDs...)
	}
	args = append(args, opts.ExtraArgs...)

	cmd := exec.CommandContext(ctx, pythonBin, args...)
	cmd.Dir = reportDir // swebench ignores --report_dir; cwd controls log location
	cmd.Stdout = os.Stdout // surface harness progress to the operator
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// The harness exits non-zero when ANY instance errors during
		// build/run, even when others completed and wrote reports.
		// Don't bail — we still want to merge whatever reports landed.
		fmt.Fprintf(os.Stderr, "warning: harness exited with %v; merging available reports anyway\n", err)
	}

	// Merge per-instance reports back into a fresh slice.
	merged := make([]*EvalResult, len(results))
	for i, r := range results {
		clone := *r
		report, ok := loadReport(reportDir, opts.RunID, "spore", r.InstanceID)
		if !ok {
			// No report file — could be a build error, an empty
			// patch, or an instance the harness never reached. Mark
			// reason if we don't already have one.
			if clone.Reason == "" || strings.HasPrefix(clone.Reason, "patch-only") {
				clone.Reason = "no harness report — see harness logs"
			}
			merged[i] = &clone
			continue
		}
		applyReport(&clone, report)
		merged[i] = &clone
	}
	return merged, nil
}

// loadReport reads the per-instance report.json the harness writes to
// `<reportDir>/logs/run_evaluation/<run_id>/<model>/<instance_id>/report.json`.
//
// Returns (report, true) on success and (zero, false) when the file is
// missing — distinguishing "instance failed to grade" from
// "instance was graded as a failure".
func loadReport(reportDir, runID, model, instanceID string) (HarnessReport, bool) {
	path := filepath.Join(
		reportDir, "logs", "run_evaluation", runID, model, instanceID, "report.json",
	)
	raw, err := os.ReadFile(path)
	if err != nil {
		return HarnessReport{}, false
	}
	// Top level is `{instance_id: {...report...}}`, so unmarshal as a
	// map and pull the only entry.
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return HarnessReport{}, false
	}
	for _, body := range wrapper {
		var rep HarnessReport
		if err := json.Unmarshal(body, &rep); err != nil {
			return HarnessReport{}, false
		}
		return rep, true
	}
	return HarnessReport{}, false
}

// applyReport copies the relevant pieces of a HarnessReport into an
// EvalResult, overwriting whatever the patch-only mode populated.
func applyReport(res *EvalResult, rep HarnessReport) {
	res.Resolved = rep.Resolved
	res.FailToPass = TestSummary{
		Pass: rep.TestsStatus.FailToPass.Success,
		Fail: rep.TestsStatus.FailToPass.Failure,
	}
	res.PassToPass = TestSummary{
		Pass: rep.TestsStatus.PassToPass.Success,
		Fail: rep.TestsStatus.PassToPass.Failure,
	}
	switch {
	case res.Resolved:
		res.Reason = ""
	case rep.PatchIsNone:
		res.Reason = "agent produced empty patch (per harness)"
	case !rep.PatchSuccessfullyApplied:
		res.Reason = "patch did not apply cleanly"
	case len(rep.TestsStatus.FailToPass.Failure) > 0:
		res.Reason = fmt.Sprintf("%d FAIL_TO_PASS test(s) still failing",
			len(rep.TestsStatus.FailToPass.Failure))
	case len(rep.TestsStatus.PassToPass.Failure) > 0:
		res.Reason = fmt.Sprintf("%d PASS_TO_PASS test(s) regressed",
			len(rep.TestsStatus.PassToPass.Failure))
	default:
		res.Reason = "unresolved (no specific reason from harness)"
	}
}
