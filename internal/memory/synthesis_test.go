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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemorySynthesizer_Synthesize(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	tmpDir := t.TempDir()

	now := time.Now()

	// Insert entries with varying ages
	ages := []struct {
		key   string
		value string
		age   time.Duration
	}{
		{"task:recent1", "completed code review", 1 * time.Hour},
		{"task:recent2", "deployed service", 6 * time.Hour},
		{"task:midweek1", "fixed auth bug", 3 * 24 * time.Hour},
		{"task:midweek2", "updated API docs", 5 * 24 * time.Hour},
		{"task:old1", "initial setup", 14 * 24 * time.Hour},
		{"task:old2", "database migration", 30 * 24 * time.Hour},
	}

	for _, a := range ages {
		err := store.Put(&Entry{
			AgentID:   "test-agent",
			Key:       a.key,
			Value:     a.value,
			CreatedAt: now.Add(-a.age).Unix(),
		})
		if err != nil {
			t.Fatalf("Put(%s): %v", a.key, err)
		}
	}

	cfg := SynthesisConfig{
		IntervalHours: 6,
		WorkDir:       tmpDir,
	}

	// No LLM provider — uses fallback grouping
	synth := NewMemorySynthesizer(store, nil, "test-agent", cfg)

	if err := synth.Synthesize(context.Background()); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	// Verify active_learnings.md was created
	content, err := LoadActiveLearnings(tmpDir)
	if err != nil {
		t.Fatalf("LoadActiveLearnings: %v", err)
	}

	// Check structure
	if !strings.Contains(content, "# Active Learnings") {
		t.Error("missing header '# Active Learnings'")
	}
	if !strings.Contains(content, "## Recent (< 24h)") {
		t.Error("missing section '## Recent (< 24h)'")
	}
	if !strings.Contains(content, "## This Week (1-7d)") {
		t.Error("missing section '## This Week (1-7d)'")
	}
	if !strings.Contains(content, "## Older (> 7d)") {
		t.Error("missing section '## Older (> 7d)'")
	}

	// Verify recent entries appear in full
	if !strings.Contains(content, "completed code review") {
		t.Error("recent entry 'completed code review' missing from output")
	}
	if !strings.Contains(content, "deployed service") {
		t.Error("recent entry 'deployed service' missing from output")
	}

	// Verify learnings.jsonl was created
	jsonlPath := filepath.Join(tmpDir, "memory", "learnings.jsonl")
	if _, err := os.Stat(jsonlPath); os.IsNotExist(err) {
		t.Error("learnings.jsonl was not created")
	}

	// Verify LastSynthesis was updated
	if synth.LastSynthesis().IsZero() {
		t.Error("LastSynthesis should be set after Synthesize")
	}
}

func TestMemorySynthesizer_EmptyStore(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	cfg := SynthesisConfig{
		IntervalHours: 6,
		WorkDir:       t.TempDir(),
	}
	synth := NewMemorySynthesizer(store, nil, "test-agent", cfg)

	// Should return nil with no entries
	if err := synth.Synthesize(context.Background()); err != nil {
		t.Fatalf("Synthesize on empty store: %v", err)
	}
}

func TestLoadActiveLearnings_Missing(t *testing.T) {
	_, err := LoadActiveLearnings(t.TempDir())
	if err == nil {
		t.Error("expected error for missing file")
	}
}
