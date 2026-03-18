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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockIPFSServer creates a mock IPFS HTTP API for testing.
func mockIPFSServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := make(map[string][]byte) // CID -> data
	cidCounter := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v0/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Read the multipart form data.
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		var buf []byte
		tmp := make([]byte, 4096)
		for {
			n, err := file.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}

		cidCounter++
		cid := fmt.Sprintf("QmTest%d", cidCounter)
		store[cid] = buf

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"Hash": cid})
	})

	mux.HandleFunc("/api/v0/cat", func(w http.ResponseWriter, r *http.Request) {
		cid := r.URL.Query().Get("arg")
		data, ok := store[cid]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Write(data)
	})

	mux.HandleFunc("/api/v0/pin/rm", func(w http.ResponseWriter, r *http.Request) {
		cid := r.URL.Query().Get("arg")
		delete(store, cid)
		json.NewEncoder(w).Encode(map[string]string{"Pins": cid})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestIPFSStore(t *testing.T, srv *httptest.Server) *IPFSStore {
	t.Helper()
	// Strip "http://" prefix for the endpoint.
	endpoint := srv.Listener.Addr().String()
	store, err := NewIPFSStore(":memory:", IPFSConfig{APIEndpoint: endpoint})
	if err != nil {
		t.Fatalf("NewIPFSStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestIPFSStore_PutGet(t *testing.T) {
	srv := mockIPFSServer(t)
	store := newTestIPFSStore(t, srv)

	entry := &Entry{
		ID:      "ipfs-1",
		AgentID: "agent-0",
		Key:     "knowledge",
		Value:   "spore is a swarm protocol",
	}

	if err := store.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get("knowledge")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected entry, got nil")
	}
	if got.Value != "spore is a swarm protocol" {
		t.Errorf("expected value 'spore is a swarm protocol', got %q", got.Value)
	}
}

func TestIPFSStore_Publish(t *testing.T) {
	srv := mockIPFSServer(t)
	store := newTestIPFSStore(t, srv)

	entry := &Entry{
		ID:      "pub-1",
		AgentID: "agent-0",
		Key:     "shared-fact",
		Value:   "shared knowledge",
	}
	store.SQLiteStore.Put(entry)

	cid, err := store.Publish("shared-fact")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if cid == "" {
		t.Fatal("expected non-empty CID")
	}

	// Publishing again should return the same CID.
	cid2, err := store.Publish("shared-fact")
	if err != nil {
		t.Fatalf("Publish (second): %v", err)
	}
	if cid2 != cid {
		t.Errorf("expected same CID %q, got %q", cid, cid2)
	}
}

func TestIPFSStore_PublishNotFound(t *testing.T) {
	srv := mockIPFSServer(t)
	store := newTestIPFSStore(t, srv)

	_, err := store.Publish("nonexistent")
	if err == nil {
		t.Fatal("expected error publishing nonexistent key")
	}
}

func TestIPFSStore_Fetch(t *testing.T) {
	srv := mockIPFSServer(t)
	store := newTestIPFSStore(t, srv)

	entry := &Entry{
		ID:      "fetch-1",
		AgentID: "agent-0",
		Key:     "fetchable",
		Value:   "fetched from IPFS",
	}
	store.SQLiteStore.Put(entry)

	cid, err := store.Publish("fetchable")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got, err := store.Fetch(cid)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Value != "fetched from IPFS" {
		t.Errorf("expected 'fetched from IPFS', got %q", got.Value)
	}
	if got.CID != cid {
		t.Errorf("expected CID %q, got %q", cid, got.CID)
	}
	if !got.Shared {
		t.Error("expected Shared=true")
	}
}

func TestIPFSStore_Delete(t *testing.T) {
	srv := mockIPFSServer(t)
	store := newTestIPFSStore(t, srv)

	entry := &Entry{
		ID:      "del-1",
		AgentID: "agent-0",
		Key:     "deleteme",
		Value:   "temporary",
	}
	store.Put(entry)

	if err := store.Delete("deleteme"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := store.Get("deleteme")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestIPFSStore_Search(t *testing.T) {
	srv := mockIPFSServer(t)
	store := newTestIPFSStore(t, srv)

	entries := []*Entry{
		{ID: "s1", AgentID: "a", Key: "s-key-1", Value: "Go programming"},
		{ID: "s2", AgentID: "a", Key: "s-key-2", Value: "Rust programming"},
		{ID: "s3", AgentID: "a", Key: "s-key-3", Value: "cats are fun"},
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
}

func TestIPFSStore_GetFallbackToIPFS(t *testing.T) {
	srv := mockIPFSServer(t)
	store := newTestIPFSStore(t, srv)

	entry := &Entry{
		ID:      "fb-1",
		AgentID: "agent-0",
		Key:     "fallback-key",
		Value:   "ipfs only data",
	}

	// Put to get CID mapping, then delete from SQLite only.
	if err := store.Put(entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Delete only from SQLite, keep CID mapping.
	store.SQLiteStore.Delete("fallback-key")

	// Get should fall back to IPFS.
	got, err := store.Get("fallback-key")
	if err != nil {
		t.Fatalf("Get (fallback): %v", err)
	}
	if got == nil {
		t.Fatal("expected entry from IPFS fallback, got nil")
	}
	if got.Value != "ipfs only data" {
		t.Errorf("expected 'ipfs only data', got %q", got.Value)
	}
}
