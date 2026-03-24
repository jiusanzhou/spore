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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"go.zoe.im/spore/internal/network/ipfsnode"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	mh "github.com/multiformats/go-multihash"
)

const (
	contentProtocol = "/spore/content/1.0.0"
	maxContentSize  = 1 << 20 // 1MB per content item
)

// ContentRef is a reference to content stored in the collective memory.
// Broadcast this via GossipSub — peers fetch the actual data on demand.
type ContentRef struct {
	CID       string `json:"cid"`                  // SHA-256 hex of content
	IPFSCID   string `json:"ipfs_cid,omitempty"`   // IPFS CIDv1 (Bitswap-addressable)
	AgentID   string `json:"agent_id"`             // who published
	Type      string `json:"type"`                 // "experience_digest", "experience_pack", etc.
	Size      int    `json:"size"`                 // byte size
	Timestamp int64  `json:"timestamp"`
	Summary   string `json:"summary,omitempty"`    // human-readable one-liner
}

// ContentStore is a content-addressed store backed by SQLite + IPFS + libp2p.
// Local content is persisted to disk via SQLite and published to IPFS for
// global addressability. Remote content is fetched via Bitswap or libp2p streams.
type ContentStore struct {
	bus  *P2PBus
	db   *ContentDB         // persistent storage (nil = in-memory only)
	ipfs *ipfsnode.Node     // embedded IPFS node (nil = disabled)

	// Hot cache: recently accessed items kept in memory for fast access.
	cache   map[string][]byte
	cacheMu sync.RWMutex

	// Index: who has what. Populated via GossipSub announcements.
	providers map[string]map[peer.ID]struct{} // CID → set of peer IDs
	provMu    sync.RWMutex
}

// NewContentStore creates a content store attached to a P2PBus.
// If dataDir is non-empty, content is persisted to SQLite.
func NewContentStore(bus *P2PBus, dataDir string) *ContentStore {
	cs := &ContentStore{
		bus:       bus,
		cache:     make(map[string][]byte),
		providers: make(map[string]map[peer.ID]struct{}),
	}

	// Try to open persistent DB.
	if dataDir != "" {
		db, err := NewContentDB(dataDir)
		if err != nil {
			fmt.Printf("⚠️  Content store: failed to open DB at %s: %v (using memory only)\n", dataDir, err)
		} else {
			cs.db = db
			// Pre-warm provider index from DB.
			for _, ref := range db.ListRefs() {
				if cs.providers[ref.CID] == nil {
					cs.providers[ref.CID] = make(map[peer.ID]struct{})
				}
				cs.providers[ref.CID][bus.host.ID()] = struct{}{}
			}
		}
	}

	// Register libp2p stream handler for content fetch requests.
	bus.host.SetStreamHandler(protocol.ID(contentProtocol), cs.handleFetchStream)

	// Initialize embedded IPFS node (Bitswap).
	ipfsNode, err := ipfsnode.New(ipfsnode.Config{
		Host: bus.host,
	})
	if err != nil {
		fmt.Printf("⚠️  Content store: IPFS node init failed: %v (Bitswap disabled)\n", err)
	} else {
		cs.ipfs = ipfsNode
		fmt.Printf("📦 IPFS node enabled (peer: %s)\n", bus.host.ID().String()[:16])
	}

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

	// Publish to IPFS (Bitswap-addressable).
	if cs.ipfs != nil {
		ipfsCID, err := cs.ipfs.AddRaw(data)
		if err != nil {
			fmt.Printf("⚠️  IPFS publish failed for %s: %v\n", cid[:12], err)
		} else {
			ref.IPFSCID = ipfsCID.String()
		}
	}

	// Persist to DB (with IPFS CID if available).
	if cs.db != nil {
		if err := cs.db.Put(cid, data, ref); err != nil {
			fmt.Printf("⚠️  Content store: DB write failed for %s: %v\n", cid[:12], err)
		}
	}

	// Hot cache.
	cs.cacheMu.Lock()
	cs.cache[cid] = data
	cs.cacheMu.Unlock()

	// Register self as provider.
	cs.provMu.Lock()
	if cs.providers[cid] == nil {
		cs.providers[cid] = make(map[peer.ID]struct{})
	}
	cs.providers[cid][cs.bus.host.ID()] = struct{}{}
	cs.provMu.Unlock()

	return &ref, nil
}

// Get retrieves content by CID. Checks: hot cache → DB → peer fetch.
func (cs *ContentStore) Get(cid string) ([]byte, error) {
	// 1. Hot cache.
	cs.cacheMu.RLock()
	data, ok := cs.cache[cid]
	cs.cacheMu.RUnlock()
	if ok {
		return data, nil
	}

	// 2. Persistent DB.
	if cs.db != nil {
		data, _, err := cs.db.Get(cid)
		if err == nil && data != nil {
			// Re-warm cache.
			cs.cacheMu.Lock()
			cs.cache[cid] = data
			cs.cacheMu.Unlock()
			return data, nil
		}
	}

	// 3. IPFS Bitswap (convert SHA-256 hex to IPFS CID).
	if cs.ipfs != nil {
		if ipfsCID, err := sha256HexToIPFSCID(cid); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			data, err := cs.ipfs.Get(ctx, ipfsCID)
			if err == nil {
				// Verify.
				hash := sha256.Sum256(data)
				if hex.EncodeToString(hash[:]) == cid {
					// Persist locally.
					cs.cacheMu.Lock()
					cs.cache[cid] = data
					cs.cacheMu.Unlock()
					if cs.db != nil {
						cs.db.Put(cid, data, ContentRef{CID: cid, Size: len(data), Timestamp: time.Now().Unix()})
					}
					return data, nil
				}
			}
		}
	}

	// 4. Fetch from a peer (legacy libp2p protocol).
	return cs.fetchFromPeer(cid)
}

