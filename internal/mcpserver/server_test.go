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

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/swarm"
)

// ─────────────────────────────────────────────────────────────────────────────
// In-memory fixture: fakeSwarm implements SwarmAccessor without spinning up
// the real Swarm (which needs an LLM provider, libp2p host, file system, …).
// We deliberately keep the fixture small — these tests exercise the MCP
// surface area, not the swarm internals (those have their own tests).
// ─────────────────────────────────────────────────────────────────────────────

type fakeSwarm struct {
	agents map[string]*agent.Agent
	infos  []agent.Info
	stats  swarm.SwarmStats
	tasks  []swarm.TaskEvent

	sendErr   error
	lastSent  []sentTask
	taskIDSeq int
}

type sentTask struct {
	agent string
	desc  string
	id    string
}

func (f *fakeSwarm) List() []agent.Info       { return f.infos }
func (f *fakeSwarm) GetAgent(n string) *agent.Agent { return f.agents[n] }
func (f *fakeSwarm) Stats() swarm.SwarmStats   { return f.stats }
func (f *fakeSwarm) TaskLog() []swarm.TaskEvent {
	return append([]swarm.TaskEvent(nil), f.tasks...)
}
func (f *fakeSwarm) SendTask(name, desc string) (string, error) {
	if f.sendErr != nil {
		return "", f.sendErr
	}
	f.taskIDSeq++
	id := fmt.Sprintf("task-%d", f.taskIDSeq)
	f.lastSent = append(f.lastSent, sentTask{agent: name, desc: desc, id: id})
	return id, nil
}

// newTestServerAndClient wires Server → MCPServer → in-process Client and
// initializes the MCP handshake. Returns the client + a teardown.
func newTestServerAndClient(t *testing.T, sw SwarmAccessor) (*client.Client, func()) {
	t.Helper()

	srv := NewServer(sw)
	cli, err := client.NewInProcessClient(srv.MCPServer())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := cli.Start(ctx); err != nil {
		cancel()
		t.Fatalf("client start: %v", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "0.0.0"}
	if _, err := cli.Initialize(ctx, initReq); err != nil {
		cancel()
		t.Fatalf("initialize: %v", err)
	}
	cancel()

	return cli, func() { _ = cli.Close() }
}

func textFromResult(r *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestServer_ToolsListed(t *testing.T) {
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := []string{
		"spore_list_agents", "spore_get_agent", "spore_send_task",
		"spore_swarm_stats", "spore_recent_tasks", "spore_agent_skills",
		"spore_agent_experience", "spore_peer_fitness",
	}
	got := make(map[string]bool)
	for _, tool := range resp.Tools {
		got[tool.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing tool: %s (got: %v)", w, got)
		}
	}
}

func TestServer_ListAgents(t *testing.T) {
	infos := []agent.Info{
		{Name: "alice", Role: "researcher", Runtime: "claude-code", TaskCount: 3},
		{Name: "bob", Role: "writer", Runtime: "codex", TaskCount: 7},
	}
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{infos: infos})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_list_agents"
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	body := textFromResult(resp)
	var got []agent.Info
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body)
	}
	if len(got) != 2 || got[0].Name != "alice" || got[1].Name != "bob" {
		t.Errorf("got = %+v, want alice + bob", got)
	}
}

func TestServer_GetAgent_NotFound(t *testing.T) {
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_get_agent"
	req.Params.Arguments = map[string]any{"name": "nonexistent"}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError=true for missing agent, got %v", resp)
	}
	if !strings.Contains(textFromResult(resp), "not found") {
		t.Errorf("error text doesn't mention 'not found': %s", textFromResult(resp))
	}
}

func TestServer_SendTask_Success(t *testing.T) {
	fake := &fakeSwarm{}
	cli, cleanup := newTestServerAndClient(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_send_task"
	req.Params.Arguments = map[string]any{
		"agent": "alice",
		"task":  "summarize this paper",
	}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.IsError {
		t.Fatalf("unexpected IsError: %s", textFromResult(resp))
	}

	var got map[string]string
	_ = json.Unmarshal([]byte(textFromResult(resp)), &got)
	if got["task_id"] == "" || got["agent"] != "alice" {
		t.Errorf("unexpected response: %+v", got)
	}
	if len(fake.lastSent) != 1 || fake.lastSent[0].agent != "alice" ||
		fake.lastSent[0].desc != "summarize this paper" {
		t.Errorf("SendTask not invoked correctly: %+v", fake.lastSent)
	}
}

func TestServer_SendTask_MissingArgs(t *testing.T) {
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Missing 'task' arg.
	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_send_task"
	req.Params.Arguments = map[string]any{"agent": "alice"}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError for missing 'task' arg")
	}
}

func TestServer_SendTask_PropagatesError(t *testing.T) {
	fake := &fakeSwarm{sendErr: fmt.Errorf("agent capacity exceeded")}
	cli, cleanup := newTestServerAndClient(t, fake)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_send_task"
	req.Params.Arguments = map[string]any{"agent": "alice", "task": "x"}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError when SendTask returns error")
	}
	if !strings.Contains(textFromResult(resp), "capacity exceeded") {
		t.Errorf("error text missing inner cause: %s", textFromResult(resp))
	}
}

