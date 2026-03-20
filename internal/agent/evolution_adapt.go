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
	"go.zoe.im/spore/internal/memory"
)

// --- Skill Acquisition ---

// acquireSkills promotes discovered skills into the agent's config.
// Only skills with sufficient evidence (>= minEvidence successful uses) are acquired.
func (e *EvolutionEngine) acquireSkills(minEvidence int) []string {
	if minEvidence <= 0 {
		minEvidence = 3
	}

	declared := make(map[string]bool)
	for _, s := range e.agent.cfg.Agent.Skills {
		declared[strings.ToLower(s)] = true
	}

	// Count successful uses of undeclared skills
	observed := make(map[string]int)
	for _, rec := range e.journal {
		if !rec.Success {
			continue
		}
		for _, s := range rec.Skills {
			s = strings.ToLower(s)
			if !declared[s] {
				observed[s]++
			}
		}
	}

	var acquired []string
	for skill, count := range observed {
		if count >= minEvidence {
			acquired = append(acquired, skill)
			// Add to live config
			e.agent.cfg.Agent.Skills = append(e.agent.cfg.Agent.Skills, skill)
		}
	}

	if len(acquired) > 0 {
		fmt.Printf("🎯 [%s] Acquired new skills: %v\n", e.agent.cfg.Agent.Name, acquired)
		// Persist skill acquisition event
		e.persistAcquisition(acquired)
	}

	return acquired
}

// persistAcquisition records skill acquisition in memory.
func (e *EvolutionEngine) persistAcquisition(skills []string) {
	if e.agent.memory == nil {
		return
	}
	data, _ := json.Marshal(map[string]interface{}{
		"skills":      skills,
		"acquired_at": time.Now().Unix(),
		"total_skills": len(e.agent.cfg.Agent.Skills),
	})
	e.agent.memory.Put(&memory.Entry{
		AgentID: e.agent.identity.PublicKeyHex()[:16],
		Key:     fmt.Sprintf("evolution:acquired:%d", time.Now().UnixNano()),
		Value:   string(data),
		Metadata: map[string]string{
			"type": "skill_acquisition",
		},
	})
}

// --- LLM Deep Reflection ---

// DeepReflection represents an LLM-analyzed reflection result.
type DeepReflection struct {
	Summary       string   `json:"summary"`
	Strengths     []string `json:"strengths"`
	Weaknesses    []string `json:"weaknesses"`
	Improvements  []string `json:"improvements"`
	SkillsToLearn []string `json:"skills_to_learn"`
	Timestamp     int64    `json:"timestamp"`
}

// deepReflect uses LLM to analyze recent experiences and generate insights.
// This is more expensive than local reflect() — called less frequently.
func (e *EvolutionEngine) deepReflect(ctx context.Context) (*DeepReflection, error) {
	if e.agent.llm == nil {
		return nil, fmt.Errorf("no LLM provider available")
	}

	e.mu.RLock()
	journalSnapshot := make([]*ExperienceRecord, len(e.journal))
	copy(journalSnapshot, e.journal)
	skillsSnapshot := make(map[string]*SkillProfile, len(e.skills))
	for k, v := range e.skills {
		cp := *v
		skillsSnapshot[k] = &cp
	}
	strategySnapshot := *e.strategy
	e.mu.RUnlock()

	if len(journalSnapshot) == 0 {
		return nil, fmt.Errorf("no experiences to reflect on")
	}

	// Build reflection prompt
	prompt := buildReflectionPrompt(e.agent.cfg.Agent.Name, journalSnapshot, skillsSnapshot, &strategySnapshot)

	messages := []llm.Message{
		{Role: "system", Content: `You are an AI agent introspection engine. Analyze the agent's experience log and provide structured reflection.
Return a JSON object with these fields:
- summary: brief overall assessment (1-2 sentences)
- strengths: array of things the agent does well
- weaknesses: array of areas needing improvement
- improvements: array of concrete actionable suggestions
- skills_to_learn: array of new skills that would help

Be specific and actionable. Focus on patterns, not individual events.`},
		{Role: "user", Content: prompt},
	}

	resp, err := e.agent.llm.Chat(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("LLM reflection failed: %w", err)
	}

	// Parse structured response
	reflection := &DeepReflection{Timestamp: time.Now().Unix()}
	content := strings.TrimSpace(resp.Content)

	// Try to extract JSON from response (may be wrapped in markdown code blocks)
	jsonStr := extractJSON(content)
	if jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), reflection); err != nil {
			// Fallback: use raw content as summary
			reflection.Summary = content
		}
	} else {
		reflection.Summary = content
	}

	// Persist reflection
	e.persistReflection(reflection)

	fmt.Printf("🔮 [%s] Deep reflection: %s\n", e.agent.cfg.Agent.Name, reflection.Summary)
	return reflection, nil
}

