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
// Claude Code adapter.
//
// We invoke `claude --print --output-format stream-json --verbose` and parse
// the JSONL stream so spore can observe per-tool-call progress, real token
// accounting, and partial agent messages — instead of black-boxing through
// plain stdout.
//
// Schema (claude-code 2.x):
//
//   { type: "system", subtype: "init", session_id, model, tools[], ... }
//   { type: "assistant", message: { content: [{type: "text"|"tool_use", ...}], ... }, session_id }
//   { type: "user", message: { content: [{type: "tool_result", tool_use_id, content, is_error}], ... } }
//   { type: "result", subtype: "success"|..., result, duration_ms, num_turns,
//                     total_cost_usd, usage: {input_tokens, output_tokens, ...},
//                     is_error, session_id }
//
// Implements both Runtime (one-shot Execute) and StreamingRuntime (live
// EventHandler callbacks).
// ─────────────────────────────────────────────────────────────────────────────

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

// Execute is the basic, blocking interface. Forwards to ExecuteStream with
// nil handler so non-streaming callers still benefit from per-tool accounting
// and proper token/cost data in TaskOutput.
func (c *ClaudeCode) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	return c.ExecuteStream(ctx, task, nil)
}

// Compile-time check: ClaudeCode satisfies StreamingRuntime.
var _ StreamingRuntime = (*ClaudeCode)(nil)

// ExecuteStream runs claude --output-format stream-json, parses the JSONL
// stream, and forwards each event to onEvent (when non-nil). The returned
// TaskOutput carries the final accounting whether or not a handler was set.
func (c *ClaudeCode) ExecuteStream(ctx context.Context, task TaskInput, onEvent EventHandler) (*TaskOutput, error) {
	args := c.buildArgs(task)

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
	cmd.Stderr = &stderrPrefixWriter{prefix: "[claude stderr] ", w: &stderrBuf}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	parsed, parseErr := parseClaudeStream(stdout, onEvent)
	waitErr := cmd.Wait()
	duration := time.Since(start)

	output := &TaskOutput{
		Success: waitErr == nil && parsed.errMsg == "" && !parsed.fatalErr,
		Result:  parsed.finalText,
		Tokens:  int(parsed.inputTokens + parsed.outputTokens),
		Cost:    parsed.costUSD,
		Logs: fmt.Sprintf("runtime=claude-code session=%s tools=%d duration=%s tokens_in=%d tokens_out=%d cached_read=%d cost=%.4f stderr=%s",
			parsed.sessionID, parsed.toolCalls, duration,
			parsed.inputTokens, parsed.outputTokens, parsed.cachedTokens,
			parsed.costUSD, truncateForLog(stderrBuf.String(), 500)),
	}

	switch {
	case waitErr != nil:
		output.Error = fmt.Sprintf("claude exited: %v; stderr: %s", waitErr, truncateForLog(stderrBuf.String(), 500))
	case parsed.errMsg != "":
		output.Error = parsed.errMsg
	case parseErr != nil && parsed.finalText == "":
		output.Error = fmt.Sprintf("stream parse: %v", parseErr)
	}

	if onEvent != nil {
		_ = onEvent(StreamEvent{
			Type:         EventComplete,
			Runtime:      "claude-code",
			Session:      parsed.sessionID,
			Content:      parsed.finalText,
			InputTokens:  int(parsed.inputTokens),
			OutputTokens: int(parsed.outputTokens),
			CachedTokens: int(parsed.cachedTokens),
			CostUSD:      parsed.costUSD,
			DurationMS:   duration.Milliseconds(),
		})
	}

	return output, nil
}

