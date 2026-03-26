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
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SkillOrigin describes how a skill was created.
type SkillOrigin string

const (
	SkillOriginImported SkillOrigin = "imported" // declared in agent.yaml
	SkillOriginCaptured SkillOrigin = "captured" // auto-captured from successful task
	SkillOriginDerived  SkillOrigin = "derived"  // enhanced from existing skill
	SkillOriginFixed    SkillOrigin = "fixed"     // repaired broken skill
)

// EvolutionType is the kind of skill evolution action.
type EvolutionType string

const (
	EvolutionFix      EvolutionType = "fix"
	EvolutionDerived  EvolutionType = "derived"
	EvolutionCaptured EvolutionType = "captured"
)

// SkillRecord represents a versioned skill in the store.
type SkillRecord struct {
	SkillID     string      `json:"skill_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	IsActive    bool        `json:"is_active"`
	Origin      SkillOrigin `json:"origin"`

	// Quality metrics
	TotalSelections  int `json:"total_selections"`  // times picked for a task
	TotalApplied     int `json:"total_applied"`     // times actually used
	TotalCompletions int `json:"total_completions"` // successful completions
	TotalFallbacks   int `json:"total_fallbacks"`   // fallback/degraded uses

	// Lineage
	Generation    int      `json:"generation"`      // distance from root in version DAG
	ParentIDs     []string `json:"parent_ids"`      // parent skill IDs
	SourceTaskID  string   `json:"source_task_id"`  // task that triggered creation
	ChangeSummary string   `json:"change_summary"`  // what changed vs parent

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SkillRecord quality helpers.

// SuccessRate returns completions / applied (0 if no data).
func (s *SkillRecord) SuccessRate() float64 {
	if s.TotalApplied == 0 {
		return 0
	}
	return float64(s.TotalCompletions) / float64(s.TotalApplied)
}

// FallbackRate returns fallbacks / selections (0 if no data).
func (s *SkillRecord) FallbackRate() float64 {
	if s.TotalSelections == 0 {
		return 0
	}
	return float64(s.TotalFallbacks) / float64(s.TotalSelections)
}

// ExecutionAnalysisResult is the LLM-produced analysis of a task execution.
type ExecutionAnalysisResult struct {
	TaskID    string `json:"task_id"`
	AgentID   string `json:"agent_id"`
	Timestamp time.Time `json:"timestamp"`

	// Judgment
	Success       bool    `json:"success"`
	Quality       float64 `json:"quality"`        // 0.0 - 1.0
	Efficiency    float64 `json:"efficiency"`      // 0.0 - 1.0
	QualityReason string  `json:"quality_reason"`

	// Skill involvement
	SkillsUsed    []string `json:"skills_used"`
	SkillsNeeded  []string `json:"skills_needed"`  // skills that would have helped

	// Evolution suggestions
	Suggestions []EvolutionSuggestion `json:"suggestions"`
}

// EvolutionSuggestion is a concrete evolution action proposed by the analyzer.
type EvolutionSuggestion struct {
	Type        EvolutionType `json:"type"`
	SkillName   string        `json:"skill_name"`   // target skill (existing or new)
	Reason      string        `json:"reason"`
	Description string        `json:"description"`  // what the evolved skill should do
	Priority    float64       `json:"priority"`      // 0.0 - 1.0
}

// ─── SkillStore ─────────────────────────────────────────────────────────────

// SkillStore persists SkillRecords and ExecutionAnalyses in SQLite.
type SkillStore struct {
	mu sync.RWMutex
	db *sql.DB
}

const skillDDL = `
CREATE TABLE IF NOT EXISTS skill_records (
    skill_id           TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    is_active          INTEGER NOT NULL DEFAULT 1,
    origin             TEXT NOT NULL DEFAULT 'imported',
    total_selections   INTEGER NOT NULL DEFAULT 0,
    total_applied      INTEGER NOT NULL DEFAULT 0,
    total_completions  INTEGER NOT NULL DEFAULT 0,
    total_fallbacks    INTEGER NOT NULL DEFAULT 0,
    generation         INTEGER NOT NULL DEFAULT 0,
    parent_ids         TEXT NOT NULL DEFAULT '[]',
    source_task_id     TEXT NOT NULL DEFAULT '',
    change_summary     TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_skill_name ON skill_records(name);
CREATE INDEX IF NOT EXISTS idx_skill_active ON skill_records(is_active);

CREATE TABLE IF NOT EXISTS execution_analyses (
    task_id       TEXT PRIMARY KEY,
    agent_id      TEXT NOT NULL,
    success       INTEGER NOT NULL DEFAULT 0,
    quality       REAL NOT NULL DEFAULT 0,
    efficiency    REAL NOT NULL DEFAULT 0,
    quality_reason TEXT NOT NULL DEFAULT '',
    skills_used   TEXT NOT NULL DEFAULT '[]',
    skills_needed TEXT NOT NULL DEFAULT '[]',
    suggestions   TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_analysis_agent ON execution_analyses(agent_id);
`

// NewSkillStore opens (or creates) the skill database at the given directory.
func NewSkillStore(dir string) (*SkillStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	dbPath := filepath.Join(dir, "skills.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal=WAL&_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open skills db: %w", err)
	}
	if _, err := db.Exec(skillDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init skills schema: %w", err)
	}
	return &SkillStore{db: db}, nil
}

// Close closes the database.
func (s *SkillStore) Close() error {
	return s.db.Close()
}

// PutSkill upserts a skill record.
func (s *SkillStore) PutSkill(rec *SkillRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	parentJSON, _ := json.Marshal(rec.ParentIDs)
	now := time.Now().UTC().Format(time.RFC3339)
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}

	_, err := s.db.Exec(`
		INSERT INTO skill_records (
			skill_id, name, description, is_active, origin,
			total_selections, total_applied, total_completions, total_fallbacks,
			generation, parent_ids, source_task_id, change_summary,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(skill_id) DO UPDATE SET
			name=excluded.name, description=excluded.description,
			is_active=excluded.is_active,
			total_selections=excluded.total_selections,
			total_applied=excluded.total_applied,
			total_completions=excluded.total_completions,
			total_fallbacks=excluded.total_fallbacks,
			change_summary=excluded.change_summary,
			updated_at=excluded.updated_at`,
		rec.SkillID, rec.Name, rec.Description,
		boolToInt(rec.IsActive), string(rec.Origin),
		rec.TotalSelections, rec.TotalApplied, rec.TotalCompletions, rec.TotalFallbacks,
		rec.Generation, string(parentJSON), rec.SourceTaskID, rec.ChangeSummary,
		rec.CreatedAt.UTC().Format(time.RFC3339), now,
	)
	return err
}

// GetSkill returns a skill by ID, or nil if not found.
func (s *SkillStore) GetSkill(skillID string) (*SkillRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := s.db.QueryRow(`SELECT
		skill_id, name, description, is_active, origin,
		total_selections, total_applied, total_completions, total_fallbacks,
		generation, parent_ids, source_task_id, change_summary,
		created_at, updated_at
		FROM skill_records WHERE skill_id = ?`, skillID)

	return scanSkillRow(row)
}

// ActiveSkills returns all active skills.
func (s *SkillStore) ActiveSkills() ([]*SkillRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT
		skill_id, name, description, is_active, origin,
		total_selections, total_applied, total_completions, total_fallbacks,
		generation, parent_ids, source_task_id, change_summary,
		created_at, updated_at
		FROM skill_records WHERE is_active = 1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*SkillRecord
	for rows.Next() {
		rec, err := scanSkillRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

// IncrementSelection bumps the selection counter for a skill.
func (s *SkillStore) IncrementSelection(skillID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE skill_records SET total_selections = total_selections + 1, updated_at = ? WHERE skill_id = ?`,
		time.Now().UTC().Format(time.RFC3339), skillID)
	return err
}

// IncrementApplied bumps the applied counter.
func (s *SkillStore) IncrementApplied(skillID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE skill_records SET total_applied = total_applied + 1, updated_at = ? WHERE skill_id = ?`,
		time.Now().UTC().Format(time.RFC3339), skillID)
	return err
}

// IncrementCompletion bumps the successful completion counter.
func (s *SkillStore) IncrementCompletion(skillID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE skill_records SET total_completions = total_completions + 1, updated_at = ? WHERE skill_id = ?`,
		time.Now().UTC().Format(time.RFC3339), skillID)
	return err
}

// IncrementFallback bumps the fallback counter.
func (s *SkillStore) IncrementFallback(skillID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE skill_records SET total_fallbacks = total_fallbacks + 1, updated_at = ? WHERE skill_id = ?`,
		time.Now().UTC().Format(time.RFC3339), skillID)
	return err
}

// PutAnalysis persists an execution analysis.
func (s *SkillStore) PutAnalysis(a *ExecutionAnalysisResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	usedJSON, _ := json.Marshal(a.SkillsUsed)
	neededJSON, _ := json.Marshal(a.SkillsNeeded)
	sugJSON, _ := json.Marshal(a.Suggestions)

	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO execution_analyses (
			task_id, agent_id, success, quality, efficiency,
			quality_reason, skills_used, skills_needed, suggestions, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.TaskID, a.AgentID, boolToInt(a.Success), a.Quality, a.Efficiency,
		a.QualityReason, string(usedJSON), string(neededJSON), string(sugJSON),
		a.Timestamp.UTC().Format(time.RFC3339),
	)
	return err
}

// RecentAnalyses returns the most recent N analyses for an agent.
func (s *SkillStore) RecentAnalyses(agentID string, limit int) ([]*ExecutionAnalysisResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT
		task_id, agent_id, success, quality, efficiency,
		quality_reason, skills_used, skills_needed, suggestions, created_at
		FROM execution_analyses WHERE agent_id = ?
		ORDER BY created_at DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*ExecutionAnalysisResult
	for rows.Next() {
		a := &ExecutionAnalysisResult{}
		var successInt int
		var usedJSON, neededJSON, sugJSON, createdStr string
		err := rows.Scan(
			&a.TaskID, &a.AgentID, &successInt, &a.Quality, &a.Efficiency,
			&a.QualityReason, &usedJSON, &neededJSON, &sugJSON, &createdStr,
		)
		if err != nil {
			return nil, err
		}
		a.Success = successInt != 0
		json.Unmarshal([]byte(usedJSON), &a.SkillsUsed)
		json.Unmarshal([]byte(neededJSON), &a.SkillsNeeded)
		json.Unmarshal([]byte(sugJSON), &a.Suggestions)
		a.Timestamp, _ = time.Parse(time.RFC3339, createdStr)
		result = append(result, a)
	}
	return result, rows.Err()
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type scannable interface {
	Scan(dest ...any) error
}

func scanSkillRecord(s scannable) (*SkillRecord, error) {
	rec := &SkillRecord{}
	var isActive int
	var origin, parentJSON, createdStr, updatedStr string
	err := s.Scan(
		&rec.SkillID, &rec.Name, &rec.Description, &isActive, &origin,
		&rec.TotalSelections, &rec.TotalApplied, &rec.TotalCompletions, &rec.TotalFallbacks,
		&rec.Generation, &parentJSON, &rec.SourceTaskID, &rec.ChangeSummary,
		&createdStr, &updatedStr,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.IsActive = isActive != 0
	rec.Origin = SkillOrigin(origin)
	json.Unmarshal([]byte(parentJSON), &rec.ParentIDs)
	rec.CreatedAt, _ = time.Parse(time.RFC3339, createdStr)
	rec.UpdatedAt, _ = time.Parse(time.RFC3339, updatedStr)
	return rec, nil
}

func scanSkillRow(row *sql.Row) (*SkillRecord, error) {
	return scanSkillRecord(row)
}

func scanSkillRows(rows *sql.Rows) (*SkillRecord, error) {
	return scanSkillRecord(rows)
}

// SkillStats is a summary for API/dashboard consumption.
type SkillStats struct {
	TotalSkills   int     `json:"total_skills"`
	ActiveSkills  int     `json:"active_skills"`
	AvgSuccess    float64 `json:"avg_success_rate"`
	TotalAnalyses int     `json:"total_analyses"`
	Evolved       int     `json:"evolved"` // skills with generation > 0
}

// Stats returns summary skill statistics.
func (s *SkillStore) Stats(agentID string) *SkillStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &SkillStats{}

	s.db.QueryRow(`SELECT COUNT(*) FROM skill_records`).Scan(&stats.TotalSkills)
	s.db.QueryRow(`SELECT COUNT(*) FROM skill_records WHERE is_active = 1`).Scan(&stats.ActiveSkills)
	s.db.QueryRow(`SELECT COUNT(*) FROM skill_records WHERE generation > 0`).Scan(&stats.Evolved)
	s.db.QueryRow(`SELECT COUNT(*) FROM execution_analyses WHERE agent_id = ?`, agentID).Scan(&stats.TotalAnalyses)

	// Average success rate across active skills with data
	var totalRate float64
	var count int
	rows, err := s.db.Query(`SELECT total_completions, total_applied FROM skill_records WHERE is_active = 1 AND total_applied > 0`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var completions, applied int
			rows.Scan(&completions, &applied)
			totalRate += float64(completions) / float64(applied)
			count++
		}
	}
	if count > 0 {
		stats.AvgSuccess = totalRate / float64(count)
	}

	return stats
}
