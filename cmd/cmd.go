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
	"log"
	"os"
	"path/filepath"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/x/cli"
	"go.zoe.im/x/cli/config"
	"go.zoe.im/x/version"
)

const banner = `
     ___ _ __   ___  _ __ ___ 
    / __| '_ \ / _ \| '__/ _ \
    \__ \ |_) | (_) | | |  __/
    |___/ .__/ \___/|_|  \___|
        |_|
`

// globalCfg is the shared config loaded from ~/.spore/config.toml
// All subcommands can access this to inherit LLM, network, etc. settings.
var globalCfg = agent.DefaultConfig("spore", "gpt-4o")

func sporeConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".spore")
}

var app = cli.New(
	cli.Name("spore"),
	cli.Short("Decentralized AI agent swarm protocol & runtime"),
	version.NewOption(true),
	cli.GlobalConfig(
		globalCfg,
		cli.WithConfigCommand(true),
		cli.WithConfigName("config"),
		cli.WithConfigChanged(func(o, n any) {
			if newCfg, ok := n.(*agent.Config); ok {
				log.Printf("Config reloaded: llm=%s/%s", newCfg.LLM.Provider, newCfg.LLM.Model)
			}
		}),
		cli.WithConfigOptions(config.WithProvider(
			mustFSProvider(sporeConfigDir()),
		)),
	),
)

func mustFSProvider(path string) config.Provider {
	p, err := config.NewFSProvider(path)
	if err != nil {
		// Fallback to default
		return config.DefaultFSProvider
	}
	return p
}

// Run is the entry point.
var Run = app.Run
