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
	"testing"
	"time"
)

func TestEvolution_AcquireSkills(t *testing.T) {
	cfg := DefaultConfig("test-acquire", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution
	// Disable auto-reflect
	evo.reflectInterval = 24 * time.Hour
	evo.minRecordsReflect = 999

	// Record successful uses of a new skill "devops"
	for i := 0; i < 4; i++ {
		evo.Record(&ExperienceRecord{
			TaskID:  "d" + string(rune('0'+i)),
			Runtime: "builtin",
			Success: true,
			Duration: 2.0,
			Skills:  []string{"devops"},
		})
	}

	// Record only 1 use of "ml" — not enough
	evo.Record(&ExperienceRecord{
		TaskID:  "m0",
		Runtime: "builtin",
		Success: true,
		Duration: 3.0,
		Skills:  []string{"ml"},
	})

	evo.mu.Lock()
	acquired := evo.acquireSkills(3)
	evo.mu.Unlock()

	if len(acquired) != 1 {
		t.Fatalf("expected 1 acquired skill, got %d: %v", len(acquired), acquired)
	}
	if acquired[0] != "devops" {
		t.Errorf("expected acquired skill 'devops', got %q", acquired[0])
	}

	// Verify it's in the config now
	found := false
	for _, s := range a.cfg.Agent.Skills {
		if s == "devops" {
			found = true
			break
		}
	}
	if !found {
		t.Error("devops should be in agent config skills after acquisition")
	}
}

func TestEvolution_AcquireSkills_NoFalsePositive(t *testing.T) {
	cfg := DefaultConfig("test-no-fp", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution
	evo.reflectInterval = 24 * time.Hour
	evo.minRecordsReflect = 999

	// Record failed uses of "hacking" — should not be acquired
	for i := 0; i < 5; i++ {
		evo.Record(&ExperienceRecord{
			TaskID:  "h" + string(rune('0'+i)),
			Runtime: "builtin",
			Success: false,
			Duration: 1.0,
			Skills:  []string{"hacking"},
		})
	}

	evo.mu.Lock()
	acquired := evo.acquireSkills(3)
	evo.mu.Unlock()

	if len(acquired) != 0 {
		t.Errorf("should not acquire skills from failures, got: %v", acquired)
	}
}

func TestEvolution_ExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "direct JSON",
			input: `{"summary": "good"}`,
			want:  `{"summary": "good"}`,
		},
		{
			name:  "markdown code block",
			input: "```json\n{\"summary\": \"good\"}\n```",
			want:  `{"summary": "good"}`,
		},
		{
			name:  "text with embedded JSON",
			input: "Here is my analysis:\n{\"summary\": \"good\", \"strengths\": []}",
			want:  `{"summary": "good", "strengths": []}`,
		},
		{
			name:  "no JSON",
			input: "just plain text",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEvolution_BuildReflectionPrompt(t *testing.T) {
	journal := []*ExperienceRecord{
		{TaskID: "t1", Description: "fix bug", Runtime: "builtin", Success: true, Duration: 2.5, Skills: []string{"coding"}},
		{TaskID: "t2", Description: "write docs", Runtime: "builtin", Success: false, Duration: 1.0, Error: "timeout", Skills: []string{"writing"}},
	}
	skills := map[string]*SkillProfile{
		"coding":  {Name: "coding", Attempts: 5, Successes: 4, SuccessRate: 0.8, Trend: "improving", AvgDuration: 2.0},
		"writing": {Name: "writing", Attempts: 3, Successes: 1, SuccessRate: 0.33, Trend: "declining", AvgDuration: 1.5},
	}
	strategy := &StrategyProfile{PreferredRuntime: "builtin"}

	prompt := buildReflectionPrompt("test-agent", journal, skills, strategy)

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}

	// Should contain key sections
	for _, section := range []string{"Experience Log", "Runtime Usage", "Skill Profiles", "Failure Patterns", "Recent Tasks"} {
		if !contains(prompt, section) {
			t.Errorf("prompt missing section: %s", section)
		}
	}
}

func TestEvolution_Evolve(t *testing.T) {
	cfg := DefaultConfig("test-evolve", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"general"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution

	// Record enough data for evolution cycle
	for i := 0; i < 5; i++ {
		evo.Record(&ExperienceRecord{
			TaskID:  "ev" + string(rune('0'+i)),
			Runtime: "builtin",
			Success: true,
			Duration: 1.0,
			Skills:  []string{"general"},
		})
	}

	// Run evolution cycle without deep reflect (interval=0)
	ctx := context.Background()
	evo.Evolve(ctx, 0)

	// Strategy should be updated
	strategy := evo.Strategy()
	if strategy.PreferredRuntime != "builtin" {
		t.Errorf("expected preferred runtime 'builtin', got %q", strategy.PreferredRuntime)
	}
	if strategy.SkillConfidence["general"] <= 0 {
		t.Error("expected positive skill confidence for 'general'")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsImpl(s, substr)
}

func containsImpl(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
