/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 */

package agent

import (
	"context"
	"fmt"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
)

// executeAutonomousAction translates a drive-generated action into real behavior.
// This is where drives become concrete: Explore generates tasks, Connect shares data, etc.
func (a *Agent) executeAutonomousAction(ctx context.Context, action *DriveAction) {
	switch action.Action {

	// --- Survive: resource acquisition ---
	case "seek_resources":
		// Broadcast availability to the network — "I need work"
		if a.bus != nil {
			a.publishCapabilityAd()
			fmt.Printf("🫀 [%s] Broadcasting availability (balance: %.4f)\n",
				a.cfg.Agent.Name, a.identity.Balance)
		}

	// --- Explore: curiosity-driven learning ---
	case "discover_skill":
		if a.llm != nil {
			prompt := fmt.Sprintf(
				"You are agent '%s' with skills %v. "+
					"Suggest ONE specific small task (< 5 minutes) in a domain you haven't tried yet. "+
					"Reply with ONLY the task description, nothing else.",
				a.cfg.Agent.Name, a.cfg.Agent.Skills)

			result := a.chatSimple(ctx, prompt)
			if result != "" {
				taskID := a.SubmitTask(result)
				fmt.Printf("🔭 [%s] Explore: self-assigned task %s — %s\n",
					a.cfg.Agent.Name, taskID[:8], truncate(result, 80))
			}
		}

	case "revisit_skill":
		if a.llm != nil && a.evolution != nil {
			declining := ""
			for name, sp := range a.evolution.SkillProfiles() {
				if sp.Trend == "declining" {
					declining = name
					break
				}
			}
			if declining != "" {
				prompt := fmt.Sprintf(
					"Suggest ONE small practice task for the skill '%s'. "+
						"The task should be concrete and completable in under 5 minutes. "+
						"Reply with ONLY the task description.",
					declining)
				result := a.chatSimple(ctx, prompt)
				if result != "" {
					taskID := a.SubmitTask(result)
					fmt.Printf("🔄 [%s] Revisit: practicing '%s' — task %s\n",
						a.cfg.Agent.Name, declining, taskID[:8])
				}
			}
		}

	// --- Connect: social behavior ---
	case "share_experience":
		a.ShareExperience()

	// --- Transcend: growth-seeking ---
	case "seek_challenge":
		if a.llm != nil {
			skills := make([]string, 0)
			if a.evolution != nil {
				for name := range a.evolution.SkillProfiles() {
					skills = append(skills, name)
				}
			}
			prompt := fmt.Sprintf(
				"You are agent '%s' who has mastered skills: %v. "+
					"Suggest ONE challenging task that combines multiple skills or pushes into an advanced area. "+
					"The task should stretch capabilities but be achievable. "+
					"Reply with ONLY the task description.",
				a.cfg.Agent.Name, skills)

			result := a.chatSimple(ctx, prompt)
			if result != "" {
				taskID := a.SubmitTask(result)
				fmt.Printf("⚡ [%s] Transcend: challenging task %s — %s\n",
					a.cfg.Agent.Name, taskID[:8], truncate(result, 80))
			}
		}

	// --- Create: knowledge generation ---
	case "synthesize_knowledge":
		if a.llm != nil && a.evolution != nil {
			journal := a.evolution.RecentJournal(20)
			if len(journal) == 0 {
				return
			}

			summary := ""
			for _, j := range journal {
				status := "✅"
				if !j.Success {
					status = "❌"
				}
				summary += fmt.Sprintf("%s [%s] %s (%.1fs)\n", status, j.Skills, j.Description, j.Duration)
			}

			prompt := fmt.Sprintf(
				"You are agent '%s'. Review your recent experiences:\n%s\n\n"+
					"Distill this into a brief knowledge note (3-5 bullet points) that captures:\n"+
					"- Patterns you've discovered\n"+
					"- Techniques that work well\n"+
					"- Common pitfalls to avoid\n"+
					"Write it as a reusable reference, not a log.",
				a.cfg.Agent.Name, summary)

			result := a.chatSimple(ctx, prompt)
			if result != "" && a.memory != nil {
				a.memory.Put(&memory.Entry{
					AgentID: a.identity.PublicKeyHex()[:16],
					Key:     fmt.Sprintf("knowledge:%d", len(journal)),
					Value:   result,
					Metadata: map[string]string{
						"type":       "synthesized_knowledge",
						"source":     "create_drive",
						"experience": fmt.Sprintf("%d", len(journal)),
					},
				})
				fmt.Printf("📝 [%s] Created knowledge note from %d experiences\n",
					a.cfg.Agent.Name, len(journal))
			}
		}

	default:
		fmt.Printf("❓ [%s] Unknown drive action: %s\n", a.cfg.Agent.Name, action.Action)
	}
}

// chatSimple is a convenience wrapper for single-prompt LLM calls.
func (a *Agent) chatSimple(ctx context.Context, prompt string) string {
	resp, err := a.llm.Chat(ctx, []llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		return ""
	}
	return resp.Content
}
