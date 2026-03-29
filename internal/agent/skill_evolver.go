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

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.zoe.im/spore/internal/llm"
)

// SkillEvolver executes skill evolution actions based on analysis suggestions.
// Three types:
//   - FIX: repair broken skill instructions
//   - DERIVED: enhance an existing skill for a new pattern
//   - CAPTURED: capture a novel pattern as a brand-new skill
//
// V2: operates on SkillFS (file-system-first, IPFS-backed) instead of legacy SkillStore.
type SkillEvolver struct {
	provider llm.Provider
	fs       *SkillFS
	agentID  string
}

// NewSkillEvolver creates an evolver backed by SkillFS.
func NewSkillEvolver(provider llm.Provider, fs *SkillFS, agentID string) *SkillEvolver {
	return &SkillEvolver{
		provider: provider,
		fs:       fs,
		agentID:  agentID,
	}
}

// EvolvedSkill is the result of a single evolution action.
type EvolvedSkill struct {
	Name       string
	Type       EvolutionType
	Generation int
	Summary    string
}

// Evolve processes evolution suggestions from the analyzer.
// It applies high-priority suggestions (priority >= threshold).
func (se *SkillEvolver) Evolve(ctx context.Context, analysis *ExecutionAnalysisResult, threshold float64) ([]*EvolvedSkill, error) {
	if se.fs == nil || se.provider == nil {
		return nil, nil
	}

	var evolved []*EvolvedSkill

	for _, sug := range analysis.Suggestions {
		if sug.Priority < threshold {
			continue
		}

		select {
		case <-ctx.Done():
			return evolved, ctx.Err()
		default:
		}

		var result *EvolvedSkill
		var err error

		switch sug.Type {
		case EvolutionFix:
			result, err = se.executeFix(ctx, &sug, analysis.TaskID)
		case EvolutionDerived:
			result, err = se.executeDerive(ctx, &sug, analysis.TaskID)
		case EvolutionCaptured:
			result, err = se.executeCapture(ctx, &sug, analysis.TaskID)
		default:
			fmt.Printf("⚠️  [evolver] Unknown evolution type: %s\n", sug.Type)
			continue
		}

		if err != nil {
			fmt.Printf("⚠️  [evolver] %s evolution failed for %s: %v\n", sug.Type, sug.SkillName, err)
			continue
		}

		if result != nil {
			evolved = append(evolved, result)
			fmt.Printf("🧬 [evolver] %s → %s: %s (gen=%d)\n",
				result.Type, result.Name, truncate(result.Summary, 60), result.Generation)
		}
	}

	return evolved, nil
}

// executeFix repairs a broken skill by generating a new SKILL.md body via LLM.
func (se *SkillEvolver) executeFix(ctx context.Context, sug *EvolutionSuggestion, taskID string) (*EvolvedSkill, error) {
	existing, ok := se.fs.Get(sug.SkillName)
	if !ok {
		return nil, fmt.Errorf("skill %q not found for fix", sug.SkillName)
	}

	prompt := fmt.Sprintf(`You are fixing a broken AI agent skill defined in SKILL.md format.

## Current SKILL.md body:
%s

## Problem:
%s

Generate a FIXED version of the skill body in Markdown.
Keep the same structure (headings: When to Use, Procedure, Pitfalls, Verification) but fix the problem.

Respond with ONLY a JSON object:
{
  "body": "the complete fixed SKILL.md body in markdown (use \\n for newlines)",
  "change_summary": "one-line description of what was fixed"
}`, existing.Body, sug.Reason)

	resp, err := se.provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	fixResult, err := parseSkillEvolutionResponse(resp.Content)
	if err != nil {
		return nil, err
	}

	meta := existing.Meta
	meta.Origin = "fixed"
	meta.Generation = existing.Meta.Generation + 1
	meta.ParentIDs = []string{existing.Meta.Name}
	meta.SourceTask = taskID

	skill, err := se.fs.Update(sug.SkillName, meta, fixResult.Body, fixResult.ChangeSummary)
	if err != nil {
		return nil, fmt.Errorf("writing fixed skill: %w", err)
	}

	return &EvolvedSkill{
		Name:       skill.Meta.Name,
		Type:       EvolutionFix,
		Generation: skill.Meta.Generation,
		Summary:    fixResult.ChangeSummary,
	}, nil
}

