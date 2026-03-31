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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.zoe.im/spore/internal/llm"
)

// LearningEntry is a single raw learning record in the append-only log.
type LearningEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Category  string    `json:"category"` // task, reflection, peer, error
	Summary   string    `json:"summary"`
	Details   string    `json:"details,omitempty"`
	Source    string    `json:"source,omitempty"` // task_id, peer_id, etc.
	Tags      []string  `json:"tags,omitempty"`
}

// SynthesisConfig configures the memory synthesis engine.
type SynthesisConfig struct {
	IntervalHours int    // how often to run synthesis (default 6)
	WorkDir       string // agent working directory
}

// DefaultSynthesisConfig returns sensible defaults.
func DefaultSynthesisConfig(workDir string) SynthesisConfig {
	return SynthesisConfig{
		IntervalHours: 6,
		WorkDir:       workDir,
	}
}

// MemorySynthesizer reads raw experience entries from SQLite, compresses older
// ones by theme using an LLM, and produces an active_learnings.md file.
type MemorySynthesizer struct {
	mu       sync.Mutex
	store    Store
	provider llm.Provider
	cfg      SynthesisConfig
	agentID  string

	lastSynthesis time.Time
	lastRawTS     int64  // last appended entry timestamp (dedup)
	rawPath       string // learnings.jsonl (append-only)
	activePath    string // active_learnings.md
}

// NewMemorySynthesizer creates a synthesizer.
func NewMemorySynthesizer(store Store, provider llm.Provider, agentID string, cfg SynthesisConfig) *MemorySynthesizer {
	memDir := filepath.Join(cfg.WorkDir, "memory")
	return &MemorySynthesizer{
		store:      store,
		provider:   provider,
		cfg:        cfg,
		agentID:    agentID,
		rawPath:    filepath.Join(memDir, "learnings.jsonl"),
		activePath: filepath.Join(memDir, "active_learnings.md"),
	}
}

// Interval returns the configured synthesis interval as a Duration.
func (ms *MemorySynthesizer) Interval() time.Duration {
	h := ms.cfg.IntervalHours
	if h <= 0 {
		h = 6
	}
	return time.Duration(h) * time.Hour
}

// AppendLearning adds a raw learning entry to the append-only JSONL log.
func (ms *MemorySynthesizer) AppendLearning(entry LearningEntry) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("learn-%d", time.Now().UnixNano())
	}

	if err := os.MkdirAll(filepath.Dir(ms.rawPath), 0755); err != nil {
		return fmt.Errorf("creating memory dir: %w", err)
	}

	f, err := os.OpenFile(ms.rawPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening learnings log: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n", data)
	return err
}

