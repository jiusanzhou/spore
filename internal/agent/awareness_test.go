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

func TestAwareness_DefaultSelf(t *testing.T) {
	cfg := DefaultConfig("test-aware", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	self := a.awareness.Self()
	if self.Name != "test-aware" {
		t.Errorf("name: got %q, want 'test-aware'", self.Name)
	}
	if self.Personality != "nascent" {
		t.Errorf("personality: got %q, want 'nascent'", self.Personality)
	}
	if self.Mood != "calm" {
		t.Errorf("mood: got %q, want 'calm'", self.Mood)
	}
	if self.Energy != 0.5 {
		t.Errorf("energy: got %.2f, want 0.50", self.Energy)
	}
}

func TestAwareness_MoodAnxious(t *testing.T) {
	cfg := DefaultConfig("test-mood", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Set very low balance
	a.identity.Balance = 0.05
	a.awareness.UpdateMood()

	self := a.awareness.Self()
	if self.Mood != "anxious" {
		t.Errorf("mood with low balance: got %q, want 'anxious'", self.Mood)
	}
	if self.Energy > 0.15 {
		t.Errorf("energy should be low: got %.2f", self.Energy)
	}
}

func TestAwareness_MoodContent(t *testing.T) {
	cfg := DefaultConfig("test-content", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	a.identity.Balance = 0.6
	a.awareness.self.Morale = 0.8
	a.awareness.UpdateMood()

	self := a.awareness.Self()
	if self.Mood != "content" {
		t.Errorf("mood with high morale + balance: got %q, want 'content'", self.Mood)
	}
}

func TestAwareness_ObserveSuccess(t *testing.T) {
	cfg := DefaultConfig("test-observe", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	before := a.awareness.Self().Morale

	a.awareness.ObserveTaskOutcome(&ExperienceRecord{
		TaskID:      "t1",
		Description: "fix critical bug",
		Success:     true,
		Duration:    3.0,
	})

	after := a.awareness.Self().Morale
	if after <= before {
		t.Errorf("morale should increase after success: before=%.3f after=%.3f", before, after)
	}

	// Should have generated a thought
	thoughts := a.awareness.Monologue(5)
	if len(thoughts) == 0 {
		t.Error("should have generated a thought after fast success")
	}
}

func TestAwareness_ObserveFailure(t *testing.T) {
	cfg := DefaultConfig("test-fail-aware", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	before := a.awareness.Self().Morale

	a.awareness.ObserveTaskOutcome(&ExperienceRecord{
		TaskID:      "t1",
		Description: "deploy to production",
		Success:     false,
		Error:       "timeout",
	})

	after := a.awareness.Self().Morale
	if after >= before {
		t.Errorf("morale should decrease after failure: before=%.3f after=%.3f", before, after)
	}
}

func TestAwareness_LocalIntrospect(t *testing.T) {
	cfg := DefaultConfig("test-introspect", "gpt-4o-mini")
	cfg.Memory.Path = ""
	cfg.Drive = &Drive{Explore: 0.9, Survive: 0.1, Connect: 0.3, Transcend: 0.3, Create: 0.2}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Set introspect interval to 0 to force it
	a.awareness.introspectInterval = 0
	a.awareness.Introspect(context.Background())

	self := a.awareness.Self()
	if self.Personality != "curious explorer" {
		t.Errorf("personality: got %q, want 'curious explorer' (explore is dominant)", self.Personality)
	}
}

func TestAwareness_PersistRestore(t *testing.T) {
	cfg := DefaultConfig("test-persist-aware", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Modify self-model
	a.awareness.self.Personality = "bold pioneer"
	a.awareness.self.Purpose = "push the boundaries of knowledge"
	a.awareness.self.Morale = 0.9
	a.awareness.self.Narrative = "I am becoming something greater."
	a.awareness.self.ReflectCount = 42
	a.awareness.Persist()

	// Create fresh awareness and restore
	a.awareness = NewAwareness(a)
	a.awareness.Restore()

	self := a.awareness.Self()
	if self.Personality != "bold pioneer" {
		t.Errorf("personality: got %q, want 'bold pioneer'", self.Personality)
	}
	if self.ReflectCount != 42 {
		t.Errorf("reflect count: got %d, want 42", self.ReflectCount)
	}
	if self.Narrative != "I am becoming something greater." {
		t.Errorf("narrative: got %q", self.Narrative)
	}
}

func TestAwareness_MonologueCap(t *testing.T) {
	cfg := DefaultConfig("test-monologue", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Generate lots of thoughts
	for i := 0; i < 60; i++ {
		a.awareness.ObserveTaskOutcome(&ExperienceRecord{
			TaskID:  "t",
			Success: i%2 == 0,
		})
	}

	// Should be capped at maxThoughts (50)
	all := a.awareness.Monologue(100)
	if len(all) > 50 {
		t.Errorf("monologue should be capped at 50, got %d", len(all))
	}

	// Limit works
	limited := a.awareness.Monologue(5)
	if len(limited) != 5 {
		t.Errorf("limited monologue: got %d, want 5", len(limited))
	}
}

func TestAwareness_InfoIncludesSelf(t *testing.T) {
	cfg := DefaultConfig("test-info-aware", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	info := a.Info()
	if info.Self == nil {
		t.Fatal("Info should include self-model")
	}
	if info.Self.Name != "test-info-aware" {
		t.Errorf("self name: got %q", info.Self.Name)
	}
}

func TestAwareness_AgeUpdates(t *testing.T) {
	cfg := DefaultConfig("test-age", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	a.startedAt = time.Now().Add(-5 * time.Minute)
	a.awareness.UpdateMood()

	self := a.awareness.Self()
	if self.Age < 4*time.Minute {
		t.Errorf("age should be ~5min, got %v", self.Age)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{26 * time.Hour, "1d2h"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v): got %q, want %q", tt.d, got, tt.want)
		}
	}
}
