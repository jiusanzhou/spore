//go:build mcp_integration

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

// Package mcp integration tests. These tests spawn a real MCP server
// subprocess and require external tooling (npx + Node.js) on PATH.
//
// Run with:
//
//	go test -tags mcp_integration ./internal/mcp/...
//
// Skipped automatically if `npx` is not available.
package mcp

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestEverythingServer connects to the official @modelcontextprotocol/server-everything
// reference server, lists its tools, and calls one. This exercises the full
// stack: stdio transport, initialize handshake, ListTools, CallTool, and
// engine.Tool surface.
func TestEverythingServer(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mgr := NewManager(Config{
		Enabled: true,
		Servers: map[string]ServerConfig{
			"everything": {
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"-y", "@modelcontextprotocol/server-everything"},
			},
		},
	})
	defer mgr.Close()

	report, err := mgr.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("server errors: %+v", report.Errors)
	}
	if len(report.Connected) != 1 {
		t.Fatalf("expected 1 connected server, got %+v", report.Connected)
	}
	if report.Connected[0].ToolCount == 0 {
		t.Fatalf("server reported zero tools")
	}

	tools := mgr.Tools()
	t.Logf("loaded %d tools from everything server", len(tools))
	for _, tl := range tools {
		t.Logf("  - %s: %s", tl.Name(), strings.Split(tl.Description(), "|")[0])
	}

	// Find the "echo" tool (everything server has it as a sanity check)
	var echo EngineTool
	for _, tl := range tools {
		if strings.HasSuffix(tl.Name(), ":echo") {
			echo = tl
			break
		}
	}
	if echo == nil {
		t.Skip("everything server does not expose 'echo' tool in this version; skipping call test")
	}

	out, err := echo.Execute(ctx, `{"message":"ping from spore"}`)
	if err != nil {
		t.Fatalf("echo Execute: %v", err)
	}
	if !strings.Contains(out, "ping from spore") {
		t.Errorf("echo output missing payload: %q", out)
	}
}
