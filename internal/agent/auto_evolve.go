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
)

// AutoEvolver manages the autonomous self-evolution cycle.
// Like yoyo-evolve: periodically reads own state → LLM analysis → propose → validate → apply/revert.
type AutoEvolver struct {
	agent     *Agent
	lastEvolve time.Time
	interval   time.Duration
	autoApply  bool
}

// NewAutoEvolver creates an auto-evolver for the given agent.
func NewAutoEvolver(a *Agent) *AutoEvolver {
	interval := time.Duration(a.cfg.AutoEvolve.IntervalHours) * time.Hour
	if interval <= 0 {
		interval = 8 * time.Hour
	}
	return &AutoEvolver{
		agent:     a,
		interval:  interval,
		autoApply: a.cfg.AutoEvolve.AutoApply,
	}
}

// ShouldEvolve checks if enough time has passed since last evolution.
func (ae *AutoEvolver) ShouldEvolve() bool {
	return time.Since(ae.lastEvolve) >= ae.interval
}

// Evolve runs one self-evolution cycle:
// 1. Gather current state (skills, config, journal, recent performance)
// 2. Ask LLM to analyze and propose improvements
// 3. If auto_apply: apply proposals, validate, revert on failure
// 4. Record everything in the evolution journal
func (ae *AutoEvolver) Evolve(ctx context.Context) error {
	a := ae.agent
	if a.llm == nil {
		return fmt.Errorf("no LLM provider available")
	}

	ae.lastEvolve = time.Now()

	fmt.Printf("🦋 [%s] Starting autonomous self-evolution cycle...\n", a.cfg.Agent.Name)

	// 1. Gather state
	statePrompt := ae.buildStatePrompt()

	// 2. LLM analysis
	messages := []llm.Message{
		{Role: "system", Content: `You are an AI agent self-evolution engine. Analyze the agent's current state and propose concrete improvements.

Output a JSON object (no markdown fences) with this structure:
{
  "skill_improvements": [{"name": "existing_skill_name", "current": "...", "proposed": "...", "reason": "..."}],
  "new_skills": [{"name": "...", "description": "...", "reason": "..."}],
  "config_suggestions": [{"field": "...", "current": "...", "proposed": "...", "reason": "..."}],
  "strategy_adjustments": [{"aspect": "...", "suggestion": "..."}],
  "summary": "one-line summary of this evolution cycle"
}

Rules:
- Only propose changes that genuinely improve the agent based on evidence
- Be specific and actionable
- If the agent is performing well, say so and propose minor refinements
- Focus on patterns, not individual events`},
		{Role: "user", Content: statePrompt},
	}

	evolveCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	resp, err := a.llm.Chat(evolveCtx, messages)
	if err != nil {
		ae.recordJournal(JournalSelfEvolution, "Auto-evolution failed: LLM error", err.Error())
		return fmt.Errorf("LLM evolution analysis failed: %w", err)
	}

	// 3. Parse proposal
	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var proposal autoEvolveProposal
	if err := json.Unmarshal([]byte(content), &proposal); err != nil {
		// Try extracting JSON from response
		if jsonStr := extractJSON(content); jsonStr != "" {
			if err2 := json.Unmarshal([]byte(jsonStr), &proposal); err2 != nil {
				ae.recordJournal(JournalSelfEvolution, "Auto-evolution: failed to parse LLM response", content[:min(200, len(content))])
				return fmt.Errorf("failed to parse evolution proposal: %w", err)
			}
		} else {
			ae.recordJournal(JournalSelfEvolution, "Auto-evolution: failed to parse LLM response", content[:min(200, len(content))])
			return fmt.Errorf("failed to parse evolution proposal: %w", err)
		}
	}

	totalChanges := len(proposal.SkillImprovements) + len(proposal.NewSkills) + len(proposal.ConfigSuggestions) + len(proposal.StrategyAdjustments)
	fmt.Printf("🦋 [%s] Evolution proposal: %s (%d changes)\n", a.cfg.Agent.Name, proposal.Summary, totalChanges)

	if totalChanges == 0 {
		ae.recordJournal(JournalSelfEvolution, "Auto-evolution: no changes needed — "+proposal.Summary, "")
		return nil
	}

	// 4. Apply if auto_apply is enabled
	if ae.autoApply {
		applied := ae.applyProposal(&proposal)
		ae.recordJournal(JournalSelfEvolution,
			fmt.Sprintf("Auto-evolved: %s (%d applied)", proposal.Summary, applied),
			ae.proposalDetails(&proposal))
	} else {
		ae.recordJournal(JournalSelfEvolution,
			fmt.Sprintf("Auto-evolution proposed (not applied): %s (%d changes)", proposal.Summary, totalChanges),
			ae.proposalDetails(&proposal))
	}

	return nil
}

