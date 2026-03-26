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

package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
)

// TaskState tracks the lifecycle of a task.
type TaskState string

const (
	TaskPending   TaskState = "pending"
	TaskRunning   TaskState = "running"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskDelegated TaskState = "delegated"
)

// Task represents a unit of work for an agent.
type Task struct {
	ID          string
	Description string
	State       TaskState
	Result      string
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Steps       []Step

	// Runtime metrics
	TotalTokens int // accumulated prompt + completion tokens
}

// Step records one cycle of the agent loop.
type Step struct {
	Observation string
	Thought     string
	Action      string
	Reflection  string
	Timestamp   time.Time
	Tokens      int           // tokens used in this step
	Duration    time.Duration // wall time for this step
	ToolRetries int           // how many retries for tool execution
}

// EthicsChecker is the interface the engine uses to validate actions.
type EthicsChecker interface {
	Check(agentID, taskID, action string) (decision string, level string, reason string)
}

// EngineConfig holds tunable parameters for the engine.
type EngineConfig struct {
	MaxSteps        int           // max agent loop iterations (0 = default 20)
	MaxContextChars int           // max chars in prompt context window (0 = default 8000)
	RecentSteps     int           // recent steps in full detail (0 = default 5)
	ToolTimeout     time.Duration // per-tool execution timeout (0 = 30s)
	ToolMaxRetries  int           // max retries for transient tool failures (0 = 2)
}

// DefaultEngineConfig returns sensible defaults.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		MaxSteps:        20,
		MaxContextChars: 8000,
		RecentSteps:     5,
		ToolTimeout:     30 * time.Second,
		ToolMaxRetries:  2,
	}
}

// Engine drives the Observe → Think → Act → Reflect loop.
type Engine struct {
	llm     llm.Provider
	memory  memory.Store
	tools   map[string]Tool
	ethics  EthicsChecker
	agentID string
	config  EngineConfig
}

// Tool is something the agent can invoke during Act phase.
type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, input string) (string, error)
}

// New creates a new task engine with default config.
func New(provider llm.Provider, store memory.Store) *Engine {
	return &Engine{
		llm:    provider,
		memory: store,
		tools:  make(map[string]Tool),
		config: DefaultEngineConfig(),
	}
}

// SetConfig updates the engine configuration.
func (e *Engine) SetConfig(cfg EngineConfig) {
	if cfg.MaxSteps > 0 {
		e.config.MaxSteps = cfg.MaxSteps
	}
	if cfg.MaxContextChars > 0 {
		e.config.MaxContextChars = cfg.MaxContextChars
	}
	if cfg.RecentSteps > 0 {
		e.config.RecentSteps = cfg.RecentSteps
	}
	if cfg.ToolTimeout > 0 {
		e.config.ToolTimeout = cfg.ToolTimeout
	}
	if cfg.ToolMaxRetries > 0 {
		e.config.ToolMaxRetries = cfg.ToolMaxRetries
	}
}

func (e *Engine) SetEthics(checker EthicsChecker) { e.ethics = checker }
func (e *Engine) SetAgentID(id string)            { e.agentID = id }
func (e *Engine) SetMemory(store memory.Store)     { e.memory = store }
func (e *Engine) RegisterTool(t Tool)              { e.tools[t.Name()] = t }

// Run executes a task through the full agent loop.
func (e *Engine) Run(ctx context.Context, task *Task) error {
	task.State = TaskRunning
	task.UpdatedAt = time.Now()

	maxSteps := e.config.MaxSteps

	// Adaptive step budget: simple tasks get fewer steps,
	// complex tasks (long description or many tool calls expected) get more.
	descLen := len(task.Description)
	if descLen > 500 {
		maxSteps = min(maxSteps*2, 40) // up to 2x for complex tasks
	}

	consecutiveNoOps := 0 // track stuck loops
	const maxNoOps = 3     // bail if stuck

	for i := 0; i < maxSteps; i++ {
		select {
		case <-ctx.Done():
			task.State = TaskFailed
			task.Error = "context cancelled"
			return ctx.Err()
		default:
		}

		stepStart := time.Now()
		step, done, err := e.tick(ctx, task, i, maxSteps)
		if step != nil {
			step.Duration = time.Since(stepStart)
			task.Steps = append(task.Steps, *step)
		}
		task.UpdatedAt = time.Now()

		if err != nil {
			task.State = TaskFailed
			task.Error = err.Error()
			return err
		}

		if done {
			task.State = TaskCompleted
			return nil
		}

		// Detect stuck loops (consecutive no-action steps)
		if step != nil && step.Action == "" {
			consecutiveNoOps++
			if consecutiveNoOps >= maxNoOps {
				// Force the agent to produce a result
				forceResult := e.forceComplete(ctx, task)
				if forceResult != "" {
					task.Result = forceResult
					task.State = TaskCompleted
					return nil
				}
			}
		} else {
			consecutiveNoOps = 0
		}
	}

	// Last resort: extract whatever partial result we have
	if partial := e.extractPartialResult(task); partial != "" {
		task.Result = partial
		task.State = TaskCompleted
		return nil
	}

	task.State = TaskFailed
	task.Error = fmt.Sprintf("max steps exceeded (%d)", maxSteps)
	return fmt.Errorf("task exceeded %d steps", maxSteps)
}

