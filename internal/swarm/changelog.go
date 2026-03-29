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

package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Swarm Changelog — tracks group-level capability changes across all agents
//
// Records:
// - Skill evolution (new/improved/fixed skills)
// - Agent spawns/deaths
// - Collective memory synthesis events
// - Configuration changes
//
// Outputs:
// - changelog.jsonl (append-only structured log)
// - CHANGELOG.md (human-readable rendered summary)
// ────────────────────────────────────────────────────────────────────────────

// ChangeType classifies a changelog entry.
type ChangeType string

const (
	ChangeSkillNew      ChangeType = "skill_new"
	ChangeSkillImproved ChangeType = "skill_improved"
	ChangeSkillFixed    ChangeType = "skill_fixed"
	ChangeAgentSpawned  ChangeType = "agent_spawned"
	ChangeAgentStopped  ChangeType = "agent_stopped"
	ChangeCollective    ChangeType = "collective_synthesis"
	ChangeConfig        ChangeType = "config_change"
	ChangeHumanFeedback ChangeType = "human_feedback"
)

// ChangeEntry is a single changelog record.
type ChangeEntry struct {
	Timestamp time.Time  `json:"timestamp"`
	Type      ChangeType `json:"type"`
	Agent     string     `json:"agent"`
	Summary   string     `json:"summary"`
	Details   string     `json:"details,omitempty"`
	Tags      []string   `json:"tags,omitempty"`
}

// Changelog tracks swarm-level capability changes.
type Changelog struct {
	mu       sync.Mutex
	entries  []ChangeEntry
	maxItems int
	jsonlPath string
	mdPath    string
}

// NewChangelog creates a changelog persisted to the given directory.
func NewChangelog(baseDir string) *Changelog {
	return &Changelog{
		maxItems:  500,
		jsonlPath: filepath.Join(baseDir, "changelog.jsonl"),
		mdPath:    filepath.Join(baseDir, "CHANGELOG.md"),
	}
}

// Record adds a new changelog entry.
func (cl *Changelog) Record(entry ChangeEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	cl.mu.Lock()
	cl.entries = append(cl.entries, entry)
	if len(cl.entries) > cl.maxItems {
		cl.entries = cl.entries[len(cl.entries)-cl.maxItems:]
	}
	cl.mu.Unlock()

	// Append to JSONL
	cl.appendJSONL(entry)
}

// RecordSkillEvolution is a convenience for skill changes.
func (cl *Changelog) RecordSkillEvolution(agent, skillName, evolutionType, summary string) {
	var ct ChangeType
	switch evolutionType {
	case "captured":
		ct = ChangeSkillNew
	case "fixed":
		ct = ChangeSkillFixed
	default:
		ct = ChangeSkillImproved
	}
	cl.Record(ChangeEntry{
		Type:    ct,
		Agent:   agent,
		Summary: fmt.Sprintf("[%s] %s skill: %s", agent, evolutionType, skillName),
		Details: summary,
		Tags:    []string{skillName, evolutionType},
	})
}

// RecordSpawn records an agent spawn event.
func (cl *Changelog) RecordSpawn(parent, child, reason string) {
	cl.Record(ChangeEntry{
		Type:    ChangeAgentSpawned,
		Agent:   child,
		Summary: fmt.Sprintf("%s spawned by %s: %s", child, parent, reason),
		Tags:    []string{"spawn", parent},
	})
}

// Recent returns the N most recent entries.
func (cl *Changelog) Recent(n int) []ChangeEntry {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	if n <= 0 || n > len(cl.entries) {
		n = len(cl.entries)
	}
	start := len(cl.entries) - n
	result := make([]ChangeEntry, n)
	copy(result, cl.entries[start:])
	// Reverse — newest first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// RenderMarkdown generates a CHANGELOG.md from recent entries.
func (cl *Changelog) RenderMarkdown() error {
	entries := cl.Recent(100)

	var sb strings.Builder
	sb.WriteString("# Swarm Changelog\n\n")
	sb.WriteString(fmt.Sprintf("> Auto-generated at %s\n\n", time.Now().Format(time.RFC3339)))

	// Group by date
	grouped := make(map[string][]ChangeEntry)
	var dates []string
	for _, e := range entries {
		date := e.Timestamp.Format("2006-01-02")
		if _, ok := grouped[date]; !ok {
			dates = append(dates, date)
		}
		grouped[date] = append(grouped[date], e)
	}

	for _, date := range dates {
		sb.WriteString(fmt.Sprintf("## %s\n\n", date))
		for _, e := range grouped[date] {
			icon := changeIcon(e.Type)
			sb.WriteString(fmt.Sprintf("- %s **%s** `%s` %s\n",
				icon, e.Type, e.Agent, e.Summary))
			if e.Details != "" {
				sb.WriteString(fmt.Sprintf("  > %s\n", e.Details))
			}
		}
		sb.WriteString("\n")
	}

	os.MkdirAll(filepath.Dir(cl.mdPath), 0755)
	return os.WriteFile(cl.mdPath, []byte(sb.String()), 0644)
}

// Load reads existing entries from changelog.jsonl.
func (cl *Changelog) Load() {
	data, err := os.ReadFile(cl.jsonlPath)
	if err != nil {
		return
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry ChangeEntry
		if json.Unmarshal([]byte(line), &entry) == nil {
			cl.entries = append(cl.entries, entry)
		}
	}
	if len(cl.entries) > cl.maxItems {
		cl.entries = cl.entries[len(cl.entries)-cl.maxItems:]
	}
}

// Count returns the number of entries.
func (cl *Changelog) Count() int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return len(cl.entries)
}

func (cl *Changelog) appendJSONL(entry ChangeEntry) {
	os.MkdirAll(filepath.Dir(cl.jsonlPath), 0755)
	f, err := os.OpenFile(cl.jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	data, _ := json.Marshal(entry)
	fmt.Fprintf(f, "%s\n", data)
}

func changeIcon(ct ChangeType) string {
	switch ct {
	case ChangeSkillNew:
		return "🆕"
	case ChangeSkillImproved:
		return "🔧"
	case ChangeSkillFixed:
		return "🩹"
	case ChangeAgentSpawned:
		return "🐣"
	case ChangeAgentStopped:
		return "💀"
	case ChangeCollective:
		return "🧠"
	case ChangeConfig:
		return "⚙️"
	case ChangeHumanFeedback:
		return "👤"
	default:
		return "📝"
	}
}
