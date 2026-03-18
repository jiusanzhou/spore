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
	Agents int    `opts:"short=n,help=number of agents to start"`
	Model  string `opts:"short=m,help=LLM model"`
	Dir    string `opts:"short=d,help=data directory"`
}

func init() {
	c := &swarmCmd{
		Agents: 3,
		Model:  "gpt-4o",
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
		if _, err := sw.AddAgent(cfg); err != nil {
			return fmt.Errorf("creating agent %s: %w", names[i], err)
		}
	}

	// Start all agents
	sw.RunAll()

	fmt.Println()
	fmt.Println("🦠 Spore swarm started!")
	fmt.Println()
	printAgentTable(sw)
	fmt.Println()
	fmt.Println("Commands: task <agent> <description> | ps | quit")
	fmt.Println()

	// REPL
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	scanner := bufio.NewScanner(os.Stdin)
	inputCh := make(chan string)

	go func() {
		for scanner.Scan() {
			inputCh <- scanner.Text()
		}
	}()

	for {
		fmt.Print("spore> ")
		select {
		case <-sigCh:
			fmt.Println("\n🛑 Shutting down swarm...")
			sw.Close()
			return nil

		case line := <-inputCh:
			line = strings.TrimSpace(line)
			if line == "" {
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
					continue
				}
				taskID, err := sw.SendTask(parts[0], parts[1])
				if err != nil {
					fmt.Printf("❌ %v\n", err)
				} else {
					fmt.Printf("📋 Task %s queued for %s\n", taskID, parts[0])
				}

			case strings.HasPrefix(line, "broadcast "):
				desc := strings.TrimPrefix(line, "broadcast ")
				// send to coordinator
				if _, err := sw.SendTask("coordinator", desc); err != nil {
					fmt.Printf("❌ %v\n", err)
				}

			default:
				fmt.Println("Unknown command. Try: task <agent> <desc> | ps | quit")
			}
		}
	}
}

func printAgentTable(sw *swarm.Swarm) {
	infos := sw.List()
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tROLE\tSTATUS\tMODEL\tTASKS\tUPTIME")
	for _, info := range infos {
		uptime := ""
		if !info.StartedAt.IsZero() {
			uptime = time.Since(info.StartedAt).Truncate(time.Second).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n",
			info.Name, info.Role, info.Status, info.Model, info.TaskCount, uptime)
	}
	w.Flush()
}
