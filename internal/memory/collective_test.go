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
)

func TestCollectiveSynthesizer_ReceivePeerDigest(t *testing.T) {
	dir := t.TempDir()
	cs := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir:        dir,
		MaxPeerDigests: 3,
	}, nil, nil)

	cs.ReceivePeerDigest("peer-a", "Alpha", "cidAAA", "Alpha learned stuff")
	cs.ReceivePeerDigest("peer-b", "Beta", "cidBBB", "Beta learned stuff")

	if cs.PeerCount() != 2 {
		t.Errorf("expected 2 peers, got %d", cs.PeerCount())
	}

	// Update existing peer
	cs.ReceivePeerDigest("peer-a", "Alpha", "cidAAA2", "Alpha updated")
	if cs.PeerCount() != 2 {
		t.Errorf("expected 2 peers after update, got %d", cs.PeerCount())
	}
}

func TestCollectiveSynthesizer_EvictOldest(t *testing.T) {
	dir := t.TempDir()
	cs := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir:        dir,
		MaxPeerDigests: 2,
	}, nil, nil)

	cs.ReceivePeerDigest("peer-a", "A", "cid1", "first")
	cs.ReceivePeerDigest("peer-b", "B", "cid2", "second")
	cs.ReceivePeerDigest("peer-c", "C", "cid3", "third") // should evict oldest

	if cs.PeerCount() != 2 {
		t.Errorf("expected 2 peers after eviction, got %d", cs.PeerCount())
	}
}

func TestCollectiveSynthesizer_PublishDigest(t *testing.T) {
	dir := t.TempDir()

	// Write a fake active_learnings.md
	memDir := filepath.Join(dir, "memory")
	os.MkdirAll(memDir, 0755)
	activePath := filepath.Join(memDir, "active_learnings.md")
	os.WriteFile(activePath, []byte("# Active Learnings\n\n- Learned Go testing\n- Learned IPFS\n"), 0644)

	var publishedData []byte
	mockPublish := func(data []byte, ct, aid, summary string) (string, error) {
		publishedData = data
		return "bafkrei_fake_cid", nil
	}

	cs := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir: dir,
	}, mockPublish, nil)

	cid, err := cs.PublishDigest(activePath)
	if err != nil {
		t.Fatalf("PublishDigest: %v", err)
	}
	if cid != "bafkrei_fake_cid" {
		t.Errorf("expected fake CID, got %q", cid)
	}
	if !strings.Contains(string(publishedData), "Learned Go testing") {
		t.Error("published data should contain active learnings")
	}
}

func TestCollectiveSynthesizer_FallbackSynthesize(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory")
	os.MkdirAll(memDir, 0755)

	activePath := filepath.Join(memDir, "active_learnings.md")
	os.WriteFile(activePath, []byte("- My own learning\n"), 0644)

	mockFetch := func(cid string) ([]byte, error) {
		return []byte("- Peer's learning from " + cid), nil
	}

	// No LLM provider → fallback
	cs := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir: dir,
	}, nil, mockFetch)

	cs.ReceivePeerDigest("peer-a", "Alpha", "cid123", "Alpha's digest")

	err := cs.Synthesize(context.Background(), activePath)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	// Check output
	data, err := os.ReadFile(filepath.Join(memDir, "collective_learnings.md"))
	if err != nil {
		t.Fatalf("reading collective_learnings.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "Collective Learnings") {
		t.Error("missing title")
	}
	if !strings.Contains(content, "My own learning") {
		t.Error("missing own learnings")
	}
	if !strings.Contains(content, "Alpha") {
		t.Error("missing peer attribution")
	}
	if !strings.Contains(content, "Peer's learning") {
		t.Error("missing peer content")
	}
}

func TestCollectiveSynthesizer_PersistRestore(t *testing.T) {
	dir := t.TempDir()

	cs1 := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir: dir,
	}, nil, nil)

	cs1.ReceivePeerDigest("peer-a", "Alpha", "cid1", "digest A")
	cs1.ReceivePeerDigest("peer-b", "Beta", "cid2", "digest B")

	// Create new instance — should load from disk
	cs2 := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir: dir,
	}, nil, nil)

	if cs2.PeerCount() != 2 {
		t.Errorf("expected 2 restored peers, got %d", cs2.PeerCount())
	}
}

func TestCollectiveSynthesizer_NoPeersNoOp(t *testing.T) {
	dir := t.TempDir()
	cs := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir: dir,
	}, nil, nil)

	err := cs.Synthesize(context.Background(), "/nonexistent/path")
	if err != nil {
		t.Errorf("should not error with no peers: %v", err)
	}
}

func TestCollectiveSynthesizer_Status(t *testing.T) {
	dir := t.TempDir()
	cs := NewCollectiveSynthesizer(nil, "agent-1", CollectiveSynthesisConfig{
		WorkDir:       dir,
		IntervalHours: 8,
	}, nil, nil)

	cs.ReceivePeerDigest("peer-a", "A", "cid1", "digest")

	status := cs.Status()
	if status.PeerDigests != 1 {
		t.Errorf("expected 1 peer, got %d", status.PeerDigests)
	}
}
