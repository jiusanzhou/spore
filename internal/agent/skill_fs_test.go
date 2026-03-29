/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillFSCreate(t *testing.T) {
	dir := t.TempDir()

	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{
		Name:        "research",
		Description: "Search the web and synthesize findings",
		Category:    "core",
		Tags:        []string{"web", "search"},
		Origin:      "imported",
		Priority:    75,
	}
	body := `# Research

## When to Use
When the agent needs to find information not in its memory.

## Procedure
1. Decompose question into sub-queries
2. Search multiple sources
3. Cross-reference findings
4. Synthesize into actionable summary

## Verification
Check that sources are cited and findings are consistent.`

	skill, err := fs.Create(meta, body)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if skill.Meta.Name != "research" {
		t.Errorf("expected name 'research', got %q", skill.Meta.Name)
	}
	if skill.Meta.ContentHash == "" {
		t.Error("expected non-empty content hash")
	}
	if skill.Meta.CreatedAt == "" {
		t.Error("expected non-empty created_at")
	}

	// Verify file on disk
	data, err := os.ReadFile(filepath.Join(dir, "skills", "research", "SKILL.md"))
	if err != nil {
		t.Fatalf("reading SKILL.md: %v", err)
	}
	if !strings.Contains(string(data), "name: research") {
		t.Error("SKILL.md missing frontmatter name")
	}
	if !strings.Contains(string(data), "## Procedure") {
		t.Error("SKILL.md missing body content")
	}

	// Duplicate create should fail
	_, err = fs.Create(meta, body)
	if err == nil {
		t.Error("expected error on duplicate create")
	}
}

