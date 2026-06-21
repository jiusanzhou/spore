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
	"sync"

	"go.zoe.im/spore/internal/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Task event broadcaster
//
// In-process pub/sub for runtime.StreamEvent keyed by task ID. Used to bridge
// agent runtime streams (agent_tasks.go's makeRuntimeEventHandler) into the
// API SSE layer (GET /api/tasks/:id/stream).
//
// Lifecycle:
//   - Agent calls PublishTaskEvent(taskID, ev) for every event the runtime
//     emits while a task is in flight.
//   - API SSE handler calls SubscribeTaskEvents(taskID) → channel + cancel,
//     reads until task completes (channel closed) or client disconnects (call
//     cancel).
//   - Buffer per subscriber is 64 events. If a slow consumer fills it, drops
//     are silent (we never block the runtime).
//
// Concurrency model:
//   Each subscription owns its channel exclusively. Only the broadcaster
//   ever sends or closes it. publish() drops events past buffer capacity and
//   for cancelled subscriptions instead of risking a write to a closed
//   channel.
// ─────────────────────────────────────────────────────────────────────────────

type taskSub struct {
	ch     chan runtime.StreamEvent
	closed bool // protected by parent broadcaster's mu
}

// taskEventBroadcaster fans out runtime.StreamEvent values to per-task
// subscribers. Safe for concurrent use.
type taskEventBroadcaster struct {
	mu   sync.Mutex
	subs map[string]map[*taskSub]struct{}
}

func newTaskEventBroadcaster() *taskEventBroadcaster {
	return &taskEventBroadcaster{
		subs: make(map[string]map[*taskSub]struct{}),
	}
}

// publish delivers ev to every subscriber of taskID. Non-blocking: a full
// subscriber buffer drops the event silently rather than stalling the
// runtime's stream.
func (b *taskEventBroadcaster) publish(taskID string, ev runtime.StreamEvent) {
	b.mu.Lock()
	set := b.subs[taskID]
	if len(set) == 0 {
		b.mu.Unlock()
		return
	}
	// Send while still holding the lock — guarantees ch is not closed
	// concurrently. Sends are non-blocking, so holding the lock is cheap.
	for s := range set {
		if s.closed {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// slow consumer — drop this event
		}
	}
	b.mu.Unlock()
}

// subscribe returns a buffered channel of events for taskID and a cancel
// function the caller MUST invoke when done. The channel is closed when the
// task ends (closeTask) or when cancel is called.
func (b *taskEventBroadcaster) subscribe(taskID string) (<-chan runtime.StreamEvent, func()) {
	s := &taskSub{ch: make(chan runtime.StreamEvent, 64)}

	b.mu.Lock()
	if b.subs[taskID] == nil {
		b.subs[taskID] = make(map[*taskSub]struct{})
	}
	b.subs[taskID][s] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if s.closed {
			return
		}
		s.closed = true
		close(s.ch)
		if set, ok := b.subs[taskID]; ok {
			delete(set, s)
			if len(set) == 0 {
				delete(b.subs, taskID)
			}
		}
	}
	return s.ch, cancel
}

// closeTask drops all subscribers for taskID, signalling the task is done.
// Idempotent.
func (b *taskEventBroadcaster) closeTask(taskID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	set, ok := b.subs[taskID]
	if !ok {
		return
	}
	delete(b.subs, taskID)
	for s := range set {
		if !s.closed {
			s.closed = true
			close(s.ch)
		}
	}
}
