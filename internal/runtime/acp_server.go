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

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// ACP Server (RFC-001 Stage 2).
//
// Inverse of acp.go (the client). Where ACPRuntime *drives* an external
// agent via stdio, ACPServer *exposes* spore itself as an ACP-compliant
// agent so any ACP client (Zed, JetBrains, Neovim, our own client, etc.)
// can connect and run prompts through spore's runtime stack.
//
// Wire protocol is identical — newline-delimited JSON-RPC 2.0 — and we
// reuse acpClient as the framing layer; server-mode just means we never
// originate calls and we register a handler for inbound methods.
//
// Methods served (Stage 2 minimum-viable):
//
//   initialize      → returns protocolVersion + serverCapabilities (we
//                     declare promptCapabilities.image=false, audio=false,
//                     embeddedContext=false; no auth methods)
//   session/new     → assigns a UUID, stashes cwd + mcpServers, replies
//                     with sessionId
//   session/prompt  → drives the configured Runtime (Builtin by default)
//                     against the prompt text, mirrors every StreamEvent
//                     back as a session/update notification, replies with
//                     PromptResponse{stopReason}
//   session/cancel  → cancels the in-flight prompt's ctx
//
// Not implemented (deliberate Stage 2 scope):
//   authenticate, session/load, session/set_mode, fs/*, terminal/*,
//   anything MCP-related (Stage 3).
//
// Architecture note:
//   The server holds a Registry plus a "default" runtime name. Each
//   session/prompt picks the runtime by tag (Route) so a single spore
//   instance can transparently delegate to claude-code (ACP), codex
//   (abox), or builtin depending on what's installed and what the
//   prompt asks for. Right now the routing is just "use the default";
//   tag-based steering will land in a follow-up.
// ─────────────────────────────────────────────────────────────────────────────

// ACPServer exposes a Runtime as an ACP-compliant agent over stdio.
type ACPServer struct {
	// Runtime to drive prompts through. Required.
	Runtime Runtime

	// AgentName/AgentVersion advertised in initialize response.
	AgentName    string
	AgentVersion string

	// Logger is called with one-line progress messages. nil = silent.
	Logger func(string)

	// internal state
	conn     *acpClient
	mu       sync.Mutex
	sessions map[string]*acpSession
}

type acpSession struct {
	id       string
	cwd      string
	cancelMu sync.Mutex
	cancelFn context.CancelFunc // set while a prompt is in-flight
}

// NewACPServer wraps a Runtime as an ACP agent with sensible defaults.
func NewACPServer(rt Runtime) *ACPServer {
	return &ACPServer{
		Runtime:      rt,
		AgentName:    "spore",
		AgentVersion: "0.1.0",
		sessions:     make(map[string]*acpSession),
	}
}

// Serve runs the ACP server loop on the given stdio pair, blocking until
// the peer hangs up or ctx is cancelled. Typical usage from a CLI entry
// point: srv.Serve(ctx, os.Stdin, os.Stdout).
func (s *ACPServer) Serve(ctx context.Context, in io.Reader, out io.WriteCloser) error {
	if s.Runtime == nil {
		return fmt.Errorf("acpserver: Runtime is nil")
	}
	if s.sessions == nil {
		s.sessions = make(map[string]*acpSession)
	}

	s.conn = newACPClient(in, out)
	s.conn.handler = (*acpServerHandlerImpl)(s)

	s.log("ACP server starting (agent=%s/%s)", s.AgentName, s.AgentVersion)
	s.conn.readLoop(ctx)
	s.log("ACP server stopped")
	return nil
}

func (s *ACPServer) log(format string, args ...any) {
	if s.Logger != nil {
		s.Logger(fmt.Sprintf(format, args...))
	}
}

// acpServerHandlerImpl satisfies acpServerHandler. We use a named pointer-
// type alias rather than implementing it on *ACPServer directly so the
// public Server type stays small and the handler dispatch stays internal.
type acpServerHandlerImpl ACPServer

func (h *acpServerHandlerImpl) handleServerRequest(ctx context.Context, method string, params json.RawMessage) (any, *acpRPCError) {
	s := (*ACPServer)(h)
	switch method {
	case "initialize":
		return s.handleInitialize(params)
	case "session/new":
		return s.handleSessionNew(params)
	case "session/prompt":
		return s.handleSessionPrompt(ctx, params)
	default:
		return nil, &acpRPCError{Code: -32601, Message: "method not found: " + method}
	}
}

func (h *acpServerHandlerImpl) handleServerNotification(method string, params json.RawMessage) {
	s := (*ACPServer)(h)
	switch method {
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(params, &p)
		s.cancelSession(p.SessionID)
	default:
		// silently ignore — protocol allows unknown notifications
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Method handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *ACPServer) handleInitialize(params json.RawMessage) (any, *acpRPCError) {
	// We accept any client capabilities; we just don't act on most of them
	// in Stage 2. Echo the agreed protocol version back.
	var req struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &req)
	if req.ProtocolVersion == 0 {
		req.ProtocolVersion = 1
	}

	return map[string]any{
		"protocolVersion": req.ProtocolVersion,
		"agentCapabilities": map[string]any{
			"loadSession": false,
			"promptCapabilities": map[string]any{
				"image":           false,
				"audio":           false,
				"embeddedContext": false,
			},
		},
		"agentInfo": map[string]any{
			"name":    s.AgentName,
			"version": s.AgentVersion,
		},
		"authMethods": []any{},
	}, nil
}

