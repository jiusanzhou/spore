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

package network

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	contentProtocol = "/spore/content/1.0.0"
	maxContentSize  = 1 << 20 // 1MB per content item
)

// ContentRef is a reference to content stored in the collective memory.
// Broadcast this via GossipSub — peers fetch the actual data on demand.
type ContentRef struct {
	CID       string `json:"cid"`       // SHA-256 hex of content
	AgentID   string `json:"agent_id"`  // who published
	Type      string `json:"type"`      // "experience_digest", "experience_pack", etc.
	Size      int    `json:"size"`      // byte size
	Timestamp int64  `json:"timestamp"`
	Summary   string `json:"summary,omitempty"` // human-readable one-liner
}

// contentItem is stored locally.
type contentItem struct {
	Data      []byte
	Ref       ContentRef
	PinnedAt  time.Time
	AccessCnt int
}

// ContentStore is an in-process content-addressed store
// backed by libp2p for peer-to-peer fetching.
// Each Spore node stores content it has published or fetched.
// Content is addressed by SHA-256 hash (CID).
type ContentStore struct {
	bus   *P2PBus
	items map[string]*contentItem // CID → item
	mu    sync.RWMutex

	// Index: who has what. Populated via GossipSub announcements.
	providers map[string]map[peer.ID]struct{} // CID → set of peer IDs that have it
	provMu    sync.RWMutex
}

// NewContentStore creates a content store attached to a P2PBus.
func NewContentStore(bus *P2PBus) *ContentStore {
	cs := &ContentStore{
		bus:       bus,
		items:     make(map[string]*contentItem),
		providers: make(map[string]map[peer.ID]struct{}),
	}

	// Register libp2p stream handler for content fetch requests.
	bus.host.SetStreamHandler(protocol.ID(contentProtocol), cs.handleFetchStream)

	return cs
}

// Put stores content and returns its CID (SHA-256 hex).
func (cs *ContentStore) Put(data []byte, contentType, agentID, summary string) (*ContentRef, error) {
	if len(data) > maxContentSize {
		return nil, fmt.Errorf("content too large: %d > %d", len(data), maxContentSize)
	}

	hash := sha256.Sum256(data)
	cid := hex.EncodeToString(hash[:])

	ref := ContentRef{
		CID:       cid,
		AgentID:   agentID,
		Type:      contentType,
		Size:      len(data),
		Timestamp: time.Now().Unix(),
		Summary:   summary,
	}

	cs.mu.Lock()
	cs.items[cid] = &contentItem{
		Data:     data,
		Ref:      ref,
		PinnedAt: time.Now(),
	}
	cs.mu.Unlock()

	// Register self as provider.
	cs.provMu.Lock()
	if cs.providers[cid] == nil {
		cs.providers[cid] = make(map[peer.ID]struct{})
	}
	cs.providers[cid][cs.bus.host.ID()] = struct{}{}
	cs.provMu.Unlock()

	return &ref, nil
}

// Get retrieves content by CID. Checks local store first, then fetches from peers.
func (cs *ContentStore) Get(cid string) ([]byte, error) {
	// Local hit?
	cs.mu.RLock()
	item, ok := cs.items[cid]
	cs.mu.RUnlock()
	if ok {
		cs.mu.Lock()
		item.AccessCnt++
		cs.mu.Unlock()
		return item.Data, nil
	}

	// Fetch from a peer that has it.
	return cs.fetchFromPeer(cid)
}

// Has checks if content is available locally.
func (cs *ContentStore) Has(cid string) bool {
	cs.mu.RLock()
	_, ok := cs.items[cid]
	cs.mu.RUnlock()
	return ok
}

// RegisterProvider records that a peer has specific content.
// Called when receiving a ContentRef via GossipSub.
func (cs *ContentStore) RegisterProvider(cid string, peerID peer.ID) {
	cs.provMu.Lock()
	defer cs.provMu.Unlock()
	if cs.providers[cid] == nil {
		cs.providers[cid] = make(map[peer.ID]struct{})
	}
	cs.providers[cid][peerID] = struct{}{}
}

