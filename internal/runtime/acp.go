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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// ACP runtime adapter (RFC-001 Stage 1).
//
// Replaces the per-runtime stream-json parsers (claude_code.go, codex.go) with
// a single adapter that speaks Agent Client Protocol — the open JSON-RPC 2.0
// stdio protocol introduced by Zed in Aug 2025 and now adopted by Claude Code,
// Codex, Gemini CLI, JetBrains, Neovim, and 50+ runtimes.
//
// Design choice — hand-rolled JSON-RPC instead of joshgarnett/acp-go:
//   The community Go library was attempted first but has a wire-format bug:
//   it uses golang.org/x/exp/jsonrpc2's HeaderFramer (LSP-style
//   "Content-Length: N\r\n\r\n" framing) by default, while ACP uses
//   newline-delimited JSON. Real claude-agent-acp servers ignore the framed
//   bytes and time out. Rather than fork the lib, we implement the protocol
//   directly: ACP is just newline JSON-RPC 2.0 with ~6 methods we care about.
//   This file is ~400 lines vs adopting a 14k-line library that still needs
//   patching.
//
// Tested against:
//   - @agentclientprotocol/claude-agent-acp v0.48 (initialize + session/new
//     + session/prompt + session/update notifications)
//
// What's mapped:
//   - session/update agent_message_chunk → EventThinking
//   - session/update agent_thought_chunk → EventThinking ("[reasoning] ...")
//   - session/update tool_call            → EventToolCall
//   - session/update tool_call_update     → EventToolResult
//   - session/update plan                 → EventThinking ("[plan] ...")
//   - PromptResponse.stopReason           → success flag in TaskOutput
//
// Client methods we serve back (handled inline, no separate handler types):
//   - fs/read_text_file                  → reads from local fs
//   - fs/write_text_file                 → writes (gated by AllowFsWrite)
//   - session/request_permission         → auto-approves (Stage 2 will gate
//                                           this through telegram gateway)
//
// Not yet implemented (Stage 1 deliberate scope):
//   - terminal/* (claude-agent-acp doesn't need it for most tasks)
//   - session/load (we always create fresh sessions)
//   - HTTP+SSE transport (stdio only)
//   - MCP server forwarding via session/new mcpServers (Stage 3)
// ─────────────────────────────────────────────────────────────────────────────

// ACPRuntime drives an external ACP-speaking agent via stdio JSON-RPC.
type ACPRuntime struct {
	RuntimeName string // identifier for discovery + StreamEvent.Runtime tagging
	BinPath     string // executable to spawn (e.g. "claude-agent-acp")
	BinArgs     []string

	// Env merges into the subprocess environment (parent env inherited).
	Env map[string]string

	// HandshakeTimeout caps initialize + session/new latency. Default 60s.
	HandshakeTimeout time.Duration

	// PromptTimeout caps a single session/prompt call. Default 5min.
	PromptTimeout time.Duration

	// AllowFsWrite enables fs/write_text_file callbacks (default false).
	AllowFsWrite bool

	// AutoApprovePermissions auto-grants session/request_permission (default true).
	AutoApprovePermissions bool
}

// NewACPRuntime returns a runtime preconfigured for claude-agent-acp.
func NewACPRuntime() *ACPRuntime {
	return &ACPRuntime{
		RuntimeName:            "acp-claude",
		BinPath:                "claude-agent-acp",
		HandshakeTimeout:       60 * time.Second,
		PromptTimeout:          5 * time.Minute,
		AllowFsWrite:           false,
		AutoApprovePermissions: true,
	}
}

var (
	_ Runtime          = (*ACPRuntime)(nil)
	_ StreamingRuntime = (*ACPRuntime)(nil)
)

func (r *ACPRuntime) Info() Info {
	return Info{
		Name:    r.RuntimeName,
		Version: "auto",
		Capabilities: []Capability{
			{Name: "coding", Description: "ACP-bridged coding agent", Tags: []string{"coding", "acp"}},
			{Name: "shell", Description: "Tool calls via ACP", Tags: []string{"shell", "acp"}},
		},
		MaxConcurrent: 2,
	}
}

