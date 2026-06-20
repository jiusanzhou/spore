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

// Package mcpserver exposes spore's swarm/agent/skill/evolution primitives
// as MCP tools so any MCP-capable client (Claude Code, Codex, Cursor,
// Goose, Zed, our own MCP client, …) can drive a running spore instance.
//
// RFC-001 Stage 3. Stages 1+2 made spore an ACP peer; Stage 3 unlocks
// spore's truly differentiated abilities — collective skill memory, swarm
// delegation, evolution telemetry — to anyone who speaks MCP.
//
// Tools registered (all read-only or low-risk by default):
//
//   spore_list_agents        list every agent in the swarm + status/skills
//   spore_get_agent          full Info for one agent
//   spore_send_task          submit a task to a named agent, return task_id
//   spore_swarm_stats        aggregated swarm counters
//   spore_recent_tasks       last N TaskEvents across the swarm
//   spore_agent_skills       a single agent's active skills
//   spore_agent_experience   evolution digest (drives, fitness, learnings)
//   spore_peer_fitness       peer-evolution rankings as seen by one agent
//
// Transport is stdio — same wire as ACP and the rest of the MCP ecosystem.
// Run via cmd/spore-mcp-server (or embed Server.AddTo and call ServeStdio
// from any host process that already holds a Swarm).
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/swarm"
)

// SwarmAccessor is the minimal surface we need from a spore Swarm. We use
// an interface so tests can drop in a fake without spinning up a real swarm.
type SwarmAccessor interface {
	List() []agent.Info
	GetAgent(name string) *agent.Agent
	SendTask(agentName, description string) (string, error)
	Stats() swarm.SwarmStats
	TaskLog() []swarm.TaskEvent
}

// Server wraps a spore Swarm as an MCP server. Construct with NewServer,
// then either call ServeStdio (blocks) or AddToMCPServer to embed in an
// existing mcp-go server.
type Server struct {
	swarm SwarmAccessor

	// AgentName/AgentVersion advertised in initialize response.
	AgentName    string
	AgentVersion string
}

// NewServer wraps a Swarm with default identity. swarm must be non-nil.
func NewServer(sw SwarmAccessor) *Server {
	return &Server{
		swarm:        sw,
		AgentName:    "spore",
		AgentVersion: "0.1.0",
	}
}

// MCPServer builds the underlying mcp-go server with all spore tools
// registered. Most callers want ServeStdio instead; this is exposed for
// embedding into a host that already serves additional tools.
func (s *Server) MCPServer() *server.MCPServer {
	srv := server.NewMCPServer(
		s.AgentName,
		s.AgentVersion,
		server.WithToolCapabilities(true),
	)
	s.registerTools(srv)
	return srv
}

