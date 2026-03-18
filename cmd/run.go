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

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/x/cli"
)

type runCmd struct {
	Dir    string `opts:"short=d,help=agent data directory"`
	Config string `opts:"short=c,help=config file path"`
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

	// Inherit from global config for empty fields
	if cfg.LLM.BaseURL == "" {
		cfg.LLM.BaseURL = globalCfg.LLM.BaseURL
	}
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = globalCfg.LLM.APIKey
	}
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = globalCfg.LLM.Provider
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = globalCfg.LLM.Model
	}
	if len(cfg.LLM.Headers) == 0 && len(globalCfg.LLM.Headers) > 0 {
		cfg.LLM.Headers = make(map[string]string)
		for k, v := range globalCfg.LLM.Headers {
			cfg.LLM.Headers[k] = v
		}
	}

	a, err := agent.New(cfg)
	if err != nil {
		return fmt.Errorf("creating agent: %w", err)
	}

	fmt.Printf("🦠 Starting agent: %s\n", cfg.Agent.Name)
	return a.Run()
}
