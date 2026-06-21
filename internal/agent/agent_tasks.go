/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// agent_tasks.go — task execution loop and supporting machinery.
//
// Lifecycle of a task on this agent:
//
//   SubmitTask / SubmitTaskWithRuntime / submitTaskWithID
//        └─ enqueue *taskEntry on a.taskQueue
//   taskWorker (goroutine started by Run)
//        └─ pop entry, check hibernate / balance, executeTask
//   executeTask → executeTaskDirect
//        └─ pick runtime (explicit > evolution-preferred > auto-route)
//        └─ inject SkillFS-derived tools into builtin runtime
//        └─ run with retry on transient errors
//        └─ broadcast result, settle tokens, rememberTask, recordEvolution
//   rememberTask
//        └─ legacy memory.Entry + structured ContextEntry case
//   runSkillAnalysis (called from agent_persistence.go's recordEvolution)
//        └─ analyzer.Analyze → publish to IPFS → skillEvolver.Evolve
//
// Why this lives in its own file: agent.go was 2.3k lines; this slice
// is the single biggest chunk by responsibility (~600 lines) and has a
// well-bounded surface — only the queue and the engine/runtime/memory
// stores it touches.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"go.zoe.im/spore/internal/engine"
	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/runtime"
)

// taskEntry is the queue item carried from SubmitTask* through the
// taskWorker into executeTask.
type taskEntry struct {
	ID          string
	Description string
	Runtime     string // preferred runtime name, empty = auto
	WorkDir     string
	CreatedAt   time.Time
}

// SubmitTask queues a task for execution and returns its ID.
func (a *Agent) SubmitTask(description string) string {
	return a.SubmitTaskWithRuntime(description, "", "")
}

// SubmitTaskWithRuntime queues a task with a specific runtime preference.
func (a *Agent) SubmitTaskWithRuntime(description, runtimeName, workDir string) string {
	entry := &taskEntry{
		ID:          uuid.New().String()[:8],
		Description: description,
		Runtime:     runtimeName,
		WorkDir:     workDir,
		CreatedAt:   time.Now(),
	}
	a.taskQueue <- entry
	return entry.ID
}

// submitTaskWithID queues a task with a specific ID (used by marketplace
// to preserve the offer task ID across agents).
func (a *Agent) submitTaskWithID(taskID, description string) {
	entry := &taskEntry{
		ID:          taskID,
		Description: description,
		CreatedAt:   time.Now(),
	}
	a.taskQueue <- entry
}

// makeRuntimeEventHandler returns a runtime.EventHandler tuned for this
// agent: it surfaces streaming events from external runtimes (claude-code,
// codex, ...) to stdout in a compact, scannable format, and updates the
// agent's awareness counters when relevant.
//
// Design notes:
//   - The handler is invoked synchronously by the runtime's stream parser, so
//     we keep it cheap. No I/O beyond a buffered fmt.Printf.
//   - We deliberately do NOT broadcast every event to the swarm bus —
//     intra-task chatter would drown the gossip channel. The post-task
//     analyzer / changelog already publishes the *summary* the swarm cares
//     about. Future hooks (dashboard SSE, WebSocket) should subscribe here.
//   - Errors from the handler stop the stream; we return nil unconditionally
//     because dropping a log line should never abort a real task.
func (a *Agent) makeRuntimeEventHandler(taskID, runtimeName string) runtime.EventHandler {
	prefix := fmt.Sprintf("   ↳ [%s/%s/%s]", a.cfg.Agent.Name, runtimeName, taskID)
	return func(ev runtime.StreamEvent) error {
		// Fan event out to subscribers (SSE / chat UI) first — even if
		// stdout formatting below fails it's never fatal, but we want
		// every observed event to reach the bus before any logging
		// errors short-circuit us.
		if a.onRuntimeEvent != nil {
			a.onRuntimeEvent(taskID, ev)
		}

		switch ev.Type {
		case runtime.EventInit:
			fmt.Printf("%s 🔌 init session=%s %s\n", prefix, ev.Session, ev.Content)
		case runtime.EventThinking:
			text := ev.Content
			// Reasoning blocks can be long; show a single-line preview.
			line := strings.ReplaceAll(text, "\n", " ")
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			fmt.Printf("%s 💭 %s\n", prefix, line)
		case runtime.EventToolCall:
			arg := ev.ToolInput
			if len(arg) > 120 {
				arg = arg[:120] + "…"
			}
			fmt.Printf("%s 🔧 %s %s\n", prefix, ev.ToolName, arg)
		case runtime.EventToolResult:
			marker := "✓"
			if ev.ToolError {
				marker = "✗"
			}
			out := strings.ReplaceAll(ev.ToolOutput, "\n", " ")
			if len(out) > 120 {
				out = out[:120] + "…"
			}
			fmt.Printf("%s %s tool_result %s\n", prefix, marker, out)
		case runtime.EventError:
			lvl := "warn"
			if ev.Fatal {
				lvl = "ERROR"
			}
			fmt.Printf("%s ⚠️  %s: %s\n", prefix, lvl, ev.Content)
		case runtime.EventComplete:
			fmt.Printf("%s ✅ done in=%d out=%d cached=%d cost=$%.4f duration=%dms\n",
				prefix, ev.InputTokens, ev.OutputTokens, ev.CachedTokens,
				ev.CostUSD, ev.DurationMS)
		}
		return nil
	}
}

