package swebench

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.zoe.im/spore/internal/runtime"
)

// Runner drives a single SWE-bench instance: hands the problem statement
// to a runtime, lets the runtime edit the working tree directly, then
// captures whatever ended up in the worktree as the candidate patch.
//
// We deliberately bypass the agent / swarm layer for this benchmark.
// SWE-bench is a single-shot patch task — multi-agent coordination,
// memory, evolution, and the stigmergic market add no signal here and
// would only complicate failure attribution. The runtime alone is the
// minimum viable surface to measure raw fix capability.
type Runner struct {
	rt      runtime.Runtime
	timeout time.Duration
}

// NewRunner wraps a runtime for benchmark execution. timeout caps a
// single instance — be generous (10–30m). LLM agents fixing a Django
// bug routinely chew through tool calls; cutting them off too early
// makes the score unrepresentative.
func NewRunner(rt runtime.Runtime, timeout time.Duration) *Runner {
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	return &Runner{rt: rt, timeout: timeout}
}

// Solve asks the runtime to fix the bug described by inst, working in
// repoDir (which the caller has already prepared via PrepareRepo).
// Returns the runtime's free-text reply (mostly for debugging — the
// real deliverable is the diff captured by Evaluate from repoDir).
func (r *Runner) Solve(ctx context.Context, inst Instance, repoDir string) (string, error) {
	subCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	prompt := buildPrompt(inst, repoDir)

	input := runtime.TaskInput{
		ID:          "swe-bench-" + inst.InstanceID,
		Description: prompt,
		WorkDir:     repoDir,
		Timeout:     int(r.timeout.Seconds()),
	}

	out, err := r.rt.Execute(subCtx, input)
	if err != nil {
		return "", fmt.Errorf("runtime execute: %w", err)
	}
	if !out.Success {
		// Runtime-level failure (sub-agent crashed, network etc.).
		// The diff in repoDir might still be usable, so we surface
		// the error but don't block evaluation downstream.
		return out.Result, fmt.Errorf("runtime returned failure: %s", truncate(out.Error, 200))
	}
	return out.Result, nil
}

// buildPrompt formats the task description we hand to the runtime.
//
// We keep this minimal and prescriptive: agents that try to be too
// chatty (suggesting fixes in prose, asking for clarification) burn
// tokens without producing diffs. The framing here matches what the
// reference SWE-agent paper found effective: state the goal, point at
// the working tree, list constraints (no test edits), demand silence
// when done.
func buildPrompt(inst Instance, repoDir string) string {
	var b strings.Builder
	b.WriteString("You are fixing a bug in a Python repository.\n\n")
	fmt.Fprintf(&b, "Repository: %s\n", inst.Repo)
	fmt.Fprintf(&b, "Working directory: %s\n", repoDir)
	fmt.Fprintf(&b, "Base commit: %s\n\n", inst.BaseCommit)

	b.WriteString("## Problem statement\n\n")
	b.WriteString(strings.TrimSpace(inst.ProblemStatement))
	b.WriteString("\n\n")

	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Read the problem statement carefully.\n")
	b.WriteString("2. Explore the repository to find the buggy code.\n")
	b.WriteString("3. Make the minimal source-code change that fixes the bug.\n")
	b.WriteString("4. Do NOT modify any tests — the grading harness applies its own tests after you finish.\n")
	b.WriteString("5. Do NOT create new files unless absolutely necessary.\n")
	b.WriteString("6. When done, simply stop. Do not commit. Do not run git. Just leave the fix in the working tree.\n\n")
	b.WriteString("Begin.\n")
	return b.String()
}
