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
	"fmt"
	"sync"

	"go.zoe.im/spore/internal/protocol"
)

// LocalBus is an in-process message bus for single-node multi-agent.
type LocalBus struct {
	mu       sync.RWMutex
	handlers map[string]Handler
	inbox    map[string]chan *protocol.Message
}

// NewLocalBus creates a new local message bus.
func NewLocalBus() *LocalBus {
	return &LocalBus{
		handlers: make(map[string]Handler),
		inbox:    make(map[string]chan *protocol.Message),
	}
}

func (b *LocalBus) Send(msg *protocol.Message) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if msg.To == "broadcast" {
		// deliver to all except sender
		for id, handler := range b.handlers {
			if id != msg.From {
				if err := handler(msg); err != nil {
					// log but don't fail
					fmt.Printf("⚠️  broadcast delivery to %s failed: %v\n", id, err)
				}
			}
		}
		return nil
	}

	handler, ok := b.handlers[msg.To]
	if !ok {
		return fmt.Errorf("agent not found: %s", msg.To)
	}
	return handler(msg)
}

func (b *LocalBus) Subscribe(agentID string, handler Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[agentID] = handler
	b.inbox[agentID] = make(chan *protocol.Message, 100)
	return nil
}

func (b *LocalBus) Unsubscribe(agentID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.handlers, agentID)
	if ch, ok := b.inbox[agentID]; ok {
		close(ch)
		delete(b.inbox, agentID)
	}
	return nil
}

func (b *LocalBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for id, ch := range b.inbox {
		close(ch)
		delete(b.inbox, id)
	}
	b.handlers = make(map[string]Handler)
	return nil
}

// Agents returns the list of registered agent IDs.
func (b *LocalBus) Agents() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	ids := make([]string, 0, len(b.handlers))
	for id := range b.handlers {
		ids = append(ids, id)
	}
	return ids
}
