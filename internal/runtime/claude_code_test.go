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
	"strings"
	"testing"
)

// claudeFixtureSimple is a real captured stream-json transcript from
// `claude --print --output-format stream-json --verbose --permission-mode
// bypassPermissions "what is 2 plus 2? respond with just the number"`.
// We embed it verbatim so the parser test exercises the real wire format.
const claudeFixtureSimple = `{"type":"system","subtype":"init","cwd":"/tmp","session_id":"abc","tools":["Bash","Read","Write"],"model":"claude-opus-4-7"}
{"type":"assistant","message":{"model":"claude-opus-4-7","content":[{"type":"text","text":"4"}]},"session_id":"abc"}
{"type":"result","subtype":"success","is_error":false,"duration_ms":3958,"num_turns":1,"result":"4","session_id":"abc","total_cost_usd":0.28,"usage":{"input_tokens":6,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":45050}}
`

// claudeFixtureToolUse mirrors the "list files via ls" run captured during
// development. It exercises tool_use → tool_result → result.
const claudeFixtureToolUse = `{"type":"system","subtype":"init","cwd":"/tmp","session_id":"xyz","tools":["Bash"],"model":"claude-opus-4-7"}
{"type":"assistant","message":{"model":"claude-opus-4-7","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls","description":"List"}}]},"session_id":"xyz"}
{"type":"user","message":{"content":[{"tool_use_id":"toolu_1","type":"tool_result","content":"file1\nfile2","is_error":false}]},"session_id":"xyz"}
{"type":"result","subtype":"success","is_error":false,"duration_ms":11596,"num_turns":2,"result":"","session_id":"xyz","total_cost_usd":0.45,"usage":{"input_tokens":7,"output_tokens":93,"cache_read_input_tokens":21519}}
`

