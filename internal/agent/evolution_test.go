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
	"testing"
	"time"
)

func TestEvolutionEngine_RecordAndReflect(t *testing.T) {
	cfg := DefaultConfig("test-evo", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding", "writing"}
	cfg.Memory.Path = "" // in-memory sqlite

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution
	if evo == nil {
		t.Fatal("evolution engine not initialized")
	}

	// Record some experiences
	for i := 0; i < 5; i++ {
		evo.Record(&ExperienceRecord{
			TaskID:      "task-" + string(rune('a'+i)),
			Description: "test task",
			Runtime:     "builtin",
			Success:     true,
			Duration:    1.5,
			Skills:      []string{"coding"},
		})
	}

	// Record a failure
	evo.Record(&ExperienceRecord{
		TaskID:      "task-fail",
		Description: "broken task",
		Runtime:     "builtin",
		Success:     false,
		Duration:    0.5,
		Error:       "some error",
		Skills:      []string{"writing"},
	})

	// Check stats
	stats := evo.Stats()
	if stats == "" {
		t.Fatal("expected non-empty stats")
	}
	t.Logf("Stats: %s", stats)

	// Check skill profiles
	profiles := evo.SkillProfiles()
	coding, ok := profiles["coding"]
	if !ok {
		t.Fatal("expected coding skill profile")
	}
	if coding.Attempts != 5 {
		t.Errorf("coding attempts: got %d, want 5", coding.Attempts)
	}
	if coding.SuccessRate != 1.0 {
		t.Errorf("coding success rate: got %.2f, want 1.0", coding.SuccessRate)
	}

	writing, ok := profiles["writing"]
	if !ok {
		t.Fatal("expected writing skill profile")
	}
	if writing.SuccessRate != 0.0 {
		t.Errorf("writing success rate: got %.2f, want 0.0", writing.SuccessRate)
	}

	// Check runtime scores
	strategy := evo.Strategy()
	if strategy.RuntimeScores["builtin"] <= 0 {
		t.Errorf("builtin runtime score should be positive, got %.2f", strategy.RuntimeScores["builtin"])
	}
}

func TestEvolutionEngine_ShouldDelegate(t *testing.T) {
	cfg := DefaultConfig("test-delegate", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution

	// No data yet → unknown skills should trigger delegation
	if !evo.ShouldDelegate([]string{"unknown-skill"}) {
		t.Error("should delegate for unknown skill with no data")
	}

	// Record successful coding experiences
	for i := 0; i < 5; i++ {
		evo.Record(&ExperienceRecord{
			TaskID:  "t" + string(rune('0'+i)),
			Runtime: "builtin",
			Success: true,
			Duration: 1.0,
			Skills:  []string{"coding"},
		})
	}

	// Force a reflect to update skill confidence
	evo.reflectInterval = 0
	evo.minRecordsReflect = 0
	time.Sleep(10 * time.Millisecond) // let goroutine run
	evo.reflect()

	if evo.ShouldDelegate([]string{"coding"}) {
		t.Error("should NOT delegate for well-known skill")
	}

	if !evo.ShouldDelegate([]string{"quantum-physics"}) {
		t.Error("should delegate for completely unknown skill")
	}
}

func TestEvolutionEngine_BestRuntime(t *testing.T) {
	cfg := DefaultConfig("test-rt", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution

	// No data → empty
	if rt := evo.BestRuntime(); rt != "" {
		t.Errorf("expected empty preferred runtime, got %q", rt)
	}

	// Record: builtin mostly succeeds, exec mostly fails
	for i := 0; i < 5; i++ {
		evo.Record(&ExperienceRecord{
			TaskID: "bt" + string(rune('0'+i)), Runtime: "builtin", Success: true, Duration: 1.0, Skills: []string{"general"},
		})
		evo.Record(&ExperienceRecord{
			TaskID: "ex" + string(rune('0'+i)), Runtime: "exec", Success: false, Duration: 0.5, Skills: []string{"general"},
		})
	}

	evo.reflect()

	if rt := evo.BestRuntime(); rt != "builtin" {
		t.Errorf("expected preferred runtime 'builtin', got %q", rt)
	}
}

func TestEvolutionEngine_PersistRestore(t *testing.T) {
	cfg := DefaultConfig("test-persist", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}

	evo := a.evolution

	// Disable auto-reflect so we can control it
	evo.reflectInterval = 24 * time.Hour
	evo.minRecordsReflect = 999

	// Record experiences
	for i := 0; i < 3; i++ {
		evo.Record(&ExperienceRecord{
			TaskID: "p" + string(rune('0'+i)), Runtime: "builtin", Success: true, Duration: 1.0, Skills: []string{"testing"},
		})
	}
	// Manually reflect + persist (synchronous)
	evo.reflect()

	// Create new evolution engine on same agent (simulating restart)
	evo2 := NewEvolutionEngine(a)
	evo2.RestoreState()

	// Verify state was restored
	if len(evo2.journal) != len(evo.journal) {
		t.Errorf("journal length mismatch: got %d, want %d", len(evo2.journal), len(evo.journal))
	}

	strategy := evo2.Strategy()
	if strategy.PreferredRuntime != "builtin" {
		t.Errorf("restored preferred runtime: got %q, want 'builtin'", strategy.PreferredRuntime)
	}

	a.memory.Close()
}