// tick executes one cycle: Observe → Think → Act → Reflect.
func (e *Engine) tick(ctx context.Context, task *Task, stepNum, maxSteps int) (*Step, bool, error) {
	step := &Step{Timestamp: time.Now()}

	// 1. Observe — gather current context
	observation := e.observe(task, stepNum, maxSteps)
	step.Observation = observation

	// 2. Think — ask LLM what to do next
	thought, tokens, err := e.think(ctx, task, observation, stepNum, maxSteps)
	if err != nil {
		return step, false, fmt.Errorf("think: %w", err)
	}
	step.Thought = thought
	step.Tokens = tokens
	task.TotalTokens += tokens

	// 3. Parse the LLM response for action or completion
	action, done := parseAction(thought)
	step.Action = action.Raw

	if done {
		task.Result = action.Result
		return step, true, nil
	}

	// No action parsed — thinking-only step
	if action.ToolName == "" {
		step.Reflection = "no action parsed from LLM response"
		return step, false, nil
	}

	// 4. Ethics check
	if e.ethics != nil {
		actionStr := fmt.Sprintf("%s %s", action.ToolName, action.ToolInput)
		decision, level, reason := e.ethics.Check(e.agentID, task.ID, actionStr)
		if decision == "deny" {
			step.Reflection = fmt.Sprintf("⛔ action blocked by ethics engine (%s): %s", level, reason)
			return step, false, nil
		}
	}

	// 5. Act — execute with retry for transient failures
	result, retries, err := e.actWithRetry(ctx, action)
	step.ToolRetries = retries
	if err != nil {
		step.Reflection = fmt.Sprintf("action failed (after %d retries): %s", retries, err)
		return step, false, nil
	}

	// 6. Truncate large results to fit context window
	if len(result) > e.config.MaxContextChars/2 {
		result = result[:e.config.MaxContextChars/2] + "\n... (truncated)"
	}

	step.Reflection = result
	return step, false, nil
}

// observe builds the current context for the LLM.
func (e *Engine) observe(task *Task, stepNum, maxSteps int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Task: %s\n", task.Description))
	sb.WriteString(fmt.Sprintf("Progress: step %d/%d\n", stepNum+1, maxSteps))

	if len(task.Steps) > 0 {
		last := task.Steps[len(task.Steps)-1]
		sb.WriteString(fmt.Sprintf("Last action: %s\n", truncateStr(last.Action, 200)))
		sb.WriteString(fmt.Sprintf("Last result: %s\n", truncateStr(last.Reflection, 500)))
	}

	// Urgency hint when approaching step limit
	remaining := maxSteps - stepNum
	if remaining <= 3 {
		sb.WriteString(fmt.Sprintf("\n⚠️ Only %d steps remaining. Wrap up and provide your final answer.\n", remaining))
	}

	return sb.String()
}

// think builds the prompt and calls the LLM.
func (e *Engine) think(ctx context.Context, task *Task, observation string, stepNum, maxSteps int) (string, int, error) {
	// Build dynamic tool descriptions
	var toolLines []string
	for _, t := range e.tools {
		toolLines = append(toolLines, fmt.Sprintf("- %s: %s", t.Name(), t.Description()))
	}
	toolDescs := strings.Join(toolLines, "\n")

	urgency := ""
	remaining := maxSteps - stepNum
	if remaining <= 3 {
		urgency = "\n\n⚠️ YOU ARE RUNNING LOW ON STEPS. Provide your final answer NOW using COMPLETE:"
	}

	systemPrompt := fmt.Sprintf(`You are an autonomous AI agent executing a task.

Available tools:
%s

Respond in this exact format:

THOUGHT: <your reasoning>
ACTION: <tool_name> <input>

Or if the task is complete:

THOUGHT: <your reasoning>
COMPLETE: <your complete final answer — can be multiple lines, include ALL content>

Rules:
- Execute one action at a time
- COMPLETE must contain your FULL answer (not just a summary header)
- If a tool fails, try a different approach
- Be thorough but efficient%s`, toolDescs, urgency)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: observation},
	}

	// Sliding window: summarize old steps, keep recent in detail
	recentN := e.config.RecentSteps
	steps := task.Steps

	if len(steps) > recentN {
		// Summarize older steps
		oldSteps := steps[:len(steps)-recentN]
		summary := summarizeSteps(oldSteps)
		messages = append(messages, llm.Message{
			Role: "user",
			Content: fmt.Sprintf("Summary of previous %d steps:\n%s", len(oldSteps), summary),
		})
		steps = steps[len(steps)-recentN:]
	}

	// Add recent steps as conversation turns
	budgetChars := e.config.MaxContextChars
	usedChars := len(systemPrompt) + len(observation)

	for _, s := range steps {
		thoughtMsg := truncateStr(s.Thought, 1000)
		reflectMsg := truncateStr(s.Reflection, 1000)
		stepChars := len(thoughtMsg) + len(reflectMsg) + 20

		if usedChars+stepChars > budgetChars {
			break // stop adding history to stay within budget
		}
		usedChars += stepChars

		messages = append(messages,
			llm.Message{Role: "assistant", Content: thoughtMsg},
			llm.Message{Role: "user", Content: fmt.Sprintf("Result: %s", reflectMsg)},
		)
	}

	resp, err := e.llm.Chat(ctx, messages)
	if err != nil {
		return "", 0, err
	}

	tokens := resp.PromptTokens + resp.CompletionTokens
	return resp.Content, tokens, nil
}