// collectSkillTools gathers all executable tool definitions from SkillFS.
// These are tools defined in SKILL.md frontmatter `tools:` sections,
// created through agent evolution (the "tool creation" capability).
func (a *Agent) collectSkillTools() []engine.SkillToolDef {
	if a.skillFS == nil {
		return nil
	}
	var tools []engine.SkillToolDef
	for _, name := range a.skillFS.List() {
		skill, ok := a.skillFS.Get(name)
		if !ok {
			continue
		}
		for _, t := range skill.Meta.Tools {
			if t.Name != "" && t.Command != "" {
				tools = append(tools, engine.SkillToolDef{
					Name:        t.Name,
					Description: t.Description,
					Command:     t.Command,
					Timeout:     t.Timeout,
					Interpreter: t.Interpreter,
				})
			}
		}
	}
	return tools
}

func (a *Agent) taskWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-a.taskQueue:
			// Check hibernate state
			a.mu.RLock()
			status := a.status
			a.mu.RUnlock()
			if status == StatusHibernate {
				fmt.Printf("💤 [%s] Rejecting task (hibernate): %s\n", a.cfg.Agent.Name, entry.Description)
				continue
			}

			// Check minimum balance for task acceptance
			if a.cfg.Economy.MinTaskBalance > 0 && !a.identity.CanAfford(a.cfg.Economy.MinTaskBalance) {
				fmt.Printf("💰 [%s] Rejecting task (insufficient_balance): %.4f < %.4f\n",
					a.cfg.Agent.Name, a.identity.Balance, a.cfg.Economy.MinTaskBalance)
				continue
			}

			active := atomic.AddInt32(&a.activeTasks, 1)
			a.mu.Lock()
			a.status = StatusBusy
			a.mu.Unlock()

			fmt.Printf("📋 [%s] Starting task (%d active): %s\n", a.cfg.Agent.Name, active, entry.Description)
			if a.onTaskUpdate != nil {
				a.onTaskUpdate(entry.ID, "running", "", "", "")
			}

			err := a.executeTask(ctx, entry)
			if err != nil {
				fmt.Printf("❌ [%s] Task failed: %s\n", a.cfg.Agent.Name, err)
			}

			remaining := atomic.AddInt32(&a.activeTasks, -1)
			a.mu.Lock()
			a.taskCount++
			if a.cfg.Economy.HibernateThreshold > 0 && a.identity.Balance <= 0 {
				a.status = StatusHibernate
				fmt.Printf("💤 [%s] Entering hibernate (balance depleted)\n", a.cfg.Agent.Name)
			} else if remaining == 0 {
				a.status = StatusIdle
			}
			a.mu.Unlock()
		}
	}
}

