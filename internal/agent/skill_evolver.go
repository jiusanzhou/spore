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
type SkillEvolver struct {
	provider llm.Provider
	store    *SkillStore
	agentID  string
}

// NewSkillEvolver creates an evolver.
func NewSkillEvolver(provider llm.Provider, store *SkillStore, agentID string) *SkillEvolver {
	return &SkillEvolver{
		provider: provider,
		store:    store,
		agentID:  agentID,
	}
}

// Evolve processes a list of evolution suggestions from the analyzer.
// It applies high-priority suggestions (priority >= threshold).
func (se *SkillEvolver) Evolve(ctx context.Context, analysis *ExecutionAnalysisResult, threshold float64) ([]*SkillRecord, error) {
	if se.store == nil || se.provider == nil {
		return nil, nil
	}

	var evolved []*SkillRecord

	for _, sug := range analysis.Suggestions {
		if sug.Priority < threshold {
			continue
		}

		select {
		case <-ctx.Done():
			return evolved, ctx.Err()
		default:
		}

		var rec *SkillRecord
		var err error

		switch sug.Type {
		case EvolutionFix:
			rec, err = se.executeFix(ctx, &sug, analysis.TaskID)
		case EvolutionDerived:
			rec, err = se.executeDerive(ctx, &sug, analysis.TaskID)
		case EvolutionCaptured:
			rec, err = se.executeCapture(ctx, &sug, analysis.TaskID)
		default:
			fmt.Printf("⚠️  [evolver] Unknown evolution type: %s\n", sug.Type)
			continue
		}

		if err != nil {
			fmt.Printf("⚠️  [evolver] %s evolution failed for %s: %v\n", sug.Type, sug.SkillName, err)
			continue
		}

		if rec != nil {
			evolved = append(evolved, rec)
			fmt.Printf("🧬 [evolver] %s → %s: %s (gen=%d)\n",
				sug.Type, rec.Name, truncate(rec.Description, 60), rec.Generation)
		}
	}

	return evolved, nil
}

// executeFix repairs a broken skill. Finds the existing skill, asks LLM to fix it.
func (se *SkillEvolver) executeFix(ctx context.Context, sug *EvolutionSuggestion, taskID string) (*SkillRecord, error) {
	// Find existing skill by name
	parent, err := se.findSkillByName(sug.SkillName)
	if err != nil || parent == nil {
		return nil, fmt.Errorf("skill %q not found for fix", sug.SkillName)
	}

	// Ask LLM for the fix
	prompt := fmt.Sprintf(`You are fixing a broken AI agent skill.

Skill name: %s
Current description: %s
Problem: %s

Generate a FIXED description for this skill. The fix should address the problem while keeping the skill's core purpose.

Respond with ONLY a JSON object:
{
  "description": "the fixed skill description (detailed, actionable instructions)"
}`, parent.Name, parent.Description, sug.Reason)

	resp, err := se.provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	fixResult, err := parseEvolutionResponse(resp.Content)
	if err != nil {
		return nil, err
	}

	// Deactivate old version
	parent.IsActive = false
	if err := se.store.PutSkill(parent); err != nil {
		return nil, fmt.Errorf("deactivate parent: %w", err)
	}

	// Create new version
	newID := generateSkillID(parent.Name, "fix", taskID)
	rec := &SkillRecord{
		SkillID:       newID,
		Name:          parent.Name,
		Description:   fixResult.Description,
		IsActive:      true,
		Origin:        SkillOriginFixed,
		Generation:    parent.Generation + 1,
		ParentIDs:     []string{parent.SkillID},
		SourceTaskID:  taskID,
		ChangeSummary: sug.Reason,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	if err := se.store.PutSkill(rec); err != nil {
		return nil, fmt.Errorf("store fixed skill: %w", err)
	}

	return rec, nil
}

// executeDerive creates an enhanced version of an existing skill.
func (se *SkillEvolver) executeDerive(ctx context.Context, sug *EvolutionSuggestion, taskID string) (*SkillRecord, error) {
	parent, err := se.findSkillByName(sug.SkillName)
	if err != nil || parent == nil {
		// If no existing skill, treat as capture instead
		return se.executeCapture(ctx, sug, taskID)
	}

	prompt := fmt.Sprintf(`You are enhancing an AI agent skill.

Parent skill: %s
Parent description: %s
Enhancement goal: %s
What the derived skill should do: %s

Generate a DERIVED skill that improves on the parent.

Respond with ONLY a JSON object:
{
  "name": "new skill name (can be same or different)",
  "description": "enhanced skill description (detailed, actionable)"
}`, parent.Name, parent.Description, sug.Reason, sug.Description)

	resp, err := se.provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	deriveResult, err := parseEvolutionResponse(resp.Content)
	if err != nil {
		return nil, err
	}

	name := deriveResult.Name
	if name == "" {
		name = sug.SkillName
	}

	newID := generateSkillID(name, "derived", taskID)
	rec := &SkillRecord{
		SkillID:       newID,
		Name:          name,
		Description:   deriveResult.Description,
		IsActive:      true,
		Origin:        SkillOriginDerived,
		Generation:    parent.Generation + 1,
		ParentIDs:     []string{parent.SkillID},
		SourceTaskID:  taskID,
		ChangeSummary: sug.Reason,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	if err := se.store.PutSkill(rec); err != nil {
		return nil, fmt.Errorf("store derived skill: %w", err)
	}

	return rec, nil
}

// executeCapture creates a brand-new skill from a novel pattern.
func (se *SkillEvolver) executeCapture(ctx context.Context, sug *EvolutionSuggestion, taskID string) (*SkillRecord, error) {
	prompt := fmt.Sprintf(`You are capturing a novel pattern as a reusable AI agent skill.

Skill name: %s
Why this pattern is valuable: %s
What the skill should do: %s

Generate a skill description that captures this pattern for future reuse.

Respond with ONLY a JSON object:
{
  "name": "skill name (concise, hyphenated)",
  "description": "detailed skill description (actionable instructions an AI agent can follow)"
}`, sug.SkillName, sug.Reason, sug.Description)

	resp, err := se.provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	captureResult, err := parseEvolutionResponse(resp.Content)
	if err != nil {
		return nil, err
	}

	name := captureResult.Name
	if name == "" {
		name = sug.SkillName
	}

	newID := generateSkillID(name, "captured", taskID)
	rec := &SkillRecord{
		SkillID:       newID,
		Name:          name,
		Description:   captureResult.Description,
		IsActive:      true,
		Origin:        SkillOriginCaptured,
		Generation:    0, // root node
		SourceTaskID:  taskID,
		ChangeSummary: fmt.Sprintf("Captured from task: %s", sug.Reason),
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	if err := se.store.PutSkill(rec); err != nil {
		return nil, fmt.Errorf("store captured skill: %w", err)
	}

	return rec, nil
}

// findSkillByName searches for an active skill by name (case-insensitive).
func (se *SkillEvolver) findSkillByName(name string) (*SkillRecord, error) {
	skills, err := se.store.ActiveSkills()
	if err != nil {
		return nil, err
	}
	nameLower := strings.ToLower(name)
	for _, s := range skills {
		if strings.ToLower(s.Name) == nameLower {
			return s, nil
		}
	}
	return nil, nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

type evolutionResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func parseEvolutionResponse(content string) (*evolutionResponse, error) {
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

	var r evolutionResponse
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return nil, fmt.Errorf("invalid evolution JSON: %w", err)
	}
	if r.Description == "" {
		return nil, fmt.Errorf("empty description in evolution response")
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
	// Collapse multiple hyphens
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
