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
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ClaudeCode wraps the `claude` CLI as a Spore runtime.
type ClaudeCode struct {
	BinPath        string // path to claude binary, default "claude"
	PermissionMode string // default "bypassPermissions"
	Model          string // optional model override
}

// NewClaudeCode creates a Claude Code runtime with defaults.
func NewClaudeCode() *ClaudeCode {
	return &ClaudeCode{
		BinPath:        "claude",
		PermissionMode: "bypassPermissions",
	}
}

func (c *ClaudeCode) Info() Info {
	return Info{
		Name:    "claude-code",
		Version: "auto",
		Capabilities: []Capability{
			{Name: "coding", Description: "Write, review, and refactor code", Tags: []string{"coding", "shell"}},
			{Name: "research", Description: "Research and analysis", Tags: []string{"research"}},
			{Name: "general", Description: "General task execution", Tags: []string{"general"}},
		},
		MaxConcurrent: 3,
	}
}

func (c *ClaudeCode) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	args := []string{
		"--permission-mode", c.PermissionMode,
		"--print",
		"--output-format", "text",
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, task.Description)

	cmd := exec.CommandContext(ctx, c.BinPath, args...)
	if task.WorkDir != "" {
		cmd.Dir = task.WorkDir
	}
	for k, v := range task.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	output := &TaskOutput{
		Success: err == nil,
		Result:  stdout.String(),
		Logs:    fmt.Sprintf("duration: %s\nstderr: %s", duration, stderr.String()),
	}
	if err != nil {
		output.Error = fmt.Sprintf("%v: %s", err, stderr.String())
	}
	return output, nil
}

func (c *ClaudeCode) Healthy(ctx context.Context) error {
	_, err := exec.LookPath(c.BinPath)
	return err
}

func (c *ClaudeCode) Close() error { return nil }
