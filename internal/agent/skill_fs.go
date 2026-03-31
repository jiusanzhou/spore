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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v3"
)

// marshalJSON is a convenience wrapper.
func marshalJSON(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	return string(data), err
}

// unmarshalJSON is a convenience wrapper.
func unmarshalJSON(data string, v interface{}) error {
	return json.Unmarshal([]byte(data), v)
}

// truncateCID safely shortens a CID string for display.
func truncateCID(s string) string {
	if len(s) <= 16 {
		return s
	}
	return s[:16]
}

// ────────────────────────────────────────────────────────────────────────────
// SkillFS — File-system-first skill store with IPFS content addressing
//
// Directory layout (per agent):
//
//   <workDir>/skills/
//     <skill-name>/
//       SKILL.md         # source of truth — YAML frontmatter + markdown body
//       references/      # optional reference docs
//       scripts/         # optional helper scripts
//       assets/          # optional supplementary files
//     index.yaml         # auto-generated skill catalog
//
// SQLite (skills_index.db) is an index-only sidecar:
//   - usage metrics (selections, applied, completions, fallbacks)
//   - IPFS CIDs per skill version
//   - analysis results
//
// IPFS integration:
//   - Every SKILL.md write → content hash → publish to ContentStore
//   - CID stored in frontmatter `x-ipfs-cid` and in SQLite index
//   - Remote skills fetched by CID, verified by hash
// ────────────────────────────────────────────────────────────────────────────

// SkillMeta is the YAML frontmatter of a SKILL.md file.
type SkillMeta struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Version     string   `yaml:"version,omitempty"`
	Category    string   `yaml:"category,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Origin      string   `yaml:"origin,omitempty"`       // imported | captured | derived | fixed
	Generation  int      `yaml:"generation,omitempty"`    // lineage depth
	ParentIDs   []string `yaml:"parent_ids,omitempty"`    // parent skill IDs
	SourceTask  string   `yaml:"source_task,omitempty"`   // task that triggered creation
	Triggers    []string `yaml:"triggers,omitempty"`      // activation conditions
	Priority    int      `yaml:"priority,omitempty"`      // 0-100
	Dependencies []string `yaml:"dependencies,omitempty"` // required peer skills

	// Executable tools — agent-created tools from evolution
	Tools []SkillToolMeta `yaml:"tools,omitempty"`

	// Content addressing
	ContentHash string `yaml:"x-content-hash,omitempty"` // SHA-256 of body
	IPFSCID     string `yaml:"x-ipfs-cid,omitempty"`     // IPFS CIDv1

	// Timestamps
	CreatedAt string `yaml:"created_at,omitempty"`
	UpdatedAt string `yaml:"updated_at,omitempty"`
}

// SkillToolMeta defines an executable tool in a SKILL.md frontmatter.
type SkillToolMeta struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Command     string `yaml:"command"`               // shell template, {{input}} replaced
	Timeout     string `yaml:"timeout,omitempty"`      // e.g. "10s"
	Interpreter string `yaml:"interpreter,omitempty"`  // "sh", "bash", "python3"
}

// Skill is a loaded skill with metadata + body.
type Skill struct {
	Meta SkillMeta
	Body string // markdown content (everything after frontmatter)
	Dir  string // absolute path to skill directory
}

// SkillFS is the file-system-first skill store.
type SkillFS struct {
	mu       sync.RWMutex
	skillDir string          // <workDir>/skills/
	indexDB  *sql.DB         // skills_index.db (metrics only)
	publish  PublishFunc     // optional: publish to IPFS ContentStore
	skills   map[string]*Skill // name → loaded skill (hot cache)
}

// PublishFunc publishes raw bytes to the content-addressed store.
// Returns (CID string, error).
type PublishFunc func(data []byte, contentType, agentID, summary string) (string, error)

const skillIndexDDL = `
CREATE TABLE IF NOT EXISTS skill_metrics (
    name              TEXT PRIMARY KEY,
    total_selections  INTEGER NOT NULL DEFAULT 0,
    total_applied     INTEGER NOT NULL DEFAULT 0,
    total_completions INTEGER NOT NULL DEFAULT 0,
    total_fallbacks   INTEGER NOT NULL DEFAULT 0,
    last_used_at      TEXT NOT NULL DEFAULT '',
    ipfs_cid          TEXT NOT NULL DEFAULT '',
    content_hash      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS skill_versions (
    name         TEXT NOT NULL,
    version      INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    ipfs_cid     TEXT NOT NULL DEFAULT '',
    change       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    PRIMARY KEY (name, version)
);

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

// NewSkillFS creates a file-system skill store.
// skillDir is typically <workDir>/skills/.
// publish is optional — if non-nil, skills are published to IPFS on write.
func NewSkillFS(skillDir string, publish PublishFunc) (*SkillFS, error) {
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", skillDir, err)
	}

	dbPath := filepath.Join(skillDir, "skills_index.db")
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open index db: %w", err)
	}
	if _, err := db.Exec(skillIndexDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init index schema: %w", err)
	}

	fs := &SkillFS{
		skillDir: skillDir,
		indexDB:  db,
		publish:  publish,
		skills:   make(map[string]*Skill),
	}

	// Scan existing skills from disk
	if err := fs.scanDisk(); err != nil {
		fmt.Printf("⚠️  SkillFS: scan error: %v\n", err)
	}

	return fs, nil
}