// executeTask is currently a thin wrapper around executeTaskDirect; the
// future stigmergic-broadcast path lives in agent_market.go and is gated
// on task complexity / agent specialization (see broadcastTask there).
func (a *Agent) executeTask(ctx context.Context, entry *taskEntry) error {
	// Every agent tries to execute directly first.
	// If the task feels too big, broadcast it to the swarm as a "pheromone signal"
	// and let other agents bid. This is stigmergic — no central coordinator.
	return a.executeTaskDirect(ctx, entry)
}

// executeTaskDirect runs the task on a single runtime, with retries on
// transient errors. This is the hot path — every task that doesn't get
// broadcast to the swarm comes through here.
func (a *Agent) executeTaskDirect(ctx context.Context, entry *taskEntry) error {
	// Worker/specialist: direct execution
	var rt runtime.Runtime
	var err error

	if entry.Runtime != "" {
		// Explicit runtime requested
		var ok bool
		rt, ok = a.registry.Get(entry.Runtime)
		if !ok {
			return fmt.Errorf("runtime not found: %s", entry.Runtime)
		}
	} else {
		// Config-pinned runtime (CLI flag / spore.toml) wins over evolution:
		// when the operator explicitly chose a backend, honour it. Evolution
		// only steers when the config is "auto" (or empty).
		cfgRT := a.cfg.Runtime.Type
		if cfgRT != "" && cfgRT != "auto" {
			if prt, ok := a.registry.Get(cfgRT); ok {
				rt = prt
			}
		}
		// Let evolution engine suggest preferred runtime when not pinned.
		if rt == nil && a.evolution != nil {
			if preferred := a.evolution.BestRuntime(); preferred != "" {
				if prt, ok := a.registry.Get(preferred); ok {
					rt = prt
				}
			}
		}
		if rt == nil {
			// Fallback: auto-route based on task tags
			rt, err = a.registry.Route(nil)
			if err != nil {
				return fmt.Errorf("no runtime available: %w", err)
			}
		}
	}

	fmt.Printf("   [%s] Using runtime: %s\n", a.cfg.Agent.Name, rt.Info().Name)

	// Plan-execute-verify loop: when the task description looks complex
	// (action verbs like "implement"/"refactor", multi-clause goals),
	// route it through the planner before single-shot execution. The
	// planner reuses the same runtime, so the only extra cost is one
	// planning round-trip + one verification round-trip per step.
	// Builtin runtime has no LLM and can't plan — skip for it.
	if _, isBuiltin := rt.(*runtime.Builtin); !isBuiltin && shouldPlan(entry.Description) {
		planner := NewLLMPlanner(rt)
		output, planErr := a.executeTaskWithPlan(ctx, entry, planner, rt)
		return a.finalizeTaskResult(entry, rt, output, planErr)
	}

	input := runtime.TaskInput{
		ID:          entry.ID,
		Description: entry.Description,
		WorkDir:     entry.WorkDir,
	}

	// Inject evolved skill tools into builtin runtime
	if builtinRT, ok := rt.(*runtime.Builtin); ok {
		builtinRT.SkillTools = a.collectSkillTools()
	}

	// Execute with retry for transient errors (529/5xx/overloaded). When the
	// runtime supports streaming (StreamingRuntime), we wire a handler that
	// surfaces tool calls and partial reasoning to the agent's log so an
	// operator watching stdout (or the dashboard, eventually) sees progress
	// instead of black-box silence. Token/cost accounting in TaskOutput is
	// populated either way.
	var output *runtime.TaskOutput
	maxRetries := 2
	streamingRT, _ := rt.(runtime.StreamingRuntime)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if streamingRT != nil {
			handler := a.makeRuntimeEventHandler(entry.ID, rt.Info().Name)
			output, err = streamingRT.ExecuteStream(ctx, input, handler)
		} else {
			output, err = rt.Execute(ctx, input)
		}
		if err == nil && output != nil && output.Success {
			break
		}
		// Check if retryable
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else if output != nil {
			errMsg = output.Error
		}
		if attempt < maxRetries && isRetryableError(errMsg) {
			delay := time.Duration(5*(attempt+1)) * time.Second
			fmt.Printf("🔄 [%s] Retrying task in %v (attempt %d/%d): %s\n",
				a.cfg.Agent.Name, delay, attempt+1, maxRetries, truncate(errMsg, 80))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		break
	}

	if err != nil {
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "failed", rt.Info().Name, "", err.Error())
		}
		return err
	}

	return a.finalizeTaskResult(entry, rt, output, nil)
}

