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
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// End-to-end ACP server tests.
//
// Strategy: wire a fakeStreamingRuntime into ACPServer, run Serve() against
// one half of an in-memory pipe, then drive the other half with our own
// acpClient. This exercises the full protocol path — frame layer, dispatch,
// handler routing, StreamEvent → session/update bridge — without subprocess
// overhead.
// ─────────────────────────────────────────────────────────────────────────────

// fakeStreamingRuntime emits a scripted sequence of events then returns a
// fixed TaskOutput. Used as the inner Runtime for ACPServer in tests.
type fakeStreamingRuntime struct {
	name   string
	events []StreamEvent
	final  *TaskOutput
	err    error

	// For cancellation tests: when set, the runtime blocks on this channel
	// until ctx is cancelled, mimicking a long-running prompt.
	hold chan struct{}

	calledMu sync.Mutex
	calls    int
}

func (f *fakeStreamingRuntime) Info() Info {
	return Info{
		Name:         f.name,
		Capabilities: []Capability{{Name: "general", Tags: []string{"general"}}},
	}
}

func (f *fakeStreamingRuntime) Healthy(_ context.Context) error { return nil }
func (f *fakeStreamingRuntime) Close() error                    { return nil }

func (f *fakeStreamingRuntime) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	return f.ExecuteStream(ctx, task, nil)
}

func (f *fakeStreamingRuntime) ExecuteStream(ctx context.Context, task TaskInput, h EventHandler) (*TaskOutput, error) {
	f.calledMu.Lock()
	f.calls++
	f.calledMu.Unlock()

	for _, ev := range f.events {
		if h != nil {
			if err := h(ev); err != nil {
				return nil, err
			}
		}
	}
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.final, f.err
}

// pairedPipes returns two duplex pipes wired up so writes on one side
// surface as reads on the other. Returns (clientIn, clientOut, serverIn,
// serverOut) where clientOut→serverIn and serverOut→clientIn.
func pairedPipes() (clientIn io.ReadCloser, clientOut io.WriteCloser, serverIn io.ReadCloser, serverOut io.WriteCloser) {
	cR, sW := io.Pipe() // server writes, client reads
	sR, cW := io.Pipe() // client writes, server reads
	return cR, &pipeWriteCloser{w: cW}, sR, &pipeWriteCloser{w: sW}
}

// startTestServer spins up an ACPServer with the given fake runtime against
// in-memory pipes and returns an acpClient already running its read loop
// on the client end. Caller must call cleanup() to tear down.
func startTestServer(t *testing.T, fake *fakeStreamingRuntime) (cli *acpClient, sessionID string, cleanup func()) {
	t.Helper()

	clientIn, clientOut, serverIn, serverOut := pairedPipes()

	srv := NewACPServer(fake)
	srv.Logger = func(s string) { t.Logf("[server] %s", s) }

	srvCtx, srvCancel := context.WithCancel(context.Background())
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = srv.Serve(srvCtx, serverIn, serverOut)
	}()

	// Client side wraps the same pipes with acpClient.
	cli = newACPClient(clientIn, clientOut)
	cliCtx, cliCancel := context.WithCancel(context.Background())
	go cli.readLoop(cliCtx)

	cleanup = func() {
		srvCancel()
		cliCancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		select {
		case <-srvDone:
		case <-time.After(2 * time.Second):
			t.Logf("server didn't shut down within 2s")
		}
	}

	return cli, "", cleanup
}