func (s *ACPServer) handleSessionNew(params json.RawMessage) (any, *acpRPCError) {
	var req struct {
		Cwd        string           `json:"cwd"`
		McpServers []map[string]any `json:"mcpServers"`
	}
	_ = json.Unmarshal(params, &req)

	id := uuid.New().String()
	sess := &acpSession{id: id, cwd: req.Cwd}

	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()

	s.log("session/new id=%s cwd=%s", id, req.Cwd)
	return map[string]any{"sessionId": id}, nil
}

func (s *ACPServer) handleSessionPrompt(ctx context.Context, params json.RawMessage) (any, *acpRPCError) {
	var req struct {
		SessionID string            `json:"sessionId"`
		Prompt    []json.RawMessage `json:"prompt"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &acpRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
	}

	s.mu.Lock()
	sess, ok := s.sessions[req.SessionID]
	s.mu.Unlock()
	if !ok {
		return nil, &acpRPCError{Code: -32602, Message: "unknown sessionId: " + req.SessionID}
	}

	// Flatten prompt content blocks → single text. ACP supports image /
	// audio / resource blocks too, but we declared promptCapabilities for
	// text-only in initialize so we only need to handle text reliably.
	promptText := flattenPromptBlocks(req.Prompt)
	if promptText == "" {
		return nil, &acpRPCError{Code: -32602, Message: "empty prompt"}
	}

	// Per-prompt cancellable ctx so session/cancel can interrupt.
	pctx, cancel := context.WithCancel(ctx)
	sess.cancelMu.Lock()
	sess.cancelFn = cancel
	sess.cancelMu.Unlock()
	defer func() {
		sess.cancelMu.Lock()
		sess.cancelFn = nil
		sess.cancelMu.Unlock()
		cancel()
	}()

	s.log("session/prompt id=%s len=%d", req.SessionID, len(promptText))

	// Bridge spore StreamEvent → ACP session/update notifications.
	bridge := s.makeUpdateBridge(req.SessionID)
	task := TaskInput{
		ID:          req.SessionID,
		Description: promptText,
		WorkDir:     sess.cwd,
	}

	var (
		out *TaskOutput
		err error
	)
	if streaming, ok := s.Runtime.(StreamingRuntime); ok {
		out, err = streaming.ExecuteStream(pctx, task, bridge)
	} else {
		out, err = s.Runtime.Execute(pctx, task)
	}

	stopReason := "end_turn"
	if err != nil {
		if pctx.Err() == context.Canceled {
			stopReason = "cancelled"
		} else {
			stopReason = "error"
		}
		s.log("session/prompt id=%s err=%v", req.SessionID, err)
	}

	resp := map[string]any{"stopReason": stopReason}
	// Note: we deliberately do NOT echo out.Result here. StreamingRuntime
	// adapters already emit it as agent_message_chunk events during the
	// run; emitting again would duplicate the final text on the wire (and
	// concatenate it on naive clients). For non-streaming runtimes the
	// client just sees stopReason without an explicit final chunk — that's
	// a known Stage 2 limitation, fixed by tightening the bridge to detect
	// "no thinking emitted" and fall back to result echo.
	if _, isStreaming := s.Runtime.(StreamingRuntime); !isStreaming && out != nil && out.Result != "" {
		_ = s.conn.notify("session/update", map[string]any{
			"sessionId": req.SessionID,
			"update": map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": out.Result},
			},
		})
	}
	return resp, nil
}

func (s *ACPServer) cancelSession(id string) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	sess.cancelMu.Lock()
	if sess.cancelFn != nil {
		sess.cancelFn()
		s.log("session/cancel id=%s", id)
	}
	sess.cancelMu.Unlock()
}

// ─────────────────────────────────────────────────────────────────────────────
// StreamEvent → ACP session/update bridge
// ─────────────────────────────────────────────────────────────────────────────

func (s *ACPServer) makeUpdateBridge(sessionID string) EventHandler {
	return func(ev StreamEvent) error {
		var update map[string]any

		switch ev.Type {
		case EventThinking:
			update = map[string]any{
				"sessionUpdate": "agent_message_chunk",
				"content":       map[string]any{"type": "text", "text": ev.Content},
			}

		case EventToolCall:
			input := json.RawMessage(ev.ToolInput)
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			update = map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    ev.ToolName, // spore IR doesn't carry a separate tool_call id
				"title":         ev.ToolName,
				"kind":          "execute",
				"status":        "pending",
				"rawInput":      input,
			}

		case EventToolResult:
			status := "completed"
			if ev.ToolError {
				status = "failed"
			}
			update = map[string]any{
				"sessionUpdate": "tool_call_update",
				"toolCallId":    ev.ToolName,
				"status":        status,
				"content": []any{
					map[string]any{"type": "text", "text": ev.ToolOutput},
				},
			}

		case EventError:
			// Map runtime errors to a thought chunk so clients show them
			// in-band; fatal errors will also surface through the final
			// PromptResponse stopReason.
			update = map[string]any{
				"sessionUpdate": "agent_thought_chunk",
				"content":       map[string]any{"type": "text", "text": "[error] " + ev.Content},
			}

		case EventInit, EventComplete:
			// init/complete are bookkeeping in spore IR; ACP doesn't have
			// equivalents (initialize and PromptResponse cover the gaps).
			return nil

		default:
			return nil
		}

		return s.conn.notify("session/update", map[string]any{
			"sessionId": sessionID,
			"update":    update,
		})
	}
}

// flattenPromptBlocks extracts text content from ACP's content-block array,
// concatenating with newlines. Non-text blocks are skipped silently.
func flattenPromptBlocks(blocks []json.RawMessage) string {
	var parts []string
	for _, raw := range blocks {
		var blk struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &blk); err != nil {
			continue
		}
		if blk.Type == "text" && blk.Text != "" {
			parts = append(parts, blk.Text)
		}
	}
	return strings.Join(parts, "\n")
}
