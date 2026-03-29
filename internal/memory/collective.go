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
	"sort"
	"strings"
	"sync"
	"time"

	"go.zoe.im/spore/internal/llm"
)

// ────────────────────────────────────────────────────────────────────────────
// Collective Memory Synthesis
//
// Each agent periodically:
// 1. Publishes its active_learnings.md digest to IPFS via ContentStore
// 2. Broadcasts the CID via GossipSub (MsgContentAnnounce, type=memory_digest)
// 3. Receives peer digests, stores them locally
// 4. Runs collective synthesis: LLM merges own + peer digests into
//    collective_learnings.md — the swarm's shared knowledge base
//
// This is Spore's unique advantage: decentralized collective memory
// that emerges from individual agent experiences.
// ────────────────────────────────────────────────────────────────────────────

// PeerDigest represents a received memory digest from another agent.
type PeerDigest struct {
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name,omitempty"`
	CID       string    `json:"cid"`
	Summary   string    `json:"summary"`
	Size      int       `json:"size"`
	Timestamp time.Time `json:"timestamp"`
	Content   string    `json:"-"` // loaded on demand
}

// CollectiveSynthesisConfig configures the collective memory synthesis.
type CollectiveSynthesisConfig struct {
	IntervalHours  int    // how often to run collective synthesis (default 12)
	MaxPeerDigests int    // max peer digests to keep (default 20)
	WorkDir        string // agent working directory
}

// CollectiveSynthesizer manages cross-agent memory synthesis.
type CollectiveSynthesizer struct {
	mu       sync.Mutex
	provider llm.Provider
	agentID  string
	cfg      CollectiveSynthesisConfig

	// Peer digest inbox — populated by GossipSub handler
	peerDigests map[string]*PeerDigest // agentID → latest digest

	// Publishing
	publishFn func(data []byte, contentType, agentID, summary string) (string, error)
	fetchFn   func(cid string) ([]byte, error)

	// State
	lastSynthesis   time.Time
	lastPublish     time.Time
	collectivePath  string // collective_learnings.md
	peerDigestsPath string // peer_digests.json
}

// NewCollectiveSynthesizer creates a collective memory synthesizer.
func NewCollectiveSynthesizer(
	provider llm.Provider,
	agentID string,
	cfg CollectiveSynthesisConfig,
	publishFn func([]byte, string, string, string) (string, error),
	fetchFn func(string) ([]byte, error),
) *CollectiveSynthesizer {
	if cfg.IntervalHours <= 0 {
		cfg.IntervalHours = 12
	}
	if cfg.MaxPeerDigests <= 0 {
		cfg.MaxPeerDigests = 20
	}

	memDir := filepath.Join(cfg.WorkDir, "memory")
	cs := &CollectiveSynthesizer{
		provider:        provider,
		agentID:         agentID,
		cfg:             cfg,
		peerDigests:     make(map[string]*PeerDigest),
		publishFn:       publishFn,
		fetchFn:         fetchFn,
		collectivePath:  filepath.Join(memDir, "collective_learnings.md"),
		peerDigestsPath: filepath.Join(memDir, "peer_digests.json"),
	}

	// Load existing peer digests from disk
	cs.loadPeerDigests()

	return cs
}

// ────────────────────────────────────────────────────────────────────────────
// Publishing own digest
// ────────────────────────────────────────────────────────────────────────────

// PublishDigest reads active_learnings.md and publishes it to IPFS.
// Returns the CID for broadcast.
func (cs *CollectiveSynthesizer) PublishDigest(activeLearningsPath string) (string, error) {
	if cs.publishFn == nil {
		return "", fmt.Errorf("no publish function configured")
	}

	data, err := os.ReadFile(activeLearningsPath)
	if err != nil {
		return "", fmt.Errorf("reading active learnings: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty active learnings")
	}

	// Summarize for broadcast metadata
	summary := string(data)
	if len(summary) > 200 {
		summary = summary[:197] + "..."
	}

	cid, err := cs.publishFn(data, "memory_digest", cs.agentID, summary)
	if err != nil {
		return "", err
	}

	cs.mu.Lock()
	cs.lastPublish = time.Now()
	cs.mu.Unlock()

	return cid, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Receiving peer digests
// ────────────────────────────────────────────────────────────────────────────

// ReceivePeerDigest records a peer's memory digest. Called by GossipSub handler.
func (cs *CollectiveSynthesizer) ReceivePeerDigest(agentID, agentName, cid, summary string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.peerDigests[agentID] = &PeerDigest{
		AgentID:   agentID,
		AgentName: agentName,
		CID:       cid,
		Summary:   summary,
		Timestamp: time.Now(),
	}

	// Evict oldest if over limit
	if len(cs.peerDigests) > cs.cfg.MaxPeerDigests {
		cs.evictOldest()
	}

	// Persist to disk
	cs.savePeerDigestsLocked()
}

// PeerCount returns the number of peer digests we have.
func (cs *CollectiveSynthesizer) PeerCount() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.peerDigests)
}