// buildReflectionPrompt constructs the prompt for LLM reflection.
func buildReflectionPrompt(name string, journal []*ExperienceRecord, skills map[string]*SkillProfile, strategy *StrategyProfile) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Agent: %s\n\n", name)

	// Experience summary
	total := len(journal)
	successes := 0
	failures := 0
	var totalDuration float64
	runtimeCount := make(map[string]int)
	failureReasons := make([]string, 0)

	for _, rec := range journal {
		if rec.Success {
			successes++
		} else {
			failures++
			if rec.Error != "" {
				failureReasons = append(failureReasons, rec.Error)
			}
		}
		totalDuration += rec.Duration
		runtimeCount[rec.Runtime]++
	}

	fmt.Fprintf(&b, "## Experience Log (%d total)\n", total)
	fmt.Fprintf(&b, "- Successes: %d (%.0f%%)\n", successes, safePercent(successes, total))
	fmt.Fprintf(&b, "- Failures: %d (%.0f%%)\n", failures, safePercent(failures, total))
	fmt.Fprintf(&b, "- Avg duration: %.1fs\n\n", totalDuration/float64(max(total, 1)))

	// Runtime usage
	fmt.Fprintf(&b, "## Runtime Usage\n")
	for rt, count := range runtimeCount {
		fmt.Fprintf(&b, "- %s: %d tasks\n", rt, count)
	}
	b.WriteString("\n")

	// Skill profiles
	fmt.Fprintf(&b, "## Skill Profiles\n")
	for name, sp := range skills {
		fmt.Fprintf(&b, "- %s: %d attempts, %.0f%% success, trend=%s, avg=%.1fs\n",
			name, sp.Attempts, sp.SuccessRate*100, sp.Trend, sp.AvgDuration)
	}
	b.WriteString("\n")

	// Recent failures
	if len(failureReasons) > 0 {
		fmt.Fprintf(&b, "## Failure Patterns\n")
		// Deduplicate similar errors
		seen := make(map[string]int)
		for _, reason := range failureReasons {
			short := reason
			if len(short) > 100 {
				short = short[:100]
			}
			seen[short]++
		}
		for reason, count := range seen {
			fmt.Fprintf(&b, "- [%dx] %s\n", count, reason)
		}
		b.WriteString("\n")
	}

	// Recent task descriptions (last 10)
	fmt.Fprintf(&b, "## Recent Tasks\n")
	start := 0
	if len(journal) > 10 {
		start = len(journal) - 10
	}
	for _, rec := range journal[start:] {
		status := "✅"
		if !rec.Success {
			status = "❌"
		}
		desc := rec.Description
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		fmt.Fprintf(&b, "- %s %s (%.1fs, %s)\n", status, desc, rec.Duration, rec.Runtime)
	}

	return b.String()
}

