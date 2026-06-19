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
	"os/signal"
	"sync"
	"syscall"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/gateway"
	"go.zoe.im/spore/internal/swarm"
	"go.zoe.im/x/cli"
)

// gatewayCmd starts an agent and exposes it through the configured chat
// gateways (currently: telegram). Unlike `spore run`, there is no REPL —
// the gateway is the only inbound channel.
type gatewayCmd struct {
	Dir     string `opts:"short=d,help=agent data directory (contains spore.toml)"`
	Config  string `opts:"short=c,help=agent config file path (default: <dir>/spore.toml)"`
	APIPort int    `opts:"help=HTTP API + dashboard port (0 to disable; default 0 in gateway mode)"`
}

func init() {
	c := &gatewayCmd{}
	app.Register(cli.New(
		cli.Name("gateway"),
		cli.Short("Run a spore agent attached to chat gateways (Telegram, ...)"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *gatewayCmd) run() error {
	cfg, err := loadAgentConfig(c.Dir, c.Config)
	if err != nil {
		return err
	}
	applyGlobalConfig(cfg)

	// At least one gateway must be enabled, else this command is a no-op.
	if !c.anyGatewayEnabled(cfg) {
		return fmt.Errorf("no gateway enabled in config — set [gateway.telegram] enabled = true")
	}

	// Build a local swarm with one agent. Same pattern as `spore run`,
	// minus the REPL.
	sw := swarm.New(c.dataDir(), 5)
	a, err := sw.AddAgent(cfg)
	if err != nil {
		return fmt.Errorf("creating agent %s: %w", cfg.Agent.Name, err)
	}
	sw.RunAll()

	if c.APIPort > 0 {
		go startAPIServer(sw, c.APIPort)
	}

	// Build all enabled gateways and start them concurrently.
	gateways, err := c.buildGateways(cfg, a)
	if err != nil {
		_ = sw.Close()
		return err
	}

	fmt.Printf("🦠 Agent %s running\n", cfg.Agent.Name)
	fmt.Printf("   Model:   %s/%s\n", cfg.LLM.Provider, cfg.LLM.Model)
	fmt.Printf("   Runtime: %s\n", cfg.Runtime.Type)
	if c.APIPort > 0 {
		fmt.Printf("   API:     http://localhost:%d\n", c.APIPort)
	}
	fmt.Printf("   Gateways: %d active\n\n", len(gateways))

	// Wire ctx to SIGINT/SIGTERM and run gateways in parallel; the first
	// fatal error or signal triggers shutdown of all of them.
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		fmt.Println("\n🛑 Shutting down gateways...")
		cancel()
	}()

	var wg sync.WaitGroup
	errCh := make(chan error, len(gateways))
	for _, g := range gateways {
		wg.Add(1)
		go func(g gateway.Gateway) {
			defer wg.Done()
			if err := g.Start(ctx); err != nil && ctx.Err() == nil {
				errCh <- fmt.Errorf("%s: %w", g.Name(), err)
				cancel()
			}
		}(g)
	}
	wg.Wait()
	close(errCh)

	if err := sw.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "swarm close: %v\n", err)
	}

	// Surface the first non-context error if any gateway crashed.
	for e := range errCh {
		return e
	}
	return nil
}

// anyGatewayEnabled returns true if at least one gateway adapter is on.
func (c *gatewayCmd) anyGatewayEnabled(cfg *agent.Config) bool {
	return cfg.Gateway.Telegram.Enabled
}

// buildGateways instantiates every enabled gateway. Configuration errors
// (missing token, empty allow-list) abort startup.
func (c *gatewayCmd) buildGateways(cfg *agent.Config, a *agent.Agent) ([]gateway.Gateway, error) {
	var gws []gateway.Gateway
	if cfg.Gateway.Telegram.Enabled {
		tg, err := gateway.NewTelegramGateway(cfg.Gateway.Telegram, a)
		if err != nil {
			return nil, fmt.Errorf("telegram gateway: %w", err)
		}
		gws = append(gws, tg)
	}
	return gws, nil
}

// dataDir returns the data directory the run was launched against, defaulting
// to ~/.spore when -d is not supplied.
func (c *gatewayCmd) dataDir() string {
	if c.Dir != "" {
		return c.Dir
	}
	home, _ := os.UserHomeDir()
	return home + "/.spore"
}

// loadAgentConfig wraps the agent.LoadConfig / LoadManifest dance used by
// `spore run`. We duplicate it here (rather than refactor run.go in a
// gateway-only PR) to keep the diff small and reviewable.
func loadAgentConfig(dirFlag, configFlag string) (*agent.Config, error) {
	dir := dirFlag
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = home + "/.spore"
	}

	if configFlag != "" {
		return agent.LoadConfig(configFlag, dirFlag)
	}
	if mPath := agent.FindManifest(dir); mPath != "" {
		cfg, _, err := agent.LoadManifest(mPath)
		if err == nil {
			fmt.Printf("📋 Loaded agent manifest: %s\n", mPath)
		}
		return cfg, err
	}
	return agent.LoadConfig(configFlag, dirFlag)
}
