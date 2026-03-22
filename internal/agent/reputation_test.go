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

func TestReputation_InitialScore(t *testing.T) {
	r := NewReputationEngine()
	score := r.Score("unknown-agent")
	if score != repInitial {
		t.Errorf("expected initial score %.2f, got %.2f", repInitial, score)
	}
}

func TestReputation_SuccessBoost(t *testing.T) {
	r := NewReputationEngine()
	r.RecordSuccess("agent-a", 0.9)
	score := r.Score("agent-a")
	if score <= repInitial {
		t.Errorf("score should increase after success, got %.2f", score)
	}
}

func TestReputation_FailurePenalty(t *testing.T) {
	r := NewReputationEngine()
	r.RecordSuccess("agent-b", 0.8) // start above initial
	before := r.Score("agent-b")
	r.RecordFailure("agent-b")
	after := r.Score("agent-b")
	if after >= before {
		t.Errorf("score should decrease after failure: before=%.2f after=%.2f", before, after)
	}
}

func TestReputation_TimeoutPenalty(t *testing.T) {
	r := NewReputationEngine()
	r.RecordTimeout("agent-c")
	score := r.Score("agent-c")
	if score >= repInitial {
		t.Errorf("score should decrease after timeout, got %.2f", score)
	}
}

func TestReputation_ViolationDrop(t *testing.T) {
	r := NewReputationEngine()
	// Build up some reputation first
	for i := 0; i < 5; i++ {
		r.RecordSuccess("agent-d", 0.9)
	}
	before := r.Score("agent-d")
	r.RecordViolation("agent-d")
	after := r.Score("agent-d")
	if after >= before-0.3 {
		t.Errorf("violation should cause major drop: before=%.2f after=%.2f", before, after)
	}
}

func TestReputation_Isolation(t *testing.T) {
	r := NewReputationEngine()
	// Drive score to zero with repeated failures
	for i := 0; i < 10; i++ {
		r.RecordFailure("bad-agent")
	}
	if !r.IsIsolated("bad-agent") {
		t.Error("agent should be isolated after many failures")
	}
	score := r.Score("bad-agent")
	if score > repIsolateThreshold {
		t.Errorf("isolated agent score should be <= %.2f, got %.2f", repIsolateThreshold, score)
	}
}

func TestReputation_Recovery(t *testing.T) {
	r := NewReputationEngine()
	// Isolate
	for i := 0; i < 10; i++ {
		r.RecordFailure("recover-agent")
	}
	if !r.IsIsolated("recover-agent") {
		t.Fatal("agent should be isolated")
	}
	// Recover with many successes
	for i := 0; i < 20; i++ {
		r.RecordSuccess("recover-agent", 1.0)
	}
	if r.IsIsolated("recover-agent") {
		t.Error("agent should have recovered from isolation")
	}
}

func TestReputation_Get(t *testing.T) {
	r := NewReputationEngine()
	r.RecordSuccess("agent-e", 0.8)
	r.RecordSuccess("agent-e", 0.7)
	r.RecordFailure("agent-e")

	rec := r.Get("agent-e")
	if rec.TasksCompleted != 2 {
		t.Errorf("expected 2 completed tasks, got %d", rec.TasksCompleted)
	}
	if rec.TasksFailed != 1 {
		t.Errorf("expected 1 failed task, got %d", rec.TasksFailed)
	}
}

func TestReputation_All(t *testing.T) {
	r := NewReputationEngine()
	r.RecordSuccess("a1", 0.5)
	r.RecordSuccess("a2", 0.5)
	r.RecordSuccess("a3", 0.5)

	all := r.All()
	if len(all) != 3 {
		t.Errorf("expected 3 records, got %d", len(all))
	}
}

func TestReputation_Decay(t *testing.T) {
	r := NewReputationEngine()
	// Manually set a high score with old interaction
	r.mu.Lock()
	r.records["old-agent"] = &ReputationRecord{
		AgentID:      "old-agent",
		Score:        0.9,
		LastInteract: time.Now().Add(-48 * time.Hour), // 2 days ago
	}
	r.mu.Unlock()

	r.Decay()
	score := r.Score("old-agent")
	if score >= 0.9 {
		t.Errorf("score should have decayed from 0.9, got %.2f", score)
	}
	if score < repInitial {
		t.Errorf("score should not decay below neutral %.2f, got %.2f", repInitial, score)
	}
}

func TestReputation_UnknownNotIsolated(t *testing.T) {
	r := NewReputationEngine()
	if r.IsIsolated("never-seen") {
		t.Error("unknown agent should not be isolated")
	}
}
