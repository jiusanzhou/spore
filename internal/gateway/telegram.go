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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TelegramConfig configures the Telegram gateway. Mirrored at config.toml:
//
//	[gateway.telegram]
//	enabled  = true
//	token    = "123:abc..."        # bot token from @BotFather
//	chat_ids = [123456789]         # allow-list of chat IDs (REQUIRED)
//	api_base = "https://api.telegram.org"  # optional, override for proxies
type TelegramConfig struct {
	Enabled bool    `toml:"enabled" yaml:"enabled" json:"enabled" opts:"help=enable telegram gateway"`
	Token   string  `toml:"token" yaml:"token" json:"token" opts:"help=bot token from @BotFather"`
	ChatIDs []int64 `toml:"chat_ids" yaml:"chat_ids" json:"chat_ids" opts:"name=chat-ids,help=comma-separated allow-list of chat IDs"`
	APIBase string  `toml:"api_base" yaml:"api_base" json:"api_base" opts:"name=api-base,help=override Telegram API base URL"`
}

// TelegramGateway connects one Spore agent to one Telegram bot.
type TelegramGateway struct {
	cfg     TelegramConfig
	agent   Agent
	client  *http.Client
	apiBase string
	allowed map[int64]struct{}

	// taskRoutes maps a submitted task's ID to the chat that originated it,
	// so when the agent emits an update we know where to reply.
	mu         sync.Mutex
	taskRoutes map[string]int64
}

// NewTelegramGateway constructs a gateway. Returns an error if the config is
// missing required fields. The gateway does not connect until Start is called.
func NewTelegramGateway(cfg TelegramConfig, agent Agent) (*TelegramGateway, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("telegram gateway: not enabled in config")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("telegram gateway: token required")
	}
	if len(cfg.ChatIDs) == 0 {
		return nil, fmt.Errorf("telegram gateway: chat_ids allow-list required (set at least one)")
	}
	if agent == nil {
		return nil, fmt.Errorf("telegram gateway: agent is nil")
	}

	apiBase := strings.TrimRight(cfg.APIBase, "/")
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}

	allowed := make(map[int64]struct{}, len(cfg.ChatIDs))
	for _, id := range cfg.ChatIDs {
		allowed[id] = struct{}{}
	}

	g := &TelegramGateway{
		cfg:        cfg,
		agent:      agent,
		client:     &http.Client{Timeout: 35 * time.Second},
		apiBase:    apiBase,
		allowed:    allowed,
		taskRoutes: make(map[string]int64),
	}

	// Wire up the agent's task lifecycle callback so we can reply when
	// tasks finish. Must be set before Start so we don't miss early updates.
	agent.SetOnTaskUpdate(g.onTaskUpdate)

	return g, nil
}

// Name implements Gateway.
func (g *TelegramGateway) Name() string { return "telegram" }

// Start implements Gateway. Long-poll Telegram until ctx is done.
func (g *TelegramGateway) Start(ctx context.Context) error {
	// Verify the bot token by calling getMe. This catches typos before the
	// long-poll loop swallows them silently.
	me, err := g.getMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram getMe: %w", err)
	}
	fmt.Printf("📱 Telegram gateway online: @%s (allowed chats: %v)\n", me, g.cfg.ChatIDs)

	// Greet every allowed chat once on startup so the user knows the agent
	// is alive. Errors here are non-fatal.
	for _, chatID := range g.cfg.ChatIDs {
		_ = g.sendMessage(ctx, chatID, "🌱 spore agent online — send me a task")
	}

	var lastUpdateID int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		updates, err := g.getUpdates(ctx, lastUpdateID+1, 30)
		if err != nil {
			// Network blips: log and back off. Don't return — the gateway
			// should self-heal on transient errors.
			fmt.Printf("⚠️  telegram getUpdates: %v (retrying in 5s)\n", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for _, u := range updates {
			lastUpdateID = u.UpdateID
			g.handleUpdate(ctx, u)
		}
	}
}

// handleUpdate processes one inbound Telegram update. Errors are logged but
// never returned — one bad message must not kill the gateway loop.
func (g *TelegramGateway) handleUpdate(ctx context.Context, u tgUpdate) {
	if u.Message == nil {
		return
	}
	m := u.Message
	if m.Text == "" {
		return // ignore stickers, photos, etc. for now
	}

	// Allow-list enforcement
	if _, ok := g.allowed[m.Chat.ID]; !ok {
		fmt.Printf("🚫 telegram: rejected message from unauthorized chat %d (text: %q)\n",
			m.Chat.ID, truncate(m.Text, 60))
		_ = g.sendMessage(ctx, m.Chat.ID,
			fmt.Sprintf("not authorized — chat ID %d is not on this agent's allow-list", m.Chat.ID))
		return
	}

	// Slash commands handled inline; everything else becomes a task.
	if strings.HasPrefix(m.Text, "/") {
		g.handleCommand(ctx, m)
		return
	}

	taskID := g.agent.SubmitTask(m.Text)

	g.mu.Lock()
	g.taskRoutes[taskID] = m.Chat.ID
	g.mu.Unlock()

	_ = g.sendMessage(ctx, m.Chat.ID,
		fmt.Sprintf("🌀 task %s queued — I'll reply when it's done", taskID))
}

