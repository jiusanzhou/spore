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
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	libp2pnet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"

	proto "go.zoe.im/spore/internal/protocol"
)

const (
	protocolID     = "/spore/msg/1.0.0"
	broadcastTopic = "spore-broadcast"
	rendezvous     = "spore-network"
	maxMessageSize = 1 << 20 // 1 MB
)

// P2PConfig holds configuration for the P2P bus.
type P2PConfig struct {
	ListenAddrs    []string
	BootstrapPeers []string
	PrivateKey     ed25519.PrivateKey
}

// P2PBus implements Bus using libp2p for cross-node agent communication.
type P2PBus struct {
	host    host.Host
	dht     *dht.IpfsDHT
	ps      *pubsub.PubSub
	topic   *pubsub.Topic
	sub     *pubsub.Subscription
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.RWMutex
	handlers map[string]Handler
	peerMap  map[string]peer.ID // agent ID -> peer ID
}

// NewP2PBus creates a new libp2p-backed message bus.
func NewP2PBus(cfg P2PConfig) (*P2PBus, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Convert ed25519 private key to libp2p crypto key.
	privKey, err := crypto.UnmarshalEd25519PrivateKey(cfg.PrivateKey)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("convert private key: %w", err)
	}

	// Parse listen addresses.
	listenAddrs := cfg.ListenAddrs
	if len(listenAddrs) == 0 {
		listenAddrs = []string{
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
		}
	}

	addrs := make([]ma.Multiaddr, 0, len(listenAddrs))
	for _, s := range listenAddrs {
		a, err := ma.NewMultiaddr(s)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("parse listen addr %q: %w", s, err)
		}
		addrs = append(addrs, a)
	}

	// Create libp2p host.
	h, err := libp2p.New(
		libp2p.Identity(privKey),
		libp2p.ListenAddrs(addrs...),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	// Initialize Kademlia DHT.
	kadDHT, err := dht.New(ctx, h, dht.Mode(dht.ModeAutoServer))
	if err != nil {
		h.Close()
		cancel()
		return nil, fmt.Errorf("create DHT: %w", err)
	}
	if err := kadDHT.Bootstrap(ctx); err != nil {
		h.Close()
		cancel()
		return nil, fmt.Errorf("bootstrap DHT: %w", err)
	}

	// Initialize GossipSub.
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		kadDHT.Close()
		h.Close()
		cancel()
		return nil, fmt.Errorf("create pubsub: %w", err)
	}

	// Join broadcast topic.
	topic, err := ps.Join(broadcastTopic)
	if err != nil {
		kadDHT.Close()
		h.Close()
		cancel()
		return nil, fmt.Errorf("join topic: %w", err)
	}

	sub, err := topic.Subscribe()
	if err != nil {
		topic.Close()
		kadDHT.Close()
		h.Close()
		cancel()
		return nil, fmt.Errorf("subscribe topic: %w", err)
	}

	bus := &P2PBus{
		host:     h,
		dht:      kadDHT,
		ps:       ps,
		topic:    topic,
		sub:      sub,
		ctx:      ctx,
		cancel:   cancel,
		handlers: make(map[string]Handler),
		peerMap:  make(map[string]peer.ID),
	}

	// Register stream handler for point-to-point messages.
	h.SetStreamHandler(protocol.ID(protocolID), bus.handleStream)

	// Connect to bootstrap peers in background.
	go bus.connectBootstrapPeers(cfg.BootstrapPeers)

	// Start mDNS discovery for local network.
	mdnsSvc := mdns.NewMdnsService(h, rendezvous, bus)
	if err := mdnsSvc.Start(); err != nil {
		fmt.Printf("warning: mDNS start failed: %v\n", err)
	}

	// Start broadcast subscription reader.
	go bus.readBroadcast()

	return bus, nil
}

// HandlePeerFound implements mdns.Notifee for local peer discovery.
func (b *P2PBus) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID == b.host.ID() {
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()
	if err := b.host.Connect(ctx, pi); err != nil {
		fmt.Printf("warning: mDNS connect to %s failed: %v\n", pi.ID.String(), err)
	}
}

// connectBootstrapPeers dials bootstrap peers in background.
func (b *P2PBus) connectBootstrapPeers(peers []string) {
	for _, s := range peers {
		addr, err := ma.NewMultiaddr(s)
		if err != nil {
			fmt.Printf("warning: parse bootstrap addr %q: %v\n", s, err)
			continue
		}
		pi, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			fmt.Printf("warning: parse bootstrap peer %q: %v\n", s, err)
			continue
		}
		ctx, cancel := context.WithTimeout(b.ctx, 15*time.Second)
		if err := b.host.Connect(ctx, *pi); err != nil {
			fmt.Printf("warning: bootstrap connect to %s: %v\n", pi.ID.String(), err)
		}
		cancel()
	}
}

