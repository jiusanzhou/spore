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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// JournalEntryType classifies evolution events.
type JournalEntryType string

const (
	JournalSkillAcquired  JournalEntryType = "skill_acquired"
	JournalSkillEvolved   JournalEntryType = "skill_evolved"
	JournalDeepReflection JournalEntryType = "deep_reflection"
	JournalPeerLearning   JournalEntryType = "peer_learning"
	JournalSelfEvolution  JournalEntryType = "self_evolution"
	JournalConfigChange   JournalEntryType = "config_change"
)

// JournalEntry records a single evolution event.
type JournalEntry struct {
	Timestamp time.Time        `json:"timestamp"`
	Type      JournalEntryType `json:"type"`
	Summary   string           `json:"summary"`
	Details   string           `json:"details,omitempty"`
	Before    string           `json:"before,omitempty"` // state before change
	After     string           `json:"after,omitempty"`  // state after change
}

// EvolutionJournal maintains an append-only JSONL log of evolution events
// and regenerates a human-readable JOURNAL.md from it.
type EvolutionJournal struct {
	mu       sync.Mutex
	workDir  string
	jsonlPath string
	mdPath    string
}

// NewEvolutionJournal creates a journal rooted at the agent's workdir.
func NewEvolutionJournal(workDir string) *EvolutionJournal {
	evoDir := filepath.Join(workDir, "evolution")
	return &EvolutionJournal{
		workDir:   workDir,
		jsonlPath: filepath.Join(evoDir, "journal.jsonl"),
		mdPath:    filepath.Join(workDir, "JOURNAL.md"),
	}
}

// Record appends an entry to the JSONL log and regenerates JOURNAL.md.
func (j *EvolutionJournal) Record(entry JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(j.jsonlPath), 0755); err != nil {
		return fmt.Errorf("creating evolution dir: %w", err)
	}

	// Append to JSONL
	f, err := os.OpenFile(j.jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening journal log: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%s\n", data); err != nil {
		return err
	}

	// Regenerate JOURNAL.md (read all entries then render)
	entries, err := j.readEntriesLocked()
	if err != nil {
		return nil // non-fatal: JSONL was written successfully
	}
	return j.renderMarkdownLocked(entries)
}

// Entries returns the most recent journal entries (up to limit, 0=all).
func (j *EvolutionJournal) Entries(limit int) []JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()

	entries, err := j.readEntriesLocked()
	if err != nil || len(entries) == 0 {
		return nil
	}

	if limit > 0 && limit < len(entries) {
		// Return most recent entries
		return entries[len(entries)-limit:]
	}
	return entries
}

// RenderMarkdown produces the JOURNAL.md content from all entries.
func (j *EvolutionJournal) RenderMarkdown() string {
	j.mu.Lock()
	defer j.mu.Unlock()

	entries, err := j.readEntriesLocked()
	if err != nil || len(entries) == 0 {
		return "# Evolution Journal\n\nNo entries yet.\n"
	}

	return j.buildMarkdown(entries)
}

func (j *EvolutionJournal) readEntriesLocked() ([]JournalEntry, error) {
	data, err := os.ReadFile(j.jsonlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var entries []JournalEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e JournalEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (j *EvolutionJournal) renderMarkdownLocked(entries []JournalEntry) error {
	md := j.buildMarkdown(entries)
	return os.WriteFile(j.mdPath, []byte(md), 0644)
}

func (j *EvolutionJournal) buildMarkdown(entries []JournalEntry) string {
	var sb strings.Builder
	sb.WriteString("# Evolution Journal\n\n")
	sb.WriteString(fmt.Sprintf("> %d evolution events recorded\n\n", len(entries)))

	// Group by date
	type dayGroup struct {
		date    string
		entries []JournalEntry
	}
	groups := make(map[string]*dayGroup)
	var order []string
	for _, e := range entries {
		date := e.Timestamp.Format("2006-01-02")
		if _, ok := groups[date]; !ok {
			groups[date] = &dayGroup{date: date}
			order = append(order, date)
		}
		groups[date].entries = append(groups[date].entries, e)
	}

	// Render newest day first
	for i := len(order) - 1; i >= 0; i-- {
		g := groups[order[i]]
		sb.WriteString(fmt.Sprintf("## %s\n\n", g.date))
		for _, e := range g.entries {
			icon := journalIcon(e.Type)
			sb.WriteString(fmt.Sprintf("- **%s** %s `%s` — %s\n",
				e.Timestamp.Format("15:04"),
				icon,
				string(e.Type),
				e.Summary,
			))
			if e.Details != "" {
				sb.WriteString(fmt.Sprintf("  > %s\n", e.Details))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func journalIcon(t JournalEntryType) string {
	switch t {
	case JournalSkillAcquired:
		return "🆕"
	case JournalSkillEvolved:
		return "🧬"
	case JournalDeepReflection:
		return "🔮"
	case JournalPeerLearning:
		return "🤝"
	case JournalSelfEvolution:
		return "🦋"
	case JournalConfigChange:
		return "⚙️"
	default:
		return "📝"
	}
}