// executeDerive creates an enhanced version of an existing skill.
func (se *SkillEvolver) executeDerive(ctx context.Context, sug *EvolutionSuggestion, taskID string) (*EvolvedSkill, error) {
	existing, ok := se.fs.Get(sug.SkillName)
	if !ok {
		// No existing skill — treat as capture
		return se.executeCapture(ctx, sug, taskID)
	}

	prompt := fmt.Sprintf(`You are enhancing an AI agent skill (SKILL.md format).

## Parent skill: %s
## Parent SKILL.md body:
%s

## Enhancement goal:
%s

## What the derived skill should do:
%s

Generate a DERIVED version. If the enhancement is for a different use case, give it a new name.
Use standard SKILL.md structure: When to Use, Procedure, Pitfalls, Verification.

Respond with ONLY a JSON object:
{
  "name": "skill name (same or new, lowercase-hyphenated)",
  "body": "the complete SKILL.md body in markdown (use \\n for newlines)",
  "description": "one-line description of the skill",
  "change_summary": "what changed from parent"
}`, existing.Meta.Name, existing.Body, sug.Reason, sug.Description)

	resp, err := se.provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	result, err := parseSkillEvolutionResponse(resp.Content)
	if err != nil {
		return nil, err
	}

	name := result.Name
	if name == "" {
		name = sug.SkillName
	}

	if name == existing.Meta.Name {
		// In-place update
		meta := existing.Meta
		meta.Origin = "derived"
		meta.Generation = existing.Meta.Generation + 1
		meta.SourceTask = taskID
		if result.Description != "" {
			meta.Description = result.Description
		}

		skill, err := se.fs.Update(name, meta, result.Body, result.ChangeSummary)
		if err != nil {
			return nil, err
		}
		return &EvolvedSkill{
			Name:       skill.Meta.Name,
			Type:       EvolutionDerived,
			Generation: skill.Meta.Generation,
			Summary:    result.ChangeSummary,
		}, nil
	}

	// New skill derived from parent
	meta := SkillMeta{
		Name:        name,
		Description: result.Description,
		Category:    existing.Meta.Category,
		Origin:      "derived",
		Generation:  existing.Meta.Generation + 1,
		ParentIDs:   []string{existing.Meta.Name},
		SourceTask:  taskID,
		Tags:        existing.Meta.Tags,
	}

	skill, err := se.fs.Create(meta, result.Body)
	if err != nil {
		return nil, err
	}
	return &EvolvedSkill{
		Name:       skill.Meta.Name,
		Type:       EvolutionDerived,
		Generation: skill.Meta.Generation,
		Summary:    result.ChangeSummary,
	}, nil
}

