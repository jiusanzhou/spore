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

package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAgent is a minimal Agent implementation that records SubmitTask calls
// and lets the test fire OnTaskUpdate callbacks on demand.
type fakeAgent struct {
	mu           sync.Mutex
	submitted    []string
	nextTaskID   atomic.Int64
	onTaskUpdate func(taskID, status, runtime, result, errMsg string)
}

func (f *fakeAgent) SubmitTask(description string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, description)
	id := "t" + strings.TrimSpace(strings.ReplaceAll(time.Now().Format("150405.000"), ".", ""))
	// Avoid clashes when called rapidly in tests.
	id += "-" + intToStr(f.nextTaskID.Add(1))
	return id
}

func (f *fakeAgent) SetOnTaskUpdate(fn func(taskID, status, runtime, result, errMsg string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onTaskUpdate = fn
}

func (f *fakeAgent) fireUpdate(taskID, status, result string) {
	f.mu.Lock()
	cb := f.onTaskUpdate
	f.mu.Unlock()
	if cb != nil {
		cb(taskID, status, "builtin", result, "")
	}
}

func (f *fakeAgent) submittedCopy() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.submitted))
	copy(out, f.submitted)
	return out
}

func intToStr(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// fakeTelegramServer captures outbound API calls and drives inbound updates
// from a queue, simulating Telegram's HTTP surface for tests.
type fakeTelegramServer struct {
	mu       sync.Mutex
	updates  []tgUpdate     // queue served by getUpdates
	sent     []sentMessage  // history of sendMessage calls
	getMeUsr string         // what getMe returns
}

type sentMessage struct {
	ChatID int64
	Text   string
}

func newFakeTelegramServer() *fakeTelegramServer {
	return &fakeTelegramServer{getMeUsr: "test_bot"}
}

func (s *fakeTelegramServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path looks like /bot<token>/<method>
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) < 2 {
			http.Error(w, "bad path", http.StatusNotFound)
			return
		}
		method := parts[1]

		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			writeOK(w, map[string]any{"username": s.getMeUsr})
		case "getUpdates":
			s.mu.Lock()
			updates := s.updates
			s.updates = nil
			s.mu.Unlock()
			writeOK(w, updates)
		case "sendMessage":
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				ChatID int64  `json:"chat_id"`
				Text   string `json:"text"`
			}
			_ = json.Unmarshal(body, &payload)
			s.mu.Lock()
			s.sent = append(s.sent, sentMessage{ChatID: payload.ChatID, Text: payload.Text})
			s.mu.Unlock()
			writeOK(w, map[string]any{"message_id": 1})
		default:
			http.Error(w, "method not implemented in fake: "+method, http.StatusNotImplemented)
		}
	})
}

func writeOK(w http.ResponseWriter, result any) {
	resp := map[string]any{"ok": true, "result": result}
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *fakeTelegramServer) enqueueText(chatID int64, text string, updateID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, tgUpdate{
		UpdateID: updateID,
		Message: &tgMessage{
			MessageID: updateID,
			Date:      time.Now().Unix(),
			Text:      text,
			From:      tgUser{ID: 100},
			Chat:      tgChat{ID: chatID, Type: "private"},
		},
	})
}

func (s *fakeTelegramServer) sentCopy() []sentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sentMessage, len(s.sent))
	copy(out, s.sent)
	return out
}

// ---- Tests ------------------------------------------------------------------

func TestNewTelegramGatewayValidation(t *testing.T) {
	cases := []struct {
		name   string
		cfg    TelegramConfig
		agent  Agent
		errSub string
	}{
		{
			name:   "disabled",
			cfg:    TelegramConfig{Enabled: false, Token: "t", ChatIDs: []int64{1}},
			agent:  &fakeAgent{},
			errSub: "not enabled",
		},
		{
			name:   "missing token",
			cfg:    TelegramConfig{Enabled: true, ChatIDs: []int64{1}},
			agent:  &fakeAgent{},
			errSub: "token required",
		},
		{
			name:   "empty allow-list",
			cfg:    TelegramConfig{Enabled: true, Token: "t"},
			agent:  &fakeAgent{},
			errSub: "chat_ids allow-list required",
		},
		{
			name:   "nil agent",
			cfg:    TelegramConfig{Enabled: true, Token: "t", ChatIDs: []int64{1}},
			agent:  nil,
			errSub: "agent is nil",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewTelegramGateway(c.cfg, c.agent)
			if err == nil {
				t.Fatalf("expected error containing %q", c.errSub)
			}
			if !strings.Contains(err.Error(), c.errSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.errSub)
			}
		})
	}
}

