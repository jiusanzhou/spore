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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/runtime"
)

// SkillBackend is the interface used by ExecutionAnalyzer and SkillEvolver
// for reading skill data and storing analyses. Both SkillStore (legacy) and
// SkillFS (new file-system-first store) implement this.
type SkillBackend interface {
	PutAnalysis(a *ExecutionAnalysisResult) error
	RecentAnalyses(agentID string, limit int) ([]*ExecutionAnalysisResult, error)
}

// SkillLister provides skill listing for analysis context.
type SkillLister interface {
	// ActiveSkillSummary returns human-readable skill summaries for LLM context.
	ActiveSkillSummary() string
}

// ExecutionAnalyzer performs post-task LLM analysis to assess quality,
// identify skill usage, and suggest evolution actions (FIX/DERIVED/CAPTURED).
//
// Inspired by OpenSpace's ExecutionAnalyzer, adapted for Spore's
// decentralized multi-agent architecture.
type ExecutionAnalyzer struct {
	provider llm.Provider
	backend  SkillBackend
	lister   SkillLister
	agentID  string
}

// NewExecutionAnalyzer creates an analyzer backed by an LLM provider and skill backend.
func NewExecutionAnalyzer(provider llm.Provider, backend SkillBackend, agentID string) *ExecutionAnalyzer {
	ea := &ExecutionAnalyzer{
		provider: provider,
		backend:  backend,
		agentID:  agentID,
	}
	if lister, ok := backend.(SkillLister); ok {
		ea.lister = lister
	}
	return ea
}

// Analyze runs post-execution analysis on a completed task.
// It asks the LLM to assess quality, identify skill patterns, and suggest evolutions.
func (ea *ExecutionAnalyzer) Analyze(ctx context.Context, entry *taskEntry, output *runtime.TaskOutput, rtName string, duration float64) (*ExecutionAnalysisResult, error) {
	if ea.provider == nil {
		return nil, fmt.Errorf("no LLM provider for analysis")
	}

	// Build skill context — what skills does this agent currently have?
	var skillSummary string
	if ea.lister != nil {
		skillSummary = ea.lister.ActiveSkillSummary()
	}
	if skillSummary == "" {
		skillSummary = "(no skills registered yet)"
	}

	// Fetch recent analyses for trend context
	var recentContext string
	if ea.backend != nil {
		recent, err := ea.backend.RecentAnalyses(ea.agentID, 3)
		if err == nil && len(recent) > 0 {
			var lines []string
			for _, r := range recent {
				status := "✅"
				if !r.Success {
					status = "❌"
				}
				lines = append(lines, fmt.Sprintf("- %s quality=%.1f eff=%.1f: %s",
					status, r.Quality, r.Efficiency, truncate(r.QualityReason, 80)))
			}
			recentContext = "Recent task analyses:\n" + strings.Join(lines, "\n")
		}
	}

	// Build analysis prompt
	resultSnippet := truncate(output.Result, 3000)
	errorSnippet := ""
	if output.Error != "" {
		errorSnippet = fmt.Sprintf("\n\nError output:\n%s", truncate(output.Error, 1000))
	}

	prompt := fmt.Sprintf(`You are an AI execution analyst. Analyze this task execution and respond with ONLY a JSON object.

## Task
Description: %s
Runtime: %s
Duration: %.1f seconds
Success: %v

## Result
%s%s

## Agent's Current Skills
%s

%s

## Your Analysis

Evaluate the execution and respond with this exact JSON structure:
{
  "success": true/false,
  "quality": 0.0-1.0,
  "efficiency": 0.0-1.0,
  "quality_reason": "brief explanation of quality/efficiency scores",
  "skills_used": ["skill names that were effectively used"],
  "skills_needed": ["skill names that would have improved the result but agent lacks"],
  "suggestions": [
    {
      "type": "fix|derived|captured",
      "skill_name": "name of skill to fix/derive/create",
      "reason": "why this evolution is needed",
      "description": "what the evolved skill should do",
      "priority": 0.0-1.0
    }
  ]
}

Rules for suggestions:
- "fix": An existing skill produced errors or bad output → repair it
- "derived": An existing skill works but could be enhanced for this task pattern → create improved version
- "captured": A novel successful pattern emerged that should become a reusable skill → capture it
- Only suggest if there's real value. Empty suggestions array is fine.
- Priority > 0.7 means urgent, 0.4-0.7 moderate, < 0.4 low priority.

Respond with ONLY the JSON object, no markdown fences, no explanation.`,
		entry.Description, rtName, duration, output.Success,
		resultSnippet, errorSnippet, skillSummary, recentContext)

	// Call LLM
	resp, err := ea.provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("analysis LLM call: %w", err)
	}

	// Parse response
	analysis, err := parseAnalysisResponse(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("parse analysis: %w", err)
	}

	analysis.TaskID = entry.ID
	analysis.AgentID = ea.agentID
	analysis.Timestamp = time.Now().UTC()

	// Persist
	if ea.backend != nil {
		if storeErr := ea.backend.PutAnalysis(analysis); storeErr != nil {
			fmt.Printf("⚠️  [analyzer] Failed to store analysis: %v\n", storeErr)
		}
	}

	return analysis, nil
}

// parseAnalysisResponse extracts the JSON analysis from LLM response.
func parseAnalysisResponse(content string) (*ExecutionAnalysisResult, error) {
	// Strip markdown fences if present
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		// Remove first and last lines (fences)
		if len(lines) >= 3 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	content = strings.TrimSpace(content)

	// Try to find JSON object in response
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		content = content[start : end+1]
	}

	var result ExecutionAnalysisResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON from LLM: %w\nraw: %s", err, truncate(content, 500))
	}

	// Clamp values
	if result.Quality < 0 {
		result.Quality = 0
	}
	if result.Quality > 1 {
		result.Quality = 1
	}
	if result.Efficiency < 0 {
		result.Efficiency = 0
	}
	if result.Efficiency > 1 {
		result.Efficiency = 1
	}
	for i := range result.Suggestions {
		if result.Suggestions[i].Priority < 0 {
			result.Suggestions[i].Priority = 0
		}
		if result.Suggestions[i].Priority > 1 {
			result.Suggestions[i].Priority = 1
		}
	}

	return &result, nil
}
