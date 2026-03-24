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

package network

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ContentDB persists content items to SQLite.
type ContentDB struct {
	db *sql.DB
}

// NewContentDB opens (or creates) a content store database at the given path.
func NewContentDB(dir string) (*ContentDB, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating content dir: %w", err)
	}
	dbPath := filepath.Join(dir, "content.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening content db: %w", err)
	}
	if err := migrateContentDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating content db: %w", err)
	}
	return &ContentDB{db: db}, nil
}

func migrateContentDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS content (
			cid        TEXT PRIMARY KEY,
			data       BLOB NOT NULL,
			agent_id   TEXT NOT NULL DEFAULT '',
			type       TEXT NOT NULL DEFAULT '',
			size       INTEGER NOT NULL DEFAULT 0,
			summary    TEXT NOT NULL DEFAULT '',
			ipfs_cid   TEXT NOT NULL DEFAULT '',
			pinned_at  INTEGER NOT NULL,
			access_cnt INTEGER DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_content_agent ON content(agent_id);
		CREATE INDEX IF NOT EXISTS idx_content_type ON content(type);
	`)
	if err != nil {
		return err
	}
	// Add ipfs_cid column if upgrading from older schema.
	db.Exec("ALTER TABLE content ADD COLUMN ipfs_cid TEXT NOT NULL DEFAULT ''")
	return nil
}

// Put stores a content item.
func (cdb *ContentDB) Put(cid string, data []byte, ref ContentRef) error {
	_, err := cdb.db.Exec(`
		INSERT OR REPLACE INTO content (cid, data, agent_id, type, size, summary, ipfs_cid, pinned_at, access_cnt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)
	`, cid, data, ref.AgentID, ref.Type, ref.Size, ref.Summary, ref.IPFSCID, time.Now().Unix())
	return err
}

// Get retrieves content data by CID.
func (cdb *ContentDB) Get(cid string) ([]byte, *ContentRef, error) {
	var data []byte
	var ref ContentRef
	var pinnedAt int64
	err := cdb.db.QueryRow(`
		SELECT data, agent_id, type, size, summary, ipfs_cid, pinned_at
		FROM content WHERE cid = ?
	`, cid).Scan(&data, &ref.AgentID, &ref.Type, &ref.Size, &ref.Summary, &ref.IPFSCID, &pinnedAt)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	ref.CID = cid
	ref.Timestamp = pinnedAt
	// Bump access
	cdb.db.Exec("UPDATE content SET access_cnt = access_cnt + 1 WHERE cid = ?", cid)
	return data, &ref, nil
}

// Has checks if content exists.
func (cdb *ContentDB) Has(cid string) bool {
	var n int
	cdb.db.QueryRow("SELECT 1 FROM content WHERE cid = ? LIMIT 1", cid).Scan(&n)
	return n == 1
}

// ListRefs returns all stored content references.
func (cdb *ContentDB) ListRefs() []ContentRef {
	rows, err := cdb.db.Query(`
		SELECT cid, agent_id, type, size, summary, ipfs_cid, pinned_at
		FROM content ORDER BY pinned_at DESC
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var refs []ContentRef
	for rows.Next() {
		var r ContentRef
		rows.Scan(&r.CID, &r.AgentID, &r.Type, &r.Size, &r.Summary, &r.IPFSCID, &r.Timestamp)
		refs = append(refs, r)
	}
	return refs
}

// Stats returns db-level statistics.
func (cdb *ContentDB) Stats() (items int, totalBytes int64) {
	cdb.db.QueryRow("SELECT COUNT(*), COALESCE(SUM(size),0) FROM content").Scan(&items, &totalBytes)
	return
}

// Close releases the database.
func (cdb *ContentDB) Close() error {
	if cdb.db != nil {
		return cdb.db.Close()
	}
	return nil
}
