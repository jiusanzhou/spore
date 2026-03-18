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
	"testing"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
)

// mockProvider implements llm.Provider for testing.
type mockProvider struct {
	responses []string
	callCount int
}

func (m *mockProvider) Chat(ctx context.Context, messages []llm.Message) (*llm.Response, error) {
	if m.callCount >= len(m.responses) {
		return &llm.Response{Content: "THOUGHT: nothing left\nCOMPLETE: done"}, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return &llm.Response{Content: resp}, nil
}

func (m *mockProvider) Model() string { return "mock" }

// mockEthics implements EthicsChecker for testing.
type mockEthics struct {
	denyAction string
}

func (m *mockEthics) Check(agentID, taskID, action string) (string, string, string) {
	if m.denyAction != "" && action == m.denyAction {
		return "deny", "L0", "test: action denied"
	}
	return "allow", "L1", "all checks passed"
}

func TestEngine_SimpleComplete(t *testing.T) {
	provider := &mockProvider{
		responses: []string{
			"THOUGHT: This is straightforward.\nCOMPLETE: the answer is 42",
		},
	}
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}
	defer store.Close()

	eng := New(provider, store)
	task := &Task{
		ID:          "test-1",
		Description: "what is the answer?",
		State:       TaskPending,
	}

	err = eng.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if task.State != TaskCompleted {
		t.Errorf("expected state %s, got %s", TaskCompleted, task.State)
	}
	if task.Result != "the answer is 42" {
		t.Errorf("expected result 'the answer is 42', got %q", task.Result)
	}
	if len(task.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(task.Steps))
	}
}

func TestEngine_ToolExecution(t *testing.T) {
	provider := &mockProvider{
		responses: []string{
			"THOUGHT: Let me run a command.\nACTION: shell echo hello",
			"THOUGHT: Got the result.\nCOMPLETE: command output was hello",
		},
	}
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}
	defer store.Close()

	eng := New(provider, store)
	eng.RegisterTool(&ShellTool{})

	task := &Task{
		ID:          "test-2",
		Description: "run echo hello",
		State:       TaskPending,
	}

	err = eng.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if task.State != TaskCompleted {
		t.Errorf("expected state %s, got %s", TaskCompleted, task.State)
	}
	if len(task.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(task.Steps))
	}
	// First step should have the shell output
	if task.Steps[0].Reflection != "hello\n" {
		t.Errorf("expected reflection 'hello\\n', got %q", task.Steps[0].Reflection)
	}
}

func TestEngine_UnknownTool(t *testing.T) {
	provider := &mockProvider{
		responses: []string{
			"THOUGHT: try unknown tool\nACTION: nonexistent do something",
			"THOUGHT: that failed, let me complete\nCOMPLETE: done",
		},
	}
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}
	defer store.Close()

	eng := New(provider, store)
	task := &Task{
		ID:          "test-3",
		Description: "test unknown tool",
		State:       TaskPending,
	}

	err = eng.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	// Should have recovered and completed
	if task.State != TaskCompleted {
		t.Errorf("expected state %s, got %s", TaskCompleted, task.State)
	}
}

func TestEngine_MaxSteps(t *testing.T) {
	// Provider that never completes
	responses := make([]string, 25)
	for i := range responses {
		responses[i] = fmt.Sprintf("THOUGHT: step %d\nACTION: shell echo step", i)
	}
	provider := &mockProvider{responses: responses}

	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}
	defer store.Close()

	eng := New(provider, store)
	eng.RegisterTool(&ShellTool{})

	task := &Task{
		ID:          "test-4",
		Description: "never ending task",
		State:       TaskPending,
	}

	err = eng.Run(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for max steps exceeded")
	}
	if task.State != TaskFailed {
		t.Errorf("expected state %s, got %s", TaskFailed, task.State)
	}
}

func TestEngine_ContextCancellation(t *testing.T) {
	provider := &mockProvider{
		responses: []string{
			"THOUGHT: working\nACTION: shell sleep 100",
		},
	}
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}
	defer store.Close()

	eng := New(provider, store)
	eng.RegisterTool(&ShellTool{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	task := &Task{
		ID:          "test-5",
		Description: "should be cancelled",
		State:       TaskPending,
	}

	err = eng.Run(ctx, task)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if task.State != TaskFailed {
		t.Errorf("expected state %s, got %s", TaskFailed, task.State)
	}
}

func TestEngine_EthicsBlocksAction(t *testing.T) {
	provider := &mockProvider{
		responses: []string{
			"THOUGHT: delete everything\nACTION: shell rm -rf /",
			"THOUGHT: that was blocked, let me complete\nCOMPLETE: aborted",
		},
	}
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("creating memory store: %v", err)
	}
	defer store.Close()

	eng := New(provider, store)
	eng.RegisterTool(&ShellTool{})
	eng.SetEthics(&mockEthics{denyAction: "shell rm -rf /"})

	task := &Task{
		ID:          "test-6",
		Description: "try destructive action",
		State:       TaskPending,
	}

	err = eng.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if task.State != TaskCompleted {
		t.Errorf("expected state %s, got %s", TaskCompleted, task.State)
	}
	// First step should show the ethics block
	if len(task.Steps) < 1 {
		t.Fatal("expected at least 1 step")
	}
	step := task.Steps[0]
	if step.Reflection != "⛔ action blocked by ethics engine (L0): test: action denied" {
		t.Errorf("expected ethics block reflection, got %q", step.Reflection)
	}
}