// ────────────────────────────────────────────────────────────────────────────
// Collective synthesis
// ────────────────────────────────────────────────────────────────────────────

// Synthesize merges own active learnings with peer digests into collective_learnings.md.
// This is the core of cross-agent memory synthesis.
func (cs *CollectiveSynthesizer) Synthesize(ctx context.Context, ownActivePath string) error {
	cs.mu.Lock()
	peers := make([]*PeerDigest, 0, len(cs.peerDigests))
	for _, p := range cs.peerDigests {
		peers = append(peers, p)
	}
	cs.mu.Unlock()

	if len(peers) == 0 {
		return nil // nothing to synthesize
	}

	// Sort by timestamp (newest first)
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].Timestamp.After(peers[j].Timestamp)
	})

	// Read own active learnings
	ownContent := ""
	if data, err := os.ReadFile(ownActivePath); err == nil {
		ownContent = string(data)
	}

	// Fetch peer digest contents (on demand via CID)
	for _, p := range peers {
		if p.Content != "" {
			continue // already loaded
		}
		if cs.fetchFn != nil {
			data, err := cs.fetchFn(p.CID)
			if err != nil {
				fmt.Printf("⚠️  Collective: failed to fetch digest from %s: %v\n", p.AgentID[:8], err)
				continue
			}
			p.Content = string(data)
			p.Size = len(data)
		}
	}

	// Filter peers with content
	var peersWithContent []*PeerDigest
	for _, p := range peers {
		if p.Content != "" {
			peersWithContent = append(peersWithContent, p)
		}
	}

	if len(peersWithContent) == 0 && ownContent == "" {
		return nil
	}

	// Build collective learnings document
	var result string
	if cs.provider != nil {
		var err error
		result, err = cs.llmSynthesize(ctx, ownContent, peersWithContent)
		if err != nil {
			fmt.Printf("⚠️  Collective synthesis LLM failed: %v, using fallback\n", err)
			result = cs.fallbackSynthesize(ownContent, peersWithContent)
		}
	} else {
		result = cs.fallbackSynthesize(ownContent, peersWithContent)
	}

	// Write collective_learnings.md
	if err := os.MkdirAll(filepath.Dir(cs.collectivePath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(cs.collectivePath, []byte(result), 0644); err != nil {
		return fmt.Errorf("writing collective learnings: %w", err)
	}

	cs.mu.Lock()
	cs.lastSynthesis = time.Now()
	cs.mu.Unlock()

	fmt.Printf("🧠 Collective synthesis complete: own + %d peers → %s\n",
		len(peersWithContent), cs.collectivePath)

	return nil
}

// llmSynthesize uses the LLM to merge own + peer learnings.
func (cs *CollectiveSynthesizer) llmSynthesize(ctx context.Context, own string, peers []*PeerDigest) (string, error) {
	var sb strings.Builder
	sb.WriteString("## My Active Learnings\n\n")
	if own != "" {
		sb.WriteString(truncate(own, 3000))
	} else {
		sb.WriteString("(none yet)\n")
	}
	sb.WriteString("\n\n")

	for _, p := range peers {
		name := p.AgentName
		if name == "" {
			name = p.AgentID[:8]
		}
		sb.WriteString(fmt.Sprintf("## Peer: %s (received %s)\n\n", name, p.Timestamp.Format("01-02 15:04")))
		sb.WriteString(truncate(p.Content, 2000))
		sb.WriteString("\n\n")
	}

	prompt := fmt.Sprintf(`You are a collective memory synthesizer for a swarm of AI agents.
Below are learnings from myself and %d peer agents. Your job:

1. Identify shared patterns — things multiple agents learned independently
2. Highlight unique insights — valuable knowledge only one agent discovered  
3. Resolve contradictions — when agents learned opposite lessons, note both perspectives
4. Remove redundancy — merge duplicate information
5. Preserve attribution — note which agent(s) contributed each insight

Output a well-structured Markdown document titled "# Collective Learnings".
Use sections: ## Shared Patterns, ## Unique Insights, ## Open Questions, ## Contributor Summary.
Be concise but preserve all actionable knowledge.

---

%s`, len(peers), sb.String())

	resp, err := cs.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: "You synthesize collective knowledge from multiple AI agents. Output only Markdown."},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return "", err
	}

	// Add metadata header
	header := fmt.Sprintf("<!-- Collective synthesis: %s | Agent: %s | Peers: %d -->\n\n",
		time.Now().Format(time.RFC3339), cs.agentID, len(peers))
	return header + resp.Content + "\n", nil
}