func (r *ACPRuntime) Healthy(_ context.Context) error {
	if r.BinPath == "" {
		return fmt.Errorf("ACPRuntime.BinPath is empty")
	}
	if _, err := exec.LookPath(r.BinPath); err != nil {
		return fmt.Errorf("ACP agent binary %q not found in PATH: %w", r.BinPath, err)
	}
	return nil
}

func (r *ACPRuntime) Close() error { return nil }

func (r *ACPRuntime) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	return r.ExecuteStream(ctx, task, nil)
}

// ExecuteStream runs a task end-to-end: spawn agent → handshake → prompt →
// drain session/update stream → tear down. onEvent (when non-nil) receives
// every observable transition as a spore IR StreamEvent.
func (r *ACPRuntime) ExecuteStream(ctx context.Context, task TaskInput, onEvent EventHandler) (*TaskOutput, error) {
	if err := r.Healthy(ctx); err != nil {
		return nil, err
	}

	// ── 1. Spawn agent subprocess ────────────────────────────────────────
	cmd := exec.CommandContext(ctx, r.BinPath, r.BinArgs...)
	if task.WorkDir != "" {
		cmd.Dir = task.WorkDir
	}
	cmd.Env = os.Environ()
	for k, v := range r.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range task.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrPrefixWriter{prefix: "[" + r.RuntimeName + " stderr] ", w: &stderrBuf}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: start %s: %w", r.BinPath, err)
	}

	// ── 2. Build the JSON-RPC client + tracker ───────────────────────────
	cli := newACPClient(stdout, stdin)
	tracker := &acpEventTracker{
		runtimeName: r.RuntimeName,
		onEvent:     onEvent,
		client:      cli,
		allowWrite:  r.AllowFsWrite,
		autoApprove: r.AutoApprovePermissions,
	}
	cli.handler = tracker

	go cli.readLoop(ctx) // start reading server messages

	// ── 3. ACP handshake ─────────────────────────────────────────────────
	hctx, hcancel := context.WithTimeout(ctx, r.HandshakeTimeout)
	defer hcancel()

	initParams := map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs": map[string]any{
				"readTextFile":  true,
				"writeTextFile": r.AllowFsWrite,
			},
		},
	}
	initRespRaw, err := cli.call(hctx, "initialize", initParams)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("acp: initialize: %w", err)
	}
	tracker.emitInit(initRespRaw)

	cwd := firstNonEmpty(task.WorkDir, mustGetwd())
	sessNewResp, err := cli.call(hctx, "session/new", map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{}, // Stage 3 will populate
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("acp: session/new: %w", err)
	}
	var sessNew struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(sessNewResp, &sessNew)
	tracker.sessionID.Store(stringPtr(sessNew.SessionID))

	// ── 4. Send the prompt ───────────────────────────────────────────────
	pctx, pcancel := context.WithTimeout(ctx, r.PromptTimeout)
	defer pcancel()

	promptResp, err := cli.call(pctx, "session/prompt", map[string]any{
		"sessionId": sessNew.SessionID,
		"prompt": []any{
			map[string]any{"type": "text", "text": task.Description},
		},
	})
	duration := time.Since(start)
	tracker.recordEnd(promptResp, err)

	// Tear down: close stdin, wait briefly, kill on timeout.
	_ = stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}

	// ── 5. Build TaskOutput ──────────────────────────────────────────────
	output := &TaskOutput{
		Success: err == nil && tracker.success(),
		Result:  tracker.finalText(),
		Tokens:  tracker.totalTokens(),
		Logs: fmt.Sprintf("runtime=%s session=%s tools=%d duration=%s stderr=%s",
			r.RuntimeName, tracker.sessionIDValue(), tracker.toolCalls.Load(),
			duration, truncateForLog(stderrBuf.String(), 500)),
	}
	if err != nil {
		output.Error = fmt.Sprintf("acp prompt: %v; stderr: %s",
			err, truncateForLog(stderrBuf.String(), 500))
	} else if !tracker.success() {
		output.Error = fmt.Sprintf("acp stop_reason=%s", tracker.stopReason())
	}

	if onEvent != nil {
		_ = onEvent(StreamEvent{
			Type:       EventComplete,
			Runtime:    r.RuntimeName,
			Session:    tracker.sessionIDValue(),
			Content:    tracker.finalText(),
			DurationMS: duration.Milliseconds(),
		})
	}

	return output, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// acpClient: minimal newline-delimited JSON-RPC 2.0 client. Each call gets
