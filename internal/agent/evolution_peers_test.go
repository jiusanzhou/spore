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
)

func TestPeerEvolution_ObserveAndFitness(t *testing.T) {
	cfg := DefaultConfig("test-peer-evo", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	pe := a.peerEvo

	// Unknown peer → neutral
	if f := pe.Fitness("unknown-agent"); f != 0.5 {
		t.Errorf("unknown peer fitness: got %.2f, want 0.5", f)
	}

	// Record successes for agent-a
	for i := 0; i < 10; i++ {
		pe.ObserveSuccess("agent-a", 1.5)
	}

	// Record failures for agent-b
	for i := 0; i < 10; i++ {
		pe.ObserveFailure("agent-b", 2.0)
	}

	fitnessA := pe.Fitness("agent-a")
	fitnessB := pe.Fitness("agent-b")

	if fitnessA <= fitnessB {
		t.Errorf("agent-a (all success) should have higher fitness than agent-b (all fail): a=%.3f, b=%.3f",
			fitnessA, fitnessB)
	}

	if fitnessA < 0.8 {
		t.Errorf("agent-a with 100%% success should have high fitness, got %.3f", fitnessA)
	}

	if fitnessB > 0.3 {
		t.Errorf("agent-b with 0%% success should have low fitness, got %.3f", fitnessB)
	}
}

func TestPeerEvolution_Rankings(t *testing.T) {
	cfg := DefaultConfig("test-rankings", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	pe := a.peerEvo

	// Create 3 agents with different performance
	for i := 0; i < 10; i++ {
		pe.ObserveSuccess("star", 1.0) // 100% success, fast
	}
	for i := 0; i < 8; i++ {
		pe.ObserveSuccess("average", 3.0) // 80% success, slow
	}
	for i := 0; i < 2; i++ {
		pe.ObserveFailure("average", 2.0)
	}
	for i := 0; i < 10; i++ {
		pe.ObserveFailure("poor", 5.0) // 0% success
	}

	rankings := pe.Rankings()
	if len(rankings) != 3 {
		t.Fatalf("expected 3 rankings, got %d", len(rankings))
	}

	if rankings[0].AgentID != "star" {
		t.Errorf("expected 'star' at #1, got %q (fitness=%.3f)", rankings[0].AgentID, rankings[0].Fitness)
	}
	if rankings[1].AgentID != "average" {
		t.Errorf("expected 'average' at #2, got %q (fitness=%.3f)", rankings[1].AgentID, rankings[1].Fitness)
	}
	if rankings[2].AgentID != "poor" {
		t.Errorf("expected 'poor' at #3, got %q (fitness=%.3f)", rankings[2].AgentID, rankings[2].Fitness)
	}

	// Verify ordering: each rank has lower fitness
	for i := 1; i < len(rankings); i++ {
		if rankings[i].Fitness > rankings[i-1].Fitness {
			t.Errorf("ranking %d (%.3f) > ranking %d (%.3f)",
				i, rankings[i].Fitness, i-1, rankings[i-1].Fitness)
		}
	}
}

func TestPeerEvolution_PersistRestore(t *testing.T) {
	cfg := DefaultConfig("test-peer-persist", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}

	pe := a.peerEvo

	for i := 0; i < 5; i++ {
		pe.ObserveSuccess("reliable", 1.0)
	}
	pe.Persist()

	// Restore into new tracker
	pe2 := NewPeerEvolution(a)
	if f := pe2.Fitness("reliable"); f < 0.7 {
		t.Errorf("restored fitness for 'reliable' too low: %.3f", f)
	}

	a.memory.Close()
}

func TestPeerEvolution_MixedPerformance(t *testing.T) {
	cfg := DefaultConfig("test-mixed", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	pe := a.peerEvo

	// 70% success rate
	for i := 0; i < 7; i++ {
		pe.ObserveSuccess("mixed-agent", 2.0)
	}
	for i := 0; i < 3; i++ {
		pe.ObserveFailure("mixed-agent", 2.0)
	}

	fitness := pe.Fitness("mixed-agent")
	// Should be between 0.4 and 0.9 (moderate fitness)
	if fitness < 0.4 || fitness > 0.9 {
		t.Errorf("mixed-agent fitness out of expected range: got %.3f, want [0.4, 0.9]", fitness)
	}
	t.Logf("Mixed agent (70%% success) fitness: %.3f", fitness)
}
