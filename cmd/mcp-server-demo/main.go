/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * RFC-001 Stage 3 end-to-end demo: drive spore-mcp-server with a real MCP
 * stdio client (mark3labs/mcp-go), list its tools, and call a few read-
 * only ones against a real spore agent loaded from disk.
 *
 * Build the server first:
 *     go build -o /tmp/spore-mcp-server ./cmd/spore-mcp-server
 *
 * Then run:
 *     go run ./cmd/mcp-server-demo --dir <agent-data-dir>
 *
 * Default --dir points at examples/consciousness-demo/scout for repo
 * smoke-testing.
 */

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {
	var (
		bin = flag.String("bin", "/tmp/spore-mcp-server", "path to spore-mcp-server binary")
		dir = flag.String("dir", "examples/consciousness-demo/scout", "spore agent data dir to load")
	)
	flag.Parse()

	if _, err := os.Stat(*bin); err != nil {
		fmt.Fprintf(os.Stderr, "[demo] %s not found: %v\n[demo] build it: go build -o %s ./cmd/spore-mcp-server\n",
			*bin, err, *bin)
		os.Exit(1)
	}
	absDir, _ := filepath.Abs(*dir)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fmt.Printf("[demo] spawning %s --dir %s\n", *bin, absDir)
	cli, err := client.NewStdioMCPClient(*bin, []string{}, "--dir", absDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[demo] NewStdioMCPClient: %v\n", err)
		os.Exit(1)
	}
	defer cli.Close()

	// Initialize.
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcp-server-demo", Version: "0.1"}
	initResp, err := cli.Initialize(ctx, initReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[demo] Initialize: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[demo] initialized — server=%s/%s\n",
		initResp.ServerInfo.Name, initResp.ServerInfo.Version)

	// List tools.
	tools, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[demo] ListTools: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[demo] %d tools advertised:\n", len(tools.Tools))
	for _, t := range tools.Tools {
		fmt.Printf("   • %s — %s\n", t.Name, oneLine(t.Description))
	}
	fmt.Println()

	// Call spore_list_agents.
	fmt.Println("[demo] calling spore_list_agents …")
	if r, err := callTool(ctx, cli, "spore_list_agents", nil); err == nil {
		fmt.Println(indent(r, "   "))
	} else {
		fmt.Fprintf(os.Stderr, "   error: %v\n", err)
	}
	fmt.Println()

	// Call spore_swarm_stats.
	fmt.Println("[demo] calling spore_swarm_stats …")
	if r, err := callTool(ctx, cli, "spore_swarm_stats", nil); err == nil {
		fmt.Println(indent(r, "   "))
	} else {
		fmt.Fprintf(os.Stderr, "   error: %v\n", err)
	}
	fmt.Println()

	// Call spore_agent_skills with the loaded agent's name. We don't know
	// the name a priori, so parse it from the previous list call.
	if name := firstAgentName(ctx, cli); name != "" {
		fmt.Printf("[demo] calling spore_agent_skills(agent=%s) …\n", name)
		if r, err := callTool(ctx, cli, "spore_agent_skills",
			map[string]any{"agent": name}); err == nil {
			fmt.Println(indent(truncate(r, 800), "   "))
		} else {
			fmt.Fprintf(os.Stderr, "   error: %v\n", err)
		}
	}
}

func callTool(ctx context.Context, cli *client.Client, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	if args != nil {
		req.Params.Arguments = args
	}
	resp, err := cli.CallTool(ctx, req)
	if err != nil {
		return "", err
	}
	if resp.IsError {
		return "", fmt.Errorf("tool reported error: %s", textFromResult(resp))
	}
	return textFromResult(resp), nil
}

func firstAgentName(ctx context.Context, cli *client.Client) string {
	r, err := callTool(ctx, cli, "spore_list_agents", nil)
	if err != nil {
		return ""
	}
	// Cheap parse: look for the first "name": "..." occurrence.
	idx := strings.Index(r, `"name":`)
	if idx < 0 {
		return ""
	}
	tail := r[idx+len(`"name":`):]
	tail = strings.TrimLeft(tail, " ")
	if !strings.HasPrefix(tail, `"`) {
		return ""
	}
	end := strings.Index(tail[1:], `"`)
	if end < 0 {
		return ""
	}
	return tail[1 : end+1]
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

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 110 {
		s = s[:110] + "…"
	}
	return s
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated]"
}
