// Demo program: drives the streaming claude-code adapter end-to-end and
// prints every neutral StreamEvent it emits. Standalone — does not require
// the rest of spore to be wired up. Useful for verifying that the parser
// handles the claude-code wire format correctly on this machine.
//
// Build & run:
//
//	cd ~/projects/labs.zoe.im/spore
//	go run ./cmd/runtime-stream-demo "what is 2 plus 2? respond with just the number"
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
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: runtime-stream-demo <prompt>")
		os.Exit(2)
	}
	prompt := strings.Join(os.Args[1:], " ")

	cc := runtime.NewClaudeCode()
	if err := cc.Healthy(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "claude binary not available: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	handler := func(ev runtime.StreamEvent) error {
		switch ev.Type {
		case runtime.EventInit:
			fmt.Printf("🔌 init session=%s %s\n", ev.Session, ev.Content)
		case runtime.EventThinking:
			text := ev.Content
			line := strings.ReplaceAll(text, "\n", " ")
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			fmt.Printf("💭 %s\n", line)
		case runtime.EventToolCall:
			arg := ev.ToolInput
			if len(arg) > 120 {
				arg = arg[:120] + "…"
			}
			fmt.Printf("🔧 %s %s\n", ev.ToolName, arg)
		case runtime.EventToolResult:
			marker := "✓"
			if ev.ToolError {
				marker = "✗"
			}
			out := strings.ReplaceAll(ev.ToolOutput, "\n", " ")
			if len(out) > 120 {
				out = out[:120] + "…"
			}
			fmt.Printf("%s tool_result %s\n", marker, out)
		case runtime.EventError:
			lvl := "warn"
			if ev.Fatal {
				lvl = "ERROR"
			}
			fmt.Printf("⚠️  %s: %s\n", lvl, ev.Content)
		case runtime.EventComplete:
			fmt.Printf("✅ done in=%d out=%d cached=%d cost=$%.4f duration=%dms\n",
				ev.InputTokens, ev.OutputTokens, ev.CachedTokens,
				ev.CostUSD, ev.DurationMS)
		}
		return nil
	}

	out, err := cc.ExecuteStream(ctx, runtime.TaskInput{Description: prompt}, handler)
	if err != nil {
		fmt.Fprintf(os.Stderr, "execute failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("─── final TaskOutput ───")
	fmt.Printf("Success:  %v\n", out.Success)
	fmt.Printf("Result:   %s\n", out.Result)
	fmt.Printf("Tokens:   %d\n", out.Tokens)
	fmt.Printf("Cost:     $%.4f\n", out.Cost)
	if out.Error != "" {
		fmt.Printf("Error:    %s\n", out.Error)
	}
}
