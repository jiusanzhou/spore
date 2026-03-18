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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// IPFSConfig holds configuration for the IPFS-backed store.
type IPFSConfig struct {
	APIEndpoint string // host:port, default "localhost:5001"
	PinRemote   bool   // whether to pin on remote pinning services
}

// IPFSStore implements Store using SQLite as a hot cache and the
// IPFS HTTP API for distributed content-addressed storage.
type IPFSStore struct {
	*SQLiteStore
	ipfsAPI   string // full URL like "http://localhost:5001"
	pinRemote bool
}

// NewIPFSStore creates an IPFSStore backed by SQLite at sqlitePath.
func NewIPFSStore(sqlitePath string, cfg IPFSConfig) (*IPFSStore, error) {
	sqlite, err := NewSQLiteStore(sqlitePath)
	if err != nil {
		return nil, fmt.Errorf("ipfs store: %w", err)
	}

	// Additional migration for CID tracking.
	_, err = sqlite.db.Exec(`
		CREATE TABLE IF NOT EXISTS memory_cids (
			key       TEXT PRIMARY KEY,
			cid       TEXT,
			pinned_at INTEGER
		);
	`)
	if err != nil {
		sqlite.Close()
		return nil, fmt.Errorf("ipfs store migration: %w", err)
	}

	endpoint := cfg.APIEndpoint
	if endpoint == "" {
		endpoint = "localhost:5001"
	}

	return &IPFSStore{
		SQLiteStore: sqlite,
		ipfsAPI:     "http://" + endpoint,
		pinRemote:   cfg.PinRemote,
	}, nil
}

// Put stores the entry in SQLite and publishes it to IPFS.
func (s *IPFSStore) Put(entry *Entry) error {
	if err := s.SQLiteStore.Put(entry); err != nil {
		return err
	}

	cid, err := s.addToIPFS(entry)
	if err != nil {
		// Local write succeeded; log IPFS failure but don't fail the Put.
		return nil
	}

	entry.CID = cid
	entry.Shared = true

	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO memory_cids (key, cid, pinned_at)
		VALUES (?, ?, ?)
	`, entry.Key, cid, time.Now().Unix())
	return err
}

// Get retrieves an entry by key, falling back to IPFS if not cached locally.
func (s *IPFSStore) Get(key string) (*Entry, error) {
	entry, err := s.SQLiteStore.Get(key)
	if err != nil {
		return nil, err
	}
	if entry != nil {
		return entry, nil
	}

	// Cache miss — look up CID and fetch from IPFS.
	var cid string
	row := s.db.QueryRow("SELECT cid FROM memory_cids WHERE key = ?", key)
	if err := row.Scan(&cid); err != nil {
		return nil, nil // not found anywhere
	}

	entry, err = s.Fetch(cid)
	if err != nil {
		return nil, fmt.Errorf("ipfs fetch %s: %w", cid, err)
	}

	// Write back to SQLite cache.
	_ = s.SQLiteStore.Put(entry)
	return entry, nil
}

// Search delegates to SQLiteStore; IPFS has no search capability.
func (s *IPFSStore) Search(query string, limit int) ([]*Entry, error) {
	return s.SQLiteStore.Search(query, limit)
}

// Delete removes an entry from SQLite, memory_cids, and unpins from IPFS.
func (s *IPFSStore) Delete(key string) error {
	// Look up CID before deleting.
	var cid string
	row := s.db.QueryRow("SELECT cid FROM memory_cids WHERE key = ?", key)
	_ = row.Scan(&cid)

	if err := s.SQLiteStore.Delete(key); err != nil {
		return err
	}

	s.db.Exec("DELETE FROM memory_cids WHERE key = ?", key)

	if cid != "" {
		// Best-effort unpin; ignore errors.
		s.ipfsPost("/api/v0/pin/rm?arg="+cid, nil)
	}
	return nil
}

// Close releases all resources.
func (s *IPFSStore) Close() error {
	return s.SQLiteStore.Close()
}

// Publish adds an existing entry to IPFS and returns its CID.
func (s *IPFSStore) Publish(key string) (string, error) {
	// Check if already published.
	var existing string
	row := s.db.QueryRow("SELECT cid FROM memory_cids WHERE key = ?", key)
	if err := row.Scan(&existing); err == nil && existing != "" {
		return existing, nil
	}

	entry, err := s.SQLiteStore.Get(key)
	if err != nil {
		return "", err
	}
	if entry == nil {
		return "", fmt.Errorf("key not found: %s", key)
	}

	cid, err := s.addToIPFS(entry)
	if err != nil {
		return "", fmt.Errorf("ipfs add: %w", err)
	}

	entry.CID = cid
	entry.Shared = true

	_, err = s.db.Exec(`
		INSERT OR REPLACE INTO memory_cids (key, cid, pinned_at)
		VALUES (?, ?, ?)
	`, key, cid, time.Now().Unix())
	if err != nil {
		return cid, err
	}
	return cid, nil
}

// Fetch retrieves an entry from IPFS by its CID.
func (s *IPFSStore) Fetch(cid string) (*Entry, error) {
	body, err := s.ipfsPost("/api/v0/cat?arg="+cid, nil)
	if err != nil {
		return nil, fmt.Errorf("ipfs cat: %w", err)
	}

	var entry Entry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("decoding entry from IPFS: %w", err)
	}
	entry.CID = cid
	entry.Shared = true
	return &entry, nil
}

// addToIPFS serializes the entry as JSON and adds it via the IPFS HTTP API.
// Returns the content identifier (CID) on success.
func (s *IPFSStore) addToIPFS(entry *Entry) (string, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return "", fmt.Errorf("marshaling entry: %w", err)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "entry.json")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	w.Close()

	req, err := http.NewRequest("POST", s.ipfsAPI+"/api/v0/add", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ipfs add request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ipfs add: status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Hash string `json:"Hash"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding ipfs add response: %w", err)
	}
	return result.Hash, nil
}

// ipfsPost sends a POST to the IPFS HTTP API and returns the response body.
func (s *IPFSStore) ipfsPost(path string, body io.Reader) ([]byte, error) {
	resp, err := http.Post(s.ipfsAPI+path, "", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ipfs %s: status %d: %s", path, resp.StatusCode, data)
	}
	return data, nil
}