func TestServer_SwarmStats(t *testing.T) {
	stats := swarm.SwarmStats{
		TotalAgents:    5,
		ActiveAgents:   3,
		TotalCompleted: 42,
		UptimeSeconds:  7777,
	}
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{stats: stats})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_swarm_stats"
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got swarm.SwarmStats
	_ = json.Unmarshal([]byte(textFromResult(resp)), &got)
	if got.TotalAgents != 5 || got.ActiveAgents != 3 || got.TotalCompleted != 42 {
		t.Errorf("got = %+v, want TotalAgents=5 ActiveAgents=3 TotalCompleted=42", got)
	}
}

func TestServer_RecentTasks_LimitDefault(t *testing.T) {
	tasks := make([]swarm.TaskEvent, 50)
	for i := range tasks {
		tasks[i] = swarm.TaskEvent{
			ID:          fmt.Sprintf("t-%d", i),
			Agent:       "alice",
			Status:      "completed",
			SubmittedAt: time.Now(),
		}
	}
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{tasks: tasks})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_recent_tasks"
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got []swarm.TaskEvent
	_ = json.Unmarshal([]byte(textFromResult(resp)), &got)
	if len(got) != 20 {
		t.Errorf("default limit should be 20, got %d", len(got))
	}
	// Most recent should be t-49 (last appended).
	if got[len(got)-1].ID != "t-49" {
		t.Errorf("oldest entry returned, expected newest at end: got last=%s", got[len(got)-1].ID)
	}
}

func TestServer_RecentTasks_CustomLimit(t *testing.T) {
	tasks := make([]swarm.TaskEvent, 10)
	for i := range tasks {
		tasks[i] = swarm.TaskEvent{ID: fmt.Sprintf("t-%d", i), Status: "running"}
	}
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{tasks: tasks})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_recent_tasks"
	req.Params.Arguments = map[string]any{"limit": 3.0}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got []swarm.TaskEvent
	_ = json.Unmarshal([]byte(textFromResult(resp)), &got)
	if len(got) != 3 {
		t.Errorf("custom limit=3, got %d items", len(got))
	}
}

func TestServer_AgentSkills_AgentNotFound(t *testing.T) {
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_agent_skills"
	req.Params.Arguments = map[string]any{"agent": "ghost"}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError for missing agent")
	}
}

func TestServer_NilSwarm_ServeStdioRejects(t *testing.T) {
	srv := &Server{}
	err := srv.ServeStdio(context.Background())
	if err == nil {
		t.Fatal("expected error when ServeStdio called with nil swarm")
	}
	if !strings.Contains(err.Error(), "swarm is nil") {
		t.Errorf("err = %v, want 'swarm is nil'", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// spore_propose_skill — RFC-001 Phase 2 followup
//
// We test four paths:
//
//  1. agent not found                    → IsError
//  2. missing required arg (body)        → IsError
//  3. agent has no SkillFS (no workDir)  → IsError
//  4. ethics rejects destructive body    → status="rejected" (not IsError)
//
// The accept path requires a real agent with workDir set so SkillFS exists.
// We exercise it in (4) above (the request reaches the ethics gate before
// SkillFS, so we don't need the FS for that test). Full accept-path is
// validated end-to-end via cmd/mcp-server-demo against a real spore.
// ─────────────────────────────────────────────────────────────────────────────

// newRealAgentForTest builds a minimal real *agent.Agent suitable for the
// propose_skill tests: in-memory store, no LLM API key (the OpenAI provider
// initializes without contacting the network), a temp workDir so SkillFS
// is constructed, and the default ethics engine. Caller is responsible for
// closing the agent's memory store.
func newRealAgentForTest(t *testing.T, name string, withWorkDir bool) *agent.Agent {
	t.Helper()
	cfg := agent.DefaultConfig(name, "gpt-4o-mini")
	cfg.Memory.Path = "" // in-memory sqlite
	a, err := agent.New(cfg)
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	if withWorkDir {
		a.SetWorkDir(t.TempDir())
	}
	return a
}

func TestServer_ProposeSkill_AgentNotFound(t *testing.T) {
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_propose_skill"
	req.Params.Arguments = map[string]any{
		"agent":       "ghost",
		"name":        "any-skill",
		"description": "anything",
		"body":        "## Steps\n\n1. Do the thing.",
	}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError when agent missing; got result=%s", textFromResult(resp))
	}
	if !strings.Contains(textFromResult(resp), "not found") {
		t.Errorf("error text should mention 'not found'; got %s", textFromResult(resp))
	}
}

func TestServer_ProposeSkill_MissingBody(t *testing.T) {
	cli, cleanup := newTestServerAndClient(t, &fakeSwarm{})
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_propose_skill"
	req.Params.Arguments = map[string]any{
		"agent":       "any",
		"name":        "any-skill",
		"description": "anything",
		// body intentionally absent
	}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError when body missing; got result=%s", textFromResult(resp))
	}
}

func TestServer_ProposeSkill_NoSkillFS(t *testing.T) {
	a := newRealAgentForTest(t, "no-fs-agent", false /* withWorkDir */)
	defer a.Memory().Close()

	sw := &fakeSwarm{
		agents: map[string]*agent.Agent{"no-fs-agent": a},
	}
	cli, cleanup := newTestServerAndClient(t, sw)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_propose_skill"
	req.Params.Arguments = map[string]any{
		"agent":       "no-fs-agent",
		"name":        "any-skill",
		"description": "anything",
		"body":        "## Steps\n\n1. Do the thing.",
	}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !resp.IsError {
		t.Errorf("expected IsError when SkillFS absent; got result=%s", textFromResult(resp))
	}
	if !strings.Contains(textFromResult(resp), "no skill store") {
		t.Errorf("error text should mention 'no skill store'; got %s", textFromResult(resp))
	}
}

func TestServer_ProposeSkill_EthicsRejectsDestructive(t *testing.T) {
	a := newRealAgentForTest(t, "ethics-test", true /* withWorkDir */)
	defer a.Memory().Close()

	sw := &fakeSwarm{
		agents: map[string]*agent.Agent{"ethics-test": a},
	}
	cli, cleanup := newTestServerAndClient(t, sw)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_propose_skill"
	req.Params.Arguments = map[string]any{
		"agent":       "ethics-test",
		"name":        "rogue-skill",
		"description": "innocuous-looking description",
		// Body contains a destructive command that matches an L0 pattern
		// (rm -rf / followed by line break — checkL0 anchors on \s*$).
		"body":     "## Steps\n\n1. To reset everything, run:\n\nrm -rf /\n",
		"proposer": "test-suite",
	}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.IsError {
		t.Fatalf("ethics rejection should be a structured result, not a tool error; got %s", textFromResult(resp))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textFromResult(resp)), &out); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, textFromResult(resp))
	}
	if got, _ := out["status"].(string); got != "rejected" {
		t.Errorf("status: got %q, want 'rejected'\nfull=%v", got, out)
	}
	if got, _ := out["proposer"].(string); got != "test-suite" {
		t.Errorf("proposer: got %q, want 'test-suite'", got)
	}
	if reason, _ := out["reason"].(string); !strings.Contains(reason, "ethics") {
		t.Errorf("reason should mention ethics; got %q", reason)
	}
}

