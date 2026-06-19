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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Streaming runtime events
//
// External agent CLIs (Claude Code, Codex, ...) all emit JSONL streams when
// invoked with the right flag. Each tool's schema differs, but the same five
// event categories cover what spore needs:
//
//   - EventInit       — runtime bootstrapped, model/session info available
//   - EventThinking   — model wrote text or "reasoning" content
//   - EventToolCall   — model invoked a tool (Bash, Read, Write, ...)
//   - EventToolResult — tool finished, output captured
//   - EventComplete   — task finished, total token/cost/duration accounting
//   - EventError      — fatal error or recoverable retry notice
//
// Each adapter's Execute method is now responsible for consuming the upstream
// JSONL stream, normalising it into StreamEvent, and forwarding to a hook
// function. The hook is optional — when nil, the adapter degrades to the
// previous "wait for the whole thing then return TaskOutput" behaviour but
// still benefits from richer accounting (token counts, tool-call metrics).
// ─────────────────────────────────────────────────────────────────────────────

// EventType classifies a streaming event from an external agent runtime.
type EventType string

const (
	EventInit       EventType = "init"        // runtime started
	EventThinking   EventType = "thinking"    // assistant text (or reasoning)
	EventToolCall   EventType = "tool_call"   // assistant invoked a tool
	EventToolResult EventType = "tool_result" // tool execution returned
	EventComplete   EventType = "complete"    // task finished
	EventError      EventType = "error"       // fatal or transient error
)

// StreamEvent is a normalized event emitted by a streaming runtime.
//
// Optional fields are populated only when relevant for the event type:
//
//   - ToolName / ToolInput  — set on EventToolCall
//   - ToolName / ToolOutput / ToolError — set on EventToolResult
//   - InputTokens / OutputTokens / CachedTokens / CostUSD — set on
//     EventComplete (and sometimes per-turn on EventThinking when the
//     upstream tool emits it)
//   - DurationMS — set on EventComplete
type StreamEvent struct {
	Type    EventType `json:"type"`
	Runtime string    `json:"runtime"`           // "claude-code", "codex", ...
	Session string    `json:"session,omitempty"` // upstream session/thread ID

	// Free-form content for EventThinking, EventInit summary, EventError.
	Content string `json:"content,omitempty"`

	// Tool-related fields.
	ToolName   string `json:"tool_name,omitempty"`
	ToolInput  string `json:"tool_input,omitempty"`  // JSON-encoded args
	ToolOutput string `json:"tool_output,omitempty"` // truncated for log
	ToolError  bool   `json:"tool_error,omitempty"`

	// Accounting (set on EventComplete; some runtimes also emit per-turn).
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	CachedTokens int     `json:"cached_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	DurationMS   int64   `json:"duration_ms,omitempty"`

	// For EventError: whether this is fatal (stops the run) or transient
	// (e.g. the upstream tool's automatic retry/reconnect notice).
	Fatal bool `json:"fatal,omitempty"`

	// Raw is the underlying JSONL line for debugging and for clients that
	// want fields the normalised view doesn't expose. May be empty when
	// the event was synthesised (e.g. EventComplete derived from exit_code).
	Raw json.RawMessage `json:"raw,omitempty"`
}

// EventHandler receives events as they stream from a runtime.
//
// Implementations MUST be safe to call from a single goroutine. The runtime
// invokes the handler synchronously while reading the upstream stream, so
// slow handlers throttle the stream — keep the handler cheap (broadcast,
// buffer, log) and do heavier work elsewhere.
//
// Returning an error stops the stream and propagates the error from
// Execute. The runtime still attempts to wait on the underlying process
// to avoid leaking it.
type EventHandler func(StreamEvent) error

// StreamingRuntime is implemented by runtimes that can deliver fine-grained
// events while a task is in flight. Runtimes that don't implement this
// continue to work via the basic Runtime.Execute interface.
//
// Adapters wired in this codebase (claude-code, codex) implement both
// Runtime and StreamingRuntime; Execute is preserved as the "no observer"
// shorthand and internally calls ExecuteStream with a nil handler.
type StreamingRuntime interface {
	Runtime
	ExecuteStream(ctx context.Context, task TaskInput, onEvent EventHandler) (*TaskOutput, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// JSONL stream parser helpers — shared between adapters.
// ─────────────────────────────────────────────────────────────────────────────

// parseJSONLine decodes a single JSONL line into a generic map.
// Empty / whitespace-only lines return (nil, nil) so callers can skip them.
func parseJSONLine(line []byte) (map[string]any, error) {
	trimmed := []byte(strings.TrimSpace(string(line)))
	if len(trimmed) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(trimmed, &m); err != nil {
		return nil, fmt.Errorf("invalid JSONL: %w (line: %s)", err, truncateForLog(string(trimmed), 200))
	}
	return m, nil
}

// truncateForLog cuts a string to max chars with an ellipsis suffix. Used when
// embedding tool output / error bodies in event Content for human inspection.
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(truncated)"
}

// asString fetches a string field from a generic map, defaulting to "".
func asString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// asInt fetches a numeric field as int64. JSON numbers come back as float64.
func asInt(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		}
	}
	return 0
}

// asFloat fetches a numeric field as float64.
func asFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		if n, ok := v.(float64); ok {
			return n
		}
	}
	return 0
}

// asMap fetches a nested object.
func asMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key]; ok {
		if mm, ok := v.(map[string]any); ok {
			return mm
		}
	}
	return nil
}

// stderrPrefixWriter is a small io.Writer that copies bytes to a wrapped
// writer while also prepending a fixed prefix the first time data arrives
// on a fresh line. Used by adapters to keep child-process stderr identifiable
// in interleaved output without having to scan the whole buffer afterwards.
type stderrPrefixWriter struct {
	prefix    string
	w         io.Writer
	atLineEnd bool // next write needs a prefix
}

// Write implements io.Writer. It scans for newlines so that every line of
// child-process stderr gets the configured prefix exactly once.
func (s *stderrPrefixWriter) Write(p []byte) (int, error) {
	// Initial state: emit the prefix before the first byte.
	if !s.atLineEnd && s.w != nil {
		if _, err := io.WriteString(s.w, s.prefix); err != nil {
			return 0, err
		}
		s.atLineEnd = true
	}
	written := 0
	for i, b := range p {
		if b == '\n' {
			n, err := s.w.Write(p[written : i+1])
			written += n
			if err != nil {
				return written, err
			}
			// After the newline, we need the prefix again — unless this
			// was the final byte (avoid trailing-prefix-on-empty-line).
			if i+1 < len(p) {
				if _, err := io.WriteString(s.w, s.prefix); err != nil {
					return written, err
				}
			} else {
				s.atLineEnd = false
			}
		}
	}
	if written < len(p) {
		n, err := s.w.Write(p[written:])
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