// readBroadcast reads from the pubsub subscription and dispatches to all handlers.
func (b *P2PBus) readBroadcast() {
	for {
		msg, err := b.sub.Next(b.ctx)
		if err != nil {
			return // context cancelled or subscription closed
		}
		// Skip messages from ourselves.
		if msg.ReceivedFrom == b.host.ID() {
			continue
		}
		var m proto.Message
		if err := json.Unmarshal(msg.Data, &m); err != nil {
			fmt.Printf("warning: unmarshal broadcast: %v\n", err)
			continue
		}
		b.mu.RLock()
		for id, handler := range b.handlers {
			if id != m.From {
				if err := handler(&m); err != nil {
					fmt.Printf("warning: broadcast handler %s: %v\n", id, err)
				}
			}
		}
		b.mu.RUnlock()
	}
}

// handleStream handles incoming point-to-point message streams.
func (b *P2PBus) handleStream(s libp2pnet.Stream) {
	defer s.Close()

	data, err := io.ReadAll(io.LimitReader(s, maxMessageSize))
	if err != nil {
		fmt.Printf("warning: read stream: %v\n", err)
		return
	}

	var msg proto.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		fmt.Printf("warning: unmarshal stream message: %v\n", err)
		return
	}

	b.mu.RLock()
	handler, ok := b.handlers[msg.To]
	b.mu.RUnlock()

	if !ok {
		fmt.Printf("warning: no handler for agent %s\n", msg.To)
		return
	}
	if err := handler(&msg); err != nil {
		fmt.Printf("warning: handler %s: %v\n", msg.To, err)
	}
}

func (b *P2PBus) Send(msg *proto.Message) error {
	if msg.To == "broadcast" {
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("marshal broadcast: %w", err)
		}
		return b.topic.Publish(b.ctx, data)
	}

	// Point-to-point: resolve agent ID to peer ID.
	peerID, err := b.resolvePeer(msg.To)
	if err != nil {
		// Try local handlers first (agent might be on same node).
		b.mu.RLock()
		handler, ok := b.handlers[msg.To]
		b.mu.RUnlock()
		if ok {
			return handler(msg)
		}
		return fmt.Errorf("resolve peer for agent %s: %w", msg.To, err)
	}

	// Open stream and send.
	s, err := b.host.NewStream(b.ctx, peerID, protocol.ID(protocolID))
	if err != nil {
		return fmt.Errorf("open stream to %s: %w", peerID.String(), err)
	}
	defer s.Close()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	if _, err := s.Write(data); err != nil {
		return fmt.Errorf("write stream: %w", err)
	}
	return nil
}

// resolvePeer looks up a peer ID for the given agent ID.
// Agents must be registered via RegisterPeer or discovered through the
// capability advertisement protocol before they can be reached.
func (b *P2PBus) resolvePeer(agentID string) (peer.ID, error) {
	b.mu.RLock()
	pid, ok := b.peerMap[agentID]
	b.mu.RUnlock()
	if ok {
		return pid, nil
	}
	return "", fmt.Errorf("agent %s not found in peer map", agentID)
}

// RegisterPeer maps an agent ID to a libp2p peer ID for routing.
func (b *P2PBus) RegisterPeer(agentID string, peerID peer.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.peerMap[agentID] = peerID
}

func (b *P2PBus) Subscribe(agentID string, handler Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[agentID] = handler
	return nil
}

func (b *P2PBus) Unsubscribe(agentID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.handlers, agentID)
	delete(b.peerMap, agentID)
	return nil
}

func (b *P2PBus) Close() error {
	b.cancel()
	b.sub.Cancel()
	b.topic.Close()
	b.dht.Close()
	return b.host.Close()
}

// PeerID returns this node's libp2p peer ID.
func (b *P2PBus) PeerID() string {
	return b.host.ID().String()
}

// ConnectedPeers returns the peer IDs of all connected peers.
func (b *P2PBus) ConnectedPeers() []string {
	peers := b.host.Network().Peers()
	ids := make([]string, len(peers))
	for i, p := range peers {
		ids[i] = p.String()
	}
	return ids
}

// Agents returns the list of registered agent IDs.
func (b *P2PBus) Agents() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	ids := make([]string, 0, len(b.handlers))
	for id := range b.handlers {
		ids = append(ids, id)
	}
	return ids
}

// Connect dials a peer by multiaddr string.
func (b *P2PBus) Connect(addr string) error {
	maddr, err := ma.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("parse multiaddr %q: %w", addr, err)
	}
	pi, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("parse peer addr %q: %w", addr, err)
	}
	ctx, cancel := context.WithTimeout(b.ctx, 15*time.Second)
	defer cancel()
	return b.host.Connect(ctx, *pi)
}
