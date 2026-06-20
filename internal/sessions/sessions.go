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

// Package sessions implements multi-turn conversation memory for the dashboard
// chat UI. A Session binds a series of Turns to a single agent so the agent
// gets prior context replayed back as part of each new task description.
//
// Design rationale:
//   - Runtime adapters (claude-code, codex, builtin, …) deliberately stay
//     dumb about session — they receive a single `description` string per
//     task. The session layer prepends prior turns to that string and the
//     runtime never has to understand chat protocol.
//   - Session storage is its own SQLite database under the swarm baseDir
//     (`sessions.db`). It does NOT share the memory store schema; conflating
//     them would force every memory.Search query to wade through chat noise.
//   - Turns are written from the API layer once a task completes, keyed off
//     the task_id we recorded at submit time. If the agent never completes
//     the task (crash, kill -9), the assistant turn just stays empty —
//     replay is best-effort, not transactional.
package sessions

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

// Role labels for chat turns. Mirrors OpenAI/Anthropic chat conventions.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Session is a chat thread bound to one agent. Title is optional and is
// auto-derived from the first user turn when empty.
type Session struct {
	ID        string    `json:"id"`
	Agent     string    `json:"agent"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	TurnCount int       `json:"turn_count"`
}

// Turn is one step of a Session. TaskID is non-empty for assistant turns and
// links back to the underlying swarm task (so the user can dive from a chat
// reply into the full task log / runtime trace if they want).
type Turn struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	TaskID    string    `json:"task_id,omitempty"`
	Runtime   string    `json:"runtime,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Store is the SQLite-backed session store. Methods are safe for concurrent
// callers; SQLite handles the locking for us, we just guard the in-memory
// task→session map.
type Store struct {
	db *sql.DB

	// taskToSession maps an in-flight task ID to (session_id, user_turn_id)
	// so the API's onTaskUpdate handler can append the assistant turn when
	// the runtime finishes. We intentionally keep this in-memory only —
	// after a process restart, in-flight tasks lose their session anchor,
	// which is fine: completed tasks are still in TaskLog and the chat UI
	// just won't get the answer auto-attached.
	mu            sync.Mutex
	taskToSession map[string]string
}

// New opens or creates a sessions DB at path. Pass ":memory:" or "" for an
// ephemeral DB (tests). Migrations are idempotent.
func New(path string) (*Store, error) {
	if path == "" {
		path = ":memory:"
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("opening sessions db: %w", err)
	}
	// SQLite default is single-writer; setting MaxOpenConns=1 sidesteps
	// "database is locked" under bursty SSE-driven writes.
	db.SetMaxOpenConns(1)
	// FK enforcement is opt-in per-connection in SQLite. Without this,
	// `ON DELETE CASCADE` on session_turns is silently ignored and orphaned
	// turns linger after a session delete.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating sessions db: %w", err)
	}
	return &Store{db: db, taskToSession: make(map[string]string)}, nil
}

// Close releases the DB handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id          TEXT PRIMARY KEY,
			agent       TEXT NOT NULL,
			title       TEXT NOT NULL DEFAULT '',
			created_at  INTEGER NOT NULL,
			updated_at  INTEGER NOT NULL,
			turn_count  INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions(updated_at DESC);

		CREATE TABLE IF NOT EXISTS session_turns (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id  TEXT NOT NULL,
			role        TEXT NOT NULL,
			content     TEXT NOT NULL,
			task_id     TEXT NOT NULL DEFAULT '',
			runtime     TEXT NOT NULL DEFAULT '',
			ts          INTEGER NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_turns_session ON session_turns(session_id, id);
		CREATE INDEX IF NOT EXISTS idx_turns_task ON session_turns(task_id);
	`)
	return err
}

// Create starts a new session bound to the given agent. Title is optional —
// when blank, the first user turn's first 60 chars become the title.
func (s *Store) Create(agent, title string) (*Session, error) {
	if agent == "" {
		return nil, errors.New("agent is required")
	}
	now := time.Now()
	sess := &Session{
		ID:        uuid.New().String()[:8],
		Agent:     agent,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, agent, title, created_at, updated_at, turn_count) VALUES (?, ?, ?, ?, ?, 0)`,
		sess.ID, sess.Agent, sess.Title, now.Unix(), now.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	return sess, nil
}