// TestParseClaudeStream_Simple verifies a no-tool transcript parses into
// the expected event sequence and final accounting.
func TestParseClaudeStream_Simple(t *testing.T) {
	var events []StreamEvent
	handler := func(ev StreamEvent) error {
		events = append(events, ev)
		return nil
	}

	sum, err := parseClaudeStream(strings.NewReader(claudeFixtureSimple), handler)
	if err != nil {
		t.Fatalf("parseClaudeStream: %v", err)
	}

	// Expected event sequence: init → thinking → (no tool) → result-event-not-emitted-here
	// (the EventComplete is emitted by ExecuteStream itself, not by parser)
	if len(events) < 2 {
		t.Fatalf("expected ≥2 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != EventInit {
		t.Errorf("events[0].Type = %q, want EventInit", events[0].Type)
	}
	if events[0].Session != "abc" {
		t.Errorf("events[0].Session = %q, want abc", events[0].Session)
	}
	if events[1].Type != EventThinking {
		t.Errorf("events[1].Type = %q, want EventThinking", events[1].Type)
	}
	if events[1].Content != "4" {
		t.Errorf("events[1].Content = %q, want '4'", events[1].Content)
	}

	// Final accounting
	if sum.sessionID != "abc" {
		t.Errorf("sessionID = %q, want abc", sum.sessionID)
	}
	if sum.finalText != "4" {
		t.Errorf("finalText = %q, want '4'", sum.finalText)
	}
	if sum.inputTokens != 6 || sum.outputTokens != 1 {
		t.Errorf("tokens in=%d out=%d, want 6/1", sum.inputTokens, sum.outputTokens)
	}
	if sum.costUSD != 0.28 {
		t.Errorf("costUSD = %f, want 0.28", sum.costUSD)
	}
	if sum.fatalErr {
		t.Error("fatalErr should be false")
	}
}

// TestParseClaudeStream_ToolUse verifies tool_use and tool_result blocks
// produce EventToolCall / EventToolResult with the right payloads.
func TestParseClaudeStream_ToolUse(t *testing.T) {
	var events []StreamEvent
	handler := func(ev StreamEvent) error {
		events = append(events, ev)
		return nil
	}
	sum, err := parseClaudeStream(strings.NewReader(claudeFixtureToolUse), handler)
	if err != nil {
		t.Fatalf("parseClaudeStream: %v", err)
	}

	// Find the tool-call and tool-result events
	var toolCall, toolResult *StreamEvent
	for i := range events {
		switch events[i].Type {
		case EventToolCall:
			toolCall = &events[i]
		case EventToolResult:
			toolResult = &events[i]
		}
	}
	if toolCall == nil {
		t.Fatal("no EventToolCall emitted")
	}
	if toolCall.ToolName != "Bash" {
		t.Errorf("ToolCall.ToolName = %q, want Bash", toolCall.ToolName)
	}
	if !strings.Contains(toolCall.ToolInput, `"command":"ls"`) {
		t.Errorf("ToolCall.ToolInput missing command: %q", toolCall.ToolInput)
	}

	if toolResult == nil {
		t.Fatal("no EventToolResult emitted")
	}
	if !strings.Contains(toolResult.ToolOutput, "file1") {
		t.Errorf("ToolResult.ToolOutput missing file1: %q", toolResult.ToolOutput)
	}
	if toolResult.ToolError {
		t.Error("ToolResult.ToolError should be false")
	}

	if sum.toolCalls != 1 {
		t.Errorf("toolCalls = %d, want 1", sum.toolCalls)
	}
}

// TestParseClaudeStream_NilHandler verifies the parser still computes
// summary correctly when the caller does not pass a handler.
func TestParseClaudeStream_NilHandler(t *testing.T) {
	sum, err := parseClaudeStream(strings.NewReader(claudeFixtureSimple), nil)
	if err != nil {
		t.Fatalf("parseClaudeStream: %v", err)
	}
	if sum.finalText != "4" {
		t.Errorf("finalText = %q, want '4'", sum.finalText)
	}
	if sum.inputTokens != 6 {
		t.Errorf("inputTokens = %d, want 6", sum.inputTokens)
	}
}

// TestParseClaudeStream_HandlerError verifies a handler returning an error
// stops the stream early and propagates.
func TestParseClaudeStream_HandlerError(t *testing.T) {
	calls := 0
	handler := func(ev StreamEvent) error {
		calls++
		if calls >= 2 {
			return strErr("handler aborts")
		}
		return nil
	}
	_, err := parseClaudeStream(strings.NewReader(claudeFixtureToolUse), handler)
	if err == nil {
		t.Fatal("expected handler error to propagate")
	}
	if !strings.Contains(err.Error(), "handler aborts") {
		t.Errorf("err = %v, want handler aborts", err)
	}
}

// TestParseClaudeStream_MalformedLine verifies bad JSON lines emit an
// EventError but don't abort the stream.
func TestParseClaudeStream_MalformedLine(t *testing.T) {
	transcript := `{"type":"system","subtype":"init","session_id":"x","model":"m","tools":[]}
this is not json
{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]},"session_id":"x"}
{"type":"result","is_error":false,"result":"ok","session_id":"x","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}
`
	var events []StreamEvent
	handler := func(ev StreamEvent) error { events = append(events, ev); return nil }
	sum, err := parseClaudeStream(strings.NewReader(transcript), handler)
	if err != nil {
		t.Fatalf("parseClaudeStream returned err: %v", err)
	}
	if sum.finalText != "ok" {
		t.Errorf("expected stream to recover and emit final text 'ok', got %q", sum.finalText)
	}
	// Verify the bad line surfaced as a non-fatal error event.
	sawError := false
	for _, ev := range events {
		if ev.Type == EventError && !ev.Fatal {
			sawError = true
			break
		}
	}
	if !sawError {
		t.Error("expected a non-fatal EventError for the malformed line")
	}
}

// TestDecodeClaudeToolResult covers the three shapes of tool_result.content.
func TestDecodeClaudeToolResult(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"plain string", "hello", "hello"},
		{"array of text blocks",
			[]any{
				map[string]any{"type": "text", "text": "a"},
				map[string]any{"type": "text", "text": "b"},
			},
			"a\nb"},
		{"empty array", []any{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeClaudeToolResult(c.in)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestClaudeCode_BuildArgs verifies CLI argv assembly.
func TestClaudeCode_BuildArgs(t *testing.T) {
	cc := &ClaudeCode{
		BinPath:        "claude",
		PermissionMode: "bypassPermissions",
		Model:          "claude-opus-4",
	}
	args := cc.buildArgs(TaskInput{Description: "do a thing"})

	wantContains := []string{
		"--permission-mode", "bypassPermissions",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--model", "claude-opus-4",
		"do a thing",
	}
	joined := strings.Join(args, " ")
	for _, w := range wantContains {
		if !strings.Contains(joined, w) {
			t.Errorf("args missing %q: %v", w, args)
		}
	}
	// Description must be the LAST arg (positional)
	if args[len(args)-1] != "do a thing" {
		t.Errorf("description should be last arg, got %v", args)
	}
}

// TestClaudeCode_Compliance verifies the runtime satisfies both interfaces.
func TestClaudeCode_Compliance(t *testing.T) {
	var _ Runtime = (*ClaudeCode)(nil)
	var _ StreamingRuntime = (*ClaudeCode)(nil)
}

// strErr is a tiny error helper to avoid importing errors package.
type strErr string

func (e strErr) Error() string { return string(e) }
