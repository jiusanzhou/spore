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
	"testing"
	"time"
)

func TestSkillStore_PutGetSkill(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	defer store.Close()

	rec := &SkillRecord{
		SkillID:     "web-search__abc123",
		Name:        "web-search",
		Description: "Search the web using DuckDuckGo",
		IsActive:    true,
		Origin:      SkillOriginImported,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := store.PutSkill(rec); err != nil {
		t.Fatalf("PutSkill: %v", err)
	}

	got, err := store.GetSkill("web-search__abc123")
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got == nil {
		t.Fatal("GetSkill returned nil")
	}
	if got.Name != "web-search" {
		t.Errorf("Name = %q, want %q", got.Name, "web-search")
	}
	if got.Origin != SkillOriginImported {
		t.Errorf("Origin = %q, want %q", got.Origin, SkillOriginImported)
	}
}

func TestSkillStore_ActiveSkills(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	skills := []*SkillRecord{
		{SkillID: "s1", Name: "coding", IsActive: true, Origin: SkillOriginImported, CreatedAt: now, UpdatedAt: now},
		{SkillID: "s2", Name: "research", IsActive: true, Origin: SkillOriginCaptured, CreatedAt: now, UpdatedAt: now},
		{SkillID: "s3", Name: "obsolete", IsActive: false, Origin: SkillOriginFixed, CreatedAt: now, UpdatedAt: now},
	}
	for _, s := range skills {
		if err := store.PutSkill(s); err != nil {
			t.Fatalf("PutSkill %s: %v", s.Name, err)
		}
	}

	active, err := store.ActiveSkills()
	if err != nil {
		t.Fatalf("ActiveSkills: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("ActiveSkills count = %d, want 2", len(active))
	}
}

func TestSkillStore_IncrementCounters(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	rec := &SkillRecord{
		SkillID: "test-skill__001", Name: "test-skill",
		IsActive: true, Origin: SkillOriginImported,
		CreatedAt: now, UpdatedAt: now,
	}
	store.PutSkill(rec)

	store.IncrementSelection("test-skill__001")
	store.IncrementSelection("test-skill__001")
	store.IncrementApplied("test-skill__001")
	store.IncrementCompletion("test-skill__001")

	got, _ := store.GetSkill("test-skill__001")
	if got.TotalSelections != 2 {
		t.Errorf("Selections = %d, want 2", got.TotalSelections)
	}
	if got.TotalApplied != 1 {
		t.Errorf("Applied = %d, want 1", got.TotalApplied)
	}
	if got.TotalCompletions != 1 {
		t.Errorf("Completions = %d, want 1", got.TotalCompletions)
	}
	if got.SuccessRate() != 1.0 {
		t.Errorf("SuccessRate = %f, want 1.0", got.SuccessRate())
	}
}

func TestSkillStore_PutAnalysis(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	defer store.Close()

	analysis := &ExecutionAnalysisResult{
		TaskID:        "task-001",
		AgentID:       "agent-abc",
		Timestamp:     time.Now().UTC(),
		Success:       true,
		Quality:       0.85,
		Efficiency:    0.7,
		QualityReason: "good result, slight inefficiency",
		SkillsUsed:    []string{"web-search"},
		SkillsNeeded:  []string{"data-analysis"},
		Suggestions: []EvolutionSuggestion{
			{Type: EvolutionCaptured, SkillName: "data-analysis", Reason: "novel pattern", Priority: 0.8},
		},
	}

	if err := store.PutAnalysis(analysis); err != nil {
		t.Fatalf("PutAnalysis: %v", err)
	}

	recent, err := store.RecentAnalyses("agent-abc", 10)
	if err != nil {
		t.Fatalf("RecentAnalyses: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("RecentAnalyses count = %d, want 1", len(recent))
	}
	if recent[0].Quality != 0.85 {
		t.Errorf("Quality = %f, want 0.85", recent[0].Quality)
	}
	if len(recent[0].Suggestions) != 1 {
		t.Errorf("Suggestions count = %d, want 1", len(recent[0].Suggestions))
	}
}

func TestSkillStore_Stats(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	store.PutSkill(&SkillRecord{SkillID: "s1", Name: "coding", IsActive: true, Origin: SkillOriginImported,
		TotalApplied: 10, TotalCompletions: 8, CreatedAt: now, UpdatedAt: now})
	store.PutSkill(&SkillRecord{SkillID: "s2", Name: "derived-coding", IsActive: true, Origin: SkillOriginDerived,
		Generation: 1, TotalApplied: 5, TotalCompletions: 5, CreatedAt: now, UpdatedAt: now})
	store.PutAnalysis(&ExecutionAnalysisResult{TaskID: "t1", AgentID: "agent-x", Timestamp: now})

	stats := store.Stats("agent-x")
	if stats.TotalSkills != 2 {
		t.Errorf("TotalSkills = %d, want 2", stats.TotalSkills)
	}
	if stats.ActiveSkills != 2 {
		t.Errorf("ActiveSkills = %d, want 2", stats.ActiveSkills)
	}
	if stats.Evolved != 1 {
		t.Errorf("Evolved = %d, want 1", stats.Evolved)
	}
	if stats.TotalAnalyses != 1 {
		t.Errorf("TotalAnalyses = %d, want 1", stats.TotalAnalyses)
	}
	// avg: (8/10 + 5/5) / 2 = 0.9
	if stats.AvgSuccess < 0.89 || stats.AvgSuccess > 0.91 {
		t.Errorf("AvgSuccess = %f, want ~0.9", stats.AvgSuccess)
	}
}

func TestSkillLineage(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()

	// Root skill
	root := &SkillRecord{
		SkillID: "web-search__root", Name: "web-search",
		IsActive: true, Origin: SkillOriginImported, Generation: 0,
		CreatedAt: now, UpdatedAt: now,
	}
	store.PutSkill(root)

	// Fixed version
	fixed := &SkillRecord{
		SkillID: "web-search__fix1", Name: "web-search",
		IsActive: true, Origin: SkillOriginFixed, Generation: 1,
		ParentIDs: []string{"web-search__root"},
		ChangeSummary: "Fixed timeout handling",
		CreatedAt: now, UpdatedAt: now,
	}
	store.PutSkill(fixed)

	// Deactivate root
	root.IsActive = false
	store.PutSkill(root)

	// Only the fixed version should be active
	active, _ := store.ActiveSkills()
	if len(active) != 1 {
		t.Fatalf("ActiveSkills = %d, want 1", len(active))
	}
	if active[0].Generation != 1 {
		t.Errorf("Generation = %d, want 1", active[0].Generation)
	}
	if len(active[0].ParentIDs) != 1 || active[0].ParentIDs[0] != "web-search__root" {
		t.Errorf("ParentIDs = %v, want [web-search__root]", active[0].ParentIDs)
	}
}

func TestParseAnalysisResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		quality float64
	}{
		{
			name:    "clean JSON",
			input:   `{"success":true,"quality":0.85,"efficiency":0.7,"quality_reason":"good","skills_used":["web"],"skills_needed":[],"suggestions":[]}`,
			quality: 0.85,
		},
		{
			name:    "with markdown fences",
			input:   "```json\n{\"success\":true,\"quality\":0.6,\"efficiency\":0.5,\"quality_reason\":\"ok\",\"skills_used\":[],\"skills_needed\":[],\"suggestions\":[]}\n```",
			quality: 0.6,
		},
		{
			name:    "with surrounding text",
			input:   "Here is the analysis:\n{\"success\":false,\"quality\":0.3,\"efficiency\":0.2,\"quality_reason\":\"bad\",\"skills_used\":[],\"skills_needed\":[],\"suggestions\":[]}\nDone.",
			quality: 0.3,
		},
		{
			name:    "clamp out of range",
			input:   `{"success":true,"quality":1.5,"efficiency":-0.5,"quality_reason":"clamped","skills_used":[],"skills_needed":[],"suggestions":[]}`,
			quality: 1.0,
		},
		{
			name:    "invalid JSON",
			input:   "this is not json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseAnalysisResponse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Quality != tt.quality {
				t.Errorf("Quality = %f, want %f", result.Quality, tt.quality)
			}
		})
	}
}

