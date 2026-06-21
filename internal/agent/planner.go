// Package agent — plan-execute-verify loop.
//
// Most agent tasks are single-shot: hand the description to a runtime,
// get a result, done. That works for simple things ("explain X", "list Y")
// but falls apart on multi-step work ("implement RFC-002", "fix the race
// in task_events.go") where the agent needs to think before it acts.
//
// This file adds a thin meta-controller around runtime execution:
//
//	Plan    — ask the runtime to break the goal into ordered steps,
//	          each with its own verification criterion
//	Execute — run each step on the runtime, collecting outputs
//	Verify  — after each step, ask the runtime to judge whether the
//	          step's stated criterion was met
//	Retry   — when verification fails, re-run with the failure reason
//	          appended as context (up to MaxRetriesPerStep)
//	Replan  — if a step exhausts retries, surface the failure and stop
//	          (future: trigger a re-plan with new constraints)
//
// The loop is deliberately small and reuses the existing Runtime
// interface so any backend (claude-code, codex, builtin) gets it for free.
// It is opt-in: shouldPlan() decides per-task whether to engage based
// on cheap heuristics (description length, action verbs). Simple tasks
// stay on the fast path.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.zoe.im/spore/internal/runtime"
)

// Step is one unit of work in a Plan.
type Step struct {
	ID          int    `json:"id"`          // 1-indexed position
	Description string `json:"description"` // what to do
	Verify      string `json:"verify"`      // how to know it succeeded
}

// Plan is an ordered list of Steps produced by the planner.
type Plan struct {
	Goal  string `json:"goal"`
	Steps []Step `json:"steps"`
}

// StepResult records what happened when a Step was executed.
type StepResult struct {
	Step     Step   `json:"step"`
	Output   string `json:"output"`
	Verified bool   `json:"verified"`
	Reason   string `json:"reason,omitempty"` // why verification failed (if it did)
	Attempts int    `json:"attempts"`
}

// PlanResult is the outcome of running a full Plan.
type PlanResult struct {
	Plan    Plan         `json:"plan"`
	Steps   []StepResult `json:"steps"`
	Success bool         `json:"success"`
	Summary string       `json:"summary"`
}

// Planner produces and verifies Plans. Implementations typically delegate
// to an LLM-backed Runtime, but a deterministic test fake is trivial to
// write against this interface.
type Planner interface {
	// Plan asks the planner to break goal into an ordered list of steps.
	Plan(ctx context.Context, goal string) (*Plan, error)

	// Verify asks the planner to judge whether step's verify criterion
	// is satisfied by output. Returns (ok, reason).
	Verify(ctx context.Context, step Step, output string) (bool, string, error)
}

// MaxRetriesPerStep bounds how many times a single Step is retried before
// the Plan is aborted. Keep this small — retry-after-retry burns tokens
// and rarely fixes a fundamentally broken plan; replanning is the right
// escape hatch once we wire it up.
const MaxRetriesPerStep = 2

// LLMPlanner is a Planner that uses a Runtime (claude-code, codex, …)
// for both planning and verification. It does NOT execute steps itself —
// step execution stays in the caller so the existing streaming /
// event-broadcast / token-accounting paths keep working unchanged.
type LLMPlanner struct {
	rt runtime.Runtime
}

// NewLLMPlanner wraps a runtime as a Planner.
func NewLLMPlanner(rt runtime.Runtime) *LLMPlanner {
	return &LLMPlanner{rt: rt}
}

// planPrompt is the instruction we send to the runtime to produce a Plan.
// We ask for strict JSON so we can parse the reply mechanically — chat
// prose would force a second LLM round-trip just to extract structure.
const planPrompt = `You are a planning module for an autonomous agent.

Break the following goal into 2–6 concrete, ordered steps. Each step must
be small enough that a single tool/runtime invocation can finish it, and
must include a verification criterion that a separate LLM judge can
evaluate from the step's output alone.

Reply with ONLY valid JSON, no prose, matching this schema:

{"steps":[{"id":1,"description":"…","verify":"…"}, …]}

Goal:
%s`

// verifyPrompt asks the runtime to judge a single step's output.
const verifyPrompt = `You are a verification judge for an autonomous agent.

A step has been executed. Decide whether the verification criterion is
satisfied by the output. Be strict but fair: partial credit is failure.

Reply with ONLY valid JSON, no prose:

{"done": true|false, "reason": "one-sentence explanation"}

Step description: %s
Verification criterion: %s

Step output:
%s`