// handleCommand processes /start, /help, /id and similar.
func (g *TelegramGateway) handleCommand(ctx context.Context, m *tgMessage) {
	cmd := strings.SplitN(m.Text, " ", 2)[0]
	cmd = strings.SplitN(cmd, "@", 2)[0] // strip bot username suffix
	switch cmd {
	case "/start", "/help":
		_ = g.sendMessage(ctx, m.Chat.ID, helpText)
	case "/id":
		_ = g.sendMessage(ctx, m.Chat.ID, fmt.Sprintf("chat_id: %d\nuser_id: %d", m.Chat.ID, m.From.ID))
	default:
		_ = g.sendMessage(ctx, m.Chat.ID, "unknown command — try /help")
	}
}

const helpText = `🌱 spore agent

Send any message and I'll treat it as a task description. I'll reply with the
result when execution finishes.

Commands:
  /help — this message
  /id   — show your chat/user IDs (useful when an admin needs to add you to
          the allow-list)`

// onTaskUpdate is the callback the agent invokes when a task changes state.
// We only reply on terminal states (completed / failed) to avoid spam.
func (g *TelegramGateway) onTaskUpdate(taskID, status, runtimeName, result, errMsg string) {
	switch strings.ToLower(status) {
	case "completed", "success", "done":
		g.flushReply(taskID, formatSuccess(runtimeName, result))
	case "failed", "error":
		g.flushReply(taskID, formatFailure(runtimeName, errMsg))
	default:
		// "running", "queued" etc. — silently ignored for now.
	}
}

// flushReply pops the chat-id route and sends the reply (if any).
func (g *TelegramGateway) flushReply(taskID, body string) {
	g.mu.Lock()
	chatID, ok := g.taskRoutes[taskID]
	if ok {
		delete(g.taskRoutes, taskID)
	}
	g.mu.Unlock()
	if !ok {
		return // task not originated by this gateway
	}
	// Use a fresh background ctx so a slow shutdown doesn't drop the
	// last result; with a short timeout to bound stuck sends.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.sendMessage(ctx, chatID, body); err != nil {
		fmt.Printf("⚠️  telegram reply send: %v\n", err)
	}
}

func formatSuccess(runtimeName, result string) string {
	header := "✅ done"
	if runtimeName != "" {
		header += " (" + runtimeName + ")"
	}
	if strings.TrimSpace(result) == "" {
		return header + "\n(no output)"
	}
	return header + "\n\n" + truncate(result, 3500)
}

func formatFailure(runtimeName, errMsg string) string {
	header := "❌ failed"
	if runtimeName != "" {
		header += " (" + runtimeName + ")"
	}
	if strings.TrimSpace(errMsg) == "" {
		return header
	}
	return header + ": " + truncate(errMsg, 3500)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n[…truncated]"
}

// ---- Telegram HTTP client (no SDK; just two endpoints) ----------------------

type tgUpdate struct {
	UpdateID int64      `json:"update_id"`
	Message  *tgMessage `json:"message,omitempty"`
}

type tgMessage struct {
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
	From      tgUser `json:"from"`
	Chat      tgChat `json:"chat"`
}

type tgUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
}

// getMe verifies the token and returns the bot username.
func (g *TelegramGateway) getMe(ctx context.Context) (string, error) {
	var raw json.RawMessage
	if err := g.callAPI(ctx, "getMe", nil, &raw); err != nil {
		return "", err
	}
	var bot struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &bot); err != nil {
		return "", fmt.Errorf("decode getMe: %w", err)
	}
	return bot.Username, nil
}

// getUpdates is the long-poll endpoint. timeoutSec=0 makes it short-poll.
func (g *TelegramGateway) getUpdates(ctx context.Context, offset int64, timeoutSec int) ([]tgUpdate, error) {
	body := map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message"},
	}
	var raw json.RawMessage
	if err := g.callAPI(ctx, "getUpdates", body, &raw); err != nil {
		return nil, err
	}
	var updates []tgUpdate
	if err := json.Unmarshal(raw, &updates); err != nil {
		return nil, fmt.Errorf("decode updates: %w", err)
	}
	return updates, nil
}

// sendMessage delivers text to one chat. Long messages are caller-truncated.
func (g *TelegramGateway) sendMessage(ctx context.Context, chatID int64, text string) error {
	body := map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	}
	return g.callAPI(ctx, "sendMessage", body, nil)
}

// callAPI is the single Telegram HTTP entry point. resultPtr (*json.RawMessage
// or struct) receives the unmarshaled .result field; pass nil to discard.
func (g *TelegramGateway) callAPI(ctx context.Context, method string, body any, resultPtr any) error {
	endpoint := g.apiBase + "/bot" + url.PathEscape(g.cfg.Token) + "/" + method

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	var tgResp tgResponse
	if err := json.Unmarshal(rawBody, &tgResp); err != nil {
		return fmt.Errorf("decode response (status %d): %w; body=%s",
			resp.StatusCode, err, truncate(string(rawBody), 200))
	}
	if !tgResp.OK {
		return fmt.Errorf("telegram %s failed: %s (code %d)",
			method, tgResp.Description, tgResp.ErrorCode)
	}

	if resultPtr != nil && len(tgResp.Result) > 0 {
		if err := json.Unmarshal(tgResp.Result, resultPtr); err != nil {
			return fmt.Errorf("decode .result: %w", err)
		}
	}
	return nil
}

// chatIDString renders the allow-list, used by tests/log output.
func chatIDString(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