// an integer ID; responses are matched back via a pending-call map. Server-
// initiated requests are dispatched to the handler.
// ─────────────────────────────────────────────────────────────────────────────

type acpClient struct {
	in  *bufio.Reader
	out io.WriteCloser

	// writeMu serializes writes — ACP frames are line-delimited JSON,
	// interleaving two writes would corrupt the stream.
	writeMu sync.Mutex

	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan *acpResponse

	handler acpServerHandler

	closeOnce sync.Once
	closed    chan struct{}
}

type acpServerHandler interface {
	handleServerRequest(ctx context.Context, method string, params json.RawMessage) (any, *acpRPCError)
	handleServerNotification(method string, params json.RawMessage)
}

type acpResponse struct {
	Result json.RawMessage
	Err    *acpRPCError
}

type acpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *acpRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("ACP RPC error %d: %s", e.Code, e.Message)
}

func newACPClient(in io.Reader, out io.WriteCloser) *acpClient {
	return &acpClient{
		in:      bufio.NewReaderSize(in, 64*1024),
		out:     out,
		pending: make(map[int64]chan *acpResponse),
		closed:  make(chan struct{}),
	}
}

// call sends a JSON-RPC request and waits for the matching response or ctx done.
func (c *acpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	respCh := make(chan *acpResponse, 1)

	c.pendingMu.Lock()
	c.pending[id] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	// Write happens in a goroutine so a stuck stdin (peer not draining) doesn't
	// strand callers past their ctx deadline. If ctx fires while we're blocked
	// on Write, we close c.out which unblocks the underlying syscall with EPIPE/
	// ErrClosedPipe — the connection is unusable after that, but the caller
	// regains control and the readLoop will tear down via c.closed.
	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- c.writeMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params":  params,
		})
	}()

	// First barrier: wait for the write to land (or be unblocked). We don't
	// observe c.closed here — even if readLoop already EOF'd, the write may
	// have completed successfully and the response may already be sitting in
	// respCh waiting for us. We handle the closed-connection case purely in
	// the second select.
	select {
	case err := <-writeErrCh:
		if err != nil {
			return nil, fmt.Errorf("write %s: %w", method, err)
		}
	case <-ctx.Done():
		_ = c.out.Close() // unblock the writer goroutine
		return nil, ctx.Err()
	}

	// respCh has priority over c.closed: when the server writes a response and
	// then closes its end of the pipe (very common in tests, plausible in real
	// peers), readLoop hits EOF in the same instant the response lands. Naive
	// `select { case <-respCh: case <-c.closed: }` picks randomly between the
	// two ready cases, leaking a "connection closed" error past valid responses.
	select {
	case resp := <-respCh:
		if resp.Err != nil {
			return nil, resp.Err
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		// One last non-blocking peek: response may have been delivered in the
		// same scheduling window that closed the connection.
		select {
		case resp := <-respCh:
			if resp.Err != nil {
				return nil, resp.Err
			}
			return resp.Result, nil
		default:
			return nil, fmt.Errorf("acp: connection closed")
		}
	}
}

// writeMessage marshals + line-writes a JSON-RPC frame.
func (c *acpClient) writeMessage(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.out.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// writeResponse sends a response to a server-initiated request.
func (c *acpClient) writeResponse(id any, result any, rpcErr *acpRPCError) error {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		msg["error"] = rpcErr
	} else {
		msg["result"] = result
	}
	return c.writeMessage(msg)
}

