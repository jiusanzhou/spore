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
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/swarm"
	"go.zoe.im/x/cli"
)

type runCmd struct {
	Dir     string `opts:"short=d,help=agent data directory (contains spore.toml)"`
	Config  string `opts:"short=c,help=agent config file path (default: <dir>/spore.toml)"`
	APIPort int    `opts:"help=HTTP API + dashboard port (0 to disable)"`
}

func init() {
	c := &runCmd{}
	app.Register(cli.New(
		cli.Name("run"),
		cli.Short("Start a spore agent"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *runCmd) run() error {
	cfg, err := agent.LoadConfig(c.Config, c.Dir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Inherit from global config: global overrides local defaults
	applyGlobalConfig(cfg)

	dir := c.Dir
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.spore"
	}

	// Use swarm as single-agent wrapper for API support
	sw := swarm.New(dir, 5)
	if _, err := sw.AddAgent(cfg); err != nil {
		return fmt.Errorf("creating agent %s: %w", cfg.Agent.Name, err)
	}

	sw.RunAll()

	if c.APIPort > 0 {
		go startAPIServer(sw, c.APIPort)
	}

	fmt.Printf("🦠 Agent %s running\n", cfg.Agent.Name)
	fmt.Printf("   Model:   %s/%s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Printf("   Runtime: %s\n", cfg.Runtime.Type)
	if c.APIPort > 0 {
		fmt.Printf("   API:     http://localhost:%d\n", c.APIPort)
	}

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	fmt.Println("\n🛑 Shutting down...")
	sw.Close()
	return nil
}