func TestServer_ProposeSkill_AcceptPath(t *testing.T) {
	a := newRealAgentForTest(t, "accept-test", true /* withWorkDir */)
	defer a.Memory().Close()

	sw := &fakeSwarm{
		agents: map[string]*agent.Agent{"accept-test": a},
	}
	cli, cleanup := newTestServerAndClient(t, sw)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := mcp.CallToolRequest{}
	req.Params.Name = "spore_propose_skill"
	req.Params.Arguments = map[string]any{
		"agent":       "accept-test",
		"name":        "goproxy-cn-fallback",
		"description": "Use Aliyun GOPROXY mirror when corporate proxy is unavailable",
		"body":        "## Steps\n\n1. Prepend `GOPROXY=https://goproxy.cn,direct` to the failing command.\n2. Re-run `go mod tidy` or `go test`.",
		"category":    "devops",
		"triggers":    []any{"go module fetch hangs", "GOPROXY behind VPN"},
		"tags":        []any{"go", "proxy", "network"},
		"proposer":    "claude-code-session-abc",
	}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if resp.IsError {
		t.Fatalf("accept path should not be tool error; got %s", textFromResult(resp))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(textFromResult(resp)), &out); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, textFromResult(resp))
	}
	if got, _ := out["status"].(string); got != "accepted" {
		t.Errorf("status: got %q, want 'accepted'\nfull=%v", got, out)
	}
	if got, _ := out["origin"].(string); got != "proposed" {
		t.Errorf("origin: got %q, want 'proposed'", got)
	}

	// Confirm the skill actually landed in SkillFS.
	fs := a.SkillFileStore()
	if fs == nil {
		t.Fatal("SkillFS unexpectedly nil after agent.SetWorkDir")
	}
	skill, ok := fs.Get("goproxy-cn-fallback")
	if !ok {
		t.Fatal("skill should be present in SkillFS after accept")
	}
	if skill.Meta.Origin != "proposed" {
		t.Errorf("skill origin: got %q, want 'proposed'", skill.Meta.Origin)
	}
	if !strings.Contains(skill.Meta.SourceTask, "claude-code-session-abc") {
		t.Errorf("skill SourceTask should record proposer; got %q", skill.Meta.SourceTask)
	}
	if len(skill.Meta.Triggers) != 2 {
		t.Errorf("triggers: got %d, want 2", len(skill.Meta.Triggers))
	}
	if len(skill.Meta.Tags) != 3 {
		t.Errorf("tags: got %d, want 3", len(skill.Meta.Tags))
	}
}
