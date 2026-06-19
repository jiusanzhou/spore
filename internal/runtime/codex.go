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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Codex adapter.
//
// We invoke `codex exec --json` and parse the JSONL event stream so spore
// can observe per-tool-call progress, real token accounting, and partial
// agent messages — instead of black-boxing through plain stdout.
//
// Schema (codex-cli 0.x; spec at developers.openai.com/codex/noninteractive
// and openai/codex/sdk/typescript/src/events.ts):
//
//   { type: "thread.started", thread_id }
//   { type: "turn.started" }
//   { type: "item.started",   item: { id, type, ... } }
//   { type: "item.updated",   item: { id, type, ... } }
//   { type: "item.completed", item: { id, type: "agent_message"|"reasoning"|
//                                     "command_execution"|"file_change"|
//                                     "mcp_tool_call"|"web_search"|
//                                     "plan_update", ... } }
//   { type: "turn.completed", usage: { input_tokens, cached_input_tokens,
//                                       output_tokens } }
//   { type: "turn.failed",    error: { message } }
//   { type: "error",          message }    // stream-level / transient
//
// Codex doesn't emit per-turn cost; we leave CostUSD=0 (callers can compute
// it from token counts and the model's pricing if they care).
// ─────────────────────────────────────────────────────────────────────────────

// Codex wraps the `codex` CLI as a Spore runtime.
type Codex struct {
	BinPath string // path to codex binary, default "codex"
	Sandbox string // "workspace-write" (default) | "danger-full-access" | ""
}

// NewCodex creates a Codex runtime with defaults.
func NewCodex() *Codex {
	return &Codex{
		BinPath: "codex",
		Sandbox: "workspace-write",
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

// Execute runs a task. Forwards to ExecuteStream with a nil handler so
// non-streaming callers still benefit from per-tool accounting.
func (c *Codex) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	return c.ExecuteStream(ctx, task, nil)
}

// Compile-time check: Codex satisfies StreamingRuntime.
var _ StreamingRuntime = (*Codex)(nil)

// ExecuteStream runs codex exec --json, parses the JSONL stream, and forwards
// each event to onEvent (when non-nil).
func (c *Codex) ExecuteStream(ctx context.Context, task TaskInput, onEvent EventHandler) (*TaskOutput, error) {
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	if c.Sandbox != "" {
		args = append(args, "--sandbox", c.Sandbox)
	}
	args = append(args, task.Description)

	cmd := exec.CommandContext(ctx, c.BinPath, args...)
	if task.WorkDir != "" {
		cmd.Dir = task.WorkDir
	}
	for k, v := range task.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrPrefixWriter{prefix: "[codex stderr] ", w: &stderrBuf}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex: %w", err)
	}

	parsed, parseErr := parseCodexStream(stdout, onEvent)
	waitErr := cmd.Wait()
	duration := time.Since(start)

	output := &TaskOutput{
		Success: waitErr == nil && parsed.errMsg == "" && !parsed.fatalErr,
		Result:  parsed.finalText,
		Tokens:  int(parsed.inputTokens + parsed.outputTokens),
		Logs: fmt.Sprintf("runtime=codex thread=%s tools=%d duration=%s tokens_in=%d tokens_out=%d cached=%d stderr=%s",
			parsed.threadID, parsed.toolCalls, duration,
			parsed.inputTokens, parsed.outputTokens, parsed.cachedTokens,
			truncateForLog(stderrBuf.String(), 500)),
	}

	switch {
	case waitErr != nil:
		output.Error = fmt.Sprintf("codex exited: %v; stderr: %s", waitErr, truncateForLog(stderrBuf.String(), 500))
	case parsed.errMsg != "":
		output.Error = parsed.errMsg
	case parseErr != nil && parsed.finalText == "":
		output.Error = fmt.Sprintf("stream parse: %v", parseErr)
	}

	if onEvent != nil {
		_ = onEvent(StreamEvent{
			Type:         EventComplete,
			Runtime:      "codex",
			Session:      parsed.threadID,
			Content:      parsed.finalText,
			InputTokens:  int(parsed.inputTokens),
			OutputTokens: int(parsed.outputTokens),
			CachedTokens: int(parsed.cachedTokens),
			DurationMS:   duration.Milliseconds(),
		})
	}

	return output, nil
}

func (c *Codex) Healthy(ctx context.Context) error {
	_, err := exec.LookPath(c.BinPath)
	return err
}

func (c *Codex) Close() error { return nil }

// ─────────────────────────────────────────────────────────────────────────────
// Codex stream parser
// ─────────────────────────────────────────────────────────────────────────────

type codexStreamSummary struct {
	threadID     string
	finalText    string
	toolCalls    int
	inputTokens  int64
	outputTokens int64
	cachedTokens int64
	errMsg       string
	fatalErr     bool
}