// actWithRetry executes a tool with retry for transient failures.
func (e *Engine) actWithRetry(ctx context.Context, action parsedAction) (string, int, error) {
	tool, ok := e.tools[action.ToolName]
	if !ok {
		return "", 0, fmt.Errorf("unknown tool: %s (available: %s)", action.ToolName, e.toolNames())
	}

	maxRetries := e.config.ToolMaxRetries
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Per-tool timeout
		toolCtx, cancel := context.WithTimeout(ctx, e.config.ToolTimeout)
		result, err := tool.Execute(toolCtx, action.ToolInput)
		cancel()

		if err == nil {
			return result, attempt, nil
		}

		lastErr = err

		// Don't retry on context cancellation or non-transient errors
		if ctx.Err() != nil {
			return "", attempt, err
		}
		if !isTransientToolError(err) {
			return "", attempt, err
		}

		if attempt < maxRetries {
			// Exponential backoff: 1s, 2s
			select {
			case <-ctx.Done():
				return "", attempt, ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}

	return "", maxRetries, lastErr
}

// forceComplete asks the LLM to produce a final answer from accumulated context.
func (e *Engine) forceComplete(ctx context.Context, task *Task) string {
	if e.llm == nil {
		return ""
	}

	// Build summary of what's been done
	var stepSummary string
	for i, s := range task.Steps {
		if s.Reflection != "" {
			stepSummary += fmt.Sprintf("Step %d result: %s\n", i+1, truncateStr(s.Reflection, 300))
		}
	}

	prompt := fmt.Sprintf(`You have been working on this task but got stuck in a loop.

Task: %s

Work done so far:
%s

Based on ALL the work above, provide your COMPLETE final answer now.
Include everything relevant from your work. Do NOT just repeat the task description.

COMPLETE:`, task.Description, truncateStr(stepSummary, 4000))

	resp, err := e.llm.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return ""
	}

	// The response IS the result (we already said COMPLETE: in the prompt)
	return strings.TrimSpace(resp.Content)
}

// extractPartialResult tries to get a useful result when max steps exceeded.
func (e *Engine) extractPartialResult(task *Task) string {
	// Look for the most substantial reflection (likely the best partial answer)
	var best string
	for _, s := range task.Steps {
		if len(s.Reflection) > len(best) && !strings.HasPrefix(s.Reflection, "action failed") {
			best = s.Reflection
		}
	}

	// If we have accumulated tool results, combine them
	if best == "" || len(best) < 50 {
		var parts []string
		for _, s := range task.Steps {
			if s.Reflection != "" && !strings.HasPrefix(s.Reflection, "no action") && !strings.HasPrefix(s.Reflection, "action failed") {
				parts = append(parts, s.Reflection)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n")
		}
	}

	return best
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func (e *Engine) toolNames() string {
	var names []string
	for name := range e.tools {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// summarizeSteps compresses older steps into a brief summary.
func summarizeSteps(steps []Step) string {
	var sb strings.Builder
	for i, s := range steps {
		if s.Action != "" {
			action := truncateStr(s.Action, 80)
			result := truncateStr(s.Reflection, 80)
			sb.WriteString(fmt.Sprintf("  %d. %s → %s\n", i+1, action, result))
		}
	}
	if sb.Len() == 0 {
		return "(no actions taken)"
	}
	return sb.String()
}

// isTransientToolError checks if a tool error is worth retrying.
func isTransientToolError(err error) bool {
	msg := strings.ToLower(err.Error())
	transient := []string{
		"timeout", "timed out", "connection refused", "connection reset",
		"temporary failure", "too many requests", "rate limit", "503", "429",
	}
	for _, t := range transient {
		if strings.Contains(msg, t) {
			return true
		}
	}
	return false
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
