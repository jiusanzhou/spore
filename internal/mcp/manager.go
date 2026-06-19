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

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpspec "github.com/mark3labs/mcp-go/mcp"
)

// Manager owns the lifecycle of all configured MCP server connections for one
// agent. Call LoadAndRegister to start it, Close to shut it down.
type Manager struct {
	cfg Config

	mu      sync.Mutex
	clients map[string]*client.Client // keyed by logical server name
	tools   []*tool                   // flattened list of all wrapped tools
	closed  bool
}

// NewManager builds an empty Manager. Use Load to connect to servers.
func NewManager(cfg Config) *Manager {
	return &Manager{
		cfg:     cfg.withDefaults(),
		clients: make(map[string]*client.Client),
	}
}

// Tools returns the engine.Tool-compatible wrappers for every tool every
// connected server exposed at initialization. The slice is a snapshot;
// callers should not mutate the underlying *tool values.
func (m *Manager) Tools() []EngineTool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EngineTool, len(m.tools))
	for i, t := range m.tools {
		out[i] = t
	}
	return out
}

// Load connects to every enabled server, lists their tools, and stores
// EngineTool wrappers. Errors connecting to one server are logged via the
// returned LoadReport but do not abort the load — surviving servers are
// still usable.
func (m *Manager) Load(ctx context.Context) (*LoadReport, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("mcp: manager already closed")
	}
	m.mu.Unlock()

	if !m.cfg.Enabled {
		return &LoadReport{}, nil
	}

	report := &LoadReport{}

	for name, sc := range m.cfg.Servers {
		if sc.Disabled {
			report.Skipped = append(report.Skipped, name)
			continue
		}
		if err := sc.validate(name); err != nil {
			report.Errors = append(report.Errors, ServerError{Server: name, Err: err})
			continue
		}

		c, tools, err := m.startServer(ctx, name, sc)
		if err != nil {
			report.Errors = append(report.Errors, ServerError{Server: name, Err: err})
			continue
		}

		m.mu.Lock()
		m.clients[name] = c
		for _, mt := range tools {
			m.tools = append(m.tools, mt)
		}
		m.mu.Unlock()

		report.Connected = append(report.Connected, ServerSummary{
			Server:    name,
			Transport: sc.normalizedTransport(),
			ToolCount: len(tools),
		})
	}

	return report, nil
}

// startServer dials one MCP server and returns the client + wrapped tools.
func (m *Manager) startServer(ctx context.Context, name string, sc ServerConfig) (*client.Client, []*tool, error) {
	initCtx, cancel := context.WithTimeout(ctx, time.Duration(m.cfg.InitTimeoutSeconds)*time.Second)
	defer cancel()

	var c *client.Client
	var err error

	switch sc.normalizedTransport() {
	case "stdio":
		envSlice := mapToEnvSlice(sc.Env)
		c, err = client.NewStdioMCPClient(sc.Command, envSlice, sc.Args...)
		if err != nil {
			return nil, nil, fmt.Errorf("stdio start: %w", err)
		}
	case "http", "streamable-http":
		opts := []transport.StreamableHTTPCOption{}
		if len(sc.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(sc.Headers))
		}
		c, err = client.NewStreamableHttpClient(sc.URL, opts...)
		if err != nil {
			return nil, nil, fmt.Errorf("http start: %w", err)
		}
		if startErr := c.Start(initCtx); startErr != nil {
			_ = c.Close()
			return nil, nil, fmt.Errorf("http start: %w", startErr)
		}
	case "sse":
		var sseOpts []transport.ClientOption
		if len(sc.Headers) > 0 {
			sseOpts = append(sseOpts, transport.WithHeaders(sc.Headers))
		}
		c, err = client.NewSSEMCPClient(sc.URL, sseOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("sse start: %w", err)
		}
		if startErr := c.Start(initCtx); startErr != nil {
			_ = c.Close()
			return nil, nil, fmt.Errorf("sse start: %w", startErr)
		}
	default:
		return nil, nil, fmt.Errorf("unknown transport %q", sc.Transport)
	}

	// MCP initialize handshake.
	initReq := mcpspec.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpspec.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpspec.Implementation{
		Name:    "spore",
		Version: "0.1.0",
	}
	if _, initErr := c.Initialize(initCtx, initReq); initErr != nil {
		_ = c.Close()
		return nil, nil, fmt.Errorf("initialize: %w", initErr)
	}

	// List tools.
	listResp, listErr := c.ListTools(initCtx, mcpspec.ListToolsRequest{})
	if listErr != nil {
		_ = c.Close()
		return nil, nil, fmt.Errorf("list tools: %w", listErr)
	}

	allowed := toSet(sc.AllowedTools)
	prefix := m.cfg.ToolPrefix

	var tools []*tool
	for _, mt := range listResp.Tools {
		if len(allowed) > 0 {
			if _, ok := allowed[mt.Name]; !ok {
				continue
			}
		}
		tools = append(tools, &tool{
			engineName:  buildEngineName(prefix, name, mt.Name),
			serverName:  name,
			remoteName:  mt.Name,
			description: buildDescription(name, mt),
			inputSchema: mt.InputSchema,
			client:      c,
			callTimeout: time.Duration(m.cfg.CallTimeoutSeconds) * time.Second,
		})
	}

	return c, tools, nil
}

