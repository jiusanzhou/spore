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

package runtime

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	aboxrt "go.zoe.im/agentbox/pkg/runtime"
)

// AboxAdapter wraps an agentbox pkg/runtime.Runtime as a Spore Runtime.
// It uses agentbox's BuildExecArgs for command construction and
// ParseStreamLine for output parsing, while Spore handles execution lifecycle.
type AboxAdapter struct {
	inner    aboxrt.Runtime
	caps     []Capability
	maxConc  int
}

// NewAboxAdapter creates a Spore Runtime backed by an agentbox Runtime.
func NewAboxAdapter(inner aboxrt.Runtime, caps []Capability, maxConcurrent int) *AboxAdapter {
	return &AboxAdapter{
		inner:   inner,
		caps:    caps,
		maxConc: maxConcurrent,
	}
}

func (a *AboxAdapter) Info() Info {
	return Info{
		Name:          a.inner.Name(),
		Version:       "auto",
		Capabilities:  a.caps,
		MaxConcurrent: a.maxConc,
	}
}

func (a *AboxAdapter) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	args := a.inner.BuildExecArgs(task.Description, false)
	if len(args) == 0 {
		return nil, fmt.Errorf("agentbox runtime %s: empty args", a.inner.Name())
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if task.WorkDir != "" {
		cmd.Dir = task.WorkDir
	}
	for k, v := range task.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Use pipe + ParseStreamLine for streaming output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", a.inner.Name(), err)
	}

	var result strings.Builder
	var finalResult string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		token, res, done := a.inner.ParseStreamLine(line)
		if token != "" {
			result.WriteString(token)
		}
		if done && res != "" {
			finalResult = res
		}
	}

	err = cmd.Wait()
	duration := time.Since(start)

	// Prefer parsed final result; fall back to accumulated tokens; fall back to raw output
	resultStr := finalResult
	if resultStr == "" {
		resultStr = result.String()
	}
	if resultStr == "" {
		resultStr = stderr.String() // last resort
	}

	output := &TaskOutput{
		Success: err == nil,
		Result:  resultStr,
		Logs:    fmt.Sprintf("duration: %s\nstderr: %s", duration, stderr.String()),
	}
	if err != nil {
		output.Error = fmt.Sprintf("%v: %s", err, stderr.String())
	}
	return output, nil
}

func (a *AboxAdapter) Healthy(ctx context.Context) error {
	bin := a.inner.BinaryName()
	if bin == "" {
		return nil // non-local runtimes are always "healthy"
	}
	_, err := exec.LookPath(bin)
	return err
}

func (a *AboxAdapter) Close() error { return nil }

// DefaultAboxAdapters returns Spore Runtimes for all agentbox-registered runtimes.
// Call this after agentbox's init() has populated the global registry.
func DefaultAboxAdapters() []Runtime {
	capMap := map[string][]Capability{
		"claude": {
			{Name: "coding", Description: "Code generation, review, refactoring", Tags: []string{"coding", "shell"}},
			{Name: "research", Description: "Research and analysis", Tags: []string{"research"}},
			{Name: "general", Description: "General task execution", Tags: []string{"general"}},
		},
		"codex": {
			{Name: "coding", Description: "Autonomous code generation", Tags: []string{"coding", "shell"}},
		},
		"gemini": {
			{Name: "coding", Description: "Code generation and analysis", Tags: []string{"coding"}},
			{Name: "general", Description: "General reasoning", Tags: []string{"general"}},
		},
		"aider": {
			{Name: "coding", Description: "AI pair programming", Tags: []string{"coding"}},
		},
		"opencode": {
			{Name: "coding", Description: "Code generation and editing", Tags: []string{"coding", "shell"}},
		},
		"goose": {
			{Name: "coding", Description: "Autonomous coding agent", Tags: []string{"coding", "shell"}},
		},
		"openhands": {
			{Name: "coding", Description: "Full-stack development", Tags: []string{"coding", "shell", "web"}},
		},
	}

	concMap := map[string]int{
		"claude": 3, "codex": 2, "gemini": 2, "aider": 1,
		"opencode": 2, "goose": 2, "openhands": 1,
	}

	var adapters []Runtime
	for _, info := range aboxrt.List() {
		caps := capMap[info.Name]
		if caps == nil {
			caps = []Capability{{Name: "general", Tags: []string{"general"}}}
		}
		maxC := concMap[info.Name]
		if maxC == 0 {
			maxC = 1
		}
		inner := aboxrt.Get(info.Name)
		if inner == nil {
			continue
		}
		adapters = append(adapters, NewAboxAdapter(inner, caps, maxC))
	}
	return adapters
}
