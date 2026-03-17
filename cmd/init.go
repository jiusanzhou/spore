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
	"path/filepath"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/x/cli"
)

type initCmd struct {
	Name  string `opts:"short=n,help=agent name"`
	Model string `opts:"short=m,help=LLM model (e.g. gpt-4o)"`
	Dir   string `opts:"short=d,help=data directory"`
}

func init() {
	c := &initCmd{
		Name:  "agent-0",
		Model: "gpt-4o",
	}
	app.Register(cli.New(
		cli.Name("init"),
		cli.Short("Initialize a new spore agent"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *initCmd) run() error {
	dir := c.Dir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("getting home dir: %w", err)
		}
		dir = filepath.Join(home, ".spore", c.Name)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	// Generate identity
	id, err := agent.NewIdentity(c.Name)
	if err != nil {
		return fmt.Errorf("generating identity: %w", err)
	}

	keyPath := filepath.Join(dir, "identity.key")
	if err := id.Save(keyPath); err != nil {
		return fmt.Errorf("saving identity: %w", err)
	}

	// Write default config
	cfg := agent.DefaultConfig(c.Name, c.Model)
	cfgPath := filepath.Join(dir, "spore.toml")
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("🌱 Agent initialized: %s\n", c.Name)
	fmt.Printf("   Identity: %s\n", id.PublicKeyHex())
	fmt.Printf("   Config:   %s\n", cfgPath)
	fmt.Printf("   Data dir: %s\n", dir)

	return nil
}