// Close releases database resources.
func (fs *SkillFS) Close() error {
	if fs.indexDB != nil {
		return fs.indexDB.Close()
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Read operations
// ────────────────────────────────────────────────────────────────────────────

// List returns all loaded skill names, sorted.
func (fs *SkillFS) List() []string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	names := make([]string, 0, len(fs.skills))
	for name := range fs.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns a skill by name.
func (fs *SkillFS) Get(name string) (*Skill, bool) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	s, ok := fs.skills[name]
	return s, ok
}

// All returns all loaded skills.
func (fs *SkillFS) All() []*Skill {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	out := make([]*Skill, 0, len(fs.skills))
	for _, s := range fs.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Meta.Name < out[j].Meta.Name })
	return out
}

// Metrics returns usage metrics for a skill from SQLite.
type SkillMetrics struct {
	Name            string `json:"name"`
	TotalSelections int    `json:"total_selections"`
	TotalApplied    int    `json:"total_applied"`
	TotalCompletions int   `json:"total_completions"`
	TotalFallbacks  int    `json:"total_fallbacks"`
	LastUsedAt      string `json:"last_used_at,omitempty"`
	IPFSCID         string `json:"ipfs_cid,omitempty"`
	ContentHash     string `json:"content_hash,omitempty"`
}

func (fs *SkillFS) Metrics(name string) *SkillMetrics {
	m := &SkillMetrics{Name: name}
	if fs.indexDB == nil {
		return m
	}
	fs.indexDB.QueryRow(`SELECT total_selections, total_applied, total_completions, total_fallbacks,
		last_used_at, ipfs_cid, content_hash FROM skill_metrics WHERE name = ?`, name).Scan(
		&m.TotalSelections, &m.TotalApplied, &m.TotalCompletions, &m.TotalFallbacks,
		&m.LastUsedAt, &m.IPFSCID, &m.ContentHash,
	)
	return m
}