// readLoop reads server messages and dispatches them. Exits on EOF or
// scanner error; signals closed on exit.
func (c *acpClient) readLoop(ctx context.Context) {
	defer c.closeOnce.Do(func() { close(c.closed) })

	for {
		line, err := c.in.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(ctx, line)
		}
		if err != nil {
			return // EOF or read error — terminates the connection
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// dispatch routes a single JSON-RPC frame to either a pending call's response
// channel, the server-request handler, or the notification handler.
func (c *acpClient) dispatch(ctx context.Context, line []byte) {
	var probe struct {
		ID     json.RawMessage `json:"id,omitempty"`
		Method string          `json:"method,omitempty"`
		Params json.RawMessage `json:"params,omitempty"`
		Result json.RawMessage `json:"result,omitempty"`
		Error  *acpRPCError    `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return // malformed — ignore (we could log this)
	}

	hasID := len(probe.ID) > 0 && string(probe.ID) != "null"

	switch {
	// Response to one of our calls: id present, no method.
	case probe.Method == "" && hasID:
		var idNum int64
		if err := json.Unmarshal(probe.ID, &idNum); err != nil {
			return
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[idNum]
		c.pendingMu.Unlock()
		if !ok {
			return
		}
		select {
		case ch <- &acpResponse{Result: probe.Result, Err: probe.Error}:
		default:
		}

	// Server-initiated request: id present, method present.
	case probe.Method != "" && hasID:
		if c.handler == nil {
			_ = c.writeResponse(json.RawMessage(probe.ID), nil,
				&acpRPCError{Code: -32601, Message: "no handler"})
			return
		}
		go func(id json.RawMessage, method string, params json.RawMessage) {
			result, rpcErr := c.handler.handleServerRequest(ctx, method, params)
			_ = c.writeResponse(id, result, rpcErr)
		}(append([]byte(nil), probe.ID...), probe.Method, probe.Params)

	// Notification: method present, no id.
	case probe.Method != "" && !hasID:
		if c.handler != nil {
			c.handler.handleServerNotification(probe.Method, probe.Params)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// acpEventTracker: bridges ACP server messages to spore IR StreamEvents and
// accumulates the final state for TaskOutput. Implements acpServerHandler.
// ─────────────────────────────────────────────────────────────────────────────

type acpEventTracker struct {
	runtimeName string
	onEvent     EventHandler
	client      *acpClient
	allowWrite  bool
	autoApprove bool

	sessionID atomic.Pointer[string]
	toolCalls atomic.Int64
	tokensIn  atomic.Int64
	tokensOut atomic.Int64

	finalTextMu  sync.Mutex
	finalTextBuf strings.Builder

	stopReasonValue atomic.Pointer[string]
	successFlag     atomic.Bool
}

func (t *acpEventTracker) emit(ev StreamEvent) {
	if t.onEvent == nil {
		return
	}
	_ = t.onEvent(ev)
}

func (t *acpEventTracker) emitInit(rawResp json.RawMessage) {
	var probe struct {
		ProtocolVersion int `json:"protocolVersion"`
		AgentInfo       struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"agentInfo"`
	}
	_ = json.Unmarshal(rawResp, &probe)
	name := probe.AgentInfo.Name
	if name == "" {
		name = "unknown"
	}
	if probe.AgentInfo.Version != "" {
		name += "/" + probe.AgentInfo.Version
	}
	t.emit(StreamEvent{
		Type:    EventInit,
		Runtime: t.runtimeName,
		Content: fmt.Sprintf("ACP v%d agent=%s", probe.ProtocolVersion, name),
		Raw:     rawResp,
	})
}

// handleServerRequest handles fs/* and session/request_permission.
func (t *acpEventTracker) handleServerRequest(_ context.Context, method string, params json.RawMessage) (any, *acpRPCError) {
	switch method {
	case "fs/read_text_file":
		return t.handleFsRead(params)
	case "fs/write_text_file":
		return t.handleFsWrite(params)
	case "session/request_permission":
		return t.handleRequestPermission(params)
	default:
		return nil, &acpRPCError{Code: -32601, Message: "method not implemented: " + method}
	}
}

// handleServerNotification handles session/update.
func (t *acpEventTracker) handleServerNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var note struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(params, &note); err != nil {
		return
	}
	var u struct {
		SessionUpdate string          `json:"sessionUpdate"` // discriminant
		Content       json.RawMessage `json:"content,omitempty"`
		ToolCallID    string          `json:"toolCallId,omitempty"`
		Title         string          `json:"title,omitempty"`
		Kind          string          `json:"kind,omitempty"`
		Status        string          `json:"status,omitempty"`
		Entries       []any           `json:"entries,omitempty"`
	}
	if err := json.Unmarshal(note.Update, &u); err != nil {
		return
	}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		text := contentBlockText(u.Content)
		if text != "" {
			t.finalTextMu.Lock()
			t.finalTextBuf.WriteString(text)
			t.finalTextMu.Unlock()
			t.emit(StreamEvent{
				Type: EventThinking, Runtime: t.runtimeName,
				Session: t.sessionIDValue(), Content: text,
			})
		}
	case "agent_thought_chunk":
		text := contentBlockText(u.Content)
		if text != "" {
			t.emit(StreamEvent{
				Type: EventThinking, Runtime: t.runtimeName,
				Session: t.sessionIDValue(),
				Content: "[reasoning] " + truncateForLog(text, 2000),
			})
		}
	case "tool_call":
		t.toolCalls.Add(1)
		title := u.Title
		if u.Kind != "" {
			title = fmt.Sprintf("%s (%s)", title, u.Kind)
		}
		t.emit(StreamEvent{
			Type: EventToolCall, Runtime: t.runtimeName,
			Session: t.sessionIDValue(), ToolName: title,
		})
	case "tool_call_update":
		out := contentBlockText(u.Content)
		t.emit(StreamEvent{
			Type: EventToolResult, Runtime: t.runtimeName,
			Session: t.sessionIDValue(), ToolName: u.Title,
			ToolOutput: truncateForLog(out, 4000),
			ToolError:  u.Status == "failed",
		})
	case "plan":
		t.emit(StreamEvent{
			Type: EventThinking, Runtime: t.runtimeName,
			Session: t.sessionIDValue(),
			Content: "[plan] " + acpPlanSummary(u.Entries),
		})
	case "user_message_chunk":
		// Echo of our prompt — drop on the floor.
	}
}

