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
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore implements Store with SQLite backend.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens or creates a SQLite memory store.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = ":memory:"
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating sqlite: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id         TEXT PRIMARY KEY,
			agent_id   TEXT NOT NULL,
			key        TEXT NOT NULL UNIQUE,
			value      TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			access_cnt INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_memories_key ON memories(key);
		CREATE INDEX IF NOT EXISTS idx_memories_agent ON memories(agent_id);
	`)
	return err
}

func (s *SQLiteStore) Put(entry *Entry) error {
	now := time.Now().Unix()
	if entry.CreatedAt == 0 {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	entry.EnsureID()

	// Use upsert keyed on `key` (the logical dedup field).
	// We query existing id first to avoid PRIMARY KEY conflicts.
	var existingID string
	err := s.db.QueryRow("SELECT id FROM memories WHERE key = ?", entry.Key).Scan(&existingID)
	if err == nil {
		// Key exists — update in place, keep original id
		_, err = s.db.Exec(`
			UPDATE memories SET value = ?, updated_at = ?, access_cnt = access_cnt + 1
			WHERE key = ?
		`, entry.Value, entry.UpdatedAt, entry.Key)
		return err
	}

	// Key does not exist — insert
	_, err = s.db.Exec(`
		INSERT INTO memories (id, agent_id, key, value, created_at, updated_at, access_cnt)
		VALUES (?, ?, ?, ?, ?, ?, 0)
	`, entry.ID, entry.AgentID, entry.Key, entry.Value, entry.CreatedAt, entry.UpdatedAt)
	return err
}

func (s *SQLiteStore) Get(key string) (*Entry, error) {
	row := s.db.QueryRow(`
		SELECT id, agent_id, key, value, created_at, updated_at, access_cnt
		FROM memories WHERE key = ?
	`, key)

	e := &Entry{}
	err := row.Scan(&e.ID, &e.AgentID, &e.Key, &e.Value, &e.CreatedAt, &e.UpdatedAt, &e.AccessCnt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// bump access count
	s.db.Exec("UPDATE memories SET access_cnt = access_cnt + 1 WHERE key = ?", key)
	return e, nil
}

func (s *SQLiteStore) Search(query string, limit int) ([]*Entry, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, agent_id, key, value, created_at, updated_at, access_cnt
		FROM memories WHERE value LIKE ? ORDER BY updated_at DESC LIMIT ?
	`, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []*Entry
	for rows.Next() {
		e := &Entry{}
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Key, &e.Value, &e.CreatedAt, &e.UpdatedAt, &e.AccessCnt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *SQLiteStore) Delete(key string) error {
	_, err := s.db.Exec("DELETE FROM memories WHERE key = ?", key)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
