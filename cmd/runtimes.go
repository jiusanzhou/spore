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
	reg := runtime.NewRegistry()

	// Always have builtin
	fmt.Println("🔍 Discovering available agent runtimes...")
	fmt.Println()

	// Check each known runtime
	type probe struct {
		name    string
		runtime runtime.Runtime
	}

	probes := []probe{
		{"claude-code", runtime.NewClaudeCode()},
		{"codex", runtime.NewCodex()},
		{"opencode", runtime.NewOpenCode()},
		{"openclaw", runtime.NewOpenClaw()},
	}

	ctx := context.Background()
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RUNTIME\tSTATUS\tCAPABILITIES")
	fmt.Fprintf(w, "builtin\t✅ available\tgeneral, shell\n")

	for _, p := range probes {
		if err := p.runtime.Healthy(checkCtx); err == nil {
			reg.Register(p.runtime)
			info := p.runtime.Info()
			var tags []string
			for _, cap := range info.Capabilities {
				tags = append(tags, cap.Tags...)
			}
			fmt.Fprintf(w, "%s\t✅ available\t%s\n", p.name, strings.Join(tags, ", "))
		} else {
			fmt.Fprintf(w, "%s\t❌ not found\t-\n", p.name)
		}
	}
	w.Flush()

	fmt.Println()
	fmt.Println("Use --runtime <name> with 'spore swarm' to select a runtime.")
	fmt.Println("Use --runtime auto to let Spore pick the best available one.")
}
