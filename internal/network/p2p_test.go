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
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"go.zoe.im/spore/internal/protocol"
)

func generateKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv
}

func newTestP2PBus(t *testing.T) *P2PBus {
	t.Helper()
	bus, err := NewP2PBus(P2PConfig{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		PrivateKey:  generateKey(t),
	})
	if err != nil {
		t.Fatalf("NewP2PBus: %v", err)
	}
	t.Cleanup(func() { bus.Close() })
	return bus
}

// connectWithRetry retries Connect up to 3 times to handle mDNS/TLS races.
func connectWithRetry(t *testing.T, bus *P2PBus, addr string) {
	t.Helper()
	var err error
	for i := 0; i < 3; i++ {
		err = bus.Connect(addr)
		if err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("Connect (after retries): %v", err)
}

func peerAddr(bus *P2PBus) string {
	addrs := bus.host.Addrs()
	return fmt.Sprintf("%s/p2p/%s", addrs[0].String(), bus.host.ID().String())
}

func TestP2PBus_PeerID(t *testing.T) {
	bus := newTestP2PBus(t)
	pid := bus.PeerID()
	if pid == "" {
		t.Fatal("expected non-empty peer ID")
	}
}

func TestP2PBus_SubscribeUnsubscribe(t *testing.T) {
	bus := newTestP2PBus(t)

	bus.Subscribe("agent-a", func(msg *protocol.Message) error { return nil })
	bus.Subscribe("agent-b", func(msg *protocol.Message) error { return nil })

	agents := bus.Agents()
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}

	bus.Unsubscribe("agent-a")
	agents = bus.Agents()
	if len(agents) != 1 {
		t.Errorf("expected 1 agent after unsubscribe, got %d", len(agents))
	}
}

func TestP2PBus_LocalDelivery(t *testing.T) {
	bus := newTestP2PBus(t)

	received := make(chan *protocol.Message, 1)
	bus.Subscribe("agent-a", func(msg *protocol.Message) error {
		received <- msg
		return nil
	})

	msg, _ := protocol.NewMessage("agent-b", "agent-a", protocol.MsgHeartbeat, map[string]string{"test": "local"})
	if err := bus.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-received:
		if got.From != "agent-b" {
			t.Errorf("expected from 'agent-b', got %q", got.From)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for local delivery")
	}
}

func TestP2PBus_TwoNodeDiscovery(t *testing.T) {
	bus1 := newTestP2PBus(t)
	bus2 := newTestP2PBus(t)

	connectWithRetry(t, bus2, peerAddr(bus1))
	time.Sleep(500 * time.Millisecond)

	peers := bus2.ConnectedPeers()
	if len(peers) == 0 {
		t.Fatal("bus2 should have at least one connected peer")
	}

	found := false
	for _, p := range peers {
		if p == bus1.PeerID() {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("bus1 peer ID %s not in bus2's connected peers: %v", bus1.PeerID(), peers)
	}
}

func TestP2PBus_TwoNodeMessageExchange(t *testing.T) {
	bus1 := newTestP2PBus(t)
	bus2 := newTestP2PBus(t)

	connectWithRetry(t, bus2, peerAddr(bus1))
	time.Sleep(500 * time.Millisecond)

	var received atomic.Int32
	bus1.Subscribe("agent-on-node1", func(msg *protocol.Message) error {
		received.Add(1)
		return nil
	})

	bus2.RegisterPeer("agent-on-node1", bus1.host.ID())

	msg, _ := protocol.NewMessage("agent-on-node2", "agent-on-node1", protocol.MsgTaskRequest,
		protocol.TaskRequest{Description: "cross-node task"})
	if err := bus2.Send(msg); err != nil {
		t.Fatalf("Send P2P: %v", err)
	}

	time.Sleep(time.Second)
	if received.Load() != 1 {
		t.Errorf("expected 1 message on bus1, got %d", received.Load())
	}
}

func TestP2PBus_BroadcastAcrossNodes(t *testing.T) {
	bus1 := newTestP2PBus(t)
	bus2 := newTestP2PBus(t)

	connectWithRetry(t, bus2, peerAddr(bus1))
	time.Sleep(time.Second) // GossipSub needs time to mesh

	var count1 atomic.Int32
	bus1.Subscribe("agent-a", func(msg *protocol.Message) error {
		count1.Add(1)
		return nil
	})

	var count2 atomic.Int32
	bus2.Subscribe("agent-b", func(msg *protocol.Message) error {
		count2.Add(1)
		return nil
	})

	msg, _ := protocol.NewMessage("agent-a", "broadcast", protocol.MsgHeartbeat,
		map[string]string{"status": "alive"})
	if err := bus1.Send(msg); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}

	time.Sleep(2 * time.Second)

	if count1.Load() != 0 {
		t.Errorf("sender should not receive own broadcast, got %d", count1.Load())
	}
	if count2.Load() != 1 {
		t.Errorf("expected 1 broadcast on bus2, got %d", count2.Load())
	}
}