// Synthesize runs the memory synthesis process:
//  1. Read all memory entries from SQLite (broad Search)
//  2. Group by age: <24h (keep full), 1-7d (summarize per topic), >7d (aggressive compress)
//  3. Use LLM to produce themed summaries for older entries
//  4. Write active_learnings.md
//  5. Append raw experiences to learnings.jsonl
func (ms *MemorySynthesizer) Synthesize(ctx context.Context) error {
	if ms.store == nil {
		return nil
	}

	// Read all memory entries from SQLite with a broad query
	entries, err := ms.store.Search("", 1000)
	if err != nil {
		return fmt.Errorf("reading memory store: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	now := time.Now()
	dayAgo := now.Add(-24 * time.Hour)
	weekAgo := now.Add(-7 * 24 * time.Hour)

	// Partition entries into three tiers by age
	var fresh, midAge, old []*Entry
	for _, e := range entries {
		ts := time.Unix(e.CreatedAt, 0)
		switch {
		case ts.After(dayAgo):
			fresh = append(fresh, e)
		case ts.After(weekAgo):
			midAge = append(midAge, e)
		default:
			old = append(old, e)
		}
	}

	// Build the active memory document
	var sb strings.Builder
	sb.WriteString("# Active Learnings\n\n")
	sb.WriteString(fmt.Sprintf("> Synthesized at %s | Agent: %s\n\n", now.Format(time.RFC3339), ms.agentID))

	// Tier 1: <24h — keep full text
	sb.WriteString("## Recent (< 24h)\n\n")
	if len(fresh) == 0 {
		sb.WriteString("_No recent entries._\n\n")
	}
	for _, e := range fresh {
		ts := time.Unix(e.CreatedAt, 0)
		sb.WriteString(fmt.Sprintf("- **%s** [%s]: %s\n",
			e.Key, ts.Format("15:04"), truncate(e.Value, 500)))
	}
	sb.WriteString("\n")

	// Tier 2: 1-7d — summarize per topic using LLM
	sb.WriteString("## This Week (1-7d)\n\n")
	if len(midAge) > 0 {
		if ms.provider != nil {
			summary, llmErr := ms.summarizeTier(ctx, midAge, "Summarize these entries by topic. Keep key facts, discard noise. Use markdown bullet points.")
			if llmErr != nil {
				sb.WriteString(ms.fallbackGroup(midAge))
			} else {
				sb.WriteString(summary)
			}
		} else {
			sb.WriteString(ms.fallbackGroup(midAge))
		}
	} else {
		sb.WriteString("_No entries from this week._\n\n")
	}

	// Tier 3: >7d — aggressive compress
	sb.WriteString("## Older (> 7d)\n\n")
	if len(old) > 0 {
		if ms.provider != nil {
			summary, llmErr := ms.summarizeTier(ctx, old, "Aggressively compress these old entries into key lessons learned. Maximum 10 bullet points. Discard anything redundant.")
			if llmErr != nil {
				sb.WriteString(ms.fallbackGroup(old))
			} else {
				sb.WriteString(summary)
			}
		} else {
			sb.WriteString(ms.fallbackGroup(old))
		}
	} else {
		sb.WriteString("_No older entries._\n\n")
	}

	// Write active_learnings.md
	if err := os.MkdirAll(filepath.Dir(ms.activePath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(ms.activePath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("writing active learnings: %w", err)
	}

	// Append raw entries to learnings.jsonl
	ms.appendRawEntries(entries)

	ms.mu.Lock()
	ms.lastSynthesis = now
	ms.mu.Unlock()

	return nil
}

// summarizeTier uses the LLM to summarize a tier of memory entries.
func (ms *MemorySynthesizer) summarizeTier(ctx context.Context, entries []*Entry, instruction string) (string, error) {
	var lines []string
	for _, e := range entries {
		ts := time.Unix(e.CreatedAt, 0)
		lines = append(lines, fmt.Sprintf("[%s] %s: %s",
			ts.Format("01-02"), e.Key, truncate(e.Value, 200)))
	}

	prompt := fmt.Sprintf("%s\n\n%d entries:\n%s",
		instruction, len(entries), strings.Join(lines, "\n"))

	resp, err := ms.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: "You are a concise memory synthesizer. Output only markdown."},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return "", err
	}
	return resp.Content + "\n", nil
}

// fallbackGroup groups entries by key prefix without LLM.
func (ms *MemorySynthesizer) fallbackGroup(entries []*Entry) string {
	grouped := make(map[string][]*Entry)
	for _, e := range entries {
		prefix := e.Key
		if idx := strings.Index(prefix, ":"); idx > 0 {
			prefix = prefix[:idx]
		}
		grouped[prefix] = append(grouped[prefix], e)
	}

	var sb strings.Builder
	for prefix, es := range grouped {
		sb.WriteString(fmt.Sprintf("### %s (%d entries)\n", prefix, len(es)))
		limit := 5
		if len(es) < limit {
			limit = len(es)
		}
		for _, e := range es[:limit] {
			sb.WriteString(fmt.Sprintf("- %s\n", truncate(e.Value, 120)))
		}
		if len(es) > limit {
			sb.WriteString(fmt.Sprintf("- ... and %d more\n", len(es)-limit))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// appendRawEntries writes memory entries to the append-only JSONL log.
func (ms *MemorySynthesizer) appendRawEntries(entries []*Entry) {
	if err := os.MkdirAll(filepath.Dir(ms.rawPath), 0755); err != nil {
		return
	}

	// Filter: only append entries newer than last appended timestamp
	var newEntries []*Entry
	for _, e := range entries {
		if e.UpdatedAt > ms.lastRawTS {
			newEntries = append(newEntries, e)
		}
	}
	if len(newEntries) == 0 {
		return
	}

	f, err := os.OpenFile(ms.rawPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	var maxTS int64
	for _, e := range newEntries {
		le := LearningEntry{
			ID:        e.ID,
			Timestamp: time.Unix(e.CreatedAt, 0),
			Category:  e.Key,
			Summary:   truncate(e.Value, 500),
			Source:    e.AgentID,
		}
		data, err := json.Marshal(le)
		if err != nil {
			continue
		}
		fmt.Fprintf(f, "%s\n", data)
		if e.UpdatedAt > maxTS {
			maxTS = e.UpdatedAt
		}
	}
	if maxTS > ms.lastRawTS {
		ms.lastRawTS = maxTS
	}
}

// LoadActiveLearnings reads the synthesized active_learnings.md file.
func LoadActiveLearnings(workDir string) (string, error) {
	path := filepath.Join(workDir, "memory", "active_learnings.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// LastSynthesis returns when synthesis last ran.
func (ms *MemorySynthesizer) LastSynthesis() time.Time {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.lastSynthesis
}

// ActiveLearningsPath returns the path to the synthesized markdown file.
func (ms *MemorySynthesizer) ActiveLearningsPath() string {
	return ms.activePath
}

// Status returns the current synthesis status.
func (ms *MemorySynthesizer) Status() SynthesisStatus {
	ms.mu.Lock()
	lastSynth := ms.lastSynthesis
	ms.mu.Unlock()

	return SynthesisStatus{
		LastSynthesis: lastSynth,
		ActivePath:    ms.activePath,
		Interval:      ms.Interval(),
	}
}

// SynthesisStatus exposes the current state of the synthesis engine.
type SynthesisStatus struct {
	LastSynthesis time.Time     `json:"last_synthesis"`
	ActivePath    string        `json:"active_path"`
	Interval      time.Duration `json:"interval_ns"`
}

// RunPeriodic starts a background goroutine that runs synthesis on the configured interval.
// It blocks until ctx is cancelled.
func (ms *MemorySynthesizer) RunPeriodic(ctx context.Context) {
	ticker := time.NewTicker(ms.Interval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = ms.Synthesize(ctx)
		}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