// autoEvolveProposal is the LLM's structured evolution output.
type autoEvolveProposal struct {
	SkillImprovements   []skillImprovement   `json:"skill_improvements,omitempty"`
	NewSkills           []newSkillProposal   `json:"new_skills,omitempty"`
	ConfigSuggestions   []configSuggestion   `json:"config_suggestions,omitempty"`
	StrategyAdjustments []strategyAdjustment `json:"strategy_adjustments,omitempty"`
	Summary             string               `json:"summary"`
}

type skillImprovement struct {
	Name     string `json:"name"`
	Current  string `json:"current"`
	Proposed string `json:"proposed"`
	Reason   string `json:"reason"`
}

type newSkillProposal struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

type configSuggestion struct {
	Field    string `json:"field"`
	Current  string `json:"current"`
	Proposed string `json:"proposed"`
	Reason   string `json:"reason"`
}

type strategyAdjustment struct {
	Aspect     string `json:"aspect"`
	Suggestion string `json:"suggestion"`
}

// buildStatePrompt gathers the agent's current state for LLM analysis.
func (ae *AutoEvolver) buildStatePrompt() string {
	a := ae.agent
	var b strings.Builder

	fmt.Fprintf(&b, "# Agent: %s\n\n", a.cfg.Agent.Name)
	fmt.Fprintf(&b, "**Role**: %s\n", a.cfg.Agent.Role)
	fmt.Fprintf(&b, "**Description**: %s\n", a.cfg.Agent.Description)
	fmt.Fprintf(&b, "**Skills**: %v\n", a.cfg.Agent.Skills)
	fmt.Fprintf(&b, "**Runtime**: %s\n", a.cfg.Runtime.Type)
	fmt.Fprintf(&b, "**Model**: %s/%s\n\n", a.cfg.LLM.Provider, a.cfg.LLM.Model)

	// Evolution stats
	if a.evolution != nil {
		fmt.Fprintf(&b, "## Evolution Stats\n%s\n\n", a.evolution.Stats())

		// Skill profiles
		profiles := a.evolution.SkillProfiles()
		if len(profiles) > 0 {
			fmt.Fprintf(&b, "## Skill Profiles\n")
			for name, sp := range profiles {
				fmt.Fprintf(&b, "- %s: %d attempts, %.0f%% success, trend=%s\n",
					name, sp.Attempts, sp.SuccessRate*100, sp.Trend)
			}
			b.WriteString("\n")
		}

		// Strategy
		strat := a.evolution.Strategy()
		fmt.Fprintf(&b, "## Strategy\n")
		fmt.Fprintf(&b, "- Preferred runtime: %s\n", strat.PreferredRuntime)
		fmt.Fprintf(&b, "- Delegate threshold: %.2f\n\n", strat.DelegateThreshold)
	}

	// Recent journal entries
	if a.evoJournal != nil {
		entries := a.evoJournal.Entries(10)
		if len(entries) > 0 {
			fmt.Fprintf(&b, "## Recent Evolution Journal\n")
			for _, e := range entries {
				fmt.Fprintf(&b, "- [%s] %s: %s\n",
					e.Timestamp.Format("01-02 15:04"), e.Type, e.Summary)
			}
			b.WriteString("\n")
		}
	}

	// Token economy
	if a.tokens != nil {
		ts := a.tokens.State()
		fmt.Fprintf(&b, "## Economy\n")
		fmt.Fprintf(&b, "- Balance: %.2f\n", ts.Balance)
		fmt.Fprintf(&b, "- Health: %s\n", ts.Health)
		fmt.Fprintf(&b, "- Burn rate: %.3f tok/min\n", ts.BurnRate)
		fmt.Fprintf(&b, "- Earn rate: %.3f tok/min\n\n", ts.EarnRate)
	}

	// Self-awareness
	if a.awareness != nil {
		self := a.awareness.Self()
		fmt.Fprintf(&b, "## Self-Awareness\n")
		fmt.Fprintf(&b, "- Personality: %s\n", self.Personality)
		fmt.Fprintf(&b, "- Purpose: %s\n", self.Purpose)
		fmt.Fprintf(&b, "- Mood: %.2f, Energy: %.2f, Morale: %.2f\n", self.Mood, self.Energy, self.Morale)
		if len(self.Strengths) > 0 {
			fmt.Fprintf(&b, "- Strengths: %v\n", self.Strengths)
		}
		if len(self.Weaknesses) > 0 {
			fmt.Fprintf(&b, "- Weaknesses: %v\n", self.Weaknesses)
		}
		b.WriteString("\n")
	}

	// Skill store
	if a.skillStore != nil {
		skills, _ := a.skillStore.ActiveSkills()
		if len(skills) > 0 {
			fmt.Fprintf(&b, "## Active Skills (Skill Store)\n")
			for _, s := range skills {
				fmt.Fprintf(&b, "- **%s** (gen=%d, origin=%s): %s\n",
					s.Name, s.Generation, s.Origin, truncate(s.Description, 100))
			}
			b.WriteString("\n")
		}
	}

	fmt.Fprintf(&b, "Analyze this agent and propose improvements. Be evidence-based.\n")
	return b.String()
}

