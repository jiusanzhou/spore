/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 */

package agent

import (
	"testing"
)

func TestTokenLedger_Seed(t *testing.T) {
	cfg := DefaultConfig("test-seed", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Agent should have been seeded with initial balance
	if a.identity.Balance != 10.0 {
		t.Errorf("expected initial balance 10.0, got %.4f", a.identity.Balance)
	}

	state := a.tokens.State()
	if state.Health != "thriving" {
		t.Errorf("expected health 'thriving', got %q", state.Health)
	}
}

func TestTokenLedger_TaskReward(t *testing.T) {
	cfg := DefaultConfig("test-reward", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	initial := a.identity.Balance
	a.tokens.RewardTask("aaaa1111bbbb2222", true)

	if a.identity.Balance != initial+1.0 {
		t.Errorf("expected balance %.4f, got %.4f", initial+1.0, a.identity.Balance)
	}

	state := a.tokens.State()
	if state.Stats.TasksCompleted != 1 {
		t.Errorf("expected 1 completed task, got %d", state.Stats.TasksCompleted)
	}
}

func TestTokenLedger_TaskFailurePenalty(t *testing.T) {
	cfg := DefaultConfig("test-penalty", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	initial := a.identity.Balance
	a.tokens.RewardTask("aaaa1111bbbb2222", false)

	if a.identity.Balance != initial-0.2 {
		t.Errorf("expected balance %.4f, got %.4f", initial-0.2, a.identity.Balance)
	}

	state := a.tokens.State()
	if state.Stats.TasksFailed != 1 {
		t.Errorf("expected 1 failed task, got %d", state.Stats.TasksFailed)
	}
}

func TestTokenLedger_Metabolism(t *testing.T) {
	cfg := DefaultConfig("test-meta", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	initial := a.identity.Balance
	a.tokens.Metabolism()

	if a.identity.Balance >= initial {
		t.Error("metabolism should decrease balance")
	}
}

func TestTokenLedger_ChargeThink(t *testing.T) {
	cfg := DefaultConfig("test-think", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	initial := a.identity.Balance
	a.tokens.ChargeThink(0.5, "test_llm_call")

	if a.identity.Balance != initial-0.5 {
		t.Errorf("expected balance %.4f, got %.4f", initial-0.5, a.identity.Balance)
	}
}

func TestTokenLedger_HealthAssessment(t *testing.T) {
	cfg := DefaultConfig("test-health", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Thriving at 10.0
	state := a.tokens.State()
	if state.Health != "thriving" {
		t.Errorf("at 10.0 expected 'thriving', got %q", state.Health)
	}

	// Drain to 1.0 (struggling)
	a.identity.Balance = 1.0
	state = a.tokens.State()
	if state.Health != "struggling" {
		t.Errorf("at 1.0 expected 'struggling', got %q", state.Health)
	}

	// Drain to 0.3 (starving)
	a.identity.Balance = 0.3
	state = a.tokens.State()
	if state.Health != "starving" {
		t.Errorf("at 0.3 expected 'starving', got %q", state.Health)
	}

	// Drain to 0.0 (critical)
	a.identity.Balance = 0.0
	state = a.tokens.State()
	if state.Health != "critical" {
		t.Errorf("at 0.0 expected 'critical', got %q", state.Health)
	}
}

func TestTokenLedger_CanExplore(t *testing.T) {
	cfg := DefaultConfig("test-can-explore", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// At 10.0 can explore
	if !a.tokens.CanExplore() {
		t.Error("should be able to explore at 10.0")
	}

	// At 1.0 can't explore (threshold is 2.0)
	a.identity.Balance = 1.0
	if a.tokens.CanExplore() {
		t.Error("should not explore at 1.0")
	}
}

func TestTokenLedger_CanCreate(t *testing.T) {
	cfg := DefaultConfig("test-can-create", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// At 10.0 can create
	if !a.tokens.CanCreate() {
		t.Error("should be able to create at 10.0")
	}

	// At 3.0 can't create (threshold is 5.0 = InitialBalance*0.5)
	a.identity.Balance = 3.0
	if a.tokens.CanCreate() {
		t.Error("should not create at 3.0")
	}
}

func TestTokenLedger_Ledger(t *testing.T) {
	cfg := DefaultConfig("test-ledger", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	a.tokens.RewardTask("aaaa1111bbbb2222", true)
	a.tokens.ChargeThink(0.3, "llm")
	a.tokens.Metabolism()

	entries := a.tokens.RecentLedger(10)
	// Should have: seed + reward + charge + metabolism = 4 entries
	if len(entries) < 3 {
		t.Errorf("expected at least 3 ledger entries, got %d", len(entries))
	}
}

func TestTokenLedger_PersistRestore(t *testing.T) {
	cfg := DefaultConfig("test-persist-token", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	a.tokens.RewardTask("aaaa1111bbbb2222", true)
	a.tokens.RewardTask("cccc3333dddd4444", true)
	a.tokens.Persist()

	// Restore into new ledger
	a.tokens = NewTokenLedger(a, DefaultTokenConfig())
	a.identity.Balance = 0 // reset
	a.tokens.Restore()

	state := a.tokens.State()
	if state.Stats.TasksCompleted != 2 {
		t.Errorf("expected 2 completed tasks after restore, got %d", state.Stats.TasksCompleted)
	}
	if a.identity.Balance < 11.0 { // 10 initial + 2 rewards
		t.Errorf("expected balance >= 11.0, got %.4f", a.identity.Balance)
	}
}

func TestTokenLedger_InfoIncluded(t *testing.T) {
	cfg := DefaultConfig("test-info-econ", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	info := a.Info()
	if info.Economy == nil {
		t.Fatal("Info should include economy state")
	}
	if info.Economy.Health != "thriving" {
		t.Errorf("expected health 'thriving', got %q", info.Economy.Health)
	}
	if info.Economy.Balance != 10.0 {
		t.Errorf("expected balance 10.0, got %.4f", info.Economy.Balance)
	}
}
