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
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// migrateContext adds the context table to an existing SQLite store.
func migrateContext(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS context (
			uri        TEXT PRIMARY KEY,
			agent_id   TEXT NOT NULL,
			type       TEXT NOT NULL,
			category   TEXT NOT NULL DEFAULT '',
			l0         TEXT NOT NULL DEFAULT '',
			l1         TEXT NOT NULL DEFAULT '',
			l2         TEXT NOT NULL DEFAULT '',
			tags       TEXT DEFAULT '[]',
			metadata   TEXT DEFAULT '{}',
			source     TEXT DEFAULT '',
			cid        TEXT DEFAULT '',
			shared     INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			access_cnt INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_context_agent ON context(agent_id);
		CREATE INDEX IF NOT EXISTS idx_context_type ON context(type);
		CREATE INDEX IF NOT EXISTS idx_context_category ON context(category);
		CREATE INDEX IF NOT EXISTS idx_context_agent_cat ON context(agent_id, category);
	`)
	return err
}

// PutContext stores a structured context entry.
func (s *SQLiteStore) PutContext(entry *ContextEntry) error {
	now := time.Now()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now

	if entry.URI == "" {
		entry.URI = entry.BuildURI()
	}

	tagsJSON, _ := json.Marshal(entry.Tags)
	metaJSON, _ := json.Marshal(entry.Metadata)

	shared := 0
	if entry.Shared {
		shared = 1
	}

	// Upsert
	var existing string
	err := s.db.QueryRow("SELECT uri FROM context WHERE uri = ?", entry.URI).Scan(&existing)
	if err == nil {
		// Update existing
		_, err = s.db.Exec(`
			UPDATE context SET
				l0 = ?, l1 = ?, l2 = ?,
				tags = ?, metadata = ?, source = ?,
				cid = ?, shared = ?,
				updated_at = ?, access_cnt = access_cnt + 1
			WHERE uri = ?
		`, entry.L0, entry.L1, entry.L2,
			string(tagsJSON), string(metaJSON), entry.Source,
			entry.CID, shared,
			entry.UpdatedAt.Unix(), entry.URI)
		return err
	}

	// Insert new
	_, err = s.db.Exec(`
		INSERT INTO context (uri, agent_id, type, category, l0, l1, l2,
			tags, metadata, source, cid, shared, created_at, updated_at, access_cnt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
	`, entry.URI, entry.AgentID, string(entry.Type), string(entry.Category),
		entry.L0, entry.L1, entry.L2,
		string(tagsJSON), string(metaJSON), entry.Source,
		entry.CID, shared,
		entry.CreatedAt.Unix(), entry.UpdatedAt.Unix())
	return err
}

// GetContext retrieves a context entry by URI.
func (s *SQLiteStore) GetContext(uri string) (*ContextEntry, error) {
	row := s.db.QueryRow(`
		SELECT uri, agent_id, type, category, l0, l1, l2,
			tags, metadata, source, cid, shared, created_at, updated_at, access_cnt
		FROM context WHERE uri = ?
	`, uri)
	return scanContextRow(row, s.db, uri)
}

// ListByCategory lists entries of a specific category.
func (s *SQLiteStore) ListByCategory(agentID string, category MemoryCategory, limit int) ([]*ContextEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT uri, agent_id, type, category, l0, l1, l2,
			tags, metadata, source, cid, shared, created_at, updated_at, access_cnt
		FROM context WHERE agent_id = ? AND category = ?
		ORDER BY updated_at DESC LIMIT ?
	`, agentID, string(category), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanContextRows(rows)
}

// ListByType lists entries of a specific context type.
func (s *SQLiteStore) ListByType(agentID string, ctxType ContextType, limit int) ([]*ContextEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT uri, agent_id, type, category, l0, l1, l2,
			tags, metadata, source, cid, shared, created_at, updated_at, access_cnt
		FROM context WHERE agent_id = ? AND type = ?
		ORDER BY updated_at DESC LIMIT ?
	`, agentID, string(ctxType), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanContextRows(rows)
}

// SearchContext searches across context entries with optional filters.
func (s *SQLiteStore) SearchContext(query string, ctxType ContextType, category MemoryCategory, limit int) ([]*ContextEntry, error) {
	if limit <= 0 {
		limit = 20
	}

	var conditions []string
	var args []interface{}

	// Text search across L0, L1, L2
	conditions = append(conditions, "(l0 LIKE ? OR l1 LIKE ? OR l2 LIKE ?)")
	q := "%" + query + "%"
	args = append(args, q, q, q)

	if ctxType != "" {
		conditions = append(conditions, "type = ?")
		args = append(args, string(ctxType))
	}
	if category != "" {
		conditions = append(conditions, "category = ?")
		args = append(args, string(category))
	}

	where := strings.Join(conditions, " AND ")
	args = append(args, limit)

	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT uri, agent_id, type, category, l0, l1, l2,
			tags, metadata, source, cid, shared, created_at, updated_at, access_cnt
		FROM context WHERE %s
		ORDER BY updated_at DESC LIMIT ?
	`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanContextRows(rows)
}

// ContextStats returns count of entries per category for an agent.
func (s *SQLiteStore) ContextStats(agentID string) map[MemoryCategory]int {
	stats := make(map[MemoryCategory]int)
	rows, err := s.db.Query(`
		SELECT category, COUNT(*) FROM context
		WHERE agent_id = ? GROUP BY category
	`, agentID)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var cat string
		var count int
		if rows.Scan(&cat, &count) == nil {
			stats[MemoryCategory(cat)] = count
		}
	}
	return stats
}

// scanContextRow scans a single row into a ContextEntry.
func scanContextRow(row *sql.Row, db *sql.DB, uri string) (*ContextEntry, error) {
	e := &ContextEntry{}
	var tagsJSON, metaJSON string
	var ctxType, category string
	var shared int
	var createdAt, updatedAt int64

	err := row.Scan(&e.URI, &e.AgentID, &ctxType, &category,
		&e.L0, &e.L1, &e.L2,
		&tagsJSON, &metaJSON, &e.Source,
		&e.CID, &shared, &createdAt, &updatedAt, &e.AccessCnt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	e.Type = ContextType(ctxType)
	e.Category = MemoryCategory(category)
	e.Shared = shared != 0
	e.CreatedAt = time.Unix(createdAt, 0)
	e.UpdatedAt = time.Unix(updatedAt, 0)
	json.Unmarshal([]byte(tagsJSON), &e.Tags)
	json.Unmarshal([]byte(metaJSON), &e.Metadata)

	// Bump access count
	if db != nil {
		db.Exec("UPDATE context SET access_cnt = access_cnt + 1 WHERE uri = ?", uri)
	}
	return e, nil
}

// scanContextRows scans multiple rows.
func scanContextRows(rows *sql.Rows) ([]*ContextEntry, error) {
	var entries []*ContextEntry
	for rows.Next() {
		e := &ContextEntry{}
		var tagsJSON, metaJSON string
		var ctxType, category string
		var shared int
		var createdAt, updatedAt int64

		if err := rows.Scan(&e.URI, &e.AgentID, &ctxType, &category,
			&e.L0, &e.L1, &e.L2,
			&tagsJSON, &metaJSON, &e.Source,
			&e.CID, &shared, &createdAt, &updatedAt, &e.AccessCnt); err != nil {
			return nil, err
		}

		e.Type = ContextType(ctxType)
		e.Category = MemoryCategory(category)
		e.Shared = shared != 0
		e.CreatedAt = time.Unix(createdAt, 0)
		e.UpdatedAt = time.Unix(updatedAt, 0)
		json.Unmarshal([]byte(tagsJSON), &e.Tags)
		json.Unmarshal([]byte(metaJSON), &e.Metadata)

		entries = append(entries, e)
	}
	return entries, rows.Err()
}