// Plan implements Planner.
func (p *LLMPlanner) Plan(ctx context.Context, goal string) (*Plan, error) {
	input := runtime.TaskInput{
		ID:          "planner-" + shortHash(goal),
		Description: fmt.Sprintf(planPrompt, goal),
	}
	out, err := p.rt.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("planner runtime: %w", err)
	}
	if !out.Success {
		return nil, fmt.Errorf("planner runtime returned failure: %s", out.Error)
	}
	plan, err := parsePlan(out.Result)
	if err != nil {
		return nil, fmt.Errorf("parse plan: %w (raw: %s)", err, truncate(out.Result, 200))
	}
	plan.Goal = goal
	return plan, nil
}

// Verify implements Planner.
func (p *LLMPlanner) Verify(ctx context.Context, step Step, output string) (bool, string, error) {
	input := runtime.TaskInput{
		ID:          "verify-" + shortHash(step.Description+output),
		Description: fmt.Sprintf(verifyPrompt, step.Description, step.Verify, truncate(output, 4000)),
	}
	out, err := p.rt.Execute(ctx, input)
	if err != nil {
		return false, "", fmt.Errorf("verifier runtime: %w", err)
	}
	if !out.Success {
		return false, "", fmt.Errorf("verifier runtime returned failure: %s", out.Error)
	}
	ok, reason, err := parseVerdict(out.Result)
	if err != nil {
		return false, "", fmt.Errorf("parse verdict: %w (raw: %s)", err, truncate(out.Result, 200))
	}
	return ok, reason, nil
}

// parsePlan extracts the JSON plan from a runtime reply. We reuse
// extractJSON (evolution_adapt.go) which handles ```json fences and
// bare-object replies. The "ONLY JSON" instruction in planPrompt is a
// hint, not a guarantee — LLMs often prepend prose.
func parsePlan(raw string) (*Plan, error) {
	blob := extractJSON(raw)
	if blob == "" {
		return nil, fmt.Errorf("no JSON object found")
	}
	// extractJSON may return a fenced block that doesn't start with '{'
	// if the LLM wrapped the JSON oddly — find the first '{' to recover.
	if i := strings.Index(blob, "{"); i > 0 {
		blob = blob[i:]
	}
	var p Plan
	if err := json.Unmarshal([]byte(blob), &p); err != nil {
		return nil, err
	}
	if len(p.Steps) == 0 {
		return nil, fmt.Errorf("plan has no steps")
	}
	return &p, nil
}

func parseVerdict(raw string) (bool, string, error) {
	blob := extractJSON(raw)
	if blob == "" {
		return false, "", fmt.Errorf("no JSON object found")
	}
	if i := strings.Index(blob, "{"); i > 0 {
		blob = blob[i:]
	}
	var v struct {
		Done   bool   `json:"done"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(blob), &v); err != nil {
		return false, "", err
	}
	return v.Done, v.Reason, nil
}

// planActionVerbs is the set of cues that flip a task onto the planning
// path. The list is intentionally conservative — false-positives waste
// tokens on a planning round-trip for a task that didn't need one, so we
// only trigger on verbs that almost always imply multiple sub-tasks.
var planActionVerbs = []string{
	"implement", "build", "create", "design",
	"refactor", "rewrite", "migrate",
	"fix bug", "debug", "diagnose",
	"add feature", "add support",
	"port to", "integrate with",
}

// shouldPlan decides whether a task is complex enough to warrant the
// plan-execute-verify loop. The heuristic is cheap (string ops only) —
// the LLM round-trip for an actual plan is expensive, so we want to
// reserve it for tasks that genuinely benefit.
func shouldPlan(description string) bool {
	d := strings.ToLower(description)
	for _, v := range planActionVerbs {
		if strings.Contains(d, v) {
			return true
		}
	}
	// Long, multi-clause descriptions usually need decomposition even
	// when they don't contain a canonical verb.
	if len(description) > 300 && strings.Count(description, ".") >= 2 {
		return true
	}
	return false
}

// shortHash is a stable 8-char hash for synthetic task IDs. Not
// cryptographic — just enough to disambiguate concurrent planner calls
// in logs and event streams.
func shortHash(s string) string {
	// FNV-1a — fast, stdlib, good enough for an ID suffix.
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)[:8]
}
