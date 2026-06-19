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

// codexFixtureToolUse exercises a complete codex JSONL turn with reasoning,
// command_execution (one tool call), plan_update, agent_message, and final
// turn.completed usage. Schema captured from real `codex exec --json` output
// (codex-cli 0.139, 2026-06).
const codexFixtureToolUse = `{"type":"thread.started","thread_id":"thread-001"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"reason_1","type":"reasoning"}}
{"type":"item.completed","item":{"id":"reason_1","type":"reasoning","text":"User wants me to count files. I'll run ls and count."}}
{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"ls /tmp"}}
{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"ls /tmp","exit_code":0,"status":"completed","aggregated_output":"file1.txt\nfile2.txt\nfile3.txt\n"}}
{"type":"item.started","item":{"id":"plan_1","type":"plan_update"}}
{"type":"item.completed","item":{"id":"plan_1","type":"plan_update","text":"1. List files\n2. Count them\n3. Report"}}
{"type":"item.started","item":{"id":"agent_1","type":"agent_message"}}
{"type":"item.completed","item":{"id":"agent_1","type":"agent_message","text":"There are 3 files in /tmp."}}
{"type":"turn.completed","usage":{"input_tokens":150,"cached_input_tokens":80,"output_tokens":40}}
`

// codexFixtureFailed reproduces the real-world reconnect-then-fail pattern
// observed against an unauthorized gateway: 5 transient errors followed by
// a fatal error and turn.failed.
const codexFixtureFailed = `{"type":"thread.started","thread_id":"err-thread"}
{"type":"turn.started"}
{"type":"error","message":"Reconnecting... 1/5 (network glitch)"}
{"type":"error","message":"Reconnecting... 2/5 (network glitch)"}
{"type":"error","message":"unexpected status 401 Unauthorized"}
{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized"}}
`

// TestParseCodexStream_ToolUse verifies a full happy-path turn parses into
// the expected event sequence and final accounting.
func TestParseCodexStream_ToolUse(t *testing.T) {
	var events []StreamEvent
	handler := func(ev StreamEvent) error {
		events = append(events, ev)
		return nil
	}

	sum, err := parseCodexStream(strings.NewReader(codexFixtureToolUse), handler)
	if err != nil {
		t.Fatalf("parseCodexStream: %v", err)
	}

	// First event must be EventInit with the thread_id
	if len(events) < 1 {
		t.Fatal("no events emitted")
	}
	if events[0].Type != EventInit {
		t.Errorf("events[0].Type = %q, want EventInit", events[0].Type)
	}
	if events[0].Session != "thread-001" {
		t.Errorf("events[0].Session = %q, want thread-001", events[0].Session)
	}

	// Find tool call / result, reasoning, agent_message, plan_update
	var toolCall, toolResult *StreamEvent
	thinkingCount := 0
	var agentMsg string
	for i := range events {
		switch events[i].Type {
		case EventToolCall:
			if events[i].ToolName == "command_execution" {
				toolCall = &events[i]
			}
		case EventToolResult:
			toolResult = &events[i]
		case EventThinking:
			thinkingCount++
			// agent_message is the last EventThinking emitted (it sets finalText)
			if events[i].Content == "There are 3 files in /tmp." {
				agentMsg = events[i].Content
			}
		}
	}

	if toolCall == nil {
		t.Fatal("no command_execution EventToolCall emitted")
	}
	if toolCall.ToolInput != "ls /tmp" {
		t.Errorf("toolCall.ToolInput = %q, want 'ls /tmp'", toolCall.ToolInput)
	}
	if toolResult == nil {
		t.Fatal("no EventToolResult emitted")
	}
	if !strings.Contains(toolResult.ToolOutput, "file1.txt") {
		t.Errorf("toolResult.ToolOutput missing files: %q", toolResult.ToolOutput)
	}
	if toolResult.ToolError {
		t.Error("toolResult.ToolError should be false (exit_code=0)")
	}

	// Reasoning + agent_message + plan_update → 3 thinking events
	if thinkingCount < 2 {
		t.Errorf("expected ≥2 EventThinking (reasoning, agent_message, plan_update), got %d", thinkingCount)
	}
	if agentMsg == "" {
		t.Error("agent_message text not surfaced as EventThinking")
	}

	// Summary accounting
	if sum.threadID != "thread-001" {
		t.Errorf("threadID = %q, want thread-001", sum.threadID)
	}
	if sum.finalText != "There are 3 files in /tmp." {
		t.Errorf("finalText = %q", sum.finalText)
	}
	if sum.toolCalls != 1 {
		t.Errorf("toolCalls = %d, want 1", sum.toolCalls)
	}
	if sum.inputTokens != 150 || sum.outputTokens != 40 || sum.cachedTokens != 80 {
		t.Errorf("tokens in=%d out=%d cached=%d, want 150/40/80",
			sum.inputTokens, sum.outputTokens, sum.cachedTokens)
	}
	if sum.fatalErr {
		t.Error("fatalErr should be false")
	}
}

