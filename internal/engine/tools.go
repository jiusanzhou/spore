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
	"os/exec"
	"strings"
)

// ShellTool executes shell commands. Use with caution.
type ShellTool struct {
	// AllowList restricts which commands can be run (empty = allow all).
	AllowList []string
	WorkDir   string
}

func (t *ShellTool) Name() string        { return "shell" }
func (t *ShellTool) Description() string { return "Execute a shell command and return output" }

func (t *ShellTool) Execute(ctx context.Context, input string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("empty command")
	}

	if len(t.AllowList) > 0 {
		allowed := false
		for _, prefix := range t.AllowList {
			if strings.HasPrefix(input, prefix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", fmt.Errorf("command not in allow list: %s", input)
		}
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", input)
	if t.WorkDir != "" {
		cmd.Dir = t.WorkDir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("command failed: %w\noutput: %s", err, string(out))
	}
	return string(out), nil
}

// WebSearchTool searches the web (placeholder).
type WebSearchTool struct{}

func (t *WebSearchTool) Name() string        { return "search" }
func (t *WebSearchTool) Description() string { return "Search the web for information" }

func (t *WebSearchTool) Execute(ctx context.Context, input string) (string, error) {
	// TODO: integrate brave search or other search API
	return fmt.Sprintf("[search results for: %s] (not yet implemented)", input), nil
}

// MemoryTool reads/writes agent memory.
type MemoryTool struct {
	Store interface {
		Get(key string) (interface{ }, error)
		Put(entry interface{}) error
	}
}

func (t *MemoryTool) Name() string        { return "memory" }
func (t *MemoryTool) Description() string { return "Read or write to agent memory. Use 'get <key>' or 'set <key> <value>'" }

func (t *MemoryTool) Execute(ctx context.Context, input string) (string, error) {
	// TODO: implement memory read/write
	return fmt.Sprintf("[memory operation: %s] (not yet implemented)", input), nil
}

// DelegateTool sends a task to another agent via the message bus.
type DelegateTool struct {
	SendFunc func(to, taskDesc string) error
}

func (t *DelegateTool) Name() string        { return "delegate" }
func (t *DelegateTool) Description() string { return "Delegate a sub-task to another agent. Usage: delegate <agent_id> <task description>" }

func (t *DelegateTool) Execute(ctx context.Context, input string) (string, error) {
	parts := strings.SplitN(input, " ", 2)
	if len(parts) < 2 {
		return "", fmt.Errorf("usage: delegate <agent_id> <task>")
	}
	if t.SendFunc == nil {
		return "", fmt.Errorf("message bus not configured")
	}
	if err := t.SendFunc(parts[0], parts[1]); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task delegated to %s", parts[0]), nil
}
