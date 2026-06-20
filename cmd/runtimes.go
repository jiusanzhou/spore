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

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"go.zoe.im/spore/internal/runtime"
	"go.zoe.im/x/cli"
)

type runtimesCmd struct{}

func init() {
	c := &runtimesCmd{}
	_ = c
	app.Register(cli.New(
		cli.Name("runtimes"),
		cli.Short("List available agent runtimes (auto-discover)"),
		cli.Run(func(cmd *cli.Command, args ...string) {
			discoverRuntimes()
		}),
	))
}

func discoverRuntimes() {
	fmt.Println("🔍 Discovering available agent runtimes...")
	fmt.Println()

	reg := runtime.NewRegistry()

	// builtin is always available — register it explicitly so it shows up
	// in the same listing.
	// (NewBuiltin needs an LLM provider; for the discovery view we just
	// print a hardcoded row.)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RUNTIME	STATUS	SOURCE	CAPABILITIES")
	fmt.Fprintf(w, "builtin	✅ available	native	general, shell\n")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Single source of truth: registry's own auto-discovery (ACP first,
	// then native, then agentbox fallback).
	discovered := reg.AutoDiscover(ctx)
	discoveredSet := make(map[string]string, len(discovered)) // name → label
	for _, label := range discovered {
		// Labels look like "claude-code (acp)" or "openclaw"; key is the
		// bare name so we can match Info().Name directly.
		name := label
		source := "native"
		if idx := strings.Index(label, " ("); idx > 0 {
			name = label[:idx]
			source = strings.TrimSuffix(label[idx+2:], ")")
		}
		discoveredSet[name] = source
	}

	for _, info := range reg.List() {
		var tags []string
		for _, cap := range info.Capabilities {
			tags = append(tags, cap.Tags...)
		}
		source := discoveredSet[info.Name]
		if source == "" {
			source = "native"
		}
		fmt.Fprintf(w, "%s	✅ available	%s	%s\n", info.Name, source, strings.Join(tags, ", "))
	}

	// Show the runtimes we *probed* but didn't find, so the user knows
	// what's wired up.
	notFound := []string{}
	candidates := []string{"claude-code", "codex", "opencode", "openclaw", "gemini", "aider", "goose"}
	for _, name := range candidates {
		if _, ok := reg.Get(name); !ok {
			notFound = append(notFound, name)
		}
	}
	for _, name := range notFound {
		fmt.Fprintf(w, "%s	❌ not found	-	-\n", name)
	}
	w.Flush()

	fmt.Println()
	fmt.Println("Source legend: acp = bidirectional Agent Client Protocol (preferred);")
	fmt.Println("               native = built into spore; abox = legacy stream-json adapter.")
	fmt.Println("Use --runtime <name> with 'spore swarm' to select a runtime.")
	fmt.Println("Use --runtime auto to let Spore pick the best available one.")
}
