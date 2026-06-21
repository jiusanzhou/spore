// Package agent — plan executor.
//
// planner.go defines what a Plan is and how to ask an LLM to produce one.
// This file actually runs a Plan: for each step, invoke the runtime,
// verify the output, retry on verification failure, and surface a final
// verdict for the caller.
//
// The executor is kept separate from the planner so a future Planner
// implementation (rule-based, deterministic, swarm-bid-based, …) drops in
// without touching execution logic.
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.zoe.im/spore/internal/runtime"
)

// executeTaskWithPlan runs entry through the plan-execute-verify loop
// using planRT for the planning/verification round-trips and execRT for
// step execution. The two can be the same Runtime — they usually are —
// but we keep them separate so tests can inject a fake planner without
// stubbing out a full Runtime.
//
// Returns the final aggregated output; the caller (executeTaskDirect)
// still owns memory write-back, evolution recording, and bus broadcast.
func (a *Agent) executeTaskWithPlan(
	ctx context.Context,
	entry *taskEntry,
	planner Planner,
	execRT runtime.Runtime,
) (*runtime.TaskOutput, error) {
	start := time.Now()

	fmt.Printf("🧠 [%s] Planning task: %s\n", a.cfg.Agent.Name, truncate(entry.Description, 80))

	plan, err := planner.Plan(ctx, entry.Description)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}
	fmt.Printf("📋 [%s] Plan: %d steps\n", a.cfg.Agent.Name, len(plan.Steps))
	for _, s := range plan.Steps {
		fmt.Printf("   %d. %s\n", s.ID, truncate(s.Description, 100))
	}

	result := &PlanResult{Plan: *plan}
	streamingRT, _ := execRT.(runtime.StreamingRuntime)

	// Each step is executed on execRT, with up to MaxRetriesPerStep
	// retries when verification fails. On verification-fail we append
	// the verifier's reason to the next attempt's context so the
	// runtime can correct course rather than repeating the same
	// mistake. (Identical retry without new info is wasted tokens.)
	for _, step := range plan.Steps {
		var stepOutput *runtime.TaskOutput
		var verified bool
		var reason string
		attempts := 0

		for attempt := 0; attempt <= MaxRetriesPerStep; attempt++ {
			attempts++
			extraCtx := ""
			if attempt > 0 && reason != "" {
				extraCtx = fmt.Sprintf("\n\nPrevious attempt failed verification: %s\nFix the gap and try again.", reason)
			}
			stepInput := runtime.TaskInput{
				ID:          fmt.Sprintf("%s-step%d", entry.ID, step.ID),
				Description: step.Description + extraCtx,
				WorkDir:     entry.WorkDir,
			}

			fmt.Printf("▶️  [%s] Step %d/%d (attempt %d): %s\n",
				a.cfg.Agent.Name, step.ID, len(plan.Steps), attempt+1,
				truncate(step.Description, 60))

			var execErr error
			if streamingRT != nil {
				handler := a.makeRuntimeEventHandler(entry.ID, execRT.Info().Name)
				stepOutput, execErr = streamingRT.ExecuteStream(ctx, stepInput, handler)
			} else {
				stepOutput, execErr = execRT.Execute(ctx, stepInput)
			}
			if execErr != nil {
				return nil, fmt.Errorf("step %d execute: %w", step.ID, execErr)
			}
			if !stepOutput.Success {
				// Runtime-level failure (not verification failure) —
				// stop the plan. Retrying a broken runtime call rarely
				// helps and the surrounding retry-on-transient loop in
				// executeTaskDirect already handles 5xx/overload.
				return stepOutput, fmt.Errorf("step %d failed: %s", step.ID, stepOutput.Error)
			}

			// Skip verification when the step omitted a verify criterion
			// (some planners produce sparse verify fields). Treat as done.
			if strings.TrimSpace(step.Verify) == "" {
				verified = true
				break
			}

			ok, why, vErr := planner.Verify(ctx, step, stepOutput.Result)
			if vErr != nil {
				// Verifier itself failed (parse error, runtime down).
				// Don't loop on a broken verifier — accept the step and
				// log so an operator notices.
				fmt.Printf("⚠️  [%s] Verifier error on step %d: %v (accepting step)\n",
					a.cfg.Agent.Name, step.ID, vErr)
				verified = true
				break
			}
			if ok {
				verified = true
				break
			}
			reason = why
			fmt.Printf("❌ [%s] Step %d verification failed: %s\n",
				a.cfg.Agent.Name, step.ID, truncate(reason, 100))
		}

		result.Steps = append(result.Steps, StepResult{
			Step:     step,
			Output:   stepOutput.Result,
			Verified: verified,
			Reason:   reason,
			Attempts: attempts,
		})

		if !verified {
			// Step exhausted retries — abort the plan. Future work:
			// trigger a re-plan with the failure surface area as a
			// new constraint instead of giving up.
			result.Success = false
			result.Summary = fmt.Sprintf("aborted at step %d: %s", step.ID, reason)
			return &runtime.TaskOutput{
				Success: false,
				Result:  formatPlanResult(result),
				Error:   result.Summary,
			}, nil
		}
	}

	result.Success = true
	result.Summary = fmt.Sprintf("completed %d steps in %s", len(plan.Steps), time.Since(start).Round(time.Millisecond))

	// Aggregate the final answer: concatenate each step's output with
	// headings so a downstream consumer (chat UI, memory recall) can
	// see the work breakdown without losing the actual deliverable.
	return &runtime.TaskOutput{
		Success: true,
		Result:  formatPlanResult(result),
	}, nil
}

// formatPlanResult turns a PlanResult into a human-readable summary
// suitable for both the chat UI and the memory/evolution write-back.
func formatPlanResult(r *PlanResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan Result: %s\n\n", r.Plan.Goal)
	if r.Success {
		fmt.Fprintf(&b, "✅ %s\n\n", r.Summary)
	} else {
		fmt.Fprintf(&b, "❌ %s\n\n", r.Summary)
	}
	for _, s := range r.Steps {
		mark := "✅"
		if !s.Verified {
			mark = "❌"
		}
		fmt.Fprintf(&b, "## %s Step %d: %s\n", mark, s.Step.ID, s.Step.Description)
		if s.Attempts > 1 {
			fmt.Fprintf(&b, "_(succeeded on attempt %d)_\n", s.Attempts)
		}
		if s.Reason != "" && !s.Verified {
			fmt.Fprintf(&b, "**Reason:** %s\n\n", s.Reason)
		}
		fmt.Fprintf(&b, "%s\n\n", s.Output)
	}
	return b.String()
}