func TestSanitizeSkillName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Web Search", "web-search"},
		{"data_analysis", "data-analysis"},
		{"  Mixed CASE--name  ", "mixed-case-name"},
		{"a-very-long-skill-name-that-exceeds-the-maximum-allowed-length-for-directory-names", "a-very-long-skill-name-that-exceeds-the-maximum"},
	}
	for _, tt := range tests {
		got := sanitizeSkillName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeSkillName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateSkillID(t *testing.T) {
	id1 := generateSkillID("web-search", "captured", "task-001")
	id2 := generateSkillID("web-search", "captured", "task-002")

	// IDs should be unique
	if id1 == id2 {
		t.Errorf("IDs should be unique: %s == %s", id1, id2)
	}

	// Should start with sanitized name
	if len(id1) < 10 {
		t.Errorf("ID too short: %s", id1)
	}
}

func TestSkillStoreDir(t *testing.T) {
	dir := t.TempDir()
	subdir := dir + "/nested/skills"

	store, err := NewSkillStore(subdir)
	if err != nil {
		t.Fatalf("NewSkillStore nested: %v", err)
	}
	defer store.Close()

	// Check skills.db was created
	if _, err := os.Stat(subdir + "/skills.db"); os.IsNotExist(err) {
		t.Error("skills.db not created")
	}
}
