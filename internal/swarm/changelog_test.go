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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangelog_Record(t *testing.T) {
	dir := t.TempDir()
	cl := NewChangelog(dir)

	cl.Record(ChangeEntry{
		Type:    ChangeSkillNew,
		Agent:   "forge",
		Summary: "New skill: deploy-k8s",
	})
	cl.Record(ChangeEntry{
		Type:    ChangeAgentSpawned,
		Agent:   "forge-child-1",
		Summary: "Spawned by forge",
	})

	if cl.Count() != 2 {
		t.Errorf("expected 2 entries, got %d", cl.Count())
	}
}

func TestChangelog_Recent(t *testing.T) {
	dir := t.TempDir()
	cl := NewChangelog(dir)

	for i := 0; i < 10; i++ {
		cl.Record(ChangeEntry{
			Type:    ChangeSkillImproved,
			Agent:   "test",
			Summary: "entry",
		})
	}

	recent := cl.Recent(3)
	if len(recent) != 3 {
		t.Errorf("expected 3 recent, got %d", len(recent))
	}
}

func TestChangelog_RenderMarkdown(t *testing.T) {
	dir := t.TempDir()
	cl := NewChangelog(dir)

	cl.RecordSkillEvolution("forge", "coding", "captured", "New coding skill")
	cl.RecordSpawn("boss", "forge-child-1", "need more workers")

	if err := cl.RenderMarkdown(); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("reading CHANGELOG.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Swarm Changelog") {
		t.Error("missing title")
	}
	if !strings.Contains(content, "forge") {
		t.Error("missing agent name")
	}
	if !strings.Contains(content, "coding") {
		t.Error("missing skill name")
	}
}

func TestChangelog_PersistRestore(t *testing.T) {
	dir := t.TempDir()

	cl1 := NewChangelog(dir)
	cl1.Record(ChangeEntry{Type: ChangeSkillNew, Agent: "a", Summary: "s1"})
	cl1.Record(ChangeEntry{Type: ChangeSkillFixed, Agent: "b", Summary: "s2"})

	cl2 := NewChangelog(dir)
	cl2.Load()
	if cl2.Count() != 2 {
		t.Errorf("expected 2 restored, got %d", cl2.Count())
	}
}

func TestChangelog_RecordSkillEvolution(t *testing.T) {
	dir := t.TempDir()
	cl := NewChangelog(dir)

	cl.RecordSkillEvolution("forge", "research", "captured", "new skill from task")
	cl.RecordSkillEvolution("forge", "deploy", "fixed", "fixed timeout issue")
	cl.RecordSkillEvolution("forge", "coding", "derived", "enhanced for Go")

	entries := cl.Recent(10)
	if len(entries) != 3 {
		t.Errorf("expected 3, got %d", len(entries))
	}
	if entries[0].Type != ChangeSkillImproved {
		t.Errorf("expected improved for derived, got %s", entries[0].Type)
	}
	if entries[1].Type != ChangeSkillFixed {
		t.Errorf("expected fixed, got %s", entries[1].Type)
	}
	if entries[2].Type != ChangeSkillNew {
		t.Errorf("expected new for captured, got %s", entries[2].Type)
	}
}