// AllMetrics returns metrics for all skills.
func (fs *SkillFS) AllMetrics() []*SkillMetrics {
	if fs.indexDB == nil {
		return nil
	}
	rows, err := fs.indexDB.Query(`SELECT name, total_selections, total_applied, total_completions,
		total_fallbacks, last_used_at, ipfs_cid, content_hash FROM skill_metrics ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []*SkillMetrics
	for rows.Next() {
		m := &SkillMetrics{}
		rows.Scan(&m.Name, &m.TotalSelections, &m.TotalApplied, &m.TotalCompletions,
			&m.TotalFallbacks, &m.LastUsedAt, &m.IPFSCID, &m.ContentHash)
		out = append(out, m)
	}
	return out
}

// ────────────────────────────────────────────────────────────────────────────
// Write operations
// ────────────────────────────────────────────────────────────────────────────

// Create writes a new SKILL.md. Returns error if skill already exists.
func (fs *SkillFS) Create(meta SkillMeta, body string) (*Skill, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, exists := fs.skills[meta.Name]; exists {
		return nil, fmt.Errorf("skill %q already exists", meta.Name)
	}

	return fs.writeSkillLocked(meta, body, "initial creation")
}

// Update replaces a skill's SKILL.md. Creates a new version entry.
func (fs *SkillFS) Update(name string, meta SkillMeta, body string, changeSummary string) (*Skill, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if _, exists := fs.skills[name]; !exists {
		return nil, fmt.Errorf("skill %q not found", name)
	}

	meta.Name = name // ensure name consistency
	return fs.writeSkillLocked(meta, body, changeSummary)
}

// Patch applies a targeted string replacement to a skill body.
// More token-efficient than full replacement.
func (fs *SkillFS) Patch(name, oldText, newText string) (*Skill, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	existing, ok := fs.skills[name]
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}

	if !strings.Contains(existing.Body, oldText) {
		return nil, fmt.Errorf("patch target not found in %q body", name)
	}

	newBody := strings.Replace(existing.Body, oldText, newText, 1)
	changeSummary := fmt.Sprintf("patch: replaced %d chars", len(oldText))
	return fs.writeSkillLocked(existing.Meta, newBody, changeSummary)
}

// Delete removes a skill directory.
func (fs *SkillFS) Delete(name string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	dir := filepath.Join(fs.skillDir, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing skill dir: %w", err)
	}
	delete(fs.skills, name)

	// Don't delete metrics — keep historical data
	return nil
}

// WriteFile writes a supporting file to a skill directory.
func (fs *SkillFS) WriteFile(skillName, relPath string, content []byte) error {
	fs.mu.RLock()
	_, ok := fs.skills[skillName]
	fs.mu.RUnlock()
	if !ok {
		return fmt.Errorf("skill %q not found", skillName)
	}

	absPath := filepath.Join(fs.skillDir, skillName, relPath)
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(absPath, content, 0644)
}

// RemoveFile removes a supporting file from a skill directory.
func (fs *SkillFS) RemoveFile(skillName, relPath string) error {
	absPath := filepath.Join(fs.skillDir, skillName, relPath)
	return os.Remove(absPath)
}

// ────────────────────────────────────────────────────────────────────────────
// Metrics tracking
// ────────────────────────────────────────────────────────────────────────────

func (fs *SkillFS) IncrSelection(name string) {
	fs.incrMetric(name, "total_selections")
}

func (fs *SkillFS) IncrApplied(name string) {
	fs.incrMetric(name, "total_applied")
}

func (fs *SkillFS) IncrCompletion(name string) {
	fs.incrMetric(name, "total_completions")
}

func (fs *SkillFS) IncrFallback(name string) {
	fs.incrMetric(name, "total_fallbacks")
}

func (fs *SkillFS) incrMetric(name, column string) {
	if fs.indexDB == nil {
		return
	}
	fs.indexDB.Exec(fmt.Sprintf(`INSERT INTO skill_metrics (name, %s, last_used_at)
		VALUES (?, 1, ?) ON CONFLICT(name) DO UPDATE SET %s = %s + 1, last_used_at = ?`,
		column, column, column), name, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
}

// ────────────────────────────────────────────────────────────────────────────
// Analysis storage (delegated from old SkillStore)
// ────────────────────────────────────────────────────────────────────────────

// PutAnalysis stores an execution analysis result.
func (fs *SkillFS) PutAnalysis(a *ExecutionAnalysisResult) error {
	if fs.indexDB == nil {
		return nil
	}
	skillsUsed, _ := marshalJSON(a.SkillsUsed)
	skillsNeeded, _ := marshalJSON(a.SkillsNeeded)
	suggestions, _ := marshalJSON(a.Suggestions)

	_, err := fs.indexDB.Exec(`INSERT OR REPLACE INTO execution_analyses
		(task_id, agent_id, success, quality, efficiency, quality_reason, skills_used, skills_needed, suggestions, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.TaskID, a.AgentID, a.Success, a.Quality, a.Efficiency, a.QualityReason,
		skillsUsed, skillsNeeded, suggestions, time.Now().Format(time.RFC3339))
	return err
}

// RecentAnalyses returns the most recent analyses.
func (fs *SkillFS) RecentAnalyses(agentID string, limit int) ([]*ExecutionAnalysisResult, error) {
	if fs.indexDB == nil {
		return nil, nil
	}
	rows, err := fs.indexDB.Query(`SELECT task_id, agent_id, success, quality, efficiency,
		quality_reason, skills_used, skills_needed, suggestions
		FROM execution_analyses WHERE agent_id = ? ORDER BY created_at DESC LIMIT ?`,
		agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*ExecutionAnalysisResult
	for rows.Next() {
		a := &ExecutionAnalysisResult{}
		var skillsUsed, skillsNeeded, suggestions string
		if err := rows.Scan(&a.TaskID, &a.AgentID, &a.Success, &a.Quality, &a.Efficiency,
			&a.QualityReason, &skillsUsed, &skillsNeeded, &suggestions); err != nil {
			continue
		}
		unmarshalJSON(skillsUsed, &a.SkillsUsed)
		unmarshalJSON(skillsNeeded, &a.SkillsNeeded)
		unmarshalJSON(suggestions, &a.Suggestions)
		results = append(results, a)
	}
	return results, nil
}

// ────────────────────────────────────────────────────────────────────────────
// IPFS integration
// ────────────────────────────────────────────────────────────────────────────

// ImportFromCID fetches a SKILL.md from the content store by CID and installs it.
// Used for cross-agent skill learning via GossipSub.
func (fs *SkillFS) ImportFromCID(cid string, fetchFn func(string) ([]byte, error)) (*Skill, error) {
	data, err := fetchFn(cid)
	if err != nil {
		return nil, fmt.Errorf("fetching CID %s: %w", truncateCID(cid), err)
	}

	meta, body, err := parseSkillMD(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing fetched skill: %w", err)
	}

	// Verify content hash
	hash := sha256.Sum256([]byte(body))
	if meta.ContentHash != "" && meta.ContentHash != hex.EncodeToString(hash[:]) {
		return nil, fmt.Errorf("content hash mismatch for %s", meta.Name)
	}

	meta.IPFSCID = cid
	meta.Origin = "imported"

	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Don't overwrite existing skills with same name (local takes precedence)
	if existing, ok := fs.skills[meta.Name]; ok {
		if existing.Meta.Generation >= meta.Generation {
			return nil, fmt.Errorf("local skill %q gen %d >= remote gen %d, skipping",
				meta.Name, existing.Meta.Generation, meta.Generation)
		}
	}

	return fs.writeSkillLocked(meta, body, fmt.Sprintf("imported from IPFS: %s", truncateCID(cid)))
}

// PublishAll publishes all skills to IPFS and updates their CIDs.
func (fs *SkillFS) PublishAll(agentID string) (int, error) {
	if fs.publish == nil {
		return 0, nil
	}

	fs.mu.RLock()
	skills := make([]*Skill, 0, len(fs.skills))
	for _, s := range fs.skills {
		skills = append(skills, s)
	}
	fs.mu.RUnlock()

	published := 0
	for _, s := range skills {
		raw := renderSkillMD(s.Meta, s.Body)
		cid, err := fs.publish([]byte(raw), "skill", agentID, s.Meta.Description)
		if err != nil {
			fmt.Printf("⚠️  IPFS publish failed for skill %s: %v\n", s.Meta.Name, err)
			continue
		}

		// Update CID in frontmatter and on disk
		fs.mu.Lock()
		if current, ok := fs.skills[s.Meta.Name]; ok {
			current.Meta.IPFSCID = cid
			fullPath := filepath.Join(fs.skillDir, s.Meta.Name, "SKILL.md")
			os.WriteFile(fullPath, []byte(renderSkillMD(current.Meta, current.Body)), 0644)
		}
		fs.mu.Unlock()

		// Update index DB
		if fs.indexDB != nil {
			fs.indexDB.Exec(`INSERT INTO skill_metrics (name, ipfs_cid) VALUES (?, ?)
				ON CONFLICT(name) DO UPDATE SET ipfs_cid = ?`, s.Meta.Name, cid, cid)
		}

		published++
	}
	return published, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Index generation
// ────────────────────────────────────────────────────────────────────────────

// WriteIndex generates skills/index.yaml — a catalog of all skills.
func (fs *SkillFS) WriteIndex() error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	type indexEntry struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Category    string   `yaml:"category,omitempty"`
		Version     string   `yaml:"version,omitempty"`
		Origin      string   `yaml:"origin,omitempty"`
		Generation  int      `yaml:"generation,omitempty"`
		Tags        []string `yaml:"tags,omitempty"`
		IPFSCID     string   `yaml:"ipfs_cid,omitempty"`
	}

	entries := make([]indexEntry, 0, len(fs.skills))
	for _, s := range fs.skills {
		entries = append(entries, indexEntry{
			Name:        s.Meta.Name,
			Description: s.Meta.Description,
			Category:    s.Meta.Category,
			Version:     s.Meta.Version,
			Origin:      s.Meta.Origin,
			Generation:  s.Meta.Generation,
			Tags:        s.Meta.Tags,
			IPFSCID:     s.Meta.IPFSCID,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	index := struct {
		GeneratedAt string       `yaml:"generated_at"`
		Count       int          `yaml:"count"`
		Skills      []indexEntry `yaml:"skills"`
	}{
		GeneratedAt: time.Now().Format(time.RFC3339),
		Count:       len(entries),
		Skills:      entries,
	}

	data, err := yaml.Marshal(index)
	if err != nil {
		return err
	}

	header := "# Skills Index — auto-generated by Spore SkillFS\n# Do not edit manually\n\n"
	return os.WriteFile(filepath.Join(fs.skillDir, "index.yaml"), append([]byte(header), data...), 0644)
}

// Stats returns summary statistics compatible with SkillStats.
func (fs *SkillFS) Stats(agentID string) *SkillStats {
	fs.mu.RLock()
	total := len(fs.skills)
	evolved := 0
	for _, s := range fs.skills {
		if s.Meta.Generation > 0 {
			evolved++
		}
	}
	fs.mu.RUnlock()

	stats := &SkillStats{
		TotalSkills:  total,
		ActiveSkills: total, // all loaded skills are active
		Evolved:      evolved,
	}

	if fs.indexDB != nil {
		fs.indexDB.QueryRow(`SELECT COUNT(*) FROM execution_analyses WHERE agent_id = ?`, agentID).Scan(&stats.TotalAnalyses)

		var totalRate float64
		var count int
		rows, err := fs.indexDB.Query(`SELECT total_completions, total_applied FROM skill_metrics WHERE total_applied > 0`)
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
	}

	return stats
}

// ActiveSkillNames returns names of active skills (for compatibility).
func (fs *SkillFS) ActiveSkillNames() []string {
	return fs.List()
}

// ActiveSkillSummary returns a human-readable summary of skills for LLM context.
// Implements SkillLister interface.
func (fs *SkillFS) ActiveSkillSummary() string {
	fs.mu.RLock()
	skills := make([]*Skill, 0, len(fs.skills))
	for _, s := range fs.skills {
		skills = append(skills, s)
	}
	fs.mu.RUnlock()

	if len(skills) == 0 {
		return ""
	}

	var lines []string
	for _, s := range skills {
		m := fs.Metrics(s.Meta.Name)
		rate := float64(0)
		if m.TotalApplied > 0 {
			rate = float64(m.TotalCompletions) / float64(m.TotalApplied) * 100
		}
		lines = append(lines, fmt.Sprintf("- %s (success: %.0f%%, used: %d times, origin: %s, gen: %d)",
			s.Meta.Name, rate, m.TotalApplied, s.Meta.Origin, s.Meta.Generation))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ────────────────────────────────────────────────────────────────────────────
// Internal
// ────────────────────────────────────────────────────────────────────────────

// writeSkillLocked writes SKILL.md to disk, updates cache, publishes to IPFS.
// Caller must hold fs.mu write lock.
func (fs *SkillFS) writeSkillLocked(meta SkillMeta, body string, changeSummary string) (*Skill, error) {
	now := time.Now().Format(time.RFC3339)
	if meta.CreatedAt == "" {
		meta.CreatedAt = now
	}
	meta.UpdatedAt = now

	// Content hash
	hash := sha256.Sum256([]byte(body))
	meta.ContentHash = hex.EncodeToString(hash[:])

	// Version number from DB
	version := 1
	if fs.indexDB != nil {
		var maxVer int
		fs.indexDB.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM skill_versions WHERE name = ?`,
			meta.Name).Scan(&maxVer)
		version = maxVer + 1
	}
	if meta.Version == "" {
		meta.Version = fmt.Sprintf("0.%d.0", version)
	}

	// Write directory + SKILL.md
	dir := filepath.Join(fs.skillDir, meta.Name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	raw := renderSkillMD(meta, body)
	skillPath := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(raw), 0644); err != nil {
		return nil, fmt.Errorf("writing SKILL.md: %w", err)
	}

	// Publish to IPFS
	if fs.publish != nil {
		cid, err := fs.publish([]byte(raw), "skill", "", meta.Description)
		if err != nil {
			fmt.Printf("⚠️  IPFS publish failed for %s: %v\n", meta.Name, err)
		} else {
			meta.IPFSCID = cid
			// Rewrite with CID in frontmatter
			raw = renderSkillMD(meta, body)
			os.WriteFile(skillPath, []byte(raw), 0644)
		}
	}

	skill := &Skill{
		Meta: meta,
		Body: body,
		Dir:  dir,
	}
	fs.skills[meta.Name] = skill

	// Record version in index DB
	if fs.indexDB != nil {
		fs.indexDB.Exec(`INSERT INTO skill_versions (name, version, content_hash, ipfs_cid, change, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			meta.Name, version, meta.ContentHash, meta.IPFSCID, changeSummary, now)
		fs.indexDB.Exec(`INSERT INTO skill_metrics (name, content_hash, ipfs_cid)
			VALUES (?, ?, ?) ON CONFLICT(name) DO UPDATE SET content_hash = ?, ipfs_cid = ?`,
			meta.Name, meta.ContentHash, meta.IPFSCID, meta.ContentHash, meta.IPFSCID)
	}

	return skill, nil
}

// scanDisk loads all skills from the skills/ directory.
func (fs *SkillFS) scanDisk() error {
	entries, err := os.ReadDir(fs.skillDir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		skillPath := filepath.Join(fs.skillDir, name, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue // directory without SKILL.md, skip
		}

		meta, body, err := parseSkillMD(string(data))
		if err != nil {
			fmt.Printf("⚠️  SkillFS: failed to parse %s/SKILL.md: %v\n", name, err)
			continue
		}

		// Ensure name matches directory
		if meta.Name == "" {
			meta.Name = name
		}

		fs.skills[name] = &Skill{
			Meta: meta,
			Body: body,
			Dir:  filepath.Join(fs.skillDir, name),
		}
	}

	if len(fs.skills) > 0 {
		fmt.Printf("📂 SkillFS: loaded %d skills from %s\n", len(fs.skills), fs.skillDir)
	}

	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// SKILL.md parsing / rendering
// ────────────────────────────────────────────────────────────────────────────

var frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.+?)\n---\n(.*)`)

// parseSkillMD parses a SKILL.md into frontmatter + body.
func parseSkillMD(raw string) (SkillMeta, string, error) {
	var meta SkillMeta

	matches := frontmatterRe.FindStringSubmatch(raw)
	if matches == nil {
		// No frontmatter — treat entire content as body, derive name from first heading
		body := strings.TrimSpace(raw)
		name := ""
		if strings.HasPrefix(body, "# ") {
			lines := strings.SplitN(body, "\n", 2)
			name = strings.TrimPrefix(lines[0], "# ")
			name = strings.TrimSpace(name)
		}
		return SkillMeta{Name: name}, body, nil
	}

	if err := yaml.Unmarshal([]byte(matches[1]), &meta); err != nil {
		return meta, "", fmt.Errorf("parsing frontmatter: %w", err)
	}

	body := strings.TrimSpace(matches[2])
	return meta, body, nil
}

// renderSkillMD renders a SKILL.md from meta + body.
func renderSkillMD(meta SkillMeta, body string) string {
	var b strings.Builder

	b.WriteString("---\n")
	data, _ := yaml.Marshal(meta)
	b.Write(data)
	b.WriteString("---\n\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}

	return b.String()
}