func TestNewTelegramGatewayWiresCallback(t *testing.T) {
	a := &fakeAgent{}
	g, err := NewTelegramGateway(TelegramConfig{
		Enabled: true, Token: "t", ChatIDs: []int64{1},
	}, a)
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		t.Fatal("nil gateway")
	}
	if a.onTaskUpdate == nil {
		t.Error("expected NewTelegramGateway to set OnTaskUpdate")
	}
}

// TestEndToEnd drives the full loop: fake server → gateway → fakeAgent →
// fake update fired back → gateway sends a reply.
func TestEndToEnd(t *testing.T) {
	fakeSrv := newFakeTelegramServer()
	httpSrv := httptest.NewServer(fakeSrv.handler())
	defer httpSrv.Close()

	a := &fakeAgent{}
	g, err := NewTelegramGateway(TelegramConfig{
		Enabled: true,
		Token:   "ABCDEF",
		ChatIDs: []int64{42},
		APIBase: httpSrv.URL,
	}, a)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- g.Start(ctx) }()

	// Allowed chat: should be submitted as a task.
	fakeSrv.enqueueText(42, "research quantum stuff", 1)
	// Disallowed chat: should be rejected.
	fakeSrv.enqueueText(99, "you can't tell me what to do", 2)
	// Slash command: should be handled inline, not submitted.
	fakeSrv.enqueueText(42, "/help", 3)

	// Wait long enough for at least one getUpdates round-trip.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(a.submittedCopy()) > 0 && len(fakeSrv.sentCopy()) >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	submitted := a.submittedCopy()
	if len(submitted) != 1 || submitted[0] != "research quantum stuff" {
		t.Fatalf("submitted = %v, want exactly [research quantum stuff]", submitted)
	}

	// Now simulate the agent finishing the task.
	// Find the taskID we just routed.
	g.mu.Lock()
	var routedTaskID string
	for id := range g.taskRoutes {
		routedTaskID = id
		break
	}
	g.mu.Unlock()
	if routedTaskID == "" {
		t.Fatal("expected one taskRoutes entry, found none")
	}

	a.fireUpdate(routedTaskID, "completed", "the answer is 42")

	// Wait for the result to propagate.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := fakeSrv.sentCopy()
		if hasMessage(got, "the answer is 42") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got := fakeSrv.sentCopy()
	if !hasMessage(got, "the answer is 42") {
		t.Errorf("expected reply with task result; got: %+v", got)
	}
	if !hasMessage(got, "not authorized") {
		t.Errorf("expected rejection message to chat 99; got: %+v", got)
	}
	if !hasMessage(got, "spore agent") { // /help text
		t.Errorf("expected /help reply; got: %+v", got)
	}

	// Routing entry should be cleared after delivery.
	g.mu.Lock()
	leftover := len(g.taskRoutes)
	g.mu.Unlock()
	if leftover != 0 {
		t.Errorf("taskRoutes not cleared: %d entries left", leftover)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

func hasMessage(msgs []sentMessage, substring string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Text, substring) {
			return true
		}
	}
	return false
}

func TestFormatHelpers(t *testing.T) {
	if got := formatSuccess("builtin", "hello"); !strings.Contains(got, "✅") || !strings.Contains(got, "hello") {
		t.Errorf("formatSuccess wrong: %q", got)
	}
	if got := formatSuccess("builtin", ""); !strings.Contains(got, "no output") {
		t.Errorf("formatSuccess empty: %q", got)
	}
	if got := formatFailure("builtin", "boom"); !strings.Contains(got, "❌") || !strings.Contains(got, "boom") {
		t.Errorf("formatFailure wrong: %q", got)
	}
	long := strings.Repeat("x", 5000)
	if got := truncate(long, 100); !strings.HasSuffix(got, "[…truncated]") {
		t.Errorf("truncate did not mark suffix: %q", got[len(got)-30:])
	}
}

func TestChatIDString(t *testing.T) {
	if got := chatIDString([]int64{1, 2, 3}); got != "[1,2,3]" {
		t.Errorf("got %q, want [1,2,3]", got)
	}
}
