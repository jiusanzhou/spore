/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * RFC-001 Stage 2 end-to-end demo: drive spore-acp-server (which is a
 * spore that *exposes* itself as an ACP agent) using spore's own
 * ACPRuntime (which is a spore that *consumes* ACP agents).
 *
 * Topology:
 *
 *     this binary (ACPRuntime client)
 *           │   stdio JSON-RPC
 *           ▼
 *     spore-acp-server  ──inner──>  claude-agent-acp
 *
 * Net effect: a prompt sent to spore-acp-server is reflected to the
 * inner runtime (claude-code by default), and we see the full ACP
 * session/update stream bubble back through both hops.
 *
 * Build the server first:
 *     go build -o /tmp/spore-acp-server ./cmd/spore-acp-server
 *
 * Then run:
 *     go run ./cmd/acp-server-demo "What is 6*7?"
 */

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.zoe.im/spore/internal/runtime"
)

func main() {
	prompt := "What is 6*7? Answer with just the number."
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	binPath := os.Getenv("SPORE_ACP_SERVER_BIN")
	if binPath == "" {
		binPath = "/tmp/spore-acp-server"
	}
	if _, err := os.Stat(binPath); err != nil {
		fmt.Fprintf(os.Stderr, "[demo] spore-acp-server binary not found at %s\n", binPath)
		fmt.Fprintln(os.Stderr, "[demo] build it first: go build -o /tmp/spore-acp-server ./cmd/spore-acp-server")
		os.Exit(1)
	}
	binPath, _ = filepath.Abs(binPath)

	r := runtime.NewACPRuntime()
	r.RuntimeName = "spore-via-spore"
	r.BinPath = binPath
	r.BinArgs = []string{"--verbose"}
	r.HandshakeTimeout = 90 * time.Second
	r.PromptTimeout = 5 * time.Minute

	if err := r.Healthy(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "[demo] health check failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[demo] driving %s\n", binPath)
	fmt.Printf("[demo] prompt: %q\n", prompt)
	fmt.Println("[demo] events:")
	fmt.Println("─────────────────────────────────────────────────────────────")

	handler := func(ev runtime.StreamEvent) error {
		switch ev.Type {
		case runtime.EventInit:
			fmt.Printf("  🔌 init   %s\n", ev.Content)
		case runtime.EventThinking:
			content := ev.Content
			if len(content) > 160 {
				content = content[:160] + "…"
			}
			fmt.Printf("  💭 think  %s\n", content)
		case runtime.EventToolCall:
			fmt.Printf("  🔧 tool   %s\n", ev.ToolName)
		case runtime.EventToolResult:
			out := ev.ToolOutput
			if len(out) > 160 {
				out = out[:160] + "…"
			}
			marker := "✓"
			if ev.ToolError {
				marker = "✗"
			}
			fmt.Printf("  %s result %s → %s\n", marker, ev.ToolName, out)
		case runtime.EventComplete:
			fmt.Printf("  ✅ done   %dms\n", ev.DurationMS)
		case runtime.EventError:
			fmt.Printf("  ⚠️  error %s\n", ev.Content)
		}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	out, err := r.ExecuteStream(ctx, runtime.TaskInput{Description: prompt}, handler)
	fmt.Println("─────────────────────────────────────────────────────────────")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[demo] ExecuteStream error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n[demo] Success: %t\n", out.Success)
	fmt.Printf("[demo] Result:  %s\n", strings.TrimSpace(out.Result))
}