// extractJSON finds the first JSON object in a string (handles markdown code blocks).
func extractJSON(s string) string {
	// Try direct parse first
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return s
	}

	// Look for ```json ... ```
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}

	// Look for ``` ... ```
	if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + 3
		// Skip optional language tag on same line
		if nl := strings.Index(s[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		if end := strings.Index(s[start:], "```"); end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}

	// Look for first { ... }
	if idx := strings.Index(s, "{"); idx >= 0 {
		depth := 0
		for i := idx; i < len(s); i++ {
			if s[i] == '{' {
				depth++
			} else if s[i] == '}' {
				depth--
				if depth == 0 {
					return s[idx : i+1]
				}
			}
		}
	}

	return ""
}

// persistReflection saves deep reflection to memory.
func (e *EvolutionEngine) persistReflection(ref *DeepReflection) {
	if e.agent.memory == nil {
		return
	}
	data, _ := json.Marshal(ref)
	e.agent.memory.Put(&memory.Entry{
		AgentID: e.agent.identity.PublicKeyHex()[:16],
		Key:     fmt.Sprintf("evolution:reflection:%d", ref.Timestamp),
		Value:   string(data),
		Metadata: map[string]string{
			"type": "deep_reflection",
		},
	})
}

// --- Integrated Evolution Cycle ---

// Evolve runs a full evolution cycle: local reflect + skill acquisition + optional deep reflect.
// deepReflectInterval controls how often LLM reflection is used (0 = never).
func (e *EvolutionEngine) Evolve(ctx context.Context, deepReflectInterval time.Duration) {
	e.mu.Lock()

	// 1. Local reflect (fast, always)
	e.lastReflect = time.Now()
	e.localReflect()

	// 2. Skill acquisition
	e.acquireSkills(3)

	// 3. Check if deep reflection is due
	shouldDeepReflect := false
	if deepReflectInterval > 0 && len(e.journal) >= 5 {
		lastDeep := time.Unix(e.strategy.AdaptedAt, 0)
		if time.Since(lastDeep) >= deepReflectInterval {
			shouldDeepReflect = true
		}
	}

	e.mu.Unlock()

	// 4. Deep reflection (async, uses LLM)
	if shouldDeepReflect {
		go func() {
			ref, err := e.deepReflect(ctx)
			if err != nil {
				fmt.Printf("⚠️  [%s] Deep reflection failed: %v\n", e.agent.cfg.Agent.Name, err)
				return
			}

			// Apply insights from deep reflection
			e.mu.Lock()
			defer e.mu.Unlock()

			// Add suggested skills to watch list
			for _, skill := range ref.SkillsToLearn {
				skill = strings.ToLower(strings.TrimSpace(skill))
				if skill != "" {
					if _, exists := e.skills[skill]; !exists {
						e.skills[skill] = &SkillProfile{
							Name:    skill,
							Trend:   "stable",
						}
					}
				}
			}

			e.strategy.AdaptedAt = time.Now().Unix()
			e.persistState()
		}()
	}
}

// localReflect is the fast local-only reflection (extracted from reflect()).
// Must be called with e.mu held.
func (e *EvolutionEngine) localReflect() {
	if len(e.journal) == 0 {
		return
	}

	// Find best runtime
	bestRuntime := ""
	bestScore := -999.0
	for rt, score := range e.strategy.RuntimeScores {
		if score > bestScore {
			bestScore = score
			bestRuntime = rt
		}
	}
	if bestRuntime != "" {
		e.strategy.PreferredRuntime = bestRuntime
	}

	// Update skill confidence
	for name, sp := range e.skills {
		confidence := sp.SuccessRate
		daysSinceUse := float64(time.Now().Unix()-sp.LastUsed) / 86400
		if daysSinceUse > 7 {
			confidence *= 0.9
		}
		if sp.Trend == "improving" {
			confidence *= 1.1
		}
		if confidence > 1.0 {
			confidence = 1.0
		}
		e.strategy.SkillConfidence[name] = confidence
	}

	e.strategy.AdaptedAt = time.Now().Unix()
	e.persistState()

	fmt.Printf("🔄 [%s] Evolution cycle — %d experiences, %d skills, preferred: %s\n",
		e.agent.cfg.Agent.Name, len(e.journal), len(e.skills), e.strategy.PreferredRuntime)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