// finalizeTaskResult applies the post-execution side-effects (status
// callback, token accounting, bus broadcast, memory write, evolution
// record) that are identical for single-shot and plan-based execution.
// Extracted from executeTaskDirect so the plan-execute-verify loop can
// reuse it without duplicating the bookkeeping.
func (a *Agent) finalizeTaskResult(
	entry *taskEntry,
	rt runtime.Runtime,
	output *runtime.TaskOutput,
	execErr error,
) error {
	if execErr != nil {
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "failed", rt.Info().Name, "", execErr.Error())
		}
		return execErr
	}
	if output == nil {
		err := fmt.Errorf("nil output from runtime %s", rt.Info().Name)
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "failed", rt.Info().Name, "", err.Error())
		}
		return err
	}

	if output.Success {
		fmt.Printf("✅ [%s] Task completed via %s: %s\n", a.cfg.Agent.Name, rt.Info().Name, truncate(output.Result, 200))
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "completed", rt.Info().Name, output.Result, "")
		}
		// Token economy: charge LLM cost, reward completion
		if a.tokens != nil {
			if output.Cost > 0 {
				a.tokens.ChargeThink(output.Cost, "task:"+entry.ID[:8])
			}
			a.tokens.RewardTask(entry.ID, true)
		} else if output.Cost > 0 {
			// Legacy: direct debit
			if err := a.identity.Debit(output.Cost); err != nil {
				fmt.Printf("⚠️  [%s] Balance debit failed: %v\n", a.cfg.Agent.Name, err)
			}
		}
		// Broadcast result to bus (for coordinator collection)
		a.broadcastTaskResult(entry.ID, output.Result, true, "")
		// Deliver to marketplace if this is a paid task
		if a.marketplace != nil {
			a.marketplace.DeliverResult(entry.ID, output.Result, true)
		}
		// Store task experience in memory
		a.rememberTask(entry, output, rt.Info().Name)
		// Record to evolution engine for self-improvement
		a.recordEvolution(entry, output, rt.Info().Name, true, "")
		return nil
	}

	if a.onTaskUpdate != nil {
		a.onTaskUpdate(entry.ID, "failed", rt.Info().Name, "", output.Error)
	}
	// Token economy: penalize failure
	if a.tokens != nil {
		a.tokens.RewardTask(entry.ID, false)
	}
	a.broadcastTaskResult(entry.ID, "", false, output.Error)
	a.recordEvolution(entry, nil, rt.Info().Name, false, output.Error)
	return fmt.Errorf("task failed: %s", output.Error)
}

// rememberTask stores the task experience in two places: the legacy
// flat memory.Entry (for backward compat with old recall paths) and a
// structured memory.ContextEntry case (the new path used by collective
// synthesis and skill evolution).
func (a *Agent) rememberTask(entry *taskEntry, output *runtime.TaskOutput, rtName string) {
	if a.memory == nil {
		return
	}
	agentID := a.identity.PublicKeyHex()[:16]

	// Legacy flat memory (backward compat)
	memEntry := &memory.Entry{
		AgentID: agentID,
		Key:     "task:" + entry.ID,
		Value:   fmt.Sprintf("Task: %s\nResult: %s", entry.Description, truncate(output.Result, 4000)),
		Metadata: map[string]string{
			"type":    "task_experience",
			"task_id": entry.ID,
			"runtime": rtName,
			"success": "true",
		},
	}
	if err := a.memory.Put(memEntry); err != nil {
		fmt.Printf("⚠️  [%s] Failed to store task memory: %v\n", a.cfg.Agent.Name, err)
	}

	// Structured context memory (new)
	ctxStore, ok := a.memory.(memory.ContextStore)
	if !ok {
		return
	}

	// Store as a case (problem + solution)
	l0 := truncate(entry.Description, 100)
	skills := a.cfg.Agent.Skills
	l1 := fmt.Sprintf("## Case: %s\n\n**Runtime**: %s\n**Skills**: %s\n\n### Problem\n%s\n\n### Solution\n%s",
		truncate(entry.Description, 80),
		rtName,
		strings.Join(skills, ", "),
		entry.Description,
		truncate(output.Result, 2000))
	l2 := fmt.Sprintf("Task: %s\n\nFull Result:\n%s", entry.Description, output.Result)

	caseEntry := &memory.ContextEntry{
		URI:      fmt.Sprintf("spore://%s/memory/cases/%s", agentID, entry.ID),
		AgentID:  agentID,
		Type:     memory.CtxMemory,
		Category: memory.CatCases,
		L0:       l0,
		L1:       l1,
		L2:       l2,
		Tags:     skills,
		Source:   "task:" + entry.ID,
		Metadata: map[string]string{
			"runtime": rtName,
		},
	}
	if err := ctxStore.PutContext(caseEntry); err != nil {
		fmt.Printf("⚠️  [%s] Failed to store case memory: %v\n", a.cfg.Agent.Name, err)
	} else {
		fmt.Printf("🧠 [%s] Stored case: %s\n", a.cfg.Agent.Name, l0)
	}
}

