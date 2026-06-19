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

// Package gateway exposes a Spore agent over a human-friendly chat interface.
//
// The first supported channel is Telegram (telegram.go). Each gateway is
// 1:1 with one agent: the bot token identifies the agent's public face, and
// the chat ID allow-list controls which humans may submit tasks to it.
//
// Architecture (per-agent gateway, not per-swarm router):
//
//	┌───────────┐  text message      ┌────────────┐  SubmitTask    ┌───────┐
//	│ Telegram  │ ─────────────────▶ │  Gateway   │ ─────────────▶ │ Agent │
//	│   user    │                    │            │                │       │
//	│           │ ◀──── result ───── │            │ ◀── callback ─│       │
//	└───────────┘                    └────────────┘                 └───────┘
//
// The gateway is intentionally thin: it has no state beyond a taskID→chatID
// map for routing results, and no business logic. Anything requiring memory
// or reasoning happens inside the agent.
package gateway

import (
	"context"
)

// Gateway is the common interface every chat adapter implements.
//
// Lifecycle: NewXxxGateway → Start (blocking) → Stop (cancel ctx).
//
// Implementations MUST:
//   - call agent.SubmitTask for every accepted message,
//   - register an OnTaskUpdate callback to deliver results back,
//   - filter inbound messages by the configured allow-list.
type Gateway interface {
	// Name identifies this gateway in logs ("telegram", "discord", ...).
	Name() string

	// Start runs the gateway loop until ctx is cancelled or a fatal error
	// occurs. Implementations should be resilient to transient errors
	// (network blips, rate limits) and only return for unrecoverable failures.
	Start(ctx context.Context) error
}

// Agent is the subset of agent.Agent that gateways need. We restate it here
// to avoid a hard import cycle and to make tests easy to fake.
type Agent interface {
	// SubmitTask enqueues a task for execution and returns its ID.
	SubmitTask(description string) string

	// SetOnTaskUpdate installs the lifecycle callback. Gateways use this to
	// route results back to the originating chat.
	SetOnTaskUpdate(fn func(taskID, status, runtimeName, result, errMsg string))
}
