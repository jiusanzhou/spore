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

	"go.zoe.im/spore/internal/spawner"
	"go.zoe.im/x/cli"
)

type spawnCmd struct {
	From  string `opts:"help=parent agent name"`
	Name  string `opts:"short=n,help=child agent name"`
	Role  string `opts:"short=r,help=child role"`
	Model string `opts:"short=m,help=child model override"`
	Mode  string `opts:"help=spawn mode: clone or fork"`
	Dir   string `opts:"short=d,help=data directory"`
}

func init() {
	c := &spawnCmd{
		Mode: "clone",
	}
	app.Register(cli.New(
		cli.Name("spawn"),
		cli.Short("Spawn a child agent from an existing parent"),
		cli.Config(c),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := c.run(); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

func (c *spawnCmd) run() error {
	if c.From == "" || c.Name == "" {
		return fmt.Errorf("both --from and --name are required")
	}

	mode := spawner.ModeClone
	if c.Mode == "fork" {
		mode = spawner.ModeFork
	}

	fmt.Printf("🌱 Spawning %s from %s (mode: %s)\n", c.Name, c.From, c.Mode)
	fmt.Printf("   Role:  %s\n", c.Role)
	fmt.Printf("   Model: %s\n", c.Model)

	_ = &spawner.Request{
		ParentName: c.From,
		ChildName:  c.Name,
		Mode:       mode,
		Role:       c.Role,
		Model:      c.Model,
	}

	// TODO: load parent config from disk, spawn, and start
	fmt.Println("⚠️  Standalone spawn not yet implemented (use 'swarm' command instead)")
	return nil
}
