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

// Codex wraps the `codex` CLI as a Spore runtime.
type Codex struct {
	BinPath string // path to codex binary, default "codex"
	Mode    string // "full-auto" or "yolo", default "full-auto"
}

// NewCodex creates a Codex runtime with defaults.
func NewCodex() *Codex {
	return &Codex{
		BinPath: "codex",
		Mode:    "full-auto",
	}
}

func (c *Codex) Info() Info {
	return Info{
		Name:    "codex",
		Version: "auto",
		Capabilities: []Capability{
			{Name: "coding", Description: "Autonomous code generation and editing", Tags: []string{"coding", "shell"}},
		},
		MaxConcurrent: 2,
	}
}

func (c *Codex) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	args := []string{"exec"}
	if c.Mode == "yolo" {
		args = append(args, "--yolo")
	} else {
		args = append(args, "--full-auto")
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

func (c *Codex) Healthy(ctx context.Context) error {
	_, err := exec.LookPath(c.BinPath)
	return err
}

func (c *Codex) Close() error { return nil }

// OpenCode wraps the `opencode` CLI as a Spore runtime.
type OpenCode struct {
	BinPath string
}

func NewOpenCode() *OpenCode {
	return &OpenCode{BinPath: "opencode"}
}

func (o *OpenCode) Info() Info {
	return Info{
		Name:    "opencode",
		Version: "auto",
		Capabilities: []Capability{
			{Name: "coding", Description: "Code generation and editing", Tags: []string{"coding", "shell"}},
		},
		MaxConcurrent: 2,
	}
}

func (o *OpenCode) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	cmd := exec.CommandContext(ctx, o.BinPath, "run", task.Description)
	if task.WorkDir != "" {
		cmd.Dir = task.WorkDir
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

func (o *OpenCode) Healthy(ctx context.Context) error {
	_, err := exec.LookPath(o.BinPath)
	return err
}

func (o *OpenCode) Close() error { return nil }