// executeCapture creates a brand-new skill from a novel pattern.
func (se *SkillEvolver) executeCapture(ctx context.Context, sug *EvolutionSuggestion, taskID string) (*EvolvedSkill, error) {
	prompt := fmt.Sprintf(`You are capturing a novel pattern as a reusable AI agent skill (SKILL.md format).

## Skill name: %s
## Why this pattern is valuable: %s
## What the skill should do: %s

Generate a complete SKILL.md body with these sections:
- When to Use (trigger conditions)
- Procedure (numbered steps)
- Pitfalls (known failure modes)
- Verification (how to confirm success)

Respond with ONLY a JSON object:
{
  "name": "skill name (lowercase-hyphenated)",
  "body": "the complete SKILL.md body in markdown (use \\n for newlines)",
  "description": "one-line description of the skill",
  "category": "skill category (e.g. core, meta, devops, social)"
}`, sug.SkillName, sug.Reason, sug.Description)

	resp, err := se.provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	result, err := parseSkillEvolutionResponse(resp.Content)
	if err != nil {
		return nil, err
	}

	name := result.Name
	if name == "" {
		name = sanitizeSkillName(sug.SkillName)
	}

	meta := SkillMeta{
		Name:        name,
		Description: result.Description,
		Category:    result.Category,
		Origin:      "captured",
		Generation:  0,
		SourceTask:  taskID,
	}

	// Avoid duplicate
	if _, exists := se.fs.Get(name); exists {
		// Update instead of create
		skill, err := se.fs.Update(name, meta, result.Body, fmt.Sprintf("captured from task: %s", sug.Reason))
		if err != nil {
			return nil, err
		}
		return &EvolvedSkill{
			Name:       skill.Meta.Name,
			Type:       EvolutionCaptured,
			Generation: skill.Meta.Generation,
			Summary:    fmt.Sprintf("captured: %s", sug.Reason),
		}, nil
	}

	skill, err := se.fs.Create(meta, result.Body)
	if err != nil {
		return nil, err
	}
	return &EvolvedSkill{
		Name:       skill.Meta.Name,
		Type:       EvolutionCaptured,
		Generation: skill.Meta.Generation,
		Summary:    fmt.Sprintf("captured: %s", sug.Reason),
	}, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

type skillEvolutionResponse struct {
	Name          string `json:"name"`
	Body          string `json:"body"`
	Description   string `json:"description"`
	Category      string `json:"category"`
	ChangeSummary string `json:"change_summary"`
}

func parseSkillEvolutionResponse(content string) (*skillEvolutionResponse, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) >= 3 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	content = strings.TrimSpace(content)

	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		content = content[start : end+1]
	}

	var r skillEvolutionResponse
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return nil, fmt.Errorf("invalid evolution JSON: %w\nraw: %s", err, truncate(content, 500))
	}
	if r.Body == "" {
		return nil, fmt.Errorf("empty body in evolution response")
	}
	return &r, nil
}

func generateSkillID(name, action, taskID string) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte(action))
	h.Write([]byte(taskID))
	h.Write([]byte(time.Now().String()))
	return fmt.Sprintf("%s__%s", sanitizeSkillName(name), hex.EncodeToString(h.Sum(nil))[:8])
}

func sanitizeSkillName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, name)
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	if len(name) > 50 {
		name = name[:50]
		if last := strings.LastIndex(name, "-"); last > 25 {
			name = name[:last]
		}
	}
	return name
}

// ─── Legacy Markdown serialization (kept for backward compatibility) ───────

// SkillToMarkdown serializes a SkillRecord to Markdown for IPFS storage.
func SkillToMarkdown(rec *SkillRecord) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Skill: %s\n\n", rec.Name))
	sb.WriteString(fmt.Sprintf("- **ID**: `%s`\n", rec.SkillID))
	sb.WriteString(fmt.Sprintf("- **Origin**: %s\n", rec.Origin))
	sb.WriteString(fmt.Sprintf("- **Generation**: %d\n", rec.Generation))
	if len(rec.ParentIDs) > 0 {
		sb.WriteString(fmt.Sprintf("- **Parents**: %s\n", strings.Join(rec.ParentIDs, ", ")))
	}
	if rec.SourceTaskID != "" {
		sb.WriteString(fmt.Sprintf("- **Source Task**: `%s`\n", rec.SourceTaskID))
	}
	if rec.ChangeSummary != "" {
		sb.WriteString(fmt.Sprintf("- **Change**: %s\n", rec.ChangeSummary))
	}
	sb.WriteString(fmt.Sprintf("- **Created**: %s\n", rec.CreatedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("\n## Description\n\n%s\n", rec.Description))
	if rec.TotalApplied > 0 {
		sb.WriteString(fmt.Sprintf("\n## Metrics\n\n"))
		sb.WriteString(fmt.Sprintf("- Selections: %d\n", rec.TotalSelections))
		sb.WriteString(fmt.Sprintf("- Applied: %d\n", rec.TotalApplied))
		sb.WriteString(fmt.Sprintf("- Completions: %d\n", rec.TotalCompletions))
		sb.WriteString(fmt.Sprintf("- Success Rate: %.0f%%\n", rec.SuccessRate()*100))
	}
	return sb.String()
}

