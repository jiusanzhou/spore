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
	"fmt"
	"testing"
)

func TestSQLiteStore_PutGet(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	entry := &Entry{
		ID:      "test-1",
		AgentID: "agent-0",
		Key:     "greeting",
		Value:   "hello world",
	}

	if err := store.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get("greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.Value != "hello world" {
		t.Errorf("expected value 'hello world', got %q", got.Value)
	}
	if got.AgentID != "agent-0" {
		t.Errorf("expected agent 'agent-0', got %q", got.AgentID)
	}
}

func TestSQLiteStore_GetNonExistent(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	got, err := store.Get("nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestSQLiteStore_Upsert(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	entry1 := &Entry{ID: "1", AgentID: "a", Key: "k", Value: "v1"}
	entry2 := &Entry{ID: "2", AgentID: "a", Key: "k", Value: "v2"}

	store.Put(entry1)
	store.Put(entry2)

	got, _ := store.Get("k")
	if got.Value != "v2" {
		t.Errorf("expected upserted value 'v2', got %q", got.Value)
	}
}

func TestSQLiteStore_Search(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	entries := []*Entry{
		{ID: "1", AgentID: "a", Key: "fact-1", Value: "Go is a programming language"},
		{ID: "2", AgentID: "a", Key: "fact-2", Value: "Rust is also a programming language"},
		{ID: "3", AgentID: "a", Key: "fact-3", Value: "cats are cool"},
	}
	for _, e := range entries {
		store.Put(e)
	}

	results, err := store.Search("programming", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	results2, err := store.Search("cats", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results2) != 1 {
		t.Errorf("expected 1 result, got %d", len(results2))
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	entry := &Entry{ID: "1", AgentID: "a", Key: "temp", Value: "value"}
	store.Put(entry)

	if err := store.Delete("temp"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, _ := store.Get("temp")
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestSQLiteStore_SearchLimit(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	for i := 0; i < 30; i++ {
		store.Put(&Entry{
			ID:      fmt.Sprintf("id-%d", i),
			AgentID: "a",
			Key:     fmt.Sprintf("key-%d", i),
			Value:   "common value here",
		})
	}

	results, err := store.Search("common", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results (limit), got %d", len(results))
	}
}

func TestSQLiteStore_AccessCount(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	entry := &Entry{ID: "1", AgentID: "a", Key: "counter", Value: "val"}
	store.Put(entry)

	// Access a few times
	store.Get("counter")
	store.Get("counter")
	store.Get("counter")

	got, _ := store.Get("counter")
	// Each Get increments, so after 3 Gets + 1 more Get = 4 bumps total
	// But implementation increments after scan, so it's 3 from previous + 1 from last = scan shows 3
	if got.AccessCnt < 3 {
		t.Errorf("expected access count >= 3, got %d", got.AccessCnt)
	}
}
