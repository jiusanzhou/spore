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
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/swarm"
	"go.zoe.im/x/cli"
)

type swarmCmd struct {
	Agents  int    `opts:"short=n,help=number of agents to start"`
	Model   string `opts:"short=m,help=LLM model"`
	Runtime string `opts:"short=r,help=runtime: auto/builtin/claude-code/codex/openclaw"`
	Dir     string `opts:"short=d,help=data directory"`
	APIPort int    `opts:"help=HTTP API port (0 to disable)"`
	BaseURL string `opts:"help=LLM API base URL"`
}

func init() {
	c := &swarmCmd{
		Agents:  3,
		Model:   "gpt-4o",
		Runtime: "auto",
	}
	app.Register(cli.New(
		cli.Name("swarm"),
		cli.Short("Start a multi-agent swarm with interactive REPL"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *swarmCmd) run() error {
	dir := c.Dir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.spore"
	}

	roles := []string{"coordinator", "worker", "worker"}
	names := []string{"coordinator", "worker-1", "worker-2"}
	if c.Agents > 3 {
		for i := 3; i < c.Agents; i++ {
			roles = append(roles, "worker")
			names = append(names, fmt.Sprintf("worker-%d", i))
		}
	}

	sw := swarm.New(dir, c.Agents+5)

	// Create agents
	for i := 0; i < c.Agents && i < len(names); i++ {
		cfg := agent.DefaultConfig(names[i], c.Model)
		cfg.Agent.Role = roles[i]
		cfg.Runtime.Type = c.Runtime

		// Apply LLM config from env/flags
		if c.BaseURL != "" {
			cfg.LLM.BaseURL = c.BaseURL
		} else if envURL := os.Getenv("SPORE_LLM_BASE_URL"); envURL != "" {
			cfg.LLM.BaseURL = envURL
		}
		if apiKey := os.Getenv("SPORE_LLM_API_KEY"); apiKey != "" {
			cfg.LLM.APIKey = apiKey
			// Also set x-api-key header for gateways that require it
			cfg.LLM.Headers = map[string]string{"x-api-key": apiKey}
		}

		if _, err := sw.AddAgent(cfg); err != nil {
			return fmt.Errorf("creating agent %s: %w", names[i], err)
		}
	}

	// Start all agents
	sw.RunAll()

	// Start HTTP API if port specified
	if c.APIPort > 0 {
		go startAPIServer(sw, c.APIPort)
	}

	fmt.Println()
	fmt.Println("🦠 Spore swarm started!")
	fmt.Println()
	printAgentTable(sw)
	fmt.Println()
	fmt.Println("Commands: task <agent> <description> | broadcast <description> | ps | quit")
	fmt.Println()

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Single reader goroutine — avoids multiple goroutines blocking on stdin
	inputCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			inputCh <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- fmt.Errorf("EOF")
		}
	}()

	fmt.Print("spore> ")
	for {
		select {
		case <-sigCh:
			fmt.Println("\n🛑 Shutting down swarm...")
			sw.Close()
			return nil

		case err := <-errCh:
			fmt.Printf("\n🛑 Input closed (%v), shutting down...\n", err)
			sw.Close()
			return nil

		case line := <-inputCh:
			line = strings.TrimSpace(line)
			if line == "" {
				fmt.Print("spore> ")
				continue
			}

			switch {
			case line == "quit" || line == "exit":
				fmt.Println("🛑 Shutting down swarm...")
				sw.Close()
				return nil

			case line == "ps":
				printAgentTable(sw)

			case strings.HasPrefix(line, "task "):
				parts := strings.SplitN(line[5:], " ", 2)
				if len(parts) < 2 {
					fmt.Println("Usage: task <agent-name> <description>")
				} else {
					taskID, err := sw.SendTask(parts[0], parts[1])
					if err != nil {
						fmt.Printf("❌ %v\n", err)
					} else {
						fmt.Printf("📋 Task %s queued for %s\n", taskID, parts[0])
					}
				}

			case strings.HasPrefix(line, "broadcast "):
				desc := strings.TrimPrefix(line, "broadcast ")
				if _, err := sw.SendTask("coordinator", desc); err != nil {
					fmt.Printf("❌ %v\n", err)
				}

			case line == "help":
				fmt.Println("Commands:")
				fmt.Println("  task <agent> <description>  — send a task to an agent")
				fmt.Println("  broadcast <description>     — send a task to the coordinator")
				fmt.Println("  ps                          — list running agents")
				fmt.Println("  help                        — show this help")
				fmt.Println("  quit / exit                 — shut down the swarm")

			default:
				fmt.Println("Unknown command. Type 'help' for available commands.")
			}

			fmt.Print("spore> ")
		}
	}
}

func printAgentTable(sw *swarm.Swarm) {
	infos := sw.List()
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tROLE\tRUNTIME\tSTATUS\tMODEL\tTASKS\tUPTIME")
	for _, info := range infos {
		uptime := ""
		if !info.StartedAt.IsZero() {
			uptime = time.Since(info.StartedAt).Truncate(time.Second).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			info.Name, info.Role, info.Runtime, info.Status, info.Model, info.TaskCount, uptime)
	}
	w.Flush()
}
