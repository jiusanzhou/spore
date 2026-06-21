// Package swebench is a SWE-bench Lite harness for spore.
//
// SWE-bench Lite is a 300-task benchmark (plus a 23-task dev split) of
// real-world GitHub bug-fix issues drawn from 11 popular Python
// repositories. Each instance pairs a problem statement (the issue
// body) with a base commit, a hidden "gold" patch, a test patch that
// adds/updates the regression tests, and two lists of test names:
//
//	FAIL_TO_PASS — tests that fail at base_commit and must pass after
//	               the agent's patch is applied
//	PASS_TO_PASS — tests that already pass at base_commit and must
//	               keep passing after the patch
//
// An instance is "resolved" when both conditions hold. The leaderboard
// metric is the resolved fraction over the test split.
//
// This package loads instances from the JSONL dump produced by
// converting the HuggingFace parquet files, so the harness has no
// runtime dependency on HF Datasets or Python.
package swebench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Instance is one SWE-bench Lite task. Field tags match the parquet
// schema published at huggingface.co/datasets/princeton-nlp/SWE-bench_Lite
// so jsonl rows decode directly. FAIL_TO_PASS / PASS_TO_PASS are stored
// upstream as JSON-encoded strings (not arrays) — we decode them lazily
// via the typed accessors below to keep the raw struct round-trippable.
type Instance struct {
	Repo                   string `json:"repo"`                      // "owner/name"
	InstanceID             string `json:"instance_id"`               // "owner__name-PR-N"
	BaseCommit             string `json:"base_commit"`               // SHA where work starts
	Patch                  string `json:"patch"`                     // hidden gold patch (we don't show this to the agent)
	TestPatch              string `json:"test_patch"`                // adds the regression tests; we apply this AFTER the agent submits
	ProblemStatement       string `json:"problem_statement"`         // issue title + body
	HintsText              string `json:"hints_text,omitempty"`      // comments on the issue before the fix PR
	CreatedAt              string `json:"created_at,omitempty"`
	Version                string `json:"version,omitempty"`
	FailToPassRaw          string `json:"FAIL_TO_PASS"`              // JSON-encoded []string
	PassToPassRaw          string `json:"PASS_TO_PASS"`              // JSON-encoded []string
	EnvironmentSetupCommit string `json:"environment_setup_commit,omitempty"`
}

// FailToPass parses the upstream JSON-encoded list. Returning an
// error here is deliberate: a malformed list silently treated as empty
// would let any submission "pass" by skipping its regression tests.
func (i *Instance) FailToPass() ([]string, error) {
	return parseTestList(i.FailToPassRaw)
}

// PassToPass parses the upstream JSON-encoded list.
func (i *Instance) PassToPass() ([]string, error) {
	return parseTestList(i.PassToPassRaw)
}

func parseTestList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse test list: %w (raw: %q)", err, truncate(raw, 80))
	}
	return out, nil
}

// LoadInstances reads a JSONL file (one Instance per line) from path.
// Blank lines and lines whose first non-whitespace char is '#' are
// skipped — handy for hand-curated subset files.
func LoadInstances(path string) ([]Instance, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dataset: %w", err)
	}
	defer f.Close()
	return parseJSONL(f)
}

func parseJSONL(r io.Reader) ([]Instance, error) {
	sc := bufio.NewScanner(r)
	// SWE-bench problem_statements can run to a few KB and test_patches
	// even larger. Default Scanner buffer (64K) is fine for Lite but
	// we bump it so the full SWE-bench dataset works too.
	sc.Buffer(make([]byte, 1<<16), 4<<20)
	var out []Instance
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var inst Instance
		if err := json.Unmarshal([]byte(line), &inst); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		out = append(out, inst)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return out, nil
}

// FilterByIDs keeps only the instances whose InstanceID appears in ids.
// Order follows the original slice, not ids. Unknown IDs are silently
// ignored — the caller usually wants a summary report rather than a
// hard error when the dataset has been updated.
func FilterByIDs(instances []Instance, ids []string) []Instance {
	if len(ids) == 0 {
		return instances
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make([]Instance, 0, len(ids))
	for _, inst := range instances {
		if _, ok := want[inst.InstanceID]; ok {
			out = append(out, inst)
		}
	}
	return out
}

// truncate caps s to n runes for safe inclusion in error messages.
// Local copy (instead of importing internal/agent) keeps this package
// dependency-free for benchmark consumers.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
