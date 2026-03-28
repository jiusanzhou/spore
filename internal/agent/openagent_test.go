/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 */

package agent

import (
	"os"
	"path/filepath"
	"testing"

	manifest "go.zoe.im/agentbox/pkg/agent"
	"gopkg.in/yaml.v3"
)

func TestExperienceLevel(t *testing.T) {
	tests := []struct {
		packs int
		level string
	}{
		{0, "junior"},
		{1, "junior"},
		{10, "junior"},
		{11, "mid"},
		{50, "mid"},
		{51, "senior"},
		{200, "expert"},
		{201, "expert"},
	}
	for _, tt := range tests {
		if got := ExperienceLevel(tt.packs); got != tt.level {
			t.Errorf("ExperienceLevel(%d) = %q, want %q", tt.packs, got, tt.level)
		}
	}
}

func TestInferModelTier(t *testing.T) {
	tests := []struct {
		model string
		tier  string
	}{
		{"claude-opus-4-6", "opus"},
		{"claude-sonnet-4", "sonnet"},
		{"claude-3-haiku", "haiku"},
		{"gpt-4o", "sonnet"},
		{"gpt-3.5-turbo", "haiku"},
		{"qwen3-0.6b", "sonnet"},
	}
	for _, tt := range tests {
		if got := inferModelTier(tt.model); got != tt.tier {
			t.Errorf("inferModelTier(%q) = %q, want %q", tt.model, got, tt.tier)
		}
	}
}

func TestManifestRoundTrip(t *testing.T) {
	m := &manifest.Manifest{
		ID:          "test-agent-01",
		Name:        "TestAgent",
		Version:     "1.0.0",
		Description: "A test agent",
		Persona: &manifest.Persona{
			Style: "test style",
			Tone:  "test tone",
		},
		Skills: []manifest.SkillRef{
			{Name: "coding"},
			{Name: "research", Version: "gen1"},
		},
		Collaboration: &manifest.Collaboration{
			CanDelegate: true,
			CanReceive:  true,
			Protocols:   []string{"spore/gossipsub"},
		},
		Experience: &manifest.Experience{
			Level:   "mid",
			Packs:   25,
			Domains: []string{"coding", "research"},
		},
		Marketplace: &manifest.Marketplace{
			Category: "development",
			Tags:     []string{"coding"},
			Pricing:  &manifest.Pricing{Model: "usage", Base: "1.0 tokens/task"},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "agent.yaml")

	// Save via yaml marshal
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Load back via Spore's LoadManifest
	cfg, loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	// Verify manifest
	if loaded.ID != m.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, m.ID)
	}
	if loaded.Name != m.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, m.Name)
	}
	if len(loaded.Skills) != 2 {
		t.Errorf("Skills len = %d, want 2", len(loaded.Skills))
	}
	if loaded.Experience.Level != "mid" {
		t.Errorf("Experience.Level = %q, want mid", loaded.Experience.Level)
	}

	// Verify config conversion
	if cfg.Agent.Name != "TestAgent" {
		t.Errorf("cfg.Name = %q, want TestAgent", cfg.Agent.Name)
	}
	if len(cfg.Agent.Skills) != 2 {
		t.Errorf("cfg.Skills = %v, want 2 skills", cfg.Agent.Skills)
	}
}

func TestLoadConfigAgentYaml(t *testing.T) {
	dir := t.TempDir()

	m := &manifest.Manifest{
		ID:          "yaml-agent",
		Name:        "YamlBot",
		Version:     "1.0.0",
		Description: "loaded from yaml",
		Persona:     &manifest.Persona{Style: "yaml", Tone: "yaml"},
		Skills:      []manifest.SkillRef{{Name: "testing"}},
		Collaboration: &manifest.Collaboration{CanDelegate: true, CanReceive: true},
	}
	if err := writeManifestFile(filepath.Join(dir, "agent.yaml"), m); err != nil {
		t.Fatal(err)
	}

	// LoadConfig should pick agent.yaml over (missing) spore.toml
	cfg, err := LoadConfig("", dir)
	if err != nil {
		t.Fatalf("LoadConfig from agent.yaml: %v", err)
	}
	if cfg.Agent.Name != "YamlBot" {
		t.Errorf("Name = %q, want YamlBot", cfg.Agent.Name)
	}
	if len(cfg.Agent.Skills) != 1 || cfg.Agent.Skills[0] != "testing" {
		t.Errorf("Skills = %v, want [testing]", cfg.Agent.Skills)
	}
}

func writeManifestFile(path string, m *manifest.Manifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
