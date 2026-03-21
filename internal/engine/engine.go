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
}

// Step records one cycle of the agent loop.
type Step struct {
	Observation string
	Thought     string
	Action      string
	Reflection  string
	Timestamp   time.Time
}

// EthicsChecker is the interface the engine uses to validate actions.
// This decouples the engine from the ethics package.
type EthicsChecker interface {
	Check(agentID, taskID, action string) (decision string, level string, reason string)
}

// Engine drives the Observe → Think → Act → Reflect loop.
type Engine struct {
	llm     llm.Provider
	memory  memory.Store
	tools   map[string]Tool
	ethics  EthicsChecker
	agentID string // for ethics audit
}

// Tool is something the agent can invoke during Act phase.
type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, input string) (string, error)
}

// New creates a new task engine.
func New(provider llm.Provider, store memory.Store) *Engine {
	return &Engine{
		llm:    provider,
		memory: store,
		tools:  make(map[string]Tool),
	}
}

// SetEthics attaches an ethics checker to the engine.
func (e *Engine) SetEthics(checker EthicsChecker) {
	e.ethics = checker
}

// SetAgentID sets the agent identifier for ethics audit logging.
func (e *Engine) SetAgentID(id string) {
	e.agentID = id
}

// SetMemory replaces the memory store (e.g., when upgrading from :memory: to file).
func (e *Engine) SetMemory(store memory.Store) {
	e.memory = store
}

// RegisterTool adds a tool the agent can use.
func (e *Engine) RegisterTool(t Tool) {
	e.tools[t.Name()] = t
}

// Run executes a task through the full agent loop.
func (e *Engine) Run(ctx context.Context, task *Task) error {
	task.State = TaskRunning
	task.UpdatedAt = time.Now()

	const maxSteps = 20

	for i := 0; i < maxSteps; i++ {
		select {
		case <-ctx.Done():
			task.State = TaskFailed
			task.Error = "context cancelled"
			return ctx.Err()
		default:
		}

		step, done, err := e.tick(ctx, task)
		if err != nil {
			task.State = TaskFailed
			task.Error = err.Error()
			task.UpdatedAt = time.Now()
			return err
		}

		task.Steps = append(task.Steps, *step)
		task.UpdatedAt = time.Now()

		if done {
			task.State = TaskCompleted
			return nil
		}
	}

	task.State = TaskFailed
	task.Error = "max steps exceeded"
	return fmt.Errorf("task exceeded %d steps", maxSteps)
}

// tick executes one cycle: Observe → Think → Act → Reflect.
func (e *Engine) tick(ctx context.Context, task *Task) (*Step, bool, error) {
	step := &Step{Timestamp: time.Now()}

	// 1. Observe — gather current context
	observation := e.observe(task)
	step.Observation = observation

	// 2. Think — ask LLM what to do next
	thought, err := e.think(ctx, task, observation)
	if err != nil {
		return step, false, fmt.Errorf("think: %w", err)
	}
	step.Thought = thought

	// 3. Parse the LLM response for action or completion
	action, done := parseAction(thought)
	step.Action = action.Raw

	if done {
		task.Result = action.Result
		return step, true, nil
	}

	// No action parsed — LLM didn't follow format, treat as thinking-only step
	if action.ToolName == "" {
		step.Reflection = "no action parsed from LLM response"
		return step, false, nil
	}

	// 4. Ethics check — validate before execution
	if e.ethics != nil {
		actionStr := fmt.Sprintf("%s %s", action.ToolName, action.ToolInput)
		decision, level, reason := e.ethics.Check(e.agentID, task.ID, actionStr)
		if decision == "deny" {
			step.Reflection = fmt.Sprintf("⛔ action blocked by ethics engine (%s): %s", level, reason)
			return step, false, nil
		}
	}

	// 5. Act — execute the chosen action
	result, err := e.act(ctx, action)
	if err != nil {
		step.Reflection = fmt.Sprintf("action failed: %s", err)
		// don't return error, let agent retry
		return step, false, nil
	}

	// 6. Reflect — record what happened
	step.Reflection = result
	return step, false, nil
}

func (e *Engine) observe(task *Task) string {
	// Build observation from task state + recent steps
	obs := fmt.Sprintf("Task: %s\nState: %s\n", task.Description, task.State)
	if len(task.Steps) > 0 {
		last := task.Steps[len(task.Steps)-1]
		obs += fmt.Sprintf("Last action: %s\nLast result: %s\n", last.Action, last.Reflection)
	}
	return obs
}

func (e *Engine) think(ctx context.Context, task *Task, observation string) (string, error) {
	// Build tool descriptions
	toolDescs := ""
	for _, t := range e.tools {
		toolDescs += fmt.Sprintf("- %s: %s\n", t.Name(), t.Description())
	}

	systemPrompt := fmt.Sprintf(`You are an autonomous AI agent executing a task.

Available tools:
%s
Respond in this exact format:

THOUGHT: <your reasoning>
ACTION: <tool_name> <input>

Or if the task is complete:

THOUGHT: <your reasoning>
COMPLETE: <final result>

Be concise. Execute one action at a time.`, toolDescs)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: observation},
	}

	// Add recent step history as context
	for _, s := range recentSteps(task.Steps, 5) {
		messages = append(messages,
			llm.Message{Role: "assistant", Content: s.Thought},
			llm.Message{Role: "user", Content: fmt.Sprintf("Result: %s", s.Reflection)},
		)
	}

	resp, err := e.llm.Chat(ctx, messages)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

func (e *Engine) act(ctx context.Context, action parsedAction) (string, error) {
	tool, ok := e.tools[action.ToolName]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", action.ToolName)
	}
	return tool.Execute(ctx, action.ToolInput)
}

func recentSteps(steps []Step, n int) []Step {
	if len(steps) <= n {
		return steps
	}
	return steps[len(steps)-n:]
}
