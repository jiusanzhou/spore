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
	"fmt"
	"strings"
	"time"
)

// SeedSkill defines a built-in seed skill template.
type SeedSkill struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Category    string   `yaml:"category" json:"category"`
	Triggers    []string `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	Priority    int      `yaml:"priority,omitempty" json:"priority,omitempty"`
	Dependencies []string `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
}

// DefaultSeedSkills returns the built-in seed skills that agents start with.
func DefaultSeedSkills() []SeedSkill {
	return []SeedSkill{
		{
			Name: "self-assess",
			Description: `Evaluate own performance by analyzing recent task outcomes, identifying strengths and weaknesses.
Steps: (1) Review last N task results from evolution journal. (2) Compute success rate per skill area.
(3) Identify patterns in failures. (4) Produce a concise self-assessment with actionable improvements.
Output: structured assessment with ratings per skill, top 3 strengths, top 3 areas for improvement.`,
			Category:   "meta",
			Triggers:   []string{"periodic", "after_failure_streak"},
			Priority:   90,
		},
		{
			Name: "collaborate",
			Description: `Coordinate with peer agents to share knowledge and distribute work effectively.
Steps: (1) Broadcast capability advertisement to the swarm. (2) Discover peers with complementary skills.
(3) Propose task decomposition for complex work. (4) Share successful patterns and learnings.
(5) Accept delegated subtasks within competence threshold.
Output: collaboration report with peers contacted, knowledge shared, tasks distributed.`,
			Category:     "social",
			Triggers:     []string{"complex_task", "low_confidence"},
			Priority:     70,
			Dependencies: []string{"communicate"},
		},
		{
			Name: "evolve",
			Description: `Self-improvement through analysis of own skills, configuration, and recent performance.
Steps: (1) Read own skill store and configuration. (2) Analyze recent journal entries for patterns.
(3) Identify skills that need refinement or are missing. (4) Propose specific, testable improvements.
(5) Apply changes if validation passes, revert on failure.
Output: evolution proposal with before/after diffs and validation results.`,
			Category: "meta",
			Triggers: []string{"periodic", "idle"},
			Priority: 60,
		},
		{
			Name: "communicate",
			Description: `Clear task reporting, status updates, and inter-agent messaging.
Steps: (1) Format task results with structured summary (status, key findings, next steps).
(2) Adapt communication style to recipient (human vs agent, coordinator vs worker).
(3) Include relevant context without information overload.
(4) Flag blockers and dependencies clearly.
Output: well-structured status report or task result.`,
			Category: "core",
			Triggers: []string{"task_complete", "status_request", "blocker"},
			Priority: 80,
		},
		{
			Name: "research",
			Description: `Information gathering, web search, analysis, and knowledge synthesis.
Steps: (1) Decompose research question into searchable sub-queries. (2) Search multiple sources.
(3) Cross-reference findings for accuracy. (4) Synthesize into a concise, actionable summary.
(5) Store key findings in memory for future reference.
Output: research report with sources, confidence levels, and key takeaways.`,
			Category: "core",
			Triggers: []string{"unknown_topic", "task_requires_knowledge"},
			Priority: 75,
		},
	}
}

// loadSeedSkills imports default seed skills into the skill store if no skills exist yet.
func (a *Agent) loadSeedSkills() {
	if a.skillStore == nil {
		return
	}

	// Check if any skills already exist
	existing, _ := a.skillStore.ActiveSkills()
	if len(existing) > 0 {
		return // skills already populated
	}

	seeds := DefaultSeedSkills()
	imported := 0
	for _, seed := range seeds {
		id := generateSkillID(seed.Name, "imported", "seed")

		// Build a rich description with YAML frontmatter style
		desc := fmt.Sprintf("category: %s\npriority: %d\n---\n%s",
			seed.Category, seed.Priority, seed.Description)
		if len(seed.Triggers) > 0 {
			desc = fmt.Sprintf("category: %s\npriority: %d\ntriggers: [%s]\n---\n%s",
				seed.Category, seed.Priority,
				strings.Join(seed.Triggers, ", "), seed.Description)
		}

		rec := &SkillRecord{
			SkillID:       id,
			Name:          seed.Name,
			Description:   desc,
			IsActive:      true,
			Origin:        SkillOriginImported,
			Generation:    0,
			ChangeSummary: "seed skill — built-in template",
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		}

		if err := a.skillStore.PutSkill(rec); err != nil {
			fmt.Printf("⚠️  [%s] Failed to import seed skill %s: %v\n", a.cfg.Agent.Name, seed.Name, err)
			continue
		}
		imported++
	}

	if imported > 0 {
		fmt.Printf("🌱 [%s] Imported %d seed skills\n", a.cfg.Agent.Name, imported)
	}
}
