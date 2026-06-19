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

// Package mcp wires Model Context Protocol servers into Spore agents as tools.
//
// MCP is an open protocol that lets LLM applications (clients) consume tools
// hosted by external processes (servers). For Spore this means an agent can
// call any of the hundreds of MCP servers in the wild — filesystem, github,
// postgres, playwright, etc. — without writing a Go wrapper for each one.
//
// Architecture:
//
//	┌──────────────────────────────────────────────┐
//	│ engine.Engine                                │
//	│   tools = map[string]engine.Tool             │
//	│           ├─ shell, search, fetch ...        │
//	│           └─ mcp:fs:read_file (this package) │
//	└──────────────────────────────────────────────┘
//	          │
//	          ▼ Execute(ctx, jsonArgs)
//	┌──────────────────────────────────────────────┐
//	│ mcp.tool                                     │
//	│   wraps a single MCP tool from one server    │
//	└──────────────────────────────────────────────┘
//	          │
//	          ▼ CallTool RPC
//	┌──────────────────────────────────────────────┐
//	│ mcp.Manager                                  │
//	│   one MCP client per configured server       │
//	│   (stdio or streamable-http)                 │
//	└──────────────────────────────────────────────┘
package mcp

import (
	"fmt"
	"strings"
)

// Config is the user-facing MCP configuration block, mirrored under [mcp]
// in agent config.toml. It maps a logical server name to a transport spec.
//
//	[mcp]
//	enabled = true
//	tool_prefix = "mcp"          # optional, default "mcp"
//	init_timeout_seconds = 15    # optional, default 15
//	call_timeout_seconds = 60    # optional, default 60
//
//	[mcp.servers.fs]
//	transport = "stdio"
//	command   = "npx"
//	args      = ["-y", "@modelcontextprotocol/server-filesystem", "/Users/me/work"]
//
//	[mcp.servers.github]
//	transport = "stdio"
//	command   = "docker"
//	args      = ["run", "-i", "--rm", "-e", "GITHUB_TOKEN", "ghcr.io/github/github-mcp-server"]
//	env       = { GITHUB_TOKEN=*** }
//
//	[mcp.servers.remote]
//	transport = "http"
//	url       = "https://example.com/mcp"
//	headers   = { Authorization = "Bearer ..." }
type Config struct {
	Enabled            bool   `toml:"enabled" yaml:"enabled" json:"enabled" opts:"help=enable MCP servers"`
	ToolPrefix         string `toml:"tool_prefix" yaml:"tool_prefix" json:"tool_prefix" opts:"help=tool name prefix (default mcp)"`
	InitTimeoutSeconds int    `toml:"init_timeout_seconds" yaml:"init_timeout_seconds" json:"init_timeout_seconds" opts:"help=server init timeout"`
	CallTimeoutSeconds int    `toml:"call_timeout_seconds" yaml:"call_timeout_seconds" json:"call_timeout_seconds" opts:"help=per-tool call timeout"`
	// Servers is configured via TOML/YAML only — the CLI framework cannot
	// represent a map[string]struct as flags, so we skip it with `opts:"-"`.
	Servers map[string]ServerConfig `toml:"servers" yaml:"servers" json:"servers" opts:"-"`
}

// ServerConfig describes how to reach one MCP server.
type ServerConfig struct {
	// Transport: "stdio" (default) or "http" (StreamableHTTP).
	Transport string `toml:"transport" yaml:"transport" json:"transport"`

	// stdio fields
	Command string            `toml:"command" yaml:"command" json:"command"`
	Args    []string          `toml:"args" yaml:"args" json:"args"`
	Env     map[string]string `toml:"env" yaml:"env" json:"env" opts:"-"`

	// http fields
	URL     string            `toml:"url" yaml:"url" json:"url"`
	Headers map[string]string `toml:"headers" yaml:"headers" json:"headers" opts:"-"`

	// Disabled lets a user keep a server entry but skip it without deleting it.
	Disabled bool `toml:"disabled" yaml:"disabled" json:"disabled"`

	// AllowedTools, if non-empty, restricts which tools from this server are
	// exposed to the agent (allow-list of tool names as the server reports them).
	AllowedTools []string `toml:"allowed_tools" yaml:"allowed_tools" json:"allowed_tools" opts:"-"`
}

// validate returns nil if the server config is internally consistent.
func (s ServerConfig) validate(name string) error {
	transport := strings.ToLower(strings.TrimSpace(s.Transport))
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio":
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("mcp server %q: stdio transport requires command", name)
		}
	case "http", "streamable-http", "sse":
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("mcp server %q: %s transport requires url", name, transport)
		}
	default:
		return fmt.Errorf("mcp server %q: unknown transport %q (want stdio|http|sse)", name, transport)
	}
	return nil
}

// normalizedTransport returns the lowercase transport with stdio default.
func (s ServerConfig) normalizedTransport() string {
	t := strings.ToLower(strings.TrimSpace(s.Transport))
	if t == "" {
		return "stdio"
	}
	return t
}

// Defaults returns a Config with sensible defaults applied to zero values.
func (c Config) withDefaults() Config {
	out := c
	if out.ToolPrefix == "" {
		out.ToolPrefix = "mcp"
	}
	if out.InitTimeoutSeconds <= 0 {
		out.InitTimeoutSeconds = 15
	}
	if out.CallTimeoutSeconds <= 0 {
		out.CallTimeoutSeconds = 60
	}
	return out
}
