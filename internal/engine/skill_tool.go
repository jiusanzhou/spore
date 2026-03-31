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
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// SkillTool — dynamically generated tool from SKILL.md
//
// A SKILL.md can define executable tools in its frontmatter:
//
//   tools:
//     - name: get-weather
//       description: Get current weather for a location
//       command: curl -s "wttr.in/{{input}}?format=3"
//       timeout: 10s
//     - name: check-dns
//       description: Check DNS resolution for a domain
//       command: dig +short {{input}}
//       timeout: 5s
//
// The engine loads these tools dynamically before executing a task,
// giving agents the ability to CREATE TOOLS through evolution.
// ────────────────────────────────────────────────────────────────────────────

// SkillToolDef defines a tool embedded in a SKILL.md.
type SkillToolDef struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Command     string `yaml:"command" json:"command"`         // shell command template, {{input}} is replaced
	Timeout     string `yaml:"timeout" json:"timeout"`         // e.g. "10s", "30s"
	WorkDir     string `yaml:"workdir" json:"workdir"`         // optional working directory
	Interpreter string `yaml:"interpreter" json:"interpreter"` // "sh", "bash", "python3" (default: "sh")
}

// SkillTool wraps a SkillToolDef as an engine.Tool.
type SkillTool struct {
	def       SkillToolDef
	timeout   time.Duration
	skillName string // which skill defined this tool
}

// NewSkillTool creates an engine Tool from a SKILL.md tool definition.
func NewSkillTool(def SkillToolDef, skillName string) *SkillTool {
	timeout := 30 * time.Second
	if def.Timeout != "" {
		if d, err := time.ParseDuration(def.Timeout); err == nil {
			timeout = d
		}
	}
	return &SkillTool{
		def:       def,
		timeout:   timeout,
		skillName: skillName,
	}
}

func (t *SkillTool) Name() string {
	return t.def.Name
}

func (t *SkillTool) Description() string {
	desc := t.def.Description
	if desc == "" {
		desc = fmt.Sprintf("Skill-defined tool from %s", t.skillName)
	}
	return desc + " [evolved]"
}

func (t *SkillTool) Execute(ctx context.Context, input string) (string, error) {
	// Build command by replacing {{input}} placeholder
	cmdStr := t.def.Command
	cmdStr = strings.ReplaceAll(cmdStr, "{{input}}", input)
	cmdStr = strings.ReplaceAll(cmdStr, "{{ input }}", input)

	// Security: basic sanitization — prevent obvious injection
	// The input is user-provided task context, not arbitrary user input,
	// but we still sanitize shell metacharacters in the interpolated value
	if containsDangerousChars(input) {
		// Re-build with quoted input
		quoted := shellQuote(input)
		cmdStr = t.def.Command
		cmdStr = strings.ReplaceAll(cmdStr, "{{input}}", quoted)
		cmdStr = strings.ReplaceAll(cmdStr, "{{ input }}", quoted)
	}

	interpreter := t.def.Interpreter
	if interpreter == "" {
		interpreter = "sh"
	}

	// Create command with timeout
	tctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	cmd := exec.CommandContext(tctx, interpreter, "-c", cmdStr)
	if t.def.WorkDir != "" {
		cmd.Dir = t.def.WorkDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if tctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("skill tool %s timed out after %s", t.def.Name, t.timeout)
		}
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("skill tool %s failed: %s", t.def.Name, strings.TrimSpace(errMsg))
	}

	result := stdout.String()
	if result == "" {
		result = "(no output)"
	}
	return strings.TrimSpace(result), nil
}

// containsDangerousChars checks for shell injection risk.
func containsDangerousChars(s string) bool {
	dangerous := []string{";", "|", "&", "`", "$", "(", ")", "{", "}", "<", ">", "\n", "\\"}
	for _, d := range dangerous {
		if strings.Contains(s, d) {
			return true
		}
	}
	return false
}

// shellQuote wraps a string in single quotes for safe shell use.
func shellQuote(s string) string {
	// Replace single quotes with '"'"' (end quote, literal quote, start quote)
	s = strings.ReplaceAll(s, "'", "'\"'\"'")
	return "'" + s + "'"
}
