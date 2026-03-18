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
	"go.zoe.im/spore/internal/protocol"
)

// Handler processes incoming messages.
type Handler func(msg *protocol.Message) error

// Bus is the interface for inter-agent communication.
type Bus interface {
	// Send delivers a message to a specific agent or broadcasts.
	Send(msg *protocol.Message) error

	// Subscribe registers a handler for incoming messages.
	Subscribe(agentID string, handler Handler) error

	// Unsubscribe removes a handler.
	Unsubscribe(agentID string) error

	// Close shuts down the bus.
	Close() error
}