// fallbackSynthesize produces collective learnings without LLM.
func (cs *CollectiveSynthesizer) fallbackSynthesize(own string, peers []*PeerDigest) string {
	var sb strings.Builder
	sb.WriteString("# Collective Learnings\n\n")
	sb.WriteString(fmt.Sprintf("> Synthesized at %s | Agent: %s | Peers: %d\n\n",
		time.Now().Format(time.RFC3339), cs.agentID, len(peers)))

	sb.WriteString("## My Learnings\n\n")
	if own != "" {
		sb.WriteString(truncate(own, 2000))
	} else {
		sb.WriteString("_No active learnings yet._\n")
	}
	sb.WriteString("\n\n")

	sb.WriteString("## Peer Learnings\n\n")
	for _, p := range peers {
		name := p.AgentName
		if name == "" {
			name = p.AgentID[:8]
		}
		sb.WriteString(fmt.Sprintf("### %s (received %s)\n\n", name, p.Timestamp.Format("01-02 15:04")))
		sb.WriteString(truncate(p.Content, 1000))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// ────────────────────────────────────────────────────────────────────────────
// Status / persistence
// ────────────────────────────────────────────────────────────────────────────

// Status returns the current collective synthesis status.
type CollectiveSynthesisStatus struct {
	PeerDigests   int       `json:"peer_digests"`
	LastSynthesis time.Time `json:"last_synthesis"`
	LastPublish   time.Time `json:"last_publish"`
}

func (cs *CollectiveSynthesizer) Status() CollectiveSynthesisStatus {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return CollectiveSynthesisStatus{
		PeerDigests:   len(cs.peerDigests),
		LastSynthesis: cs.lastSynthesis,
		LastPublish:   cs.lastPublish,
	}
}

// Interval returns the synthesis interval as a Duration.
func (cs *CollectiveSynthesizer) Interval() time.Duration {
	return time.Duration(cs.cfg.IntervalHours) * time.Hour
}

// CollectiveLearnings reads the collective_learnings.md content.
func (cs *CollectiveSynthesizer) CollectiveLearnings() (string, error) {
	data, err := os.ReadFile(cs.collectivePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ────────────────────────────────────────────────────────────────────────────
// Internal
// ────────────────────────────────────────────────────────────────────────────

func (cs *CollectiveSynthesizer) evictOldest() {
	var oldest string
	var oldestTime time.Time
	for id, p := range cs.peerDigests {
		if oldest == "" || p.Timestamp.Before(oldestTime) {
			oldest = id
			oldestTime = p.Timestamp
		}
	}
	if oldest != "" {
		delete(cs.peerDigests, oldest)
	}
}

func (cs *CollectiveSynthesizer) savePeerDigestsLocked() {
	dir := filepath.Dir(cs.peerDigestsPath)
	os.MkdirAll(dir, 0755)
	data, err := json.MarshalIndent(cs.peerDigests, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(cs.peerDigestsPath, data, 0644)
}

func (cs *CollectiveSynthesizer) loadPeerDigests() {
	data, err := os.ReadFile(cs.peerDigestsPath)
	if err != nil {
		return
	}
	var digests map[string]*PeerDigest
	if json.Unmarshal(data, &digests) == nil && digests != nil {
		cs.peerDigests = digests
		if len(digests) > 0 {
			fmt.Printf("🧠 Loaded %d peer memory digests from disk\n", len(digests))
		}
	}
}
