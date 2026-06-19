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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestACP_Compliance verifies ACPRuntime satisfies both interfaces.
func TestACP_Compliance(t *testing.T) {
	var _ Runtime = (*ACPRuntime)(nil)
	var _ StreamingRuntime = (*ACPRuntime)(nil)
}

func TestNewACPRuntime_Defaults(t *testing.T) {
	r := NewACPRuntime()
	if r.RuntimeName == "" {
		t.Error("RuntimeName should default to non-empty")
	}
	if r.BinPath != "claude-agent-acp" {
		t.Errorf("BinPath = %q, want claude-agent-acp", r.BinPath)
	}
	if r.HandshakeTimeout == 0 {
		t.Error("HandshakeTimeout should default to non-zero")
	}
	if r.AllowFsWrite {
		t.Error("AllowFsWrite should default to false")
	}
	if !r.AutoApprovePermissions {
		t.Error("AutoApprovePermissions should default to true")
	}
}

func TestACP_Healthy_MissingBinary(t *testing.T) {
	r := &ACPRuntime{BinPath: "this-binary-does-not-exist-12345"}
	err := r.Healthy(context.Background())
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestACP_Healthy_EmptyBinPath(t *testing.T) {
	r := &ACPRuntime{}
	if err := r.Healthy(context.Background()); err == nil {
		t.Error("expected error for empty BinPath")
	}
}

func TestACPInfo(t *testing.T) {
	r := NewACPRuntime()
	info := r.Info()
	if info.Name == "" {
		t.Error("Info().Name should be non-empty")
	}
	if len(info.Capabilities) == 0 {
		t.Error("Info().Capabilities should be non-empty")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure-function tests
// ─────────────────────────────────────────────────────────────────────────────

func TestContentBlockText(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"text block", `{"type":"text","text":"hello world"}`, "hello world"},
		{"image block", `{"type":"image","data":"...","mimeType":"image/png"}`, "[image]"},
		{"audio block", `{"type":"audio","data":"...","mimeType":"audio/wav"}`, "[audio]"},
		{"resource_link", `{"type":"resource_link","uri":"file:///x.go","name":"x.go"}`, "[link: file:///x.go]"},
		{"resource", `{"type":"resource","resource":{}}`, "[resource]"},
		{"unknown type", `{"type":"weird"}`, ""},
		{"array of texts", `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, "a\nb"},
		{"empty raw", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := contentBlockText(json.RawMessage(c.json))
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestACPPlanSummary(t *testing.T) {
	entries := []any{
		map[string]any{"title": "step one", "status": "completed"},
		map[string]any{"title": "step two", "status": "in_progress"},
		map[string]any{"content": "step three (no title)", "status": "pending"},
	}
	got := acpPlanSummary(entries)
	if !strings.Contains(got, "step one") {
		t.Errorf("missing step one: %q", got)
	}
	if !strings.Contains(got, "step three") {
		t.Errorf("missing step three (content fallback): %q", got)
	}
	if !strings.Contains(got, "completed") {
		t.Errorf("missing status: %q", got)
	}
	if acpPlanSummary(nil) != "(empty)" {
		t.Error("nil entries should yield (empty)")
	}
}

func TestACPPlanSummary_Truncation(t *testing.T) {
	var entries []any
	for i := 0; i < 10; i++ {
		entries = append(entries, map[string]any{"title": "x", "status": "pending"})
	}
	got := acpPlanSummary(entries)
	if !strings.Contains(got, "more") {
		t.Errorf("expected truncation indicator: %q", got)
	}
}

func TestSliceLines(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"
	one, two, three := 1, 2, 3

	cases := []struct {
		name  string
		line  *int
		limit *int
		want  string
	}{
		{"no slice", nil, nil, content},
		{"start at line 2", &two, nil, "line2\nline3\nline4\nline5"},
		{"first 2 lines", &one, &two, "line1\nline2"},
		{"line 2 limit 1", &two, &one, "line2"},
		{"line 3 limit 3 (clamped)", &three, &three, "line3\nline4\nline5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sliceLines(content, c.line, c.limit)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}

	bigStart := 100
	if sliceLines(content, &bigStart, nil) != "" {
		t.Error("out-of-range start should yield empty string")
	}
}

func TestStringPtrAndFirstNonEmpty(t *testing.T) {
	p := stringPtr("foo")
	if p == nil || *p != "foo" {
		t.Error("stringPtr round-trip failed")
	}
	cases := []struct{ a, b, want string }{
		{"", "", ""},
		{"a", "b", "a"},
		{"", "b", "b"},
		{"a", "", "a"},
	}
	for _, c := range cases {
		got := firstNonEmpty(c.a, c.b)
		if got != c.want {
			t.Errorf("firstNonEmpty(%q,%q)=%q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON-RPC client tests using an in-memory pipe pair
// ─────────────────────────────────────────────────────────────────────────────

// pipeRWC is an in-memory ReadWriteCloser used to drive the ACP client
// without a real subprocess.
type pipeRWC struct {
	r io.Reader
	w *pipeWriter
}

type pipeWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (p *pipeWriter) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	return p.buf.Write(data)
}

func (p *pipeWriter) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}

func (p *pipeWriter) snapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.String()
}

// TestACPClient_HandlesResponse tests that a call() resolves when a matching
// response arrives.
func TestACPClient_HandlesResponse(t *testing.T) {
	// Server-side reads our requests, server-side writes responses.
	serverIn, clientOut := io.Pipe()    // client → server
	clientIn, serverOut := io.Pipe()    // server → client

	cli := newACPClient(clientIn, &pipeWriteCloser{w: clientOut})
	go cli.readLoop(context.Background())

	// Respond on the server side after seeing our request.
	go func() {
		defer serverOut.Close()
		buf := make([]byte, 4096)
		n, _ := serverIn.Read(buf)
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(bytes.TrimSpace(buf[:n]), &req); err != nil {
			t.Errorf("server failed to unmarshal: %v", err)
			return
		}
		if req.Method != "test/method" {
			t.Errorf("server got method %q, want test/method", req.Method)
		}
		// Send response with same id.
		resp := []byte(`{"jsonrpc":"2.0","id":` + jsonNumber(req.ID) + `,"result":{"ok":true}}` + "\n")
		_, _ = serverOut.Write(resp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := cli.call(ctx, "test/method", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("call returned error: %v", err)
	}
	var got struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !got.OK {
		t.Errorf("got %+v, want OK=true", got)
	}
}

// TestACPClient_HandlesError verifies an RPC error response surfaces as an error.
func TestACPClient_HandlesError(t *testing.T) {
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()

	cli := newACPClient(clientIn, &pipeWriteCloser{w: clientOut})
	go cli.readLoop(context.Background())

	go func() {
		defer serverOut.Close()
		buf := make([]byte, 4096)
		n, _ := serverIn.Read(buf)
		var req struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(bytes.TrimSpace(buf[:n]), &req)
		resp := []byte(`{"jsonrpc":"2.0","id":` + jsonNumber(req.ID) + `,"error":{"code":-32601,"message":"method not found"}}` + "\n")
		_, _ = serverOut.Write(resp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := cli.call(ctx, "missing/method", nil)
	if err == nil {
		t.Fatal("expected error from RPC error response")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("err = %v, want method not found", err)
	}
}

// TestACPClient_ServerNotification verifies inbound notifications dispatch
// through the handler.
func TestACPClient_ServerNotification(t *testing.T) {
	clientIn, serverOut := io.Pipe()
	cli := newACPClient(clientIn, &pipeWriteCloser{w: nopWriter{}})

	var captured struct {
		mu     sync.Mutex
		method string
		params json.RawMessage
	}
	cli.handler = &captureHandler{captured: &captured}

	go cli.readLoop(context.Background())

	// Send a notification (no id).
	_, _ = serverOut.Write([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}` + "\n"))
	_ = serverOut.Close()

	// Wait briefly for dispatch.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		captured.mu.Lock()
		got := captured.method
		captured.mu.Unlock()
		if got != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.method != "session/update" {
		t.Errorf("captured method = %q, want session/update", captured.method)
	}
}

// TestACPClient_CtxCancel verifies a pending call honors context cancellation.
func TestACPClient_CtxCancel(t *testing.T) {
	_, clientOut := io.Pipe()
	clientIn, _ := io.Pipe() // server never responds

	cli := newACPClient(clientIn, &pipeWriteCloser{w: clientOut})
	go cli.readLoop(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := cli.call(ctx, "slow/method", nil)
	if err == nil {
		t.Fatal("expected ctx deadline error")
	}
}

// TestACPEventTracker_SessionUpdate exercises the notification handler end-
// to-end: feed an agent_message_chunk and verify it accumulates as final
// text + emits an EventThinking.
func TestACPEventTracker_SessionUpdate(t *testing.T) {
	var events []StreamEvent
	tracker := &acpEventTracker{
		runtimeName: "acp-test",
		onEvent:     func(e StreamEvent) error { events = append(events, e); return nil },
	}

	// agent_message_chunk
	tracker.handleServerNotification("session/update", json.RawMessage(`{
		"sessionId":"s1",
		"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}
	}`))

	if tracker.finalText() != "hi" {
		t.Errorf("finalText = %q, want hi", tracker.finalText())
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventThinking {
		t.Errorf("event[0].Type = %q, want EventThinking", events[0].Type)
	}

	// tool_call
	tracker.handleServerNotification("session/update", json.RawMessage(`{
		"sessionId":"s1",
		"update":{"sessionUpdate":"tool_call","title":"Bash","kind":"execute","toolCallId":"t1"}
	}`))
	if tracker.toolCalls.Load() != 1 {
		t.Errorf("toolCalls = %d, want 1", tracker.toolCalls.Load())
	}
	last := events[len(events)-1]
	if last.Type != EventToolCall || !strings.Contains(last.ToolName, "Bash") {
		t.Errorf("last event = %+v, want EventToolCall Bash", last)
	}

	// tool_call_update with completed status
	tracker.handleServerNotification("session/update", json.RawMessage(`{
		"sessionId":"s1",
		"update":{"sessionUpdate":"tool_call_update","title":"Bash","status":"completed","content":{"type":"text","text":"done"}}
	}`))
	last = events[len(events)-1]
	if last.Type != EventToolResult {
		t.Errorf("last event = %+v, want EventToolResult", last)
	}
	if last.ToolError {
		t.Error("ToolError should be false for status=completed")
	}

	// tool_call_update with failed status
	tracker.handleServerNotification("session/update", json.RawMessage(`{
		"sessionId":"s1",
		"update":{"sessionUpdate":"tool_call_update","title":"Bash","status":"failed","content":{"type":"text","text":"err"}}
	}`))
	last = events[len(events)-1]
	if !last.ToolError {
		t.Error("ToolError should be true for status=failed")
	}
}

// TestACPEventTracker_RecordEnd verifies stop_reason→success mapping.
func TestACPEventTracker_RecordEnd(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		err         error
		wantSuccess bool
		wantReason  string
	}{
		{"end_turn = success", `{"stopReason":"end_turn"}`, nil, true, "end_turn"},
		{"max_tokens = fail", `{"stopReason":"max_tokens"}`, nil, false, "max_tokens"},
		{"cancelled = fail", `{"stopReason":"cancelled"}`, nil, false, "cancelled"},
		{"empty = no-response", `{}`, nil, false, "no-response"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tracker := &acpEventTracker{}
			tracker.recordEnd(json.RawMessage(c.raw), c.err)
			if tracker.success() != c.wantSuccess {
				t.Errorf("success = %v, want %v", tracker.success(), c.wantSuccess)
			}
			if tracker.stopReason() != c.wantReason {
				t.Errorf("stopReason = %q, want %q", tracker.stopReason(), c.wantReason)
			}
		})
	}
}

// TestACPEventTracker_Permission_AutoApprove verifies the auto-approve path
// picks the first option (preferring allow_once / allow_always by kind).
func TestACPEventTracker_Permission_AutoApprove(t *testing.T) {
	tracker := &acpEventTracker{autoApprove: true}

	// allow_once should be preferred over reject.
	resp, rpcErr := tracker.handleRequestPermission(json.RawMessage(`{
		"options":[
			{"optionId":"reject","kind":"reject","name":"Reject"},
			{"optionId":"allow1","kind":"allow_once","name":"Allow once"}
		]
	}`))
	if rpcErr != nil {
		t.Fatalf("rpc err: %v", rpcErr)
	}
	respMap, _ := resp.(map[string]any)
	outcome, _ := respMap["outcome"].(map[string]any)
	if outcome["optionId"] != "allow1" {
		t.Errorf("optionId = %v, want allow1 (allow_once preferred)", outcome["optionId"])
	}

	// No allow_* option: fall back to first.
	resp, _ = tracker.handleRequestPermission(json.RawMessage(`{
		"options":[
			{"optionId":"first","kind":"unknown"},
			{"optionId":"second","kind":"unknown"}
		]
	}`))
	respMap, _ = resp.(map[string]any)
	outcome, _ = respMap["outcome"].(map[string]any)
	if outcome["optionId"] != "first" {
		t.Errorf("optionId = %v, want first (fallback)", outcome["optionId"])
	}
}

// TestACPEventTracker_Permission_Disabled verifies cancellation when
// auto-approve is off.
func TestACPEventTracker_Permission_Disabled(t *testing.T) {
	tracker := &acpEventTracker{autoApprove: false}
	resp, _ := tracker.handleRequestPermission(json.RawMessage(`{"options":[]}`))
	respMap, _ := resp.(map[string]any)
	outcome, _ := respMap["outcome"].(map[string]any)
	if outcome["outcome"] != "cancelled" {
		t.Errorf("outcome = %v, want cancelled", outcome["outcome"])
	}
}

// TestACPEventTracker_FsRead_Denied shouldn't apply (read is always allowed),
// but FsWrite_Denied does.
func TestACPEventTracker_FsWrite_Denied(t *testing.T) {
	tracker := &acpEventTracker{allowWrite: false}
	_, rpcErr := tracker.handleFsWrite(json.RawMessage(`{"path":"/tmp/x","content":"y"}`))
	if rpcErr == nil {
		t.Fatal("expected rpc error when AllowFsWrite=false")
	}
	if !strings.Contains(rpcErr.Message, "denied") {
		t.Errorf("error = %q, want 'denied'", rpcErr.Message)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers for client tests
// ─────────────────────────────────────────────────────────────────────────────

type pipeWriteCloser struct{ w io.Writer }

func (p *pipeWriteCloser) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeWriteCloser) Close() error {
	if c, ok := p.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

type nopWriter struct{}

func (nopWriter) Write(b []byte) (int, error) { return len(b), nil }

type captureHandler struct {
	captured *struct {
		mu     sync.Mutex
		method string
		params json.RawMessage
	}
}

func (h *captureHandler) handleServerRequest(_ context.Context, method string, params json.RawMessage) (any, *acpRPCError) {
	h.captured.mu.Lock()
	defer h.captured.mu.Unlock()
	h.captured.method = method
	h.captured.params = params
	return map[string]any{}, nil
}

func (h *captureHandler) handleServerNotification(method string, params json.RawMessage) {
	h.captured.mu.Lock()
	defer h.captured.mu.Unlock()
	h.captured.method = method
	h.captured.params = params
}

// jsonNumber renders an int64 as a JSON number string (no quotes).
func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