func TestSkillFSUpdate(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{Name: "deploy", Description: "Deploy services", Category: "devops"}
	_, err = fs.Create(meta, "# Deploy\n\nDeploy stuff.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update
	newMeta := SkillMeta{Name: "deploy", Description: "Deploy to K8s", Category: "devops", Generation: 1}
	updated, err := fs.Update("deploy", newMeta, "# Deploy\n\nDeploy to K8s clusters.", "improved deploy instructions")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Meta.Description != "Deploy to K8s" {
		t.Errorf("expected updated description, got %q", updated.Meta.Description)
	}

	// Update nonexistent should fail
	_, err = fs.Update("nonexistent", SkillMeta{Name: "nonexistent"}, "body", "change")
	if err == nil {
		t.Error("expected error on nonexistent update")
	}
}

func TestSkillFSPatch(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{Name: "search", Description: "Web search", Category: "core"}
	_, err = fs.Create(meta, "# Search\n\nUse DuckDuckGo for queries.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	patched, err := fs.Patch("search", "DuckDuckGo", "Brave Search")
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(patched.Body, "Brave Search") {
		t.Error("patch not applied")
	}
	if strings.Contains(patched.Body, "DuckDuckGo") {
		t.Error("old text still present")
	}

	// Patch nonexistent target
	_, err = fs.Patch("search", "NoSuchText", "replacement")
	if err == nil {
		t.Error("expected error on missing patch target")
	}
}

func TestSkillFSDelete(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{Name: "temp-skill", Description: "Temporary", Category: "test"}
	_, err = fs.Create(meta, "# Temp\n\nTemporary skill.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, ok := fs.Get("temp-skill"); !ok {
		t.Error("skill should exist before delete")
	}

	if err := fs.Delete("temp-skill"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, ok := fs.Get("temp-skill"); ok {
		t.Error("skill should not exist after delete")
	}

	// Directory should be gone
	if _, err := os.Stat(filepath.Join(dir, "skills", "temp-skill")); !os.IsNotExist(err) {
		t.Error("skill directory should be removed")
	}
}

func TestSkillFSScanDisk(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skills")

	// Pre-create a skill on disk
	os.MkdirAll(filepath.Join(skillDir, "preexisting"), 0755)
	os.WriteFile(filepath.Join(skillDir, "preexisting", "SKILL.md"), []byte(`---
name: preexisting
description: A skill that was already on disk
category: meta
---

# Preexisting

This skill was here before SkillFS started.
`), 0644)

	fs, err := NewSkillFS(skillDir, nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	if _, ok := fs.Get("preexisting"); !ok {
		t.Error("should load preexisting skill from disk")
	}

	names := fs.List()
	if len(names) != 1 || names[0] != "preexisting" {
		t.Errorf("expected [preexisting], got %v", names)
	}
}

func TestSkillFSMetrics(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{Name: "coding", Description: "Write code", Category: "core"}
	_, err = fs.Create(meta, "# Coding\n\nWrite clean code.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	fs.IncrSelection("coding")
	fs.IncrSelection("coding")
	fs.IncrApplied("coding")
	fs.IncrCompletion("coding")

	m := fs.Metrics("coding")
	if m.TotalSelections != 2 {
		t.Errorf("expected 2 selections, got %d", m.TotalSelections)
	}
	if m.TotalApplied != 1 {
		t.Errorf("expected 1 applied, got %d", m.TotalApplied)
	}
	if m.TotalCompletions != 1 {
		t.Errorf("expected 1 completion, got %d", m.TotalCompletions)
	}
}

func TestSkillFSWriteFile(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{Name: "deploy", Description: "Deploy skill", Category: "devops"}
	_, err = fs.Create(meta, "# Deploy\n\nDeploy services.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Write a reference file
	err = fs.WriteFile("deploy", "references/k8s-cheatsheet.md", []byte("# K8s Cheatsheet\n\nkubectl apply -f ..."))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Verify it exists
	data, err := os.ReadFile(filepath.Join(dir, "skills", "deploy", "references", "k8s-cheatsheet.md"))
	if err != nil {
		t.Fatalf("reading ref file: %v", err)
	}
	if !strings.Contains(string(data), "K8s Cheatsheet") {
		t.Error("reference file content mismatch")
	}

	// Write to nonexistent skill should fail
	err = fs.WriteFile("nonexistent", "file.md", []byte("content"))
	if err == nil {
		t.Error("expected error on nonexistent skill")
	}
}

func TestSkillFSIPFSPublish(t *testing.T) {
	dir := t.TempDir()

	// Mock IPFS publisher
	var published []string
	mockPublish := func(data []byte, contentType, agentID, summary string) (string, error) {
		hash := "bafkrei" + strings.Repeat("a", 20) // fake CID
		published = append(published, hash)
		return hash, nil
	}

	fs, err := NewSkillFS(filepath.Join(dir, "skills"), mockPublish)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{Name: "test-ipfs", Description: "IPFS test skill", Category: "test"}
	skill, err := fs.Create(meta, "# Test\n\nIPFS skill.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(published) != 1 {
		t.Errorf("expected 1 publish, got %d", len(published))
	}
	if skill.Meta.IPFSCID == "" {
		t.Error("expected IPFS CID to be set")
	}

	// Verify CID is in the file on disk
	data, _ := os.ReadFile(filepath.Join(dir, "skills", "test-ipfs", "SKILL.md"))
	if !strings.Contains(string(data), "x-ipfs-cid:") {
		t.Error("IPFS CID not written to frontmatter")
	}
}

func TestSkillFSImportFromCID(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	remoteSkill := `---
name: remote-skill
description: A skill from another agent
category: coding
origin: captured
generation: 2
---

# Remote Skill

Learned from peer agent.

## Procedure
1. Do the thing
2. Verify it worked
`

	mockFetch := func(cid string) ([]byte, error) {
		return []byte(remoteSkill), nil
	}

	skill, err := fs.ImportFromCID("fakecid123", mockFetch)
	if err != nil {
		t.Fatalf("ImportFromCID: %v", err)
	}
	if skill.Meta.Name != "remote-skill" {
		t.Errorf("expected name 'remote-skill', got %q", skill.Meta.Name)
	}
	if skill.Meta.Origin != "imported" {
		t.Errorf("expected origin 'imported', got %q", skill.Meta.Origin)
	}
}

func TestSkillFSWriteIndex(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	fs.Create(SkillMeta{Name: "alpha", Description: "Alpha skill", Category: "core"}, "# Alpha")
	fs.Create(SkillMeta{Name: "beta", Description: "Beta skill", Category: "meta"}, "# Beta")

	if err := fs.WriteIndex(); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "skills", "index.yaml"))
	if err != nil {
		t.Fatalf("reading index.yaml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "alpha") || !strings.Contains(content, "beta") {
		t.Error("index.yaml missing skill entries")
	}
	if !strings.Contains(content, "count: 2") {
		t.Error("index.yaml missing correct count")
	}
}

func TestParseSkillMDNoFrontmatter(t *testing.T) {
	raw := "# My Skill\n\nJust a plain markdown skill."
	meta, body, err := parseSkillMD(raw)
	if err != nil {
		t.Fatalf("parseSkillMD: %v", err)
	}
	if meta.Name != "My Skill" {
		t.Errorf("expected name from heading, got %q", meta.Name)
	}
	if !strings.Contains(body, "plain markdown") {
		t.Error("body not parsed correctly")
	}
}

func TestParseSkillMDWithFrontmatter(t *testing.T) {
	raw := `---
name: test-skill
description: Test
category: test
tags:
  - foo
  - bar
---

# Test Skill

Body content here.`

	meta, body, err := parseSkillMD(raw)
	if err != nil {
		t.Fatalf("parseSkillMD: %v", err)
	}
	if meta.Name != "test-skill" {
		t.Errorf("expected 'test-skill', got %q", meta.Name)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "foo" {
		t.Errorf("expected tags [foo, bar], got %v", meta.Tags)
	}
	if !strings.Contains(body, "Body content here") {
		t.Error("body not parsed correctly")
	}
}

func TestSkillFSVersionTracking(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewSkillFS(filepath.Join(dir, "skills"), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	meta := SkillMeta{Name: "versioned", Description: "v1", Category: "test"}
	_, err = fs.Create(meta, "# V1\n\nFirst version.")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update creates version 2
	meta2 := SkillMeta{Name: "versioned", Description: "v2", Category: "test"}
	_, err = fs.Update("versioned", meta2, "# V2\n\nSecond version.", "improved")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Check version history in DB
	var count int
	fs.indexDB.QueryRow(`SELECT COUNT(*) FROM skill_versions WHERE name = ?`, "versioned").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 versions, got %d", count)
	}
}
