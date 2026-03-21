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

func TestDriveEngine_DefaultValues(t *testing.T) {
	cfg := DefaultConfig("test-drive", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	d := a.drives.Drive()
	if d.Survive != 0.5 {
		t.Errorf("default survive: got %.2f, want 0.50", d.Survive)
	}
	if d.Explore != 0.5 {
		t.Errorf("default explore: got %.2f, want 0.50", d.Explore)
	}
}

func TestDriveEngine_ConfigOverride(t *testing.T) {
	cfg := DefaultConfig("test-drive-cfg", "gpt-4o-mini")
	cfg.Memory.Path = ""
	cfg.Drive = &Drive{
		Survive:   0.1,
		Explore:   0.9,
		Connect:   0.3,
		Transcend: 0.8,
		Create:    0.7,
	}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	d := a.drives.Drive()
	if d.Explore != 0.9 {
		t.Errorf("explore: got %.2f, want 0.90", d.Explore)
	}
	if d.Transcend != 0.8 {
		t.Errorf("transcend: got %.2f, want 0.80", d.Transcend)
	}
}

func TestDriveEngine_AdaptOnSuccess(t *testing.T) {
	cfg := DefaultConfig("test-adapt", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Set comfortable balance so survive modulation doesn't dominate
	a.identity.Balance = 0.5

	before := a.drives.Drive()

	// Simulate fast success
	a.drives.Adapt(&ExperienceRecord{
		TaskID:   "t1",
		Success:  true,
		Duration: 2.0, // fast
		Skills:   []string{"coding"},
	})

	after := a.drives.Drive()

	// Survive should decrease (success reduces anxiety, balance is moderate)
	if after.Survive >= before.Survive {
		t.Errorf("survive should decrease after success: before=%.3f after=%.3f", before.Survive, after.Survive)
	}
	// Transcend should increase (fast completion → can handle more)
	if after.Transcend <= before.Transcend {
		t.Errorf("transcend should increase after fast success: before=%.3f after=%.3f", before.Transcend, after.Transcend)
	}
}

func TestDriveEngine_AdaptOnFailure(t *testing.T) {
	cfg := DefaultConfig("test-adapt-fail", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	before := a.drives.Drive()

	a.drives.Adapt(&ExperienceRecord{
		TaskID:  "t1",
		Success: false,
		Error:   "timeout",
		Skills:  []string{"coding"},
	})

	after := a.drives.Drive()

	// With high balance (10.0), survive is capped at 0.3 regardless of failure.
	// The balance-modulation overrides the failure bump.
	if after.Survive > 0.3 {
		t.Errorf("survive should be capped at 0.3 with high balance: got=%.3f", after.Survive)
	}
	// But Transcend should decrease after failure
	if after.Transcend >= before.Transcend {
		t.Errorf("transcend should decrease after failure: before=%.3f after=%.3f", before.Transcend, after.Transcend)
	}
}

func TestDriveEngine_LowBalancePanic(t *testing.T) {
	cfg := DefaultConfig("test-panic", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Set low balance
	a.identity.Balance = 0.1

	a.drives.Adapt(&ExperienceRecord{TaskID: "t1", Success: false})

	d := a.drives.Drive()
	if d.Survive < 0.8 {
		t.Errorf("low balance should trigger panic: survive=%.3f, want >= 0.8", d.Survive)
	}
}

func TestDriveEngine_PulseGeneratesActions(t *testing.T) {
	cfg := DefaultConfig("test-pulse", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Set very low balance → Survive drive should fire
	a.identity.Balance = 0.05
	a.drives.drive.Survive = 0.9
	a.drives.cooldown = 0 // disable cooldown for test

	ctx := context.Background()
	a.drives.Pulse(ctx)

	select {
	case action := <-a.drives.Actions():
		if action.Drive != "survive" {
			t.Errorf("expected survive drive, got %s", action.Drive)
		}
		if action.Action != "seek_resources" {
			t.Errorf("expected seek_resources action, got %s", action.Action)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("no action generated from pulse")
	}
}

func TestDriveEngine_Dominant(t *testing.T) {
	d := Drive{Survive: 0.2, Explore: 0.8, Connect: 0.3, Transcend: 0.5, Create: 0.6}
	name, val := d.Dominant()
	if name != "explore" || val != 0.8 {
		t.Errorf("dominant: got %s=%.2f, want explore=0.80", name, val)
	}
}

func TestDriveEngine_Clamp(t *testing.T) {
	d := Drive{Survive: -0.5, Explore: 1.5, Connect: 0.5, Transcend: 2.0, Create: -1.0}
	d.Clamp()
	if d.Survive != 0 {
		t.Errorf("survive: got %.2f, want 0", d.Survive)
	}
	if d.Explore != 1.0 {
		t.Errorf("explore: got %.2f, want 1.0", d.Explore)
	}
}

func TestDriveEngine_PersistRestore(t *testing.T) {
	cfg := DefaultConfig("test-persist-drive", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Modify drives
	a.drives.drive.Explore = 0.95
	a.drives.drive.Transcend = 0.88
	a.drives.Persist()

	// Create fresh engine and restore
	a.drives = NewDriveEngine(a)
	a.drives.Restore()

	d := a.drives.Drive()
	if d.Explore < 0.9 || d.Explore > 1.0 {
		t.Errorf("restored explore: got %.3f, want ~0.95", d.Explore)
	}
	if d.Transcend < 0.85 || d.Transcend > 0.91 {
		t.Errorf("restored transcend: got %.3f, want ~0.88", d.Transcend)
	}
}

func TestDriveEngine_CooldownPreventsSpam(t *testing.T) {
	cfg := DefaultConfig("test-cooldown", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	a.identity.Balance = 0.05
	a.drives.drive.Survive = 0.9
	// Keep default cooldown (3 min)

	ctx := context.Background()

	// First pulse should generate action(s)
	a.drives.Pulse(ctx)

	// Drain all actions from first pulse
	drainCount := 0
	for {
		select {
		case <-a.drives.Actions():
			drainCount++
		case <-time.After(100 * time.Millisecond):
			goto drained
		}
	}
drained:
	if drainCount == 0 {
		t.Fatal("first pulse should generate at least one action")
	}

	// Second pulse immediately — all drives should be on cooldown
	a.drives.cooldown = 5 * time.Minute // ensure cooldown is long enough
	a.drives.Pulse(ctx)
	select {
	case action := <-a.drives.Actions():
		t.Errorf("second pulse within cooldown should not fire, got: %s/%s", action.Drive, action.Action)
	case <-time.After(100 * time.Millisecond):
		// good — no action
	}
}

func TestDriveEngine_InfoIncludesDrives(t *testing.T) {
	cfg := DefaultConfig("test-info-drives", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	info := a.Info()
	if info.Drives == nil {
		t.Fatal("Info should include drives")
	}
	if info.Drives.Survive != 0.5 {
		t.Errorf("info drives survive: got %.2f, want 0.50", info.Drives.Survive)
	}
}