func TestACPServer_Initialize(t *testing.T) {
	fake := &fakeStreamingRuntime{name: "fake"}
	cli, _, cleanup := startTestServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := cli.call(ctx, "initialize", map[string]any{"protocolVersion": 1})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	var got struct {
		ProtocolVersion int `json:"protocolVersion"`
		AgentInfo       struct {
			Name string `json:"name"`
		} `json:"agentInfo"`
		AgentCapabilities map[string]any `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ProtocolVersion != 1 {
		t.Errorf("protocolVersion = %d, want 1", got.ProtocolVersion)
	}
	if got.AgentInfo.Name != "spore" {
		t.Errorf("agentInfo.name = %q, want spore", got.AgentInfo.Name)
	}
	if got.AgentCapabilities == nil {
		t.Errorf("agentCapabilities missing")
	}
}

func TestACPServer_SessionNew(t *testing.T) {
	fake := &fakeStreamingRuntime{name: "fake"}
	cli, _, cleanup := startTestServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := cli.call(ctx, "initialize", map[string]any{"protocolVersion": 1})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	resp, err := cli.call(ctx, "session/new", map[string]any{"cwd": "/tmp"})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	var got struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SessionID == "" {
		t.Errorf("empty sessionId returned")
	}
}

// captureClient is an acpServerHandler that lets us collect inbound
// notifications (session/update) on the client side.
type captureClient struct {
	mu      sync.Mutex
	updates []json.RawMessage
}

func (c *captureClient) handleServerRequest(ctx context.Context, method string, params json.RawMessage) (any, *acpRPCError) {
	return nil, &acpRPCError{Code: -32601, Message: "no requests expected"}
}
func (c *captureClient) handleServerNotification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	c.mu.Lock()
	c.updates = append(c.updates, append(json.RawMessage(nil), params...))
	c.mu.Unlock()
}

func TestACPServer_PromptRoundtrip_StreamingEvents(t *testing.T) {
	fake := &fakeStreamingRuntime{
		name: "fake",
		events: []StreamEvent{
			{Type: EventThinking, Content: "thinking step 1"},
			{Type: EventToolCall, ToolName: "shell", ToolInput: `{"cmd":"ls"}`},
			{Type: EventToolResult, ToolName: "shell", ToolOutput: "file1\nfile2"},
			{Type: EventThinking, Content: "thinking step 2"},
		},
		final: &TaskOutput{Success: true, Result: "final answer"},
	}

	cap := &captureClient{}
	clientIn, clientOut, serverIn, serverOut := pairedPipes()

	srv := NewACPServer(fake)
	srv.Logger = func(s string) { t.Logf("[server] %s", s) }
	srvCtx, srvCancel := context.WithCancel(context.Background())
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = srv.Serve(srvCtx, serverIn, serverOut)
	}()

	cli := newACPClient(clientIn, clientOut)
	cli.handler = cap
	cliCtx, cliCancel := context.WithCancel(context.Background())
	go cli.readLoop(cliCtx)

	defer func() {
		srvCancel()
		cliCancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		select {
		case <-srvDone:
		case <-time.After(2 * time.Second):
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := cli.call(ctx, "initialize", map[string]any{"protocolVersion": 1}); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	respRaw, err := cli.call(ctx, "session/new", map[string]any{"cwd": "/tmp"})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var sn struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(respRaw, &sn)

	promptResp, err := cli.call(ctx, "session/prompt", map[string]any{
		"sessionId": sn.SessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": "hello spore"}},
	})
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	var pr struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(promptResp, &pr); err != nil {
		t.Fatalf("unmarshal prompt resp: %v", err)
	}
	if pr.StopReason != "end_turn" {
		t.Errorf("stopReason = %q, want end_turn", pr.StopReason)
	}

	// Give notifications a moment to drain.
	time.Sleep(50 * time.Millisecond)

	cap.mu.Lock()
	updates := append([]json.RawMessage{}, cap.updates...)
	cap.mu.Unlock()

	// Expect: 4 streamed events (2 thinking + 1 tool_call + 1 tool_result).
	// We do NOT expect a final result echo because fake is a StreamingRuntime
	// — the streaming path is responsible for emitting the answer text and
	// the server doesn't double-echo it.
	if len(updates) < 4 {
		t.Fatalf("got %d updates, want >= 4; updates=%v",
			len(updates), updatesToStrings(updates))
	}

	// Spot-check: at least one tool_call and one tool_call_update.
	var sawToolCall, sawToolResult, sawThinking bool
	for _, raw := range updates {
		var p struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		_ = json.Unmarshal(raw, &p)
		switch p.Update.SessionUpdate {
		case "tool_call":
			sawToolCall = true
		case "tool_call_update":
			sawToolResult = true
		case "agent_message_chunk":
			if strings.Contains(p.Update.Content.Text, "thinking step") {
				sawThinking = true
			}
		}
	}
	if !sawToolCall {
		t.Errorf("no tool_call notification found")
	}
	if !sawToolResult {
		t.Errorf("no tool_call_update notification found")
	}
	if !sawThinking {
		t.Errorf("no thinking content forwarded as agent_message_chunk")
	}
}

// TestACPServer_NonStreamingFinalEcho: when the inner Runtime does NOT
// implement StreamingRuntime, the server should fall back to echoing
// the final result text as an agent_message_chunk so the client still
// sees something. Verifies the streaming-vs-non-streaming branch in
// handleSessionPrompt.
func TestACPServer_NonStreamingFinalEcho(t *testing.T) {
	// nonStreamingRuntime is a Runtime but NOT a StreamingRuntime — Execute
	// returns synchronously without ever calling the EventHandler.
	rt := &nonStreamingRuntime{
		final: &TaskOutput{Success: true, Result: "non-streamed answer"},
	}

	cap := &captureClient{}
	clientIn, clientOut, serverIn, serverOut := pairedPipes()

	srv := NewACPServer(rt)
	srv.Logger = func(s string) { t.Logf("[server] %s", s) }
	srvCtx, srvCancel := context.WithCancel(context.Background())
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = srv.Serve(srvCtx, serverIn, serverOut)
	}()

	cli := newACPClient(clientIn, clientOut)
	cli.handler = cap
	cliCtx, cliCancel := context.WithCancel(context.Background())
	go cli.readLoop(cliCtx)

	defer func() {
		srvCancel()
		cliCancel()
		_ = clientOut.Close()
		_ = serverOut.Close()
		select {
		case <-srvDone:
		case <-time.After(2 * time.Second):
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = cli.call(ctx, "initialize", map[string]any{"protocolVersion": 1})
	respRaw, _ := cli.call(ctx, "session/new", map[string]any{"cwd": "/tmp"})
	var sn struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(respRaw, &sn)

	_, err := cli.call(ctx, "session/prompt", map[string]any{
		"sessionId": sn.SessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": "hi"}},
	})
	if err != nil {
		t.Fatalf("session/prompt: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.updates) == 0 {
		t.Fatal("expected at least one session/update echoing the result, got 0")
	}
	found := false
	for _, raw := range cap.updates {
		if strings.Contains(string(raw), "non-streamed answer") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("final result not echoed; updates=%v", updatesToStrings(cap.updates))
	}
}

// nonStreamingRuntime is a Runtime that does NOT implement StreamingRuntime.
type nonStreamingRuntime struct {
	final *TaskOutput
}

func (n *nonStreamingRuntime) Info() Info {
	return Info{Name: "non-streaming", Capabilities: []Capability{{Name: "general", Tags: []string{"general"}}}}
}
func (n *nonStreamingRuntime) Healthy(_ context.Context) error { return nil }
func (n *nonStreamingRuntime) Close() error                    { return nil }
func (n *nonStreamingRuntime) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	return n.final, nil
}

func updatesToStrings(updates []json.RawMessage) []string {
	out := make([]string, len(updates))
	for i, u := range updates {
		out[i] = string(u)
	}
	return out
}

func TestACPServer_PromptUnknownSession(t *testing.T) {
	fake := &fakeStreamingRuntime{name: "fake"}
	cli, _, cleanup := startTestServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := cli.call(ctx, "session/prompt", map[string]any{
		"sessionId": "does-not-exist",
		"prompt":    []any{map[string]any{"type": "text", "text": "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for unknown sessionId")
	}
	if !strings.Contains(err.Error(), "unknown sessionId") {
		t.Errorf("err = %v, want 'unknown sessionId'", err)
	}
}

func TestACPServer_PromptEmpty(t *testing.T) {
	fake := &fakeStreamingRuntime{name: "fake", final: &TaskOutput{Success: true}}
	cli, _, cleanup := startTestServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = cli.call(ctx, "initialize", map[string]any{"protocolVersion": 1})
	respRaw, _ := cli.call(ctx, "session/new", map[string]any{"cwd": "/tmp"})
	var sn struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(respRaw, &sn)

	_, err := cli.call(ctx, "session/prompt", map[string]any{
		"sessionId": sn.SessionID,
		"prompt":    []any{}, // empty
	})
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestACPServer_UnknownMethod(t *testing.T) {
	fake := &fakeStreamingRuntime{name: "fake"}
	cli, _, cleanup := startTestServer(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := cli.call(ctx, "session/load", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unimplemented method")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("err = %v, want 'method not found'", err)
	}
}

func TestACPServer_FlattenPromptBlocks(t *testing.T) {
	cases := []struct {
		name   string
		blocks []json.RawMessage
		want   string
	}{
		{
			name: "single text",
			blocks: []json.RawMessage{
				json.RawMessage(`{"type":"text","text":"hello"}`),
			},
			want: "hello",
		},
		{
			name: "multi text concat with newline",
			blocks: []json.RawMessage{
				json.RawMessage(`{"type":"text","text":"line 1"}`),
				json.RawMessage(`{"type":"text","text":"line 2"}`),
			},
			want: "line 1\nline 2",
		},
		{
			name: "non-text dropped",
			blocks: []json.RawMessage{
				json.RawMessage(`{"type":"image","data":"..."}`),
				json.RawMessage(`{"type":"text","text":"only this"}`),
			},
			want: "only this",
		},
		{
			name:   "empty",
			blocks: []json.RawMessage{},
			want:   "",
		},
		{
			name: "malformed dropped",
			blocks: []json.RawMessage{
				json.RawMessage(`not json`),
				json.RawMessage(`{"type":"text","text":"ok"}`),
			},
			want: "ok",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flattenPromptBlocks(tc.blocks)
			if got != tc.want {
				t.Errorf("flattenPromptBlocks = %q, want %q", got, tc.want)
			}
		})
	}
}
