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

package memory

import (
	"testing"
)

func TestContextStore_PutAndGet(t *testing.T) {
	store, err := NewSQLiteStore("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	entry := &ContextEntry{
		URI:      "spore://abc123/memory/cases/task001",
		AgentID:  "abc123",
		Type:     CtxMemory,
		Category: CatCases,
		L0:       "Write a haiku",
		L1:       "## Case: Write a haiku\n\nRuntime: builtin",
		L2:       "Full task output here...",
		Tags:     []string{"writing", "creative"},
		Source:   "task:001",
	}

	if err := store.PutContext(entry); err != nil {
		t.Fatalf("PutContext: %v", err)
	}

	got, err := store.GetContext("spore://abc123/memory/cases/task001")
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if got == nil {
		t.Fatal("GetContext returned nil")
	}
	if got.L0 != "Write a haiku" {
		t.Errorf("L0 = %q, want %q", got.L0, "Write a haiku")
	}
	if got.Category != CatCases {
		t.Errorf("Category = %q, want %q", got.Category, CatCases)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "writing" {
		t.Errorf("Tags = %v, want [writing creative]", got.Tags)
	}
}

func TestContextStore_ListByCategory(t *testing.T) {
	store, err := NewSQLiteStore("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Insert entries in different categories.
	for _, e := range []*ContextEntry{
		{URI: "spore://a/memory/profile", AgentID: "a", Type: CtxMemory, Category: CatProfile, L0: "I am A"},
		{URI: "spore://a/memory/cases/1", AgentID: "a", Type: CtxMemory, Category: CatCases, L0: "Case 1"},
		{URI: "spore://a/memory/cases/2", AgentID: "a", Type: CtxMemory, Category: CatCases, L0: "Case 2"},
		{URI: "spore://a/memory/entities/b", AgentID: "a", Type: CtxMemory, Category: CatEntities, L0: "Peer B"},
	} {
		if err := store.PutContext(e); err != nil {
			t.Fatalf("PutContext: %v", err)
		}
	}

	cases, err := store.ListByCategory("a", CatCases, 10)
	if err != nil {
		t.Fatalf("ListByCategory: %v", err)
	}
	if len(cases) != 2 {
		t.Errorf("cases count = %d, want 2", len(cases))
	}

	entities, err := store.ListByCategory("a", CatEntities, 10)
	if err != nil {
		t.Fatalf("ListByCategory: %v", err)
	}
	if len(entities) != 1 {
		t.Errorf("entities count = %d, want 1", len(entities))
	}
}

func TestContextStore_SearchContext(t *testing.T) {
	store, err := NewSQLiteStore("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.PutContext(&ContextEntry{
		URI: "spore://a/memory/cases/1", AgentID: "a", Type: CtxMemory, Category: CatCases,
		L0: "Write haiku", L1: "A poem about nature", L2: "Full text...",
	})
	store.PutContext(&ContextEntry{
		URI: "spore://a/memory/cases/2", AgentID: "a", Type: CtxMemory, Category: CatCases,
		L0: "Fix bug", L1: "Debug segfault in parser", L2: "Stack trace...",
	})

	// Search across all layers.
	results, err := store.SearchContext("haiku", "", "", 10)
	if err != nil {
		t.Fatalf("SearchContext: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("search 'haiku' got %d results, want 1", len(results))
	}

	// Search with category filter.
	results, err = store.SearchContext("bug", "", CatCases, 10)
	if err != nil {
		t.Fatalf("SearchContext: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("search 'bug' in cases got %d results, want 1", len(results))
	}
}

func TestContextStore_Upsert(t *testing.T) {
	store, err := NewSQLiteStore("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	entry := &ContextEntry{
		URI: "spore://a/memory/profile", AgentID: "a", Type: CtxMemory, Category: CatProfile,
		L0: "Version 1",
	}
	store.PutContext(entry)

	// Update.
	entry.L0 = "Version 2"
	store.PutContext(entry)

	got, _ := store.GetContext("spore://a/memory/profile")
	if got.L0 != "Version 2" {
		t.Errorf("L0 after update = %q, want %q", got.L0, "Version 2")
	}
}

func TestContextStore_Stats(t *testing.T) {
	store, err := NewSQLiteStore("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.PutContext(&ContextEntry{URI: "spore://a/memory/profile", AgentID: "a", Type: CtxMemory, Category: CatProfile, L0: "X"})
	store.PutContext(&ContextEntry{URI: "spore://a/memory/cases/1", AgentID: "a", Type: CtxMemory, Category: CatCases, L0: "Y"})
	store.PutContext(&ContextEntry{URI: "spore://a/memory/cases/2", AgentID: "a", Type: CtxMemory, Category: CatCases, L0: "Z"})

	stats := store.ContextStats("a")
	if stats[CatProfile] != 1 {
		t.Errorf("profile count = %d, want 1", stats[CatProfile])
	}
	if stats[CatCases] != 2 {
		t.Errorf("cases count = %d, want 2", stats[CatCases])
	}
}

func TestParseURI(t *testing.T) {
	tests := []struct {
		input string
		want  URI
	}{
		{"spore://abc123/memory/cases/task001", URI{AgentID: "abc123", Type: CtxMemory, Category: CatCases, Path: "task001"}},
		{"spore://abc123/memory/profile", URI{AgentID: "abc123", Type: CtxMemory, Category: CatProfile}},
		{"spore://collective/memory", URI{AgentID: "collective", Type: CtxMemory}},
	}

	for _, tt := range tests {
		got, err := ParseURI(tt.input)
		if err != nil {
			t.Errorf("ParseURI(%q): %v", tt.input, err)
			continue
		}
		if got.AgentID != tt.want.AgentID || got.Type != tt.want.Type ||
			got.Category != tt.want.Category || got.Path != tt.want.Path {
			t.Errorf("ParseURI(%q) = %+v, want %+v", tt.input, got, tt.want)
		}
	}

	// Invalid URI.
	if _, err := ParseURI("http://example.com"); err == nil {
		t.Error("expected error for non-spore URI")
	}
}
