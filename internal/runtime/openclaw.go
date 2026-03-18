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

// OpenClaw wraps an openclaw agent as a Spore runtime.
// It sends tasks via `openclaw system event` or directly to a session.
type OpenClaw struct {
	BinPath    string // path to openclaw binary
	SessionKey string // target session key (optional)
}

// NewOpenClaw creates an OpenClaw runtime with defaults.
func NewOpenClaw() *OpenClaw {
	return &OpenClaw{
		BinPath: "openclaw",
	}
}

func (o *OpenClaw) Info() Info {
	return Info{
		Name:    "openclaw",
		Version: "auto",
		Capabilities: []Capability{
			{Name: "general", Description: "General-purpose autonomous agent", Tags: []string{"general", "research", "coding", "shell"}},
			{Name: "web", Description: "Web browsing and research", Tags: []string{"web", "research"}},
			{Name: "messaging", Description: "Send messages across channels", Tags: []string{"messaging"}},
		},
		MaxConcurrent: 5,
	}
}

func (o *OpenClaw) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	// Use openclaw system event to inject a task
	args := []string{"system", "event", "--text", task.Description, "--mode", "now"}
	if o.SessionKey != "" {
		args = append(args, "--session", o.SessionKey)
	}

	cmd := exec.CommandContext(ctx, o.BinPath, args...)
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

func (o *OpenClaw) Healthy(ctx context.Context) error {
	_, err := exec.LookPath(o.BinPath)
	return err
}

func (o *OpenClaw) Close() error { return nil }