// TestParseCodexStream_Failed verifies transient reconnect errors are
// non-fatal but a real failure produces fatalErr=true and errMsg set.
func TestParseCodexStream_Failed(t *testing.T) {
	var events []StreamEvent
	handler := func(ev StreamEvent) error {
		events = append(events, ev)
		return nil
	}
	sum, err := parseCodexStream(strings.NewReader(codexFixtureFailed), handler)
	if err != nil {
		t.Fatalf("parseCodexStream: %v", err)
	}

	// Count fatal vs non-fatal errors
	fatalCount, transientCount := 0, 0
	for _, ev := range events {
		if ev.Type == EventError {
			if ev.Fatal {
				fatalCount++
			} else {
				transientCount++
			}
		}
	}
	if transientCount < 2 {
		t.Errorf("expected ≥2 transient (reconnect) errors, got %d", transientCount)
	}
	if fatalCount < 1 {
		t.Errorf("expected ≥1 fatal error, got %d", fatalCount)
	}

	if !sum.fatalErr {
		t.Error("fatalErr should be true")
	}
	if !strings.Contains(sum.errMsg, "401") {
		t.Errorf("errMsg should contain '401', got %q", sum.errMsg)
	}
}

// TestParseCodexStream_NilHandler verifies the parser still tallies the
// summary correctly when no handler is provided (Execute path).
func TestParseCodexStream_NilHandler(t *testing.T) {
	sum, err := parseCodexStream(strings.NewReader(codexFixtureToolUse), nil)
	if err != nil {
		t.Fatalf("parseCodexStream: %v", err)
	}
	if sum.finalText != "There are 3 files in /tmp." {
		t.Errorf("finalText = %q", sum.finalText)
	}
	if sum.toolCalls != 1 {
		t.Errorf("toolCalls = %d, want 1", sum.toolCalls)
	}
	if sum.inputTokens != 150 {
		t.Errorf("inputTokens = %d, want 150", sum.inputTokens)
	}
}

// TestParseCodexStream_HandlerError verifies handler-returned errors stop
// the stream and propagate up.
func TestParseCodexStream_HandlerError(t *testing.T) {
	calls := 0
	handler := func(ev StreamEvent) error {
		calls++
		if calls >= 2 {
			return strErr("aborting from codex")
		}
		return nil
	}
	_, err := parseCodexStream(strings.NewReader(codexFixtureToolUse), handler)
	if err == nil {
		t.Fatal("expected handler error to propagate")
	}
	if !strings.Contains(err.Error(), "aborting from codex") {
		t.Errorf("err = %v, want aborting from codex", err)
	}
}

// TestParseCodexStream_MalformedLine verifies a bad JSON line emits a
// non-fatal EventError and the stream continues.
func TestParseCodexStream_MalformedLine(t *testing.T) {
	transcript := `{"type":"thread.started","thread_id":"abc"}
this line is garbage
{"type":"item.started","item":{"id":"a","type":"agent_message"}}
{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"hi"}}
{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}
`
	var events []StreamEvent
	handler := func(ev StreamEvent) error { events = append(events, ev); return nil }
	sum, err := parseCodexStream(strings.NewReader(transcript), handler)
	if err != nil {
		t.Fatalf("parseCodexStream: %v", err)
	}
	if sum.finalText != "hi" {
		t.Errorf("finalText = %q, want 'hi' (recovery should still work)", sum.finalText)
	}
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

// TestCodex_Compliance verifies Codex satisfies both interfaces.
func TestCodex_Compliance(t *testing.T) {
	var _ Runtime = (*Codex)(nil)
	var _ StreamingRuntime = (*Codex)(nil)
}
