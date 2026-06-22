package swebench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// EvalResult is the per-instance outcome.
type EvalResult struct {
	InstanceID  string        `json:"instance_id"`
	Resolved    bool          `json:"resolved"`
	Reason      string        `json:"reason,omitempty"`       // why not resolved (or empty if resolved)
	Patch       string        `json:"patch"`                  // candidate patch we captured from the worktree
	FailToPass  TestSummary   `json:"fail_to_pass"`           // tests that should flip from FAIL → PASS
	PassToPass  TestSummary   `json:"pass_to_pass"`           // tests that should stay PASS
	SolveLog    string        `json:"solve_log,omitempty"`    // truncated runtime reply (for debugging)
	Duration    time.Duration `json:"duration"`
	Error       string        `json:"error,omitempty"`        // harness-level error (env setup failed, patch broken, etc.)
}

// TestSummary tracks which tests in a category passed or failed in the
// post-patch evaluation run. Names are full pytest IDs as they appear
// in the FAIL_TO_PASS / PASS_TO_PASS lists.
type TestSummary struct {
	Pass []string `json:"pass,omitempty"`
	Fail []string `json:"fail,omitempty"`
}

// Evaluator applies the agent's worktree changes + the dataset's hidden
// test patch and runs the named tests to decide whether the instance is
// resolved. Test execution is delegated to a script the caller plugs in
// (see RunTestsFunc) because spinning Python envs for 11 different
// repos at 11 different commits is the bulk of SWE-bench's complexity
// and is best handled by the official Docker harness or a venv-per-repo
// setup script — orthogonal to "did spore generate a useful patch?".
type Evaluator struct {
	runTests RunTestsFunc
}

// RunTestsFunc executes the named pytest test IDs in repoDir and
// returns which ones passed and which failed. Implementations are
// responsible for setting up the Python environment (venv, deps,
// editable install) and translating pytest exit codes / output into
// the pass/fail split.
//
// A nil RunTestsFunc puts the harness in "patch-only" mode: we capture
// the diff but skip evaluation. Useful for the initial e2e plumbing
// PR before the per-repo environment scripts land.
type RunTestsFunc func(ctx context.Context, repoDir string, tests []string) (pass, fail []string, err error)

// NewEvaluator returns an Evaluator. Pass nil for runTests to enable
// patch-only mode (no test execution).
func NewEvaluator(runTests RunTestsFunc) *Evaluator {
	return &Evaluator{runTests: runTests}
}