// Get returns a session by ID, or nil if not found.
func (s *Store) Get(id string) (*Session, error) {
	row := s.db.QueryRow(
		`SELECT id, agent, title, created_at, updated_at, turn_count FROM sessions WHERE id = ?`, id,
	)
	var sess Session
	var created, updated int64
	if err := row.Scan(&sess.ID, &sess.Agent, &sess.Title, &created, &updated, &sess.TurnCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	sess.CreatedAt = time.Unix(created, 0)
	sess.UpdatedAt = time.Unix(updated, 0)
	return &sess, nil
}

// List returns all sessions, newest-updated first. Limit caps the result;
// pass 0 for no cap.
func (s *Store) List(limit int) ([]*Session, error) {
	q := `SELECT id, agent, title, created_at, updated_at, turn_count FROM sessions ORDER BY updated_at DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		var sess Session
		var created, updated int64
		if err := rows.Scan(&sess.ID, &sess.Agent, &sess.Title, &created, &updated, &sess.TurnCount); err != nil {
			return nil, err
		}
		sess.CreatedAt = time.Unix(created, 0)
		sess.UpdatedAt = time.Unix(updated, 0)
		out = append(out, &sess)
	}
	return out, rows.Err()
}

// Delete removes a session and all its turns (CASCADE).
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// Turns returns the conversation history for a session, oldest first.
func (s *Store) Turns(sessionID string) ([]*Turn, error) {
	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, task_id, runtime, ts
		   FROM session_turns WHERE session_id = ? ORDER BY id ASC`, sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Turn
	for rows.Next() {
		var t Turn
		var ts int64
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TaskID, &t.Runtime, &ts); err != nil {
			return nil, err
		}
		t.Timestamp = time.Unix(ts, 0)
		out = append(out, &t)
	}
	return out, rows.Err()
}

// AppendUserTurn records a user message and returns the new turn ID. It also
// auto-promotes the first user message into the session title when title is
// still empty.
func (s *Store) AppendUserTurn(sessionID, content, taskID string) (int64, error) {
	return s.appendTurn(sessionID, RoleUser, content, taskID, "")
}

// AppendAssistantTurn records an agent reply and links it back to the task
// that produced it.
func (s *Store) AppendAssistantTurn(sessionID, content, taskID, runtime string) (int64, error) {
	return s.appendTurn(sessionID, RoleAssistant, content, taskID, runtime)
}

func (s *Store) appendTurn(sessionID, role, content, taskID, runtime string) (int64, error) {
	now := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`INSERT INTO session_turns (session_id, role, content, task_id, runtime, ts) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, role, content, taskID, runtime, now.Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("insert turn: %w", err)
	}
	turnID, _ := res.LastInsertId()

	// Bump turn_count + updated_at, and promote first user turn to title.
	if role == RoleUser {
		// Best-effort title: trim to 60 chars on first user turn.
		title := content
		if len(title) > 60 {
			title = title[:60] + "…"
		}
		_, err = tx.Exec(
			`UPDATE sessions
			    SET turn_count = turn_count + 1,
			        updated_at = ?,
			        title = CASE WHEN title = '' THEN ? ELSE title END
			  WHERE id = ?`,
			now.Unix(), title, sessionID,
		)
	} else {
		_, err = tx.Exec(
			`UPDATE sessions SET turn_count = turn_count + 1, updated_at = ? WHERE id = ?`,
			now.Unix(), sessionID,
		)
	}
	if err != nil {
		return 0, fmt.Errorf("bump session: %w", err)
	}
	return turnID, tx.Commit()
}

// LinkTaskToSession remembers that a task originated from a session, so when
// the task completes we can append the assistant turn under the right
// session. Pure in-memory — see Store struct comment for rationale.
func (s *Store) LinkTaskToSession(taskID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskToSession[taskID] = sessionID
}

// SessionForTask returns the session ID linked to a task, or "" if the task
// wasn't part of a session (or the link has been consumed). Calling this
// removes the link — assistant turns get appended exactly once.
func (s *Store) SessionForTask(taskID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sid := s.taskToSession[taskID]
	if sid != "" {
		delete(s.taskToSession, taskID)
	}
	return sid
}

// FormatHistory renders prior turns into a single string suitable for
// prepending to a new task description. Format is plain "Role: Content"
// blocks separated by blank lines — terse enough to not blow context, but
// unambiguous enough for the runtime/LLM to parse.
//
// maxTurns caps how many recent turns we include (0 = no cap). Older turns
// past the cap are dropped silently — we do NOT summarise them. Summarisation
// belongs in a higher-layer memory consolidation pass.
func FormatHistory(turns []*Turn, maxTurns int) string {
	if len(turns) == 0 {
		return ""
	}
	if maxTurns > 0 && len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}
	out := ""
	for _, t := range turns {
		role := "User"
		if t.Role == RoleAssistant {
			role = "Assistant"
		}
		out += fmt.Sprintf("%s: %s\n\n", role, t.Content)
	}
	return out
}
