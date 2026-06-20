/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * End-to-end demo: spore registry's AutoDiscover puts ACP behind the
 * "claude-code" name; resolve by name and execute a real prompt against
 * claude-agent-acp. This is the wiring contract for RFC-001 Stage 1.
 *
 * Run:
 *   go run ./cmd/acp-registry-demo "What is 7*6?"
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
	prompt := "What is 7*6? Answer with just the number."
	if len(os.Args) > 1 {
		prompt = strings.Join(os.Args[1:], " ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	reg := runtime.NewRegistry()
	discovered := reg.AutoDiscover(ctx)
	fmt.Printf("[demo] auto-discovered: %v\n", discovered)

	// Resolve by the canonical name — this is what manifest.go and
	// `spore swarm --runtime claude-code` do.
	rt, ok := reg.Get("claude-code")
	if !ok {
		fmt.Fprintln(os.Stderr, "[demo] FAIL: claude-code not registered")
		os.Exit(1)
	}
	fmt.Printf("[demo] resolved 'claude-code' → %T (%s)\n", rt, rt.Info().Name)

	// Verify it's actually the ACP path, not a fallback adapter.
	if _, isACP := rt.(*runtime.ACPRuntime); !isACP {
		fmt.Fprintf(os.Stderr,
			"[demo] WARN: claude-code is %T — expected *runtime.ACPRuntime "+
				"(claude-agent-acp not in PATH?)\n", rt)
	}

	// Run the task through the registry path.
	streaming, ok := rt.(runtime.StreamingRuntime)
	var (
		out *runtime.TaskOutput
		err error
	)
	task := runtime.TaskInput{Description: prompt}

	if ok {
		fmt.Println("[demo] using StreamingRuntime path")
		out, err = streaming.ExecuteStream(ctx, task, func(ev runtime.StreamEvent) error {
			content := ev.Content
			if len(content) > 120 {
				content = content[:120] + "…"
			}
			fmt.Printf("  ▸ %-10s %s\n", ev.Type, content)
			return nil
		})
	} else {
		fmt.Println("[demo] using non-streaming Execute path")
		out, err = rt.Execute(ctx, task)
	}

	fmt.Println("─────────────────────────────────────────")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[demo] FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[demo] success: %t\n", out.Success)
	fmt.Printf("[demo] result:  %s\n", strings.TrimSpace(out.Result))
	if out.Error != "" {
		fmt.Printf("[demo] error:   %s\n", out.Error)
	}
}
