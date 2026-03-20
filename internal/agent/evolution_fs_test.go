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

func TestEvolutionFS_ExportExperience(t *testing.T) {
	cfg := DefaultConfig("test-fs-export", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding", "testing"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	evo := a.evolution
	evo.reflectInterval = 24 * time.Hour
	evo.minRecordsReflect = 999

	// Record experiences
	evo.Record(&ExperienceRecord{
		TaskID: "t1", Description: "fix login bug", Runtime: "builtin",
		Success: true, Duration: 15.0, Skills: []string{"coding"},
	})
	evo.Record(&ExperienceRecord{
		TaskID: "t2", Description: "write unit tests for auth module", Runtime: "builtin",
		Success: true, Duration: 8.0, Skills: []string{"testing"},
	})
	evo.Record(&ExperienceRecord{
		TaskID: "t3", Description: "failed deploy", Runtime: "builtin",
		Success: false, Duration: 2.0, Skills: []string{"devops"}, Error: "timeout",
	})

	tmpDir := t.TempDir()
	fs := NewEvolutionFS(tmpDir, a)

	exported, err := fs.ExportExperience(evo)
	if err != nil {
		t.Fatalf("ExportExperience failed: %v", err)
	}

	// Should export 2 successful experiences (not the failure)
	if exported != 2 {
		t.Errorf("expected 2 exported, got %d", exported)
	}

	// Check experience directory exists
	expDir := filepath.Join(tmpDir, "experience")
	entries, err := os.ReadDir(expDir)
	if err != nil {
		t.Fatalf("reading experience dir: %v", err)
	}

	// Should have at least: 2 .md files + index.yaml
	mdCount := 0
	hasIndex := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
		if e.Name() == "index.yaml" {
			hasIndex = true
		}
	}

	if mdCount != 2 {
		t.Errorf("expected 2 .md files, got %d", mdCount)
	}
	if !hasIndex {
		t.Error("expected index.yaml")
	}

	// Verify .md content
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(expDir, e.Name()))
		content := string(data)
		if !strings.Contains(content, "## 领域") {
			t.Errorf("%s missing domain section", e.Name())
		}
		if !strings.Contains(content, "## 摘要") {
			t.Errorf("%s missing summary section", e.Name())
		}
	}

	// Re-export should not create duplicates
	exported2, _ := fs.ExportExperience(evo)
	if exported2 != 0 {
		t.Errorf("re-export should return 0, got %d", exported2)
	}
}

func TestEvolutionFS_SaveLoadState(t *testing.T) {
	cfg := DefaultConfig("test-fs-state", "gpt-4o-mini")
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
			TaskID: "s" + string(rune('0'+i)), Runtime: "builtin",
			Success: true, Duration: 1.0, Skills: []string{"coding"},
		})
	}
	evo.reflect()

	tmpDir := t.TempDir()
	fs := NewEvolutionFS(tmpDir, a)

	// Save
	if err := fs.SaveEvolutionState(evo); err != nil {
		t.Fatalf("SaveEvolutionState failed: %v", err)
	}

	// Check files created
	stateDir := filepath.Join(tmpDir, "evolution")
	for _, name := range []string{"strategy.yaml", "skills.yaml", "journal.yaml"} {
		if !fileExists(filepath.Join(stateDir, name)) {
			t.Errorf("expected %s to exist", name)
		}
	}

	// Load into fresh engine
	evo2 := NewEvolutionEngine(a)
	fs2 := NewEvolutionFS(tmpDir, a)
	if err := fs2.LoadEvolutionState(evo2); err != nil {
		t.Fatalf("LoadEvolutionState failed: %v", err)
	}

	if len(evo2.journal) != len(evo.journal) {
		t.Errorf("journal length mismatch: got %d, want %d", len(evo2.journal), len(evo.journal))
	}

	strategy := evo2.Strategy()
	if strategy.PreferredRuntime != "builtin" {
		t.Errorf("restored preferred runtime: got %q, want 'builtin'", strategy.PreferredRuntime)
	}
}

func TestEvolutionFS_SyncToDisk(t *testing.T) {
	cfg := DefaultConfig("test-fs-sync", "gpt-4o-mini")
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

	for i := 0; i < 3; i++ {
		evo.Record(&ExperienceRecord{
			TaskID: "sync" + string(rune('0'+i)), Runtime: "builtin",
			Success: true, Duration: 2.0, Skills: []string{"coding"},
			Description: "sync test task " + string(rune('a'+i)),
		})
	}
	evo.reflect()

	tmpDir := t.TempDir()
	fs := NewEvolutionFS(tmpDir, a)

	if err := fs.SyncToDisk(evo, a.peerEvo); err != nil {
		t.Fatalf("SyncToDisk failed: %v", err)
	}

	// Verify all subdirs exist
	for _, dir := range []string{"evolution", "experience"} {
		if !fileExists(filepath.Join(tmpDir, dir)) {
			t.Errorf("expected %s/ directory", dir)
		}
	}

	// Verify evolution state files
	for _, f := range []string{"strategy.yaml", "skills.yaml", "journal.yaml", "peers.yaml"} {
		if !fileExists(filepath.Join(tmpDir, "evolution", f)) {
			t.Errorf("expected evolution/%s", f)
		}
	}
}

func TestEvolutionFS_DirectoryLayout(t *testing.T) {
	cfg := DefaultConfig("test-layout", "gpt-4o-mini")
	cfg.Agent.Skills = []string{"coding"}
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	tmpDir := t.TempDir()

	// Set workdir triggers fs creation
	a.SetWorkDir(tmpDir)
	if a.evoFS == nil {
		t.Fatal("evoFS should be set after SetWorkDir")
	}
}