func (t *acpEventTracker) handleFsRead(params json.RawMessage) (any, *acpRPCError) {
	var req struct {
		Path  string `json:"path"`
		Line  *int   `json:"line,omitempty"`
		Limit *int   `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &acpRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return nil, &acpRPCError{Code: -32603, Message: "read failed: " + err.Error()}
	}
	content := string(data)
	if req.Line != nil || req.Limit != nil {
		content = sliceLines(content, req.Line, req.Limit)
	}
	return map[string]any{"content": content}, nil
}

func (t *acpEventTracker) handleFsWrite(params json.RawMessage) (any, *acpRPCError) {
	if !t.allowWrite {
		return nil, &acpRPCError{Code: -32603, Message: "fs/write_text_file denied: AllowFsWrite=false"}
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &acpRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	if err := os.WriteFile(req.Path, []byte(req.Content), 0o644); err != nil {
		return nil, &acpRPCError{Code: -32603, Message: "write failed: " + err.Error()}
	}
	t.emit(StreamEvent{
		Type: EventToolResult, Runtime: t.runtimeName,
		Session: t.sessionIDValue(), ToolName: "fs/write",
		ToolOutput: fmt.Sprintf("wrote %d bytes to %s", len(req.Content), req.Path),
	})
	return map[string]any{}, nil
}

func (t *acpEventTracker) handleRequestPermission(params json.RawMessage) (any, *acpRPCError) {
	if !t.autoApprove {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	// Pick the first option (typically "allow" or "allow_once"). ACP spec:
	// each option has {optionId, name, kind}.
	var req struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &req)
	var optionID string
	if len(req.Options) > 0 {
		// Prefer "allow_always" or "allow_once" by kind if available.
		for _, o := range req.Options {
			if o.Kind == "allow_always" || o.Kind == "allow_once" {
				optionID = o.OptionID
				break
			}
		}
		if optionID == "" {
			optionID = req.Options[0].OptionID
		}
	}
	return map[string]any{
		"outcome": map[string]any{"outcome": "selected", "optionId": optionID},
	}, nil
}

func (t *acpEventTracker) recordEnd(rawResp json.RawMessage, err error) {
	if err != nil {
		reason := "error"
		t.stopReasonValue.Store(&reason)
		t.successFlag.Store(false)
		return
	}
	var resp struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(rawResp, &resp)
	if resp.StopReason == "" {
		resp.StopReason = "no-response"
	}
	t.stopReasonValue.Store(&resp.StopReason)
	t.successFlag.Store(resp.StopReason == "end_turn")
}

func (t *acpEventTracker) success() bool { return t.successFlag.Load() }
func (t *acpEventTracker) finalText() string {
	t.finalTextMu.Lock()
	defer t.finalTextMu.Unlock()
	return t.finalTextBuf.String()
}
func (t *acpEventTracker) totalTokens() int { return int(t.tokensIn.Load() + t.tokensOut.Load()) }
func (t *acpEventTracker) stopReason() string {
	p := t.stopReasonValue.Load()
	if p == nil {
		return ""
	}
	return *p
}
func (t *acpEventTracker) sessionIDValue() string {
	p := t.sessionID.Load()
	if p == nil {
		return ""
	}
	return *p
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure-function helpers (testable in isolation; no I/O).
// ─────────────────────────────────────────────────────────────────────────────

func stringPtr(s string) *string { return &s }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "/tmp"
	}
	return wd
}

// contentBlockText extracts text from an ACP ContentBlock JSON payload.
// ACP content blocks come in flavors: text, image, audio, resource,
// resource_link. Only text yields readable content; other variants get
// a stub marker since the spore IR is text-only.
//
// Accepts either a single block or an array of blocks (joins with \n).
func contentBlockText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try array first.
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		var parts []string
		for _, item := range arr {
			parts = append(parts, contentBlockText(item))
		}
		return strings.Join(parts, "\n")
	}
	// Single block.
	var blk struct {
		Type string `json:"type"`
		Text string `json:"text"`
		URI  string `json:"uri"`
	}
	if json.Unmarshal(raw, &blk) != nil {
		return ""
	}
	switch blk.Type {
	case "text":
		return blk.Text
	case "image":
		return "[image]"
	case "audio":
		return "[audio]"
	case "resource_link":
		return fmt.Sprintf("[link: %s]", blk.URI)
	case "resource":
		return "[resource]"
	}
	return ""
}

// acpPlanSummary turns []any plan entries into a compact one-liner.
func acpPlanSummary(entries []any) string {
	if len(entries) == 0 {
		return "(empty)"
	}
	var parts []string
	for i, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		title, _ := em["title"].(string)
		if title == "" {
			title, _ = em["content"].(string)
		}
		status, _ := em["status"].(string)
		parts = append(parts, fmt.Sprintf("%d.[%s]%s", i+1, status, title))
		if i >= 4 {
			parts = append(parts, fmt.Sprintf("(+%d more)", len(entries)-i-1))
			break
		}
	}
	return strings.Join(parts, " ")
}

// sliceLines applies optional 1-indexed line + limit to file content for ACP
// fs/read_text_file requests.
func sliceLines(content string, line, limit *int) string {
	lines := strings.Split(content, "\n")
	startIdx := 0
	if line != nil && *line > 0 {
		startIdx = *line - 1
	}
	if startIdx >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit != nil && *limit > 0 && startIdx+*limit < end {
		end = startIdx + *limit
	}
	return strings.Join(lines[startIdx:end], "\n")
}