func parseCodexStream(r io.Reader, onEvent EventHandler) (codexStreamSummary, error) {
	sum := codexStreamSummary{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	emit := func(ev StreamEvent) error {
		if onEvent == nil {
			return nil
		}
		return onEvent(ev)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		obj, err := parseJSONLine(line)
		if err != nil {
			if emitErr := emit(StreamEvent{
				Type: EventError, Runtime: "codex",
				Content: err.Error(), Fatal: false,
			}); emitErr != nil {
				return sum, emitErr
			}
			continue
		}
		if obj == nil {
			continue
		}

		raw, _ := json.Marshal(obj)

		switch asString(obj, "type") {
		case "thread.started":
			sum.threadID = asString(obj, "thread_id")
			if err := emit(StreamEvent{
				Type:    EventInit,
				Runtime: "codex",
				Session: sum.threadID,
				Raw:     raw,
			}); err != nil {
				return sum, err
			}

		case "turn.started":
			// no-op informational; codex starts a new "turn" per agentic step.

		case "item.started", "item.updated", "item.completed":
			item := asMap(obj, "item")
			if item == nil {
				continue
			}
			itemType := asString(item, "type")
			evType := asString(obj, "type")

			switch itemType {
			case "agent_message":
				if evType == "item.completed" {
					text := asString(item, "text")
					if text != "" {
						sum.finalText = text // agent_message is the assistant's reply
						if err := emit(StreamEvent{
							Type:    EventThinking,
							Runtime: "codex",
							Session: sum.threadID,
							Content: text,
							Raw:     raw,
						}); err != nil {
							return sum, err
						}
					}
				}
			case "reasoning":
				if evType == "item.completed" {
					text := asString(item, "text")
					if text != "" {
						if err := emit(StreamEvent{
							Type:    EventThinking,
							Runtime: "codex",
							Session: sum.threadID,
							Content: "[reasoning] " + truncateForLog(text, 2000),
							Raw:     raw,
						}); err != nil {
							return sum, err
						}
					}
				}
			case "command_execution":
				switch evType {
				case "item.started":
					sum.toolCalls++
					if err := emit(StreamEvent{
						Type:      EventToolCall,
						Runtime:   "codex",
						Session:   sum.threadID,
						ToolName:  "command_execution",
						ToolInput: asString(item, "command"),
						Raw:       raw,
					}); err != nil {
						return sum, err
					}
				case "item.completed":
					exitCode := asInt(item, "exit_code")
					if err := emit(StreamEvent{
						Type:       EventToolResult,
						Runtime:    "codex",
						Session:    sum.threadID,
						ToolName:   "command_execution",
						ToolOutput: truncateForLog(asString(item, "aggregated_output"), 4000),
						ToolError:  exitCode != 0 || asString(item, "status") == "failed",
						Raw:        raw,
					}); err != nil {
						return sum, err
					}
				}
			case "file_change":
				if evType == "item.completed" {
					sum.toolCalls++
					if err := emit(StreamEvent{
						Type:     EventToolCall,
						Runtime:  "codex",
						Session:  sum.threadID,
						ToolName: "file_change",
						Content:  truncateForLog(asString(item, "summary"), 500),
						Raw:      raw,
					}); err != nil {
						return sum, err
					}
				}
			case "mcp_tool_call":
				if evType == "item.started" {
					sum.toolCalls++
					if err := emit(StreamEvent{
						Type:     EventToolCall,
						Runtime:  "codex",
						Session:  sum.threadID,
						ToolName: "mcp:" + asString(item, "tool_name"),
						Raw:      raw,
					}); err != nil {
						return sum, err
					}
				}
			case "web_search", "plan_update":
				if evType == "item.completed" {
					if err := emit(StreamEvent{
						Type:    EventThinking,
						Runtime: "codex",
						Session: sum.threadID,
						Content: fmt.Sprintf("[%s] %s", itemType,
							truncateForLog(asString(item, "text"), 500)),
						Raw: raw,
					}); err != nil {
						return sum, err
					}
				}
			}

		case "turn.completed":
			usage := asMap(obj, "usage")
			if usage != nil {
				sum.inputTokens += asInt(usage, "input_tokens")
				sum.outputTokens += asInt(usage, "output_tokens")
				sum.cachedTokens += asInt(usage, "cached_input_tokens")
			}

		case "turn.failed":
			errObj := asMap(obj, "error")
			if errObj != nil {
				sum.errMsg = asString(errObj, "message")
			} else {
				sum.errMsg = "turn failed (no detail)"
			}
			sum.fatalErr = true
			if err := emit(StreamEvent{
				Type:    EventError,
				Runtime: "codex",
				Session: sum.threadID,
				Content: sum.errMsg,
				Fatal:   true,
				Raw:     raw,
			}); err != nil {
				return sum, err
			}

		case "error":
			msg := asString(obj, "message")
			if msg == "" {
				msg = "unknown codex error"
			}
			fatal := !strings.Contains(strings.ToLower(msg), "reconnect")
			if fatal {
				sum.errMsg = msg
				sum.fatalErr = true
			}
			if err := emit(StreamEvent{
				Type:    EventError,
				Runtime: "codex",
				Session: sum.threadID,
				Content: msg,
				Fatal:   fatal,
				Raw:     raw,
			}); err != nil {
				return sum, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return sum, fmt.Errorf("scanner: %w", err)
	}
	return sum, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// OpenCode (kept on the basic Runtime API for now — its CLI doesn't yet
// expose a stable JSONL stream we'd bother parsing).
// ─────────────────────────────────────────────────────────────────────────────

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

	var stdout, stderr strings.Builder
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