// AnalysisToMarkdown serializes an ExecutionAnalysisResult to Markdown.
func AnalysisToMarkdown(a *ExecutionAnalysisResult) string {
	var sb strings.Builder
	status := "✅ Success"
	if !a.Success {
		status = "❌ Failed"
	}
	sb.WriteString(fmt.Sprintf("# Task Analysis: `%s`\n\n", a.TaskID))
	sb.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
	sb.WriteString(fmt.Sprintf("- **Agent**: `%s`\n", a.AgentID))
	sb.WriteString(fmt.Sprintf("- **Quality**: %.1f\n", a.Quality))
	sb.WriteString(fmt.Sprintf("- **Efficiency**: %.1f\n", a.Efficiency))
	sb.WriteString(fmt.Sprintf("- **Reason**: %s\n", a.QualityReason))
	sb.WriteString(fmt.Sprintf("- **Analyzed**: %s\n", a.Timestamp.Format(time.RFC3339)))
	if len(a.SkillsUsed) > 0 {
		sb.WriteString(fmt.Sprintf("\n## Skills Used\n\n%s\n", strings.Join(a.SkillsUsed, ", ")))
	}
	if len(a.SkillsNeeded) > 0 {
		sb.WriteString(fmt.Sprintf("\n## Skills Needed\n\n%s\n", strings.Join(a.SkillsNeeded, ", ")))
	}
	if len(a.Suggestions) > 0 {
		sb.WriteString("\n## Evolution Suggestions\n\n")
		for i, s := range a.Suggestions {
			sb.WriteString(fmt.Sprintf("%d. **%s** `%s` (priority: %.1f)\n   %s\n   → %s\n\n",
				i+1, s.Type, s.SkillName, s.Priority, s.Reason, s.Description))
		}
	}
	return sb.String()
}

// SkillFromMarkdown parses a SkillRecord from Markdown (legacy format).
func SkillFromMarkdown(md string) (*SkillRecord, error) {
	rec := &SkillRecord{IsActive: true}
	lines := strings.Split(md, "\n")

	for i, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "# Skill: "):
			rec.Name = strings.TrimPrefix(line, "# Skill: ")
		case strings.HasPrefix(line, "- **ID**: `"):
			rec.SkillID = strings.Trim(strings.TrimPrefix(line, "- **ID**: `"), "`")
		case strings.HasPrefix(line, "- **Origin**: "):
			rec.Origin = SkillOrigin(strings.TrimPrefix(line, "- **Origin**: "))
		case strings.HasPrefix(line, "- **Generation**: "):
			fmt.Sscanf(strings.TrimPrefix(line, "- **Generation**: "), "%d", &rec.Generation)
		case strings.HasPrefix(line, "- **Parents**: "):
			parents := strings.TrimPrefix(line, "- **Parents**: ")
			rec.ParentIDs = strings.Split(parents, ", ")
		case strings.HasPrefix(line, "- **Source Task**: `"):
			rec.SourceTaskID = strings.Trim(strings.TrimPrefix(line, "- **Source Task**: `"), "`")
		case strings.HasPrefix(line, "- **Change**: "):
			rec.ChangeSummary = strings.TrimPrefix(line, "- **Change**: ")
		case strings.HasPrefix(line, "- **Created**: "):
			rec.CreatedAt, _ = time.Parse(time.RFC3339, strings.TrimPrefix(line, "- **Created**: "))
		case line == "## Description":
			var desc []string
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(strings.TrimSpace(lines[j]), "## ") {
					break
				}
				desc = append(desc, lines[j])
			}
			rec.Description = strings.TrimSpace(strings.Join(desc, "\n"))
		}
	}

	if rec.Name == "" || rec.SkillID == "" {
		return nil, fmt.Errorf("missing name or ID in skill markdown")
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	rec.UpdatedAt = time.Now().UTC()
	return rec, nil
}
