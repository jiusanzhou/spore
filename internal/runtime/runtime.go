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

// Package runtime defines the interface for pluggable agent execution backends.
//
// Spore itself is a coordination protocol — it doesn't execute tasks directly.
// Instead, it delegates execution to a Runtime, which can be any agent framework:
//
//   - Claude Code (claude CLI)
//   - Codex (codex CLI)
//   - OpenClaw (openclaw CLI)
//   - Built-in LLM loop (the default engine)
//   - Custom implementations via the Runtime interface
//
// This makes Spore a universal coordination layer for heterogeneous agent swarms.
package runtime

import (
	"context"
)

// TaskInput is what gets sent to a runtime for execution.
type TaskInput struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Context     string            `json:"context,omitempty"`     // additional context from memory/history
	WorkDir     string            `json:"work_dir,omitempty"`    // working directory
	Env         map[string]string `json:"env,omitempty"`         // environment variables
	Timeout     int               `json:"timeout,omitempty"`     // seconds, 0 = no limit
}

// TaskOutput is what comes back from a runtime.
type TaskOutput struct {
	Success bool   `json:"success"`
	Result  string `json:"result"`
	Error   string `json:"error,omitempty"`
	Logs    string `json:"logs,omitempty"`     // execution logs
	Tokens  int    `json:"tokens,omitempty"`   // tokens consumed (if applicable)
	Cost    float64 `json:"cost,omitempty"`    // cost in USD (if applicable)
}

// Capability describes what a runtime can do.
type Capability struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"` // e.g. ["coding", "research", "shell"]
}

// Info describes a runtime.
type Info struct {
	Name         string       `json:"name"`          // e.g. "claude-code", "codex", "openclaw"
	Version      string       `json:"version"`
	Capabilities []Capability `json:"capabilities"`
	MaxConcurrent int         `json:"max_concurrent"` // 0 = unlimited
}

// Runtime is the interface that all agent execution backends implement.
// Spore coordinates; Runtimes execute.
type Runtime interface {
	// Info returns metadata about this runtime.
	Info() Info

	// Execute runs a task and returns the result.
	// This is blocking — for async, the caller wraps in a goroutine.
	Execute(ctx context.Context, task TaskInput) (*TaskOutput, error)

	// Healthy checks if the runtime is available.
	Healthy(ctx context.Context) error

	// Close releases resources.
	Close() error
}