// ListRefs returns all locally stored content references.
func (cs *ContentStore) ListRefs() []ContentRef {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	refs := make([]ContentRef, 0, len(cs.items))
	for _, item := range cs.items {
		refs = append(refs, item.Ref)
	}
	return refs
}

// Stats returns store statistics.
func (cs *ContentStore) Stats() map[string]interface{} {
	cs.mu.RLock()
	itemCount := len(cs.items)
	var totalSize int
	for _, item := range cs.items {
		totalSize += len(item.Data)
	}
	cs.mu.RUnlock()

	cs.provMu.RLock()
	providerCount := len(cs.providers)
	cs.provMu.RUnlock()

	return map[string]interface{}{
		"items":     itemCount,
		"total_bytes": totalSize,
		"providers": providerCount,
	}
}

// fetchFromPeer tries to fetch content from a known provider.
func (cs *ContentStore) fetchFromPeer(cid string) ([]byte, error) {
	// Find a provider.
	cs.provMu.RLock()
	providers, ok := cs.providers[cid]
	cs.provMu.RUnlock()
	if !ok || len(providers) == 0 {
		return nil, fmt.Errorf("no providers for %s", cid)
	}

	selfID := cs.bus.host.ID()
	for peerID := range providers {
		if peerID == selfID {
			continue
		}
		data, err := cs.fetchFromSinglePeer(cid, peerID)
		if err != nil {
			continue // try next provider
		}

		// Verify CID.
		hash := sha256.Sum256(data)
		got := hex.EncodeToString(hash[:])
		if got != cid {
			fmt.Printf("⚠️  content hash mismatch from %s: expected %s, got %s\n",
				peerID.String()[:8], cid[:8], got[:8])
			continue
		}

		// Cache locally.
		cs.mu.Lock()
		cs.items[cid] = &contentItem{
			Data:     data,
			PinnedAt: time.Now(),
			Ref: ContentRef{
				CID:  cid,
				Size: len(data),
			},
		}
		cs.mu.Unlock()

		return data, nil
	}

	return nil, fmt.Errorf("all providers failed for %s", cid)
}

func (cs *ContentStore) fetchFromSinglePeer(cid string, peerID peer.ID) ([]byte, error) {
	ctx := cs.bus.ctx
	s, err := cs.bus.host.NewStream(ctx, peerID, protocol.ID(contentProtocol))
	if err != nil {
		return nil, fmt.Errorf("open stream to %s: %w", peerID.String()[:8], err)
	}
	defer s.Close()

	// Send CID as request.
	req := []byte(cid + "\n")
	if _, err := s.Write(req); err != nil {
		return nil, err
	}
	s.CloseWrite()

	// Read response.
	data, err := io.ReadAll(io.LimitReader(s, int64(maxContentSize)))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	return data, nil
}

// handleFetchStream responds to incoming content fetch requests.
func (cs *ContentStore) handleFetchStream(s network.Stream) {
	defer s.Close()

	// Read CID request (format: "CID\n").
	buf := make([]byte, 128)
	n, err := s.Read(buf)
	if err != nil && err != io.EOF {
		return
	}

	cid := string(buf[:n])
	// Trim newline.
	if len(cid) > 0 && cid[len(cid)-1] == '\n' {
		cid = cid[:len(cid)-1]
	}

	cs.mu.RLock()
	item, ok := cs.items[cid]
	cs.mu.RUnlock()

	if !ok {
		s.Write([]byte{}) // empty = not found
		return
	}

	cs.mu.Lock()
	item.AccessCnt++
	cs.mu.Unlock()

	s.Write(item.Data)
}

// PutJSON is a convenience wrapper that marshals v to JSON, stores it,
// and returns the ContentRef.
func (cs *ContentStore) PutJSON(v interface{}, contentType, agentID, summary string) (*ContentRef, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshaling content: %w", err)
	}
	return cs.Put(data, contentType, agentID, summary)
}

// GetJSON fetches content by CID and unmarshals into v.
func (cs *ContentStore) GetJSON(cid string, v interface{}) error {
	data, err := cs.Get(cid)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
