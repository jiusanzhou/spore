/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * End-to-end demo: drive claude-agent-acp via spore's ACP runtime.
 *
 * Run with:
 *   go run ./cmd/acp-runtime-demo "what is 2+2?"
 */

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.zoe.im/spore/internal/runtime"
)

func main() {
	prompt := "What is 2+2? Answer with just the number."
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	r := runtime.NewACPRuntime()
	r.HandshakeTimeout = 60 * time.Second // claude needs a moment to auth/load

	if err := r.Healthy(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "[demo] healthy check failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[demo] running ACP runtime against %s\n", r.BinPath)
	fmt.Printf("[demo] prompt: %q\n", prompt)
	fmt.Println("[demo] events:")
	fmt.Println("─────────────────────────────────────────────────────────────")

	handler := func(ev runtime.StreamEvent) error {
		switch ev.Type {
		case runtime.EventInit:
			fmt.Printf("  🔌 init   %s\n", ev.Content)
		case runtime.EventThinking:
			content := ev.Content
			if len(content) > 200 {
				content = content[:200] + "…"
			}
			fmt.Printf("  💭 think  %s\n", content)
		case runtime.EventToolCall:
			fmt.Printf("  🔧 tool   %s\n", ev.ToolName)
		case runtime.EventToolResult:
			out := ev.ToolOutput
			if len(out) > 200 {
				out = out[:200] + "…"
			}
			marker := "✓"
			if ev.ToolError {
				marker = "✗"
			}
			fmt.Printf("  %s result %s → %s\n", marker, ev.ToolName, out)
		case runtime.EventComplete:
			fmt.Printf("  ✅ done   %dms\n", ev.DurationMS)
		case runtime.EventError:
			fmt.Printf("  ⚠️  error %s (fatal=%t)\n", ev.Content, ev.Fatal)
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
	fmt.Printf("[demo] Result: %s\n", strings.TrimSpace(out.Result))
	fmt.Printf("[demo] Tokens: %d\n", out.Tokens)
	fmt.Printf("[demo] Logs:   %s\n", out.Logs)
	if out.Error != "" {
		fmt.Printf("[demo] Error:  %s\n", out.Error)
	}
}