// buildArgs assembles the CLI argv for one task.
func (c *ClaudeCode) buildArgs(task TaskInput) []string {
	args := []string{
		"--permission-mode", c.PermissionMode,
		"--print",
		"--output-format", "stream-json",
		// stream-json requires --verbose to be set explicitly on claude-code.
		"--verbose",
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	args = append(args, task.Description)
	return args
}

func (c *ClaudeCode) Healthy(ctx context.Context) error {
	_, err := exec.LookPath(c.BinPath)
	return err
}

func (c *ClaudeCode) Close() error { return nil }

// ─────────────────────────────────────────────────────────────────────────────
// Claude Code stream parser
// ─────────────────────────────────────────────────────────────────────────────

type claudeStreamSummary struct {
	sessionID    string
	finalText    string
	toolCalls    int
	inputTokens  int64
	outputTokens int64
	cachedTokens int64
	costUSD      float64
	errMsg       string
	fatalErr     bool
}

// parseClaudeStream reads the JSONL output of claude-code --output-format
// stream-json, normalises each line into a StreamEvent, and forwards it to
// onEvent (when non-nil). Returns the aggregated summary used to populate
// TaskOutput.
func parseClaudeStream(r io.Reader, onEvent EventHandler) (claudeStreamSummary, error) {
	sum := claudeStreamSummary{}
	scanner := bufio.NewScanner(r)
	// Default buffer (64K) is too small for big tool results. Bump to 4MB.
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
				Type:    EventError,
				Runtime: "claude-code",
				Content: err.Error(),
				Fatal:   false,
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
		case "system":
			if asString(obj, "subtype") != "init" {
				continue
			}
			sum.sessionID = asString(obj, "session_id")
			tools := []string{}
			if rawTools, ok := obj["tools"].([]any); ok {
				for _, t := range rawTools {
					if s, ok := t.(string); ok {
						tools = append(tools, s)
					}
				}
			}
			if err := emit(StreamEvent{
				Type:    EventInit,
				Runtime: "claude-code",
				Session: sum.sessionID,
				Content: fmt.Sprintf("model=%s tools=%d", asString(obj, "model"), len(tools)),
				Raw:     raw,
			}); err != nil {
				return sum, err
			}

		case "assistant":
			msg := asMap(obj, "message")
			if msg == nil {
				continue
			}
			contentArr, _ := msg["content"].([]any)
			for _, item := range contentArr {
				blk, ok := item.(map[string]any)
				if !ok {
					continue
				}
				switch asString(blk, "type") {
				case "text":
					text := asString(blk, "text")
					if text == "" {
						continue
					}
					// Final assistant text becomes the "result" payload —
					// claude-code's `result` event echoes it but for
					// streamed observers we want to surface it as it
					// arrives.
					sum.finalText = text
					if err := emit(StreamEvent{
						Type:    EventThinking,
						Runtime: "claude-code",
						Session: sum.sessionID,
						Content: text,
						Raw:     raw,
					}); err != nil {
						return sum, err
					}
				case "tool_use":
					sum.toolCalls++
					inputJSON, _ := json.Marshal(blk["input"])
					if err := emit(StreamEvent{
						Type:      EventToolCall,
						Runtime:   "claude-code",
						Session:   sum.sessionID,
						ToolName:  asString(blk, "name"),
						ToolInput: string(inputJSON),
						Raw:       raw,
					}); err != nil {
						return sum, err
					}
				}
			}

		case "user":
			// user messages mid-stream carry tool_result blocks coming
			// back from the agent's own tool calls.
			msg := asMap(obj, "message")
			if msg == nil {
				continue
			}
			contentArr, _ := msg["content"].([]any)
			for _, item := range contentArr {
				blk, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if asString(blk, "type") != "tool_result" {
					continue
				}
				body := decodeClaudeToolResult(blk["content"])
				isErr := false
				if v, ok := blk["is_error"].(bool); ok {
					isErr = v
				}
				if err := emit(StreamEvent{
					Type:       EventToolResult,
					Runtime:    "claude-code",
					Session:    sum.sessionID,
					ToolName:   asString(blk, "tool_use_id"), // best we have
					ToolOutput: truncateForLog(body, 4000),
					ToolError:  isErr,
					Raw:        raw,
				}); err != nil {
					return sum, err
				}
			}

		case "result":
			// Final summary line — captures cost & usage across the run.
			if r := asString(obj, "result"); r != "" {
				sum.finalText = r
			}
			usage := asMap(obj, "usage")
			if usage != nil {
				sum.inputTokens += asInt(usage, "input_tokens")
				sum.outputTokens += asInt(usage, "output_tokens")
				sum.cachedTokens += asInt(usage, "cache_read_input_tokens")
			}
			sum.costUSD = asFloat(obj, "total_cost_usd")
			if isErr, ok := obj["is_error"].(bool); ok && isErr {
				sum.fatalErr = true
				if sum.errMsg == "" {
					sum.errMsg = "claude reported is_error=true"
				}
				if err := emit(StreamEvent{
					Type:    EventError,
					Runtime: "claude-code",
					Session: sum.sessionID,
					Content: sum.errMsg,
					Fatal:   true,
					Raw:     raw,
				}); err != nil {
					return sum, err
				}
			}

		default:
			// Unknown / future event type — skip silently. We don't want
			// a new claude-code release to crash spore mid-task.
		}
	}
	if err := scanner.Err(); err != nil {
		return sum, fmt.Errorf("scanner: %w", err)
	}
	return sum, nil
}

// decodeClaudeToolResult flattens the heterogeneous "content" field of a
// tool_result block into a single string. Claude emits either a raw string
// or an array of {type:"text", text:"..."} blocks; our generic-map view
// decodes those as []any of map[string]any.
func decodeClaudeToolResult(raw any) string {
	if raw == nil {
		return ""
	}
	if s, ok := raw.(string); ok {
		return s
	}
	if arr, ok := raw.([]any); ok {
		var sb strings.Builder
		for i, b := range arr {
			if i > 0 {
				sb.WriteString("\n")
			}
			if blk, ok := b.(map[string]any); ok {
				sb.WriteString(asString(blk, "text"))
			}
		}
		return sb.String()
	}
	// Fallback: marshal whatever it is back to JSON.
	if b, err := json.Marshal(raw); err == nil {
		return string(b)
	}
	return ""
}