// runSkillAnalysis performs post-task LLM analysis and triggers skill evolution.
// Called from agent_persistence.go's recordEvolution after a successful task.
func (a *Agent) runSkillAnalysis(entry *taskEntry, output *runtime.TaskOutput, rtName string, duration float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	analysis, err := a.analyzer.Analyze(ctx, entry, output, rtName, duration)
	if err != nil {
		fmt.Printf("🔍 [%s] Skill analysis failed: %v\n", a.cfg.Agent.Name, err)
		return
	}

	fmt.Printf("🔍 [%s] Task analysis: quality=%.1f efficiency=%.1f skills=%v suggestions=%d\n",
		a.cfg.Agent.Name, analysis.Quality, analysis.Efficiency,
		analysis.SkillsUsed, len(analysis.Suggestions))

	// Publish analysis to IPFS as Markdown
	a.publishToIPFS([]byte(AnalysisToMarkdown(analysis)), "skill_analysis",
		fmt.Sprintf("Analysis: task=%s q=%.1f", entry.ID[:8], analysis.Quality))

	// Execute evolution suggestions (threshold 0.5 = moderate+urgent)
	if a.skillEvolver != nil && len(analysis.Suggestions) > 0 {
		evolved, err := a.skillEvolver.Evolve(ctx, analysis, 0.5)
		if err != nil {
			fmt.Printf("🧬 [%s] Skill evolution error: %v\n", a.cfg.Agent.Name, err)
		}
		for _, es := range evolved {
			fmt.Printf("🧬 [%s] Evolved skill: %s (type=%s, gen=%d) %s\n",
				a.cfg.Agent.Name, es.Name, es.Type, es.Generation, es.Summary)

			// Publish evolved skill to IPFS via SkillFS (already done on write)
			// Broadcast the CID to peers
			if a.skillFS != nil {
				if skill, ok := a.skillFS.Get(es.Name); ok && skill.Meta.IPFSCID != "" {
					a.broadcastSkillCID(skill)
				}
			}
		}

		// Regenerate index after evolution
		if a.skillFS != nil && len(evolved) > 0 {
			a.skillFS.WriteIndex()
		}
	}
}

// truncate cuts s to n bytes and appends "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// isRetryableError matches the substrings used by upstream LLM providers
// to indicate a transient error worth retrying (overloaded, 5xx, 429,
// timeouts, connection resets).
func isRetryableError(errMsg string) bool {
	if errMsg == "" {
		return false
	}
	lower := strings.ToLower(errMsg)
	retryPatterns := []string{
		"529", "overloaded", "overload",
		"500", "502", "503", "504",
		"rate limit", "rate_limit", "too many requests", "429",
		"temporary", "temporarily",
		"connection reset", "connection refused",
		"timeout", "timed out",
	}
	for _, p := range retryPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// containsIgnoreCase is a case-insensitive substring check.
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