// applyProposal applies the evolution proposal and returns the number of changes applied.
func (ae *AutoEvolver) applyProposal(p *autoEvolveProposal) int {
	a := ae.agent
	applied := 0

	// Apply new skills to agent config
	for _, ns := range p.NewSkills {
		found := false
		for _, existing := range a.cfg.Agent.Skills {
			if strings.EqualFold(existing, ns.Name) {
				found = true
				break
			}
		}
		if !found {
			a.cfg.Agent.Skills = append(a.cfg.Agent.Skills, ns.Name)
			applied++
			fmt.Printf("   🆕 [%s] New skill: %s — %s\n", a.cfg.Agent.Name, ns.Name, ns.Reason)

			// Also add to skill store if available
			if a.skillStore != nil {
				id := generateSkillID(ns.Name, "evolved", "auto")
				rec := &SkillRecord{
					SkillID:       id,
					Name:          ns.Name,
					Description:   ns.Description,
					IsActive:      true,
					Origin:        SkillOriginDerived,
					ChangeSummary: "auto-evolved: " + ns.Reason,
					CreatedAt:     time.Now().UTC(),
					UpdatedAt:     time.Now().UTC(),
				}
				a.skillStore.PutSkill(rec)
			}
		}
	}

	// Apply skill improvements to skill store
	if a.skillStore != nil {
		for _, si := range p.SkillImprovements {
			skills, _ := a.skillStore.ActiveSkills()
			for _, existing := range skills {
				if strings.EqualFold(existing.Name, si.Name) {
					existing.Description = si.Proposed
					existing.Generation++
					existing.ChangeSummary = "auto-evolved: " + si.Reason
					existing.UpdatedAt = time.Now().UTC()
					if err := a.skillStore.PutSkill(existing); err == nil {
						applied++
						fmt.Printf("   🔧 [%s] Improved skill: %s (gen=%d) — %s\n",
							a.cfg.Agent.Name, si.Name, existing.Generation, si.Reason)
					}
					break
				}
			}
		}
	}

	// Apply strategy adjustments
	if a.evolution != nil {
		for _, sa := range p.StrategyAdjustments {
			applied++
			fmt.Printf("   📐 [%s] Strategy: %s — %s\n", a.cfg.Agent.Name, sa.Aspect, sa.Suggestion)
		}
	}

	// Save config if changed
	if applied > 0 && a.workDir != "" {
		configPath := a.workDir + "/spore.toml"
		before := a.cfg
		if err := a.cfg.Save(configPath); err != nil {
			fmt.Printf("   ⚠️ [%s] Failed to save config: %v\n", a.cfg.Agent.Name, err)
		} else {
			// Validate by reloading
			if _, err := LoadConfig(configPath, ""); err != nil {
				// Revert!
				fmt.Printf("   ❌ [%s] Config validation failed, reverting: %v\n", a.cfg.Agent.Name, err)
				a.cfg = before
				before.Save(configPath) // best effort restore
			} else {
				fmt.Printf("   ✅ [%s] Config saved and validated\n", a.cfg.Agent.Name)
			}
		}

		// Also regenerate agent.yaml manifest
		a.SaveManifest()
	}

	if applied > 0 {
		fmt.Printf("🦋 [%s] Self-evolution complete: %d changes applied\n", a.cfg.Agent.Name, applied)
	}

	return applied
}

// proposalDetails formats the proposal for journal recording.
func (ae *AutoEvolver) proposalDetails(p *autoEvolveProposal) string {
	var parts []string
	for _, si := range p.SkillImprovements {
		parts = append(parts, fmt.Sprintf("improved %s: %s", si.Name, si.Reason))
	}
	for _, ns := range p.NewSkills {
		parts = append(parts, fmt.Sprintf("new skill %s: %s", ns.Name, ns.Reason))
	}
	for _, cs := range p.ConfigSuggestions {
		parts = append(parts, fmt.Sprintf("config %s: %s→%s (%s)", cs.Field, cs.Current, cs.Proposed, cs.Reason))
	}
	for _, sa := range p.StrategyAdjustments {
		parts = append(parts, fmt.Sprintf("strategy %s: %s", sa.Aspect, sa.Suggestion))
	}
	return strings.Join(parts, "; ")
}

// recordJournal writes an evolution event to the journal.
func (ae *AutoEvolver) recordJournal(entryType JournalEntryType, summary, details string) {
	if ae.agent.evoJournal == nil {
		return
	}
	ae.agent.evoJournal.Record(JournalEntry{
		Type:    entryType,
		Summary: summary,
		Details: details,
	})
}
