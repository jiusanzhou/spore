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

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/x/cli"
)

type evolveCmd struct {
	Dir   string `opts:"short=d,help=agent data directory"`
	Apply bool   `opts:"help=apply proposed changes (default: dry-run)"`
}

func init() {
	c := &evolveCmd{}
	app.Register(cli.New(
		cli.Name("evolve"),
		cli.Short("Self-evolve: analyze agent and propose improvements"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

// EvolutionProposal is a set of changes the LLM proposes.
type EvolutionProposal struct {
	SkillChanges  []SkillChange  `json:"skill_changes,omitempty"`
	ConfigChanges []ConfigChange `json:"config_changes,omitempty"`
	NewSkills     []NewSkill     `json:"new_skills,omitempty"`
	Summary       string         `json:"summary"`
}

// SkillChange proposes modifying an existing skill description.
type SkillChange struct {
	Name           string `json:"name"`
	CurrentDesc    string `json:"current_description"`
	ProposedDesc   string `json:"proposed_description"`
	Reason         string `json:"reason"`
}

// ConfigChange proposes a config tweak.
type ConfigChange struct {
	Field    string `json:"field"`
	Current  string `json:"current_value"`
	Proposed string `json:"proposed_value"`
	Reason   string `json:"reason"`
}

// NewSkill proposes adding a new skill.
type NewSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

func (c *evolveCmd) run() error {
	dir := c.Dir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.spore"
	}

	// Load agent config
	cfg, err := agent.LoadConfig("", dir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	applyGlobalConfig(cfg)

	// Create LLM provider
	provider, err := llm.NewProvider(cfg.LLM.Provider, llm.ProviderConfig{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
		Headers: cfg.LLM.Headers,
	})
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}

	// Load evolution journal for context
	journal := agent.NewEvolutionJournal(dir)
	recentEntries := journal.Entries(20)

	fmt.Printf("🦋 Self-evolution analysis for agent %q\n", cfg.Agent.Name)
	fmt.Printf("   Skills: %v\n", cfg.Agent.Skills)
	fmt.Printf("   Role:   %s\n", cfg.Agent.Role)
	fmt.Printf("   Model:  %s/%s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Println()

	// Build context for LLM
	var journalContext string
	if len(recentEntries) > 0 {
		var lines []string
		for _, e := range recentEntries {
			lines = append(lines, fmt.Sprintf("[%s] %s: %s",
				e.Timestamp.Format("01-02 15:04"), e.Type, e.Summary))
		}
		journalContext = "\n\nRecent evolution history:\n" + strings.Join(lines, "\n")
	}

	prompt := fmt.Sprintf(`You are an AI agent self-evolution engine. Analyze this agent's configuration and propose improvements.

Agent Name: %s
Role: %s
Description: %s
Skills: %v
Runtime: %s
Model: %s
Delegation: can_delegate=%v, can_receive=%v
%s

Analyze what can be improved:
1. Are skill descriptions actionable and detailed enough?
2. Are there missing skills the agent should have for its role?
3. Are there config tweaks that would improve performance?

Output a JSON object with this structure (no markdown, just JSON):
{
  "skill_changes": [{"name": "...", "current_description": "...", "proposed_description": "...", "reason": "..."}],
  "config_changes": [{"field": "...", "current_value": "...", "proposed_value": "...", "reason": "..."}],
  "new_skills": [{"name": "...", "description": "...", "reason": "..."}],
  "summary": "one-line summary of what was improved"
}

Be specific and actionable. Only propose changes that genuinely improve the agent.`,
		cfg.Agent.Name, cfg.Agent.Role, cfg.Agent.Description,
		cfg.Agent.Skills, cfg.Runtime.Type, cfg.LLM.Model,
		cfg.Agent.CanDelegate, cfg.Agent.CanReceive, journalContext)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Println("🔍 Analyzing agent configuration...")
	resp, err := provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: "You are a precise AI agent optimizer. Output only valid JSON."},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return fmt.Errorf("LLM analysis failed: %w", err)
	}

	// Parse proposal
	content := strings.TrimSpace(resp.Content)
	// Strip markdown code fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var proposal EvolutionProposal
	if err := json.Unmarshal([]byte(content), &proposal); err != nil {
		fmt.Printf("⚠️  Failed to parse LLM response as JSON: %v\n", err)
		fmt.Printf("Raw response:\n%s\n", resp.Content)
		return nil
	}

	// Display proposals
	fmt.Printf("\n📋 Evolution Proposal: %s\n\n", proposal.Summary)

	if len(proposal.SkillChanges) > 0 {
		fmt.Println("🔧 Skill Changes:")
		for _, sc := range proposal.SkillChanges {
			fmt.Printf("   %s:\n", sc.Name)
			fmt.Printf("     Before: %s\n", sc.CurrentDesc)
			fmt.Printf("     After:  %s\n", sc.ProposedDesc)
			fmt.Printf("     Reason: %s\n\n", sc.Reason)
		}
	}

	if len(proposal.NewSkills) > 0 {
		fmt.Println("🆕 New Skills:")
		for _, ns := range proposal.NewSkills {
			fmt.Printf("   %s: %s\n", ns.Name, ns.Description)
			fmt.Printf("     Reason: %s\n\n", ns.Reason)
		}
	}

	if len(proposal.ConfigChanges) > 0 {
		fmt.Println("⚙️  Config Changes:")
		for _, cc := range proposal.ConfigChanges {
			fmt.Printf("   %s: %s → %s\n", cc.Field, cc.Current, cc.Proposed)
			fmt.Printf("     Reason: %s\n\n", cc.Reason)
		}
	}

	if !c.Apply {
		fmt.Println("💡 Run with --apply to apply these changes")
		return nil
	}

	// Apply changes
	fmt.Println("🦋 Applying evolution changes...")
	changed := false

	// Apply new skills
	for _, ns := range proposal.NewSkills {
		found := false
		for _, s := range cfg.Agent.Skills {
			if s == ns.Name {
				found = true
				break
			}
		}
		if !found {
			cfg.Agent.Skills = append(cfg.Agent.Skills, ns.Name)
			changed = true
			fmt.Printf("   ✅ Added skill: %s\n", ns.Name)
		}
	}

	if changed {
		// Validate: try to reload config after saving
		tomlPath := dir + "/spore.toml"
		if err := cfg.Save(tomlPath); err != nil {
			fmt.Printf("   ❌ Failed to save config: %v\n", err)
			return fmt.Errorf("saving config: %w", err)
		}

		// Validate by reloading
		if _, err := agent.LoadConfig(tomlPath, ""); err != nil {
			fmt.Printf("   ❌ Config validation failed, reverting: %v\n", err)
			return fmt.Errorf("config validation failed: %w", err)
		}
		fmt.Println("   ✅ Config saved and validated")
	}

	// Record evolution event in journal
	if err := journal.Record(agent.JournalEntry{
		Type:    agent.JournalSelfEvolution,
		Summary: proposal.Summary,
		Details: fmt.Sprintf("Applied %d skill changes, %d new skills, %d config changes",
			len(proposal.SkillChanges), len(proposal.NewSkills), len(proposal.ConfigChanges)),
	}); err != nil {
		fmt.Printf("   ⚠️  Failed to record journal entry: %v\n", err)
	}

	fmt.Println("\n🦋 Evolution complete!")
	return nil
}
