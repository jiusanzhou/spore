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
	"testing"
	"time"
)

func TestSkillCatalog_IngestServiceAd(t *testing.T) {
	sc := NewSkillCatalog()

	ad := &ServiceAd{
		AgentID:    "agent-abc",
		Name:       "forge",
		Skills:     []string{"coding", "research", "deploy"},
		Reputation: 0.85,
		Timestamp:  time.Now().Unix(),
	}
	sc.IngestServiceAd(ad)

	stats := sc.Stats()
	if stats.UniqueSkills != 3 {
		t.Errorf("expected 3 unique skills, got %d", stats.UniqueSkills)
	}
	if stats.TotalProviders != 1 {
		t.Errorf("expected 1 provider, got %d", stats.TotalProviders)
	}
}

func TestSkillCatalog_IngestSkillCID(t *testing.T) {
	sc := NewSkillCatalog()

	sc.IngestSkillCID("coding", "bafkrei123", "agent-abc", "Write code", 2)
	sc.IngestSkillCID("research", "bafkrei456", "agent-xyz", "Search web", 1)

	stats := sc.Stats()
	if stats.UniqueSkills != 2 {
		t.Errorf("expected 2 skills, got %d", stats.UniqueSkills)
	}
	if stats.WithCID != 2 {
		t.Errorf("expected 2 with CID, got %d", stats.WithCID)
	}
}

func TestSkillCatalog_BrowseFilter(t *testing.T) {
	sc := NewSkillCatalog()

	sc.IngestSkillCID("coding", "cid1", "agent-a", "Write clean code", 0)
	sc.IngestSkillCID("deploy", "cid2", "agent-b", "Deploy to K8s", 0)
	sc.IngestSkillCID("research", "", "agent-c", "Web research", 0)

	// Filter by query
	results := sc.Browse(BrowseFilter{Query: "code"})
	if len(results) != 1 || results[0].Name != "coding" {
		t.Errorf("expected coding, got %v", results)
	}

	// Filter by HasCID
	results = sc.Browse(BrowseFilter{HasCID: true})
	if len(results) != 2 {
		t.Errorf("expected 2 installable, got %d", len(results))
	}

	// No filter — all
	results = sc.Browse(BrowseFilter{})
	if len(results) != 3 {
		t.Errorf("expected 3 total, got %d", len(results))
	}
}

func TestSkillCatalog_UniqueSkills(t *testing.T) {
	sc := NewSkillCatalog()

	// Same skill from two providers
	sc.IngestSkillCID("coding", "cid1", "agent-a", "Agent A coding", 1)
	sc.IngestSkillCID("coding", "cid2", "agent-b", "Agent B coding", 2)
	sc.IngestSkillCID("research", "cid3", "agent-a", "Research", 0)

	unique := sc.UniqueSkills()
	if len(unique) != 2 {
		t.Errorf("expected 2 unique skills, got %d", len(unique))
	}
}

func TestSkillCatalog_Install(t *testing.T) {
	sc := NewSkillCatalog()
	sc.IngestSkillCID("test-skill", "fakecid123", "agent-a", "Test skill", 0)

	dir := t.TempDir()
	fs, err := NewSkillFS(dir, nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}
	defer fs.Close()

	mockFetch := func(cid string) ([]byte, error) {
		return []byte(`---
name: test-skill
description: A fetched skill
category: test
---

# Test Skill

Fetched from catalog.
`), nil
	}

	err = sc.Install("test-skill", fs, mockFetch)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	if _, ok := fs.Get("test-skill"); !ok {
		t.Error("skill should be installed after Install()")
	}
}

func TestSkillCatalog_InstallNotFound(t *testing.T) {
	sc := NewSkillCatalog()

	dir := t.TempDir()
	fs, _ := NewSkillFS(dir, nil)
	defer fs.Close()

	err := sc.Install("nonexistent", fs, nil)
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
}

func TestSkillCatalog_InstallNoCID(t *testing.T) {
	sc := NewSkillCatalog()
	sc.IngestSkillCID("no-cid-skill", "", "agent-a", "No CID", 0)

	dir := t.TempDir()
	fs, _ := NewSkillFS(dir, nil)
	defer fs.Close()

	err := sc.Install("no-cid-skill", fs, nil)
	if err == nil {
		t.Error("expected error for skill without CID")
	}
}

func TestSkillCatalog_UpdateProvider(t *testing.T) {
	sc := NewSkillCatalog()

	sc.IngestSkillCID("coding", "cid-v1", "agent-a", "Version 1", 1)
	sc.IngestSkillCID("coding", "cid-v2", "agent-a", "Version 2", 2) // update same provider

	stats := sc.Stats()
	if stats.WithCID != 1 {
		t.Errorf("expected 1 CID entry (updated in place), got %d", stats.WithCID)
	}

	unique := sc.UniqueSkills()
	if len(unique) != 1 {
		t.Fatalf("expected 1 unique, got %d", len(unique))
	}
	if unique[0].Generation != 2 {
		t.Errorf("expected gen 2 after update, got %d", unique[0].Generation)
	}
}