// ServeStdio runs the MCP server on stdin/stdout, blocking until the peer
// closes the connection or the context is cancelled.
func (s *Server) ServeStdio(ctx context.Context) error {
	if s.swarm == nil {
		return fmt.Errorf("mcpserver: swarm is nil")
	}
	return server.ServeStdio(s.MCPServer(), server.WithStdioContextFunc(
		func(parent context.Context) context.Context { return ctx },
	))
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool registration
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) registerTools(srv *server.MCPServer) {
	srv.AddTool(mcp.NewTool("spore_list_agents",
		mcp.WithDescription("List every agent currently registered in the spore swarm. "+
			"Returns each agent's name, role, runtime, skills, status, and task counters."),
	), s.handleListAgents)

	srv.AddTool(mcp.NewTool("spore_get_agent",
		mcp.WithDescription("Fetch the full Info struct for one named agent. "+
			"Use this after spore_list_agents to drill into a specific agent."),
		mcp.WithString("name", mcp.Required(),
			mcp.Description("Agent name as returned by spore_list_agents")),
	), s.handleGetAgent)

	srv.AddTool(mcp.NewTool("spore_send_task",
		mcp.WithDescription("Submit a task to a named agent. Returns the task_id; "+
			"use spore_recent_tasks to poll for completion. The task runs in whatever "+
			"runtime the agent is configured with (claude-code, codex, builtin, ...)."),
		mcp.WithString("agent", mcp.Required(),
			mcp.Description("Target agent name")),
		mcp.WithString("task", mcp.Required(),
			mcp.Description("Natural-language task description")),
	), s.handleSendTask)

	srv.AddTool(mcp.NewTool("spore_swarm_stats",
		mcp.WithDescription("Aggregated swarm-wide counters: agent count by status, "+
			"total tasks issued, network/peer info if libp2p is enabled."),
	), s.handleSwarmStats)

	srv.AddTool(mcp.NewTool("spore_recent_tasks",
		mcp.WithDescription("Recent task lifecycle events across the whole swarm "+
			"(submitted/started/completed/failed). Use to follow up on a "+
			"spore_send_task you issued."),
		mcp.WithNumber("limit",
			mcp.Description("Max events to return (default 20, max 200)")),
	), s.handleRecentTasks)

	srv.AddTool(mcp.NewTool("spore_agent_skills",
		mcp.WithDescription("Active skills (evolved or installed) for a named agent. "+
			"Each skill has a name, description, trigger conditions, and selection counters."),
		mcp.WithString("agent", mcp.Required(),
			mcp.Description("Agent name")),
	), s.handleAgentSkills)

	srv.AddTool(mcp.NewTool("spore_agent_experience",
		mcp.WithDescription("Evolution digest for a named agent: drive levels, "+
			"recent learnings, current strategy, peer fitness summary."),
		mcp.WithString("agent", mcp.Required(),
			mcp.Description("Agent name")),
	), s.handleAgentExperience)

	srv.AddTool(mcp.NewTool("spore_peer_fitness",
		mcp.WithDescription("Peer-evolution rankings as seen by one agent: how it "+
			"weights its peers based on observed task outcomes."),
		mcp.WithString("agent", mcp.Required(),
			mcp.Description("Observing agent name")),
	), s.handlePeerFitness)
}

// ─────────────────────────────────────────────────────────────────────────────
// Tool handlers
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) handleListAgents(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return jsonResult(s.swarm.List())
}

func (s *Server) handleGetAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	a := s.swarm.GetAgent(name)
	if a == nil {
		return mcp.NewToolResultErrorf("agent %q not found", name), nil
	}
	return jsonResult(a.Info())
}

func (s *Server) handleSendTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	agentName, err := req.RequireString("agent")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	taskDesc, err := req.RequireString("task")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	id, err := s.swarm.SendTask(agentName, taskDesc)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("send_task failed", err), nil
	}
	return jsonResult(map[string]string{"task_id": id, "agent": agentName})
}

func (s *Server) handleSwarmStats(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return jsonResult(s.swarm.Stats())
}

func (s *Server) handleRecentTasks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	limit := 20
	if v := req.GetArguments()["limit"]; v != nil {
		if f, ok := v.(float64); ok && f > 0 {
			limit = int(f)
		}
	}
	if limit > 200 {
		limit = 200
	}

	log := s.swarm.TaskLog()
	if len(log) > limit {
		log = log[len(log)-limit:]
	}
	return jsonResult(log)
}

func (s *Server) handleAgentSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("agent")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	a := s.swarm.GetAgent(name)
	if a == nil {
		return mcp.NewToolResultErrorf("agent %q not found", name), nil
	}
	store := a.Skills()
	if store == nil {
		return jsonResult([]any{})
	}
	skills, err := store.ActiveSkills()
	if err != nil {
		return mcp.NewToolResultErrorFromErr("read skills", err), nil
	}
	return jsonResult(skills)
}

func (s *Server) handleAgentExperience(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("agent")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	a := s.swarm.GetAgent(name)
	if a == nil {
		return mcp.NewToolResultErrorf("agent %q not found", name), nil
	}
	evo := a.Evolution()
	if evo == nil {
		return jsonResult(map[string]any{"agent": name, "evolution": nil})
	}
	return jsonResult(evo.BuildDigest())
}

func (s *Server) handlePeerFitness(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("agent")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	a := s.swarm.GetAgent(name)
	if a == nil {
		return mcp.NewToolResultErrorf("agent %q not found", name), nil
	}
	pe := a.PeerEvo()
	if pe == nil {
		return jsonResult([]any{})
	}
	return jsonResult(pe.Rankings())
}

// jsonResult marshals v to a JSON string and wraps it as a TextContent
// result. MCP doesn't have a structured JSON content type yet; clients
// get the JSON as-is and parse it on their side.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("marshal", err), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
