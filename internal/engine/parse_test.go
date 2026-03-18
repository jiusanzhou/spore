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

package engine

import "testing"

func TestParseAction_Complete(t *testing.T) {
	text := `THOUGHT: The task is done.
COMPLETE: all files processed successfully`

	pa, done := parseAction(text)
	if !done {
		t.Fatal("expected done=true for COMPLETE")
	}
	if pa.Result != "all files processed successfully" {
		t.Errorf("expected result 'all files processed successfully', got %q", pa.Result)
	}
}

func TestParseAction_ToolCall(t *testing.T) {
	text := `THOUGHT: I need to list the directory.
ACTION: shell ls -la /tmp`

	pa, done := parseAction(text)
	if done {
		t.Fatal("expected done=false for ACTION")
	}
	if pa.ToolName != "shell" {
		t.Errorf("expected tool 'shell', got %q", pa.ToolName)
	}
	if pa.ToolInput != "ls -la /tmp" {
		t.Errorf("expected input 'ls -la /tmp', got %q", pa.ToolInput)
	}
}

func TestParseAction_ToolCallNoInput(t *testing.T) {
	text := `THOUGHT: checking status
ACTION: status`

	pa, done := parseAction(text)
	if done {
		t.Fatal("expected done=false for ACTION")
	}
	if pa.ToolName != "status" {
		t.Errorf("expected tool 'status', got %q", pa.ToolName)
	}
	if pa.ToolInput != "" {
		t.Errorf("expected empty input, got %q", pa.ToolInput)
	}
}

func TestParseAction_NoAction(t *testing.T) {
	text := `THOUGHT: I'm not sure what to do.
Let me think more about this.`

	pa, done := parseAction(text)
	if done {
		t.Fatal("expected done=false for no action")
	}
	if pa.ToolName != "" {
		t.Errorf("expected empty tool name, got %q", pa.ToolName)
	}
}

func TestParseAction_MultipleActions_UsesLast(t *testing.T) {
	text := `THOUGHT: need to check things
ACTION: shell echo hello
ACTION: shell echo world`

	pa, done := parseAction(text)
	if done {
		t.Fatal("expected done=false")
	}
	// should use the last ACTION line
	if pa.ToolName != "shell" {
		t.Errorf("expected tool 'shell', got %q", pa.ToolName)
	}
	if pa.ToolInput != "echo world" {
		t.Errorf("expected input 'echo world', got %q", pa.ToolInput)
	}
}

func TestParseAction_CompleteWithExtraWhitespace(t *testing.T) {
	text := `THOUGHT: done
COMPLETE:    result with spaces   `

	pa, done := parseAction(text)
	if !done {
		t.Fatal("expected done=true")
	}
	if pa.Result != "result with spaces" {
		t.Errorf("expected 'result with spaces', got %q", pa.Result)
	}
}

func TestParseAction_EmptyText(t *testing.T) {
	pa, done := parseAction("")
	if done {
		t.Fatal("expected done=false for empty text")
	}
	if pa.ToolName != "" {
		t.Errorf("expected empty tool name, got %q", pa.ToolName)
	}
}

func TestParseAction_DelegateAction(t *testing.T) {
	text := `THOUGHT: I should delegate this to worker-1
ACTION: delegate worker-1 analyze the codebase`

	pa, done := parseAction(text)
	if done {
		t.Fatal("expected done=false")
	}
	if pa.ToolName != "delegate" {
		t.Errorf("expected tool 'delegate', got %q", pa.ToolName)
	}
	if pa.ToolInput != "worker-1 analyze the codebase" {
		t.Errorf("expected input 'worker-1 analyze the codebase', got %q", pa.ToolInput)
	}
}
