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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.zoe.im/spore/internal/protocol"
)

func TestLocalBus_DirectMessage(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	received := make(chan *protocol.Message, 1)
	bus.Subscribe("agent-a", func(msg *protocol.Message) error {
		received <- msg
		return nil
	})

	msg, _ := protocol.NewMessage("agent-b", "agent-a", protocol.MsgHeartbeat, map[string]string{"hello": "world"})
	if err := bus.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-received:
		if got.From != "agent-b" {
			t.Errorf("expected from 'agent-b', got %q", got.From)
		}
		if got.To != "agent-a" {
			t.Errorf("expected to 'agent-a', got %q", got.To)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestLocalBus_Broadcast(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	var countA, countB atomic.Int32

	bus.Subscribe("agent-a", func(msg *protocol.Message) error {
		countA.Add(1)
		return nil
	})
	bus.Subscribe("agent-b", func(msg *protocol.Message) error {
		countB.Add(1)
		return nil
	})

	// Broadcast from agent-a — should reach agent-b but not agent-a
	msg, _ := protocol.NewMessage("agent-a", "broadcast", protocol.MsgHeartbeat, nil)
	if err := bus.Send(msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if countA.Load() != 0 {
		t.Errorf("sender should not receive own broadcast, got %d", countA.Load())
	}
	if countB.Load() != 1 {
		t.Errorf("expected 1 message for agent-b, got %d", countB.Load())
	}
}

func TestLocalBus_SendToUnknown(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	msg, _ := protocol.NewMessage("agent-a", "nonexistent", protocol.MsgHeartbeat, nil)
	err := bus.Send(msg)
	if err == nil {
		t.Fatal("expected error sending to unknown agent")
	}
}

func TestLocalBus_Unsubscribe(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	var count atomic.Int32
	bus.Subscribe("agent-a", func(msg *protocol.Message) error {
		count.Add(1)
		return nil
	})

	bus.Unsubscribe("agent-a")

	msg, _ := protocol.NewMessage("agent-b", "agent-a", protocol.MsgHeartbeat, nil)
	err := bus.Send(msg)
	if err == nil {
		t.Fatal("expected error sending to unsubscribed agent")
	}
}

func TestLocalBus_Agents(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	bus.Subscribe("agent-a", func(msg *protocol.Message) error { return nil })
	bus.Subscribe("agent-b", func(msg *protocol.Message) error { return nil })

	agents := bus.Agents()
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}

func TestLocalBus_ConcurrentSend(t *testing.T) {
	bus := NewLocalBus()
	defer bus.Close()

	var count atomic.Int32
	bus.Subscribe("agent-a", func(msg *protocol.Message) error {
		count.Add(1)
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg, _ := protocol.NewMessage("agent-b", "agent-a", protocol.MsgHeartbeat, nil)
			bus.Send(msg)
		}()
	}
	wg.Wait()

	time.Sleep(50 * time.Millisecond)
	if count.Load() != 100 {
		t.Errorf("expected 100 messages, got %d", count.Load())
	}
}
