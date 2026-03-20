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
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/swarm"
	"go.zoe.im/x/cli"
)

type swarmCmd struct {
	Agents  int    `opts:"short=n,help=number of agents (ignored when config dir has .toml files)"`
	Model   string `opts:"short=m,help=LLM model for auto-generated agents"`
	Runtime string `opts:"short=r,help=runtime backend: auto/builtin/claude-code/codex/openclaw"`
	Dir     string `opts:"short=d,help=config directory (scan *.toml) or data directory"`
	Config  string `opts:"short=c,help=single swarm config file path"`
	APIPort int    `opts:"help=HTTP API + dashboard port (default 8080; 0 to disable)"`
	BaseURL string `opts:"help=LLM API base URL (overrides config file)"`
}

func init() {
	c := &swarmCmd{
		Agents:  3,
		Model:   "gpt-4o",
		Runtime: "auto",
		APIPort: 8080,
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

	// Try loading configs from directory
	configs, err := c.loadConfigs(dir)
	if err != nil {
		return err
	}

	// Create swarm with appropriate transport
	var sw *swarm.Swarm
	transport := globalCfg.Network.Transport
	if transport == "libp2p" || transport == "p2p" {
		// Use first agent's identity for P2P node key
		firstID, err := agent.NewIdentity(configs[0].Agent.Name)
		if err != nil {
			return fmt.Errorf("generating P2P identity: %w", err)
		}
		sw, err = swarm.NewP2PSwarm(
			dir,
			len(configs)+5,
			firstID.PrivateKey,
			globalCfg.Network.Listen,
			globalCfg.Network.Bootstrap,
		)
		if err != nil {
			return fmt.Errorf("creating P2P swarm: %w", err)
		}
		fmt.Printf("🌐 P2P transport enabled (peer: %s)\n", sw.PeerID())
	} else {
		sw = swarm.New(dir, len(configs)+5)
	}

	for _, cfg := range configs {
		// Override with env/flags
		c.applyEnvOverrides(cfg)
		if _, err := sw.AddAgent(cfg); err != nil {
			return fmt.Errorf("creating agent %s: %w", cfg.Agent.Name, err)
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

	return c.repl(sw)
}

// loadConfigs loads agent configs from directory .toml files or generates defaults.
func (c *swarmCmd) loadConfigs(dir string) ([]*agent.Config, error) {
	var configs []*agent.Config

	// 1. If --config specified, load that single file
	if c.Config != "" {
		cfg, err := agent.LoadConfig(c.Config, "")
		if err != nil {
			return nil, fmt.Errorf("loading config %s: %w", c.Config, err)
		}
		configs = append(configs, cfg)
		return configs, nil
	}

	// 2. Scan directory for agent configs (*.toml, agent.yaml, subdirs)
	pattern := filepath.Join(dir, "*.toml")
	matches, _ := filepath.Glob(pattern)

	// Also check subdirectories: dir/*/spore.toml and dir/*/agent.yaml
	subdirs, _ := filepath.Glob(filepath.Join(dir, "*", "spore.toml"))
	matches = append(matches, subdirs...)

	// Scan for agent.yaml files at top level and in subdirs
	for _, name := range []string{"agent.yaml", "agent.yml"} {
		if yamlPath := filepath.Join(dir, name); fileExists(yamlPath) {
			matches = append(matches, yamlPath)
		}
		yamlSubdirs, _ := filepath.Glob(filepath.Join(dir, "*", name))
		matches = append(matches, yamlSubdirs...)
	}

	// Filter out global config file, deduplicate
	seen := make(map[string]bool)
	var agentFiles []string
	for _, m := range matches {
		base := filepath.Base(m)
		if base == "config.toml" || base == "config.yaml" || base == "config.json" {
			continue // skip global config
		}
		abs, _ := filepath.Abs(m)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		agentFiles = append(agentFiles, m)
	}

	if len(agentFiles) > 0 {
		fmt.Printf("📂 Loading %d config(s) from %s\n", len(agentFiles), dir)
		for _, path := range agentFiles {
			var cfg *agent.Config
			var err error
			base := filepath.Base(path)

			if base == "agent.yaml" || base == "agent.yml" {
				// OpenAgent Manifest
				cfg, _, err = agent.LoadManifest(path)
				if err != nil {
					fmt.Printf("⚠️  Skipping %s: %v\n", path, err)
					continue
				}
				fmt.Printf("   ✅ %s (manifest, role=%s, skills=%v)\n", cfg.Agent.Name, cfg.Agent.Role, cfg.Agent.Skills)
			} else {
				// Legacy spore.toml
				cfg, err = agent.LoadConfig(path, "")
				if err != nil {
					fmt.Printf("⚠️  Skipping %s: %v\n", path, err)
					continue
				}
				if cfg.Agent.Name == "" {
					cfg.Agent.Name = strings.TrimSuffix(base, ".toml")
					if cfg.Agent.Name == "spore" {
						cfg.Agent.Name = filepath.Base(filepath.Dir(path))
					}
				}
				fmt.Printf("   ✅ %s (role=%s, model=%s)\n", cfg.Agent.Name, cfg.Agent.Role, cfg.LLM.Model)
			}

			configs = append(configs, cfg)
		}
		if len(configs) == 0 {
			return nil, fmt.Errorf("no valid configs found in %s", dir)
		}
		return configs, nil
	}

	// 3. Fallback: generate default configs
	roles := []string{"coordinator", "worker", "worker"}
	names := []string{"coordinator", "worker-1", "worker-2"}
	if c.Agents > 3 {
		for i := 3; i < c.Agents; i++ {
			roles = append(roles, "worker")
			names = append(names, fmt.Sprintf("worker-%d", i))
		}
	}

	for i := 0; i < c.Agents && i < len(names); i++ {
		cfg := agent.DefaultConfig(names[i], c.Model)
		cfg.Agent.Role = roles[i]
		cfg.Runtime.Type = c.Runtime
		configs = append(configs, cfg)
	}

	return configs, nil
}

// applyEnvOverrides applies global config, environment variables, and CLI flags to an agent config.
func (c *swarmCmd) applyEnvOverrides(cfg *agent.Config) {
	// Global config as base
	applyGlobalConfig(cfg)

	// CLI flags override everything
	if c.BaseURL != "" {
		cfg.LLM.BaseURL = c.BaseURL
	} else if envURL := os.Getenv("SPORE_LLM_BASE_URL"); envURL != "" {
		cfg.LLM.BaseURL = envURL
	}
	if apiKey := os.Getenv("SPORE_LLM_API_KEY"); apiKey != "" {
		cfg.LLM.APIKey = apiKey
		if cfg.LLM.Headers == nil {
			cfg.LLM.Headers = make(map[string]string)
		}
		cfg.LLM.Headers["x-api-key"] = apiKey
	}
	// CLI model/runtime override (only if explicitly set, not default)
	if c.Model != "gpt-4o" && cfg.LLM.Model == "" {
		cfg.LLM.Model = c.Model
	}
	if c.Runtime != "auto" && cfg.Runtime.Type == "" {
		cfg.Runtime.Type = c.Runtime
	}
}

// repl runs the interactive command loop.
func (c *swarmCmd) repl(sw *swarm.Swarm) error {
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