// Has checks if content is available locally.
func (cs *ContentStore) Has(cid string) bool {
	cs.cacheMu.RLock()
	_, ok := cs.cache[cid]
	cs.cacheMu.RUnlock()
	if ok {
		return true
	}
	if cs.db != nil {
		return cs.db.Has(cid)
	}
	return false
}

// RegisterProvider records that a peer has specific content.
func (cs *ContentStore) RegisterProvider(cid string, peerID peer.ID) {
	cs.provMu.Lock()
	defer cs.provMu.Unlock()
	if cs.providers[cid] == nil {
		cs.providers[cid] = make(map[peer.ID]struct{})
	}
	cs.providers[cid][peerID] = struct{}{}
}

// RegisterProviderByAgent also registers self when the agent is in the same process.
func (cs *ContentStore) RegisterProviderByAgent(cid string) {
	cs.RegisterProvider(cid, cs.bus.host.ID())
}

// ListRefs returns all content references.
// Prefers DB (persistent, complete); falls back to cache keys.
func (cs *ContentStore) ListRefs() []ContentRef {
	if cs.db != nil {
		return cs.db.ListRefs()
	}
	// Fallback: return minimal refs from cache.
	cs.cacheMu.RLock()
	defer cs.cacheMu.RUnlock()
	refs := make([]ContentRef, 0, len(cs.cache))
	for cid, data := range cs.cache {
		refs = append(refs, ContentRef{CID: cid, Size: len(data)})
	}
	return refs
}

// Stats returns store statistics.
func (cs *ContentStore) Stats() map[string]interface{} {
	cs.provMu.RLock()
	providerCount := len(cs.providers)
	cs.provMu.RUnlock()

	if cs.db != nil {
		items, totalBytes := cs.db.Stats()
		cs.cacheMu.RLock()
		cacheItems := len(cs.cache)
		cs.cacheMu.RUnlock()
		return map[string]interface{}{
			"items":       items,
			"total_bytes": totalBytes,
			"cache_items": cacheItems,
			"providers":   providerCount,
			"persistent":  true,
		}
	}

	cs.cacheMu.RLock()
	itemCount := len(cs.cache)
	var totalSize int
	for _, data := range cs.cache {
		totalSize += len(data)
	}
	cs.cacheMu.RUnlock()

	return map[string]interface{}{
		"items":       itemCount,
		"total_bytes": totalSize,
		"providers":   providerCount,
		"persistent":  false,
	}
}

// Close releases persistent resources.
func (cs *ContentStore) Close() error {
	if cs.ipfs != nil {
		cs.ipfs.Close()
	}
	if cs.db != nil {
		return cs.db.Close()
	}
	return nil
}

// sha256HexToIPFSCID converts a SHA-256 hex string to an IPFS CIDv1.
func sha256HexToIPFSCID(hexStr string) (cid.Cid, error) {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		return cid.Undef, err
	}
	hash, err := mh.Encode(raw, mh.SHA2_256)
	if err != nil {
		return cid.Undef, err
	}
	return cid.NewCidV1(cid.Raw, hash), nil
}

// fetchFromPeer tries to fetch content from a known provider.
func (cs *ContentStore) fetchFromPeer(cid string) ([]byte, error) {
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
			continue
		}

		// Verify CID.
		hash := sha256.Sum256(data)
		got := hex.EncodeToString(hash[:])
		if got != cid {
			fmt.Printf("⚠️  content hash mismatch from %s: expected %s, got %s\n",
				peerID.String()[:8], cid[:8], got[:8])
			continue
		}

		// Persist locally.
		ref := ContentRef{CID: cid, Size: len(data), Timestamp: time.Now().Unix()}
		if cs.db != nil {
			cs.db.Put(cid, data, ref)
		}
		cs.cacheMu.Lock()
		cs.cache[cid] = data
		cs.cacheMu.Unlock()

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

	req := []byte(cid + "\n")
	if _, err := s.Write(req); err != nil {
		return nil, err
	}
	s.CloseWrite()

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

	buf := make([]byte, 128)
	n, err := s.Read(buf)
	if err != nil && err != io.EOF {
		return
	}

	cid := string(buf[:n])
	if len(cid) > 0 && cid[len(cid)-1] == '\n' {
		cid = cid[:len(cid)-1]
	}

	// Try cache first, then DB.
	cs.cacheMu.RLock()
	data, ok := cs.cache[cid]
	cs.cacheMu.RUnlock()

	if !ok && cs.db != nil {
		data, _, _ = cs.db.Get(cid)
	}

	if data == nil {
		s.Write([]byte{})
		return
	}

	s.Write(data)
}

// PutJSON marshals v to JSON, stores it, and returns the ContentRef.
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