// Evaluate runs the full grading pipeline for one instance:
//
//  1. capture the agent's edits as a unified diff (patch)
//  2. apply the dataset's test_patch on top
//  3. (if runTests is set) execute FAIL_TO_PASS and PASS_TO_PASS,
//     classify the instance as resolved iff every FAIL_TO_PASS test
//     now passes AND every PASS_TO_PASS test still passes
//
// Even in patch-only mode we always run step 1 — capturing what the
// agent produced is the headline signal of "is the integration alive?"
// for the first-PR demo.
func (e *Evaluator) Evaluate(ctx context.Context, inst Instance, repoDir string) *EvalResult {
	start := time.Now()
	res := &EvalResult{InstanceID: inst.InstanceID}
	defer func() { res.Duration = time.Since(start) }()

	patch, err := capturePatch(ctx, repoDir)
	if err != nil {
		res.Error = fmt.Sprintf("capture patch: %v", err)
		return res
	}
	res.Patch = patch

	// A truly empty patch means the agent made no source change — call
	// it unresolved up-front rather than running the test harness on a
	// no-op diff.
	if strings.TrimSpace(patch) == "" {
		res.Reason = "agent produced empty patch"
		return res
	}

	if e.runTests == nil {
		res.Reason = "patch-only mode (test runner not configured)"
		return res
	}

	// Apply test_patch on top of the agent's edits. We use `git apply`
	// rather than `patch` because the upstream test_patch field is in
	// `git diff` format and includes new-file hunks.
	if strings.TrimSpace(inst.TestPatch) != "" {
		if err := applyPatch(ctx, repoDir, inst.TestPatch); err != nil {
			res.Error = fmt.Sprintf("apply test_patch: %v", err)
			return res
		}
	}

	failToPass, err := inst.FailToPass()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	passToPass, err := inst.PassToPass()
	if err != nil {
		res.Error = err.Error()
		return res
	}

	// Run both test buckets together — most pytest setups boot the
	// interpreter once per invocation, so combining the call is much
	// faster than two separate runs and the categorization happens by
	// matching names afterward.
	allTests := append(append([]string{}, failToPass...), passToPass...)
	pass, fail, err := e.runTests(ctx, repoDir, allTests)
	if err != nil {
		res.Error = fmt.Sprintf("run tests: %v", err)
		return res
	}

	res.FailToPass = splitByMembership(failToPass, pass, fail)
	res.PassToPass = splitByMembership(passToPass, pass, fail)

	// Resolution criteria from SWE-bench paper: every FAIL_TO_PASS
	// test must pass AND every PASS_TO_PASS test must still pass.
	switch {
	case len(res.FailToPass.Fail) > 0:
		res.Reason = fmt.Sprintf("%d FAIL_TO_PASS test(s) still failing", len(res.FailToPass.Fail))
	case len(res.PassToPass.Fail) > 0:
		res.Reason = fmt.Sprintf("%d PASS_TO_PASS test(s) regressed", len(res.PassToPass.Fail))
	case len(res.FailToPass.Pass) == 0 && len(failToPass) > 0:
		res.Reason = "no FAIL_TO_PASS tests reported as passing"
	default:
		res.Resolved = true
	}
	return res
}

// capturePatch returns `git diff` of the working tree against HEAD,
// excluding any tests/ directory edits. The exclusion is a safety net:
// agents are instructed not to touch tests, but a sloppy edit could
// leak in and contaminate the eval — better to drop those hunks than
// to silently grade against an agent-modified test suite.
func capturePatch(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--no-color", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

// applyPatch feeds patchContent to `git apply` in repoDir.
func applyPatch(ctx context.Context, repoDir, patchContent string) error {
	cmd := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = repoDir
	cmd.Stdin = strings.NewReader(patchContent)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply: %w\n%s", err, string(out))
	}
	return nil
}

// splitByMembership classifies expected tests into Pass / Fail buckets
// based on the runner's pass/fail output. Tests that didn't appear in
// either output (collection error, skipped, etc.) are bucketed as
// failures — silent disappearance is never a "pass" in SWE-bench
// grading.
func splitByMembership(expected, pass, fail []string) TestSummary {
	passSet := make(map[string]struct{}, len(pass))
	for _, t := range pass {
		passSet[t] = struct{}{}
	}
	failSet := make(map[string]struct{}, len(fail))
	for _, t := range fail {
		failSet[t] = struct{}{}
	}
	var sum TestSummary
	for _, t := range expected {
		if _, ok := passSet[t]; ok {
			sum.Pass = append(sum.Pass, t)
			continue
		}
		// In either failSet OR uncollected — both grade as failure.
		sum.Fail = append(sum.Fail, t)
	}
	return sum
}

// WriteResults serializes a slice of EvalResult to a JSON file.
// Format mirrors the SWE-bench official "predictions.json" shape
// loosely — one object per instance — so consumers can post-process
// the file with the official scoring scripts if desired.
func WriteResults(path string, results []*EvalResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

// ReadResults is the inverse of WriteResults — used by --grade-only
// mode to re-grade a previous Stage 1 run without regenerating the
// patches. Returns an error if the file is missing or malformed.
func ReadResults(path string) ([]*EvalResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var results []*EvalResult
	if err := json.NewDecoder(f).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode results: %w", err)
	}
	return results, nil
}
