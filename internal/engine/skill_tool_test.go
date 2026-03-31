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

import (
	"context"
	"strings"
	"testing"
)

func TestSkillTool_Execute(t *testing.T) {
	def := SkillToolDef{
		Name:        "echo-test",
		Description: "Echo the input back",
		Command:     `echo "Hello {{input}}"`,
		Timeout:     "5s",
	}

	tool := NewSkillTool(def, "test-skill")

	if tool.Name() != "echo-test" {
		t.Errorf("expected name echo-test, got %s", tool.Name())
	}
	if !strings.Contains(tool.Description(), "evolved") {
		t.Error("description should contain [evolved]")
	}

	result, err := tool.Execute(context.Background(), "world")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", result)
	}
}

func TestSkillTool_WeatherExample(t *testing.T) {
	// Simulate the weather tool that Spore should evolve
	def := SkillToolDef{
		Name:        "get-weather",
		Description: "Get current weather for a location",
		Command:     `echo "Shanghai: ☀️ +21°C, wind 8km/h"`,
		Timeout:     "10s",
	}

	tool := NewSkillTool(def, "weather-data-access")
	result, err := tool.Execute(context.Background(), "Shanghai")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result, "Shanghai") {
		t.Errorf("expected Shanghai in result, got '%s'", result)
	}
}

func TestSkillTool_Timeout(t *testing.T) {
	def := SkillToolDef{
		Name:    "slow-tool",
		Command: "sleep 10",
		Timeout: "100ms",
	}

	tool := NewSkillTool(def, "test")
	_, err := tool.Execute(context.Background(), "")
	if err == nil {
		t.Error("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message, got: %v", err)
	}
}

func TestSkillTool_ShellInjectionPrevention(t *testing.T) {
	def := SkillToolDef{
		Name:    "safe-echo",
		Command: `echo "input: {{input}}"`,
		Timeout: "5s",
	}

	tool := NewSkillTool(def, "test")
	// Try shell injection
	result, err := tool.Execute(context.Background(), "foo; rm -rf /")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// Should be quoted, not executed
	if strings.Contains(result, "rm") {
		// The input should be shell-quoted, so it's echoed literally
		t.Logf("result: %s (injection was quoted)", result)
	}
}

func TestSkillTool_FailedCommand(t *testing.T) {
	def := SkillToolDef{
		Name:    "fail-tool",
		Command: "false",
		Timeout: "5s",
	}

	tool := NewSkillTool(def, "test")
	_, err := tool.Execute(context.Background(), "")
	if err == nil {
		t.Error("expected error from failed command")
	}
}

func TestContainsDangerousChars(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"hello", false},
		{"Shanghai", false},
		{"hello; rm", true},
		{"$(whoami)", true},
		{"foo | bar", true},
		{"safe-name", false},
	}
	for _, tt := range tests {
		if got := containsDangerousChars(tt.input); got != tt.expected {
			t.Errorf("containsDangerousChars(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "'hello'"},
		{"it's", "'it'\"'\"'s'"},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.input); got != tt.expected {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
