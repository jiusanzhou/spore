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

// Package main: spore-mcp-server stdio entry point — RFC-001 Stage 3.
//
// Loads a spore agent (same path as `spore run`) but instead of opening a
// REPL it speaks MCP on stdin/stdout. Configure your MCP-capable client
// (Claude Code, Codex, Cursor, Goose, Zed, …) to spawn:
//
//     spore-mcp-server [--dir <agent-data-dir>] [--config <path>]
//
// Tools exposed:
//   spore_list_agents, spore_get_agent, spore_send_task, spore_swarm_stats,
//   spore_recent_tasks, spore_agent_skills, spore_agent_experience,
//   spore_peer_fitness
//
// stderr is the only place we log; stdin+stdout are reserved for MCP frames.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/mcpserver"
	"go.zoe.im/spore/internal/swarm"
)

func main() {
	var (
		dir    = flag.String("dir", "", "agent data directory (default: ~/.spore)")
		config = flag.String("config", "", "explicit agent config file path")
	)
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg, resolvedDir, err := loadAgentConfig(*config, *dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[spore-mcp-server] load config: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[spore-mcp-server] loaded agent %s from %s\n",
		cfg.Agent.Name, resolvedDir)

	sw := swarm.New(resolvedDir, 5)
	if _, err := sw.AddAgent(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "[spore-mcp-server] add agent: %v\n", err)
		os.Exit(1)
	}
	sw.RunAll()
	defer sw.Close()

	fmt.Fprintf(os.Stderr,
		"[spore-mcp-server] swarm running with agent %q; serving MCP on stdio\n",
		cfg.Agent.Name)

	srv := mcpserver.NewServer(sw)
	if err := srv.ServeStdio(ctx); err != nil {
		// EOF on stdin (peer disconnected) is the normal shutdown path.
		fmt.Fprintf(os.Stderr, "[spore-mcp-server] serve: %v\n", err)
	}
	fmt.Fprintln(os.Stderr, "[spore-mcp-server] shutting down")
}

// loadAgentConfig mirrors the precedence used by `spore run`: explicit
// --config wins, otherwise look for agent.yaml or spore.toml in --dir.
func loadAgentConfig(configPath, dir string) (*agent.Config, string, error) {
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.spore"
	}

	if configPath != "" {
		cfg, err := agent.LoadConfig(configPath, dir)
		return cfg, dir, err
	}
	if mPath := agent.FindManifest(dir); mPath != "" {
		cfg, _, err := agent.LoadManifest(mPath)
		return cfg, dir, err
	}
	cfg, err := agent.LoadConfig("", dir)
	return cfg, dir, err
}