// Close shuts down all clients. Safe to call multiple times.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var firstErr error
	for name, c := range m.clients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close %s: %w", name, err)
		}
	}
	m.clients = nil
	m.tools = nil
	return firstErr
}

// LoadReport summarizes what happened during Load.
type LoadReport struct {
	Connected []ServerSummary
	Skipped   []string
	Errors    []ServerError
}

// ServerSummary describes one successfully connected server.
type ServerSummary struct {
	Server    string
	Transport string
	ToolCount int
}

// ServerError pairs a server name with the error encountered loading it.
type ServerError struct {
	Server string
	Err    error
}

func (e ServerError) Error() string {
	return fmt.Sprintf("mcp server %s: %v", e.Server, e.Err)
}

// String renders a one-line summary suitable for logging.
func (r *LoadReport) String() string {
	parts := []string{}
	for _, s := range r.Connected {
		parts = append(parts, fmt.Sprintf("%s(%s,%dtools)", s.Server, s.Transport, s.ToolCount))
	}
	if len(parts) == 0 && len(r.Errors) == 0 && len(r.Skipped) == 0 {
		return "mcp: no servers configured"
	}
	out := "mcp: connected=[" + strings.Join(parts, " ") + "]"
	if len(r.Skipped) > 0 {
		out += " skipped=[" + strings.Join(r.Skipped, " ") + "]"
	}
	if len(r.Errors) > 0 {
		errs := []string{}
		for _, e := range r.Errors {
			errs = append(errs, fmt.Sprintf("%s:%v", e.Server, e.Err))
		}
		out += " errors=[" + strings.Join(errs, "; ") + "]"
	}
	return out
}

// EngineTool is the subset of go.zoe.im/spore/internal/engine.Tool that
// this package implements. We declare it here to avoid an import cycle when
// the engine wants to consume our wrappers; engine.Tool is structurally
// identical so the wrappers satisfy both.
type EngineTool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, input string) (string, error)
}

// helper functions ------------------------------------------------------------

func mapToEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func toSet(ss []string) map[string]struct{} {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

// buildEngineName joins prefix:server:tool, sanitised.
//
//	mcp + fs + read_file → "mcp:fs:read_file"
//
// We intentionally keep colons (not slashes) because the engine prompt uses
// `ACTION: <tool> <input>` and the LLM should see a single unbroken token.
func buildEngineName(prefix, server, tool string) string {
	parts := []string{}
	for _, p := range []string{prefix, server, tool} {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ":")
}

// buildDescription enriches the server-supplied tool description with a hint
// about input format, since spore's engine passes raw strings as tool input.
func buildDescription(serverName string, mt mcpspec.Tool) string {
	desc := strings.TrimSpace(mt.Description)
	if desc == "" {
		desc = "(no description)"
	}
	schemaJSON, _ := json.Marshal(mt.InputSchema)
	return fmt.Sprintf("[mcp:%s] %s | input: JSON object matching schema %s",
		serverName, desc, string(schemaJSON))
}
