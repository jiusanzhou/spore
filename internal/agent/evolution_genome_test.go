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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvolution_SelfModify(t *testing.T) {
	cfg := DefaultConfig("test-self-mod", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding", "writing"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution
	evo.reflectInterval = 24 * time.Hour
	evo.minRecordsReflect = 999

	// Record some experience
	for i := 0; i < 5; i++ {
		evo.Record(&ExperienceRecord{
			TaskID:  "sm" + string(rune('0'+i)),
			Runtime: "builtin",
			Success: true,
			Duration: 1.0,
			Skills:  []string{"coding"},
		})
	}
	evo.reflect()

	// Self-modify to temp dir
	tmpDir := t.TempDir()
	err = evo.SelfModify(tmpDir)
	if err != nil {
		t.Fatalf("SelfModify failed: %v", err)
	}

	// Verify agent.yaml was created
	yamlPath := filepath.Join(tmpDir, "agent.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("reading agent.yaml: %v", err)
	}

	content := string(data)
	t.Logf("Generated agent.yaml:\n%s", content)

	// Should contain skills
	if !strings.Contains(content, "coding") {
		t.Error("agent.yaml should contain 'coding' skill")
	}
	if !strings.Contains(content, "writing") {
		t.Error("agent.yaml should contain 'writing' skill")
	}

	// Should contain evolution metadata
	if !strings.Contains(content, "x-evolution") {
		t.Error("agent.yaml should contain x-evolution metadata")
	}
}

func TestEvolution_SelfModify_PreservesExisting(t *testing.T) {
	cfg := DefaultConfig("test-preserve", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	tmpDir := t.TempDir()

	// Create existing agent.yaml with custom fields
	existing := `name: my-agent
version: "1.0.0"
description: A custom agent
custom_field: preserved
skills:
- name: existing-skill
`
	os.WriteFile(filepath.Join(tmpDir, "agent.yaml"), []byte(existing), 0644)

	evo := a.evolution
	err = evo.SelfModify(tmpDir)
	if err != nil {
		t.Fatalf("SelfModify failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "agent.yaml"))
	content := string(data)

	// Should preserve custom fields
	if !strings.Contains(content, "custom_field") {
		t.Error("should preserve custom_field")
	}
	if !strings.Contains(content, "my-agent") {
		t.Error("should preserve existing name")
	}
	// Should merge skills
	if !strings.Contains(content, "existing-skill") {
		t.Error("should preserve existing-skill")
	}
	if !strings.Contains(content, "coding") {
		t.Error("should add new skill 'coding'")
	}
}

func TestEvolution_ComputeGenetics(t *testing.T) {
	cfg := DefaultConfig("test-genetics", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding", "testing"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution

	// No experience → neutral fitness
	genetics := evo.ComputeGenetics()
	if genetics.Fitness != 0.5 {
		t.Errorf("empty genetics fitness: got %.2f, want 0.5", genetics.Fitness)
	}

	// Add successful experience
	for i := 0; i < 10; i++ {
		evo.Record(&ExperienceRecord{
			TaskID: "g" + string(rune('0'+i)), Runtime: "builtin", Success: true, Duration: 1.0, Skills: []string{"coding"},
		})
	}

	genetics = evo.ComputeGenetics()
	if genetics.Fitness < 0.9 {
		t.Errorf("100%% success genetics fitness too low: %.3f", genetics.Fitness)
	}
	if len(genetics.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(genetics.Skills))
	}
}

func TestEvolution_SelectBestParent(t *testing.T) {
	candidates := []*SpawnGenetics{
		{AgentID: "weak", Fitness: 0.3},
		{AgentID: "strong", Fitness: 0.95},
		{AgentID: "medium", Fitness: 0.6},
	}

	best := SelectBestParent(candidates)
	if best == nil {
		t.Fatal("should select a parent")
	}
	if best.AgentID != "strong" {
		t.Errorf("expected 'strong', got %q", best.AgentID)
	}
}

func TestEvolution_SelectBestParent_Empty(t *testing.T) {
	best := SelectBestParent(nil)
	if best != nil {
		t.Error("should return nil for empty candidates")
	}
}

func TestEvolution_InheritEvolution(t *testing.T) {
	parentCfg := DefaultConfig("parent", "gpt-4o-mini")
	parentCfg.Agent.Skills = []string{"coding"}
	parentCfg.Memory.Path = ""

	parent, err := New(parentCfg)
	if err != nil {
		t.Fatalf("creating parent: %v", err)
	}
	defer parent.memory.Close()

	parentEvo := parent.evolution
	parentEvo.reflectInterval = 24 * time.Hour
	parentEvo.minRecordsReflect = 999

	// Parent accumulates experience
	for i := 0; i < 8; i++ {
		parentEvo.Record(&ExperienceRecord{
			TaskID: "p" + string(rune('0'+i)), Runtime: "builtin", Success: true, Duration: 1.5, Skills: []string{"coding"},
		})
	}
	parentEvo.reflect()

	// Create child
	childCfg := DefaultConfig("child", "gpt-4o-mini")
	childCfg.Memory.Path = ""
	child, err := New(childCfg)
	if err != nil {
		t.Fatalf("creating child: %v", err)
	}
	defer child.memory.Close()

	// Inherit
	parentEvo.InheritEvolution(child.evolution)

	childStrategy := child.evolution.Strategy()
	if childStrategy.PreferredRuntime != "builtin" {
		t.Errorf("child should inherit preferred runtime, got %q", childStrategy.PreferredRuntime)
	}

	childSkills := child.evolution.SkillProfiles()
	if _, ok := childSkills["coding"]; !ok {
		t.Error("child should inherit coding skill profile")
	}

	// Child's journal should be empty (must build own experience)
	if len(child.evolution.journal) != 0 {
		t.Errorf("child journal should be empty, got %d", len(child.evolution.journal))
	}
}

func TestEvolution_ExperienceDigest(t *testing.T) {
	cfg := DefaultConfig("test-digest", "gpt-4o-mini")
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

	for i := 0; i < 5; i++ {
		evo.Record(&ExperienceRecord{
			TaskID: "d" + string(rune('0'+i)), Runtime: "builtin", Success: i < 4, Duration: 1.0, Skills: []string{"coding"},
		})
	}
	evo.reflect()

	digest := evo.BuildDigest()
	if digest.TotalTasks != 5 {
		t.Errorf("digest total tasks: got %d, want 5", digest.TotalTasks)
	}
	if digest.SuccessRate != 0.8 {
		t.Errorf("digest success rate: got %.2f, want 0.8", digest.SuccessRate)
	}
	if _, ok := digest.Skills["coding"]; !ok {
		t.Error("digest should include coding skill")
	}
}

func TestEvolution_AbsorbExperience(t *testing.T) {
	cfg := DefaultConfig("test-absorb", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution

	// Absorb a digest from a peer with new skills
	digest := &ExperienceDigest{
		AgentID:     "peer-1234567890ab",
		Skills:      map[string]float64{"devops": 0.9, "ml": 0.3},
		BestRuntime: "claude-code",
		TotalTasks:  20,
		SuccessRate: 0.85,
		Timestamp:   time.Now().Unix(),
	}

	evo.AbsorbExperience(digest)

	// Should have learned about devops (rate > 0.5) but not ml (rate < 0.5)
	skills := evo.SkillProfiles()
	if _, ok := skills["devops"]; !ok {
		t.Error("should have absorbed devops skill from peer")
	}
	if _, ok := skills["ml"]; ok {
		t.Error("should NOT have absorbed ml skill (rate < 0.5)")
	}

	// Runtime score should have gotten a boost
	strategy := evo.Strategy()
	if strategy.RuntimeScores["claude-code"] <= 0 {
		t.Error("should have boosted claude-code runtime score from peer experience")
	}
}
