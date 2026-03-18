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

// ExecRuntime is a generic CLI wrapper that turns any command-line tool
// into a Spore runtime. Use this for custom agent frameworks.
//
// Example config:
//
//	[[runtime.exec]]
//	name = "my-agent"
//	command = "/usr/local/bin/my-agent"
//	args = ["--auto", "--quiet"]
//	task_flag = "--task"  # the task description is appended here
//	tags = ["coding", "research"]
type ExecRuntime struct {
	name     string
	command  string
	args     []string
	taskFlag string   // flag to pass task description, e.g. "--task"
	tags     []string
}

// ExecConfig configures an ExecRuntime.
type ExecConfig struct {
	Name     string   `toml:"name"`
	Command  string   `toml:"command"`
	Args     []string `toml:"args"`
	TaskFlag string   `toml:"task_flag"`
	Tags     []string `toml:"tags"`
}

// NewExecRuntime creates a generic CLI runtime.
func NewExecRuntime(cfg ExecConfig) *ExecRuntime {
	return &ExecRuntime{
		name:     cfg.Name,
		command:  cfg.Command,
		args:     cfg.Args,
		taskFlag: cfg.TaskFlag,
		tags:     cfg.Tags,
	}
}

func (e *ExecRuntime) Info() Info {
	caps := []Capability{{
		Name:        "general",
		Description: fmt.Sprintf("Custom agent: %s", e.name),
		Tags:        e.tags,
	}}
	return Info{
		Name:         e.name,
		Version:      "custom",
		Capabilities: caps,
		MaxConcurrent: 1,
	}
}

func (e *ExecRuntime) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	args := make([]string, len(e.args))
	copy(args, e.args)

	if e.taskFlag != "" {
		args = append(args, e.taskFlag, task.Description)
	} else {
		args = append(args, task.Description)
	}

	cmd := exec.CommandContext(ctx, e.command, args...)
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

func (e *ExecRuntime) Healthy(ctx context.Context) error {
	_, err := exec.LookPath(e.command)
	if err != nil {
		return fmt.Errorf("%s not found in PATH: %w", e.command, err)
	}
	return nil
}

func (e *ExecRuntime) Close() error { return nil }

// ParseExecRuntimes creates ExecRuntimes from config entries.
func ParseExecRuntimes(cfgs []ExecConfig) []*ExecRuntime {
	rts := make([]*ExecRuntime, len(cfgs))
	for i, cfg := range cfgs {
		rts[i] = NewExecRuntime(cfg)
	}
	return rts
}
