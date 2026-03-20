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
	"strings"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/x/cli"
)

func init() {
	app.Register(cli.New(
		cli.Name("init"),
		cli.Short("Initialize a new spore agent"),
		cli.Run(func(cmd *cli.Command, args ...string) {
			if err := runInit(args...); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}),
	))
}

// runInit initializes a new agent using the global config flags.
// Usage: spore init [name]
//
// The agent's name, role, skills, description, and model come from
// the global config flags (--agent-name, --agent-role, etc.).
func runInit(args ...string) error {
	// Use first positional arg as name, or fall back to global config
	name := "agent-0"
	if len(args) > 0 && args[0] != "" {
		name = args[0]
	}

	dir := sporeConfigDir()
	agentDir := filepath.Join(dir, name)

	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("creating dir: %w", err)
	}

	// Generate identity
	id, err := agent.NewIdentity(name)
	if err != nil {
		return fmt.Errorf("generating identity: %w", err)
	}
	keyPath := filepath.Join(agentDir, "identity.key")
	if err := id.Save(keyPath); err != nil {
		return fmt.Errorf("saving identity: %w", err)
	}

	// Build config from global flags
	cfg := agent.DefaultConfig(name, "gpt-4o")
	applyGlobalConfig(cfg)
	if cfg.Agent.Name == "" || cfg.Agent.Name == "spore" {
		cfg.Agent.Name = name
	}

	// Apply agent-specific global flags
	defaultAgent := agent.DefaultConfig("", "").Agent
	if globalCfg.Agent.Role != defaultAgent.Role {
		cfg.Agent.Role = globalCfg.Agent.Role
	}
	if globalCfg.Agent.Description != "" {
		cfg.Agent.Description = globalCfg.Agent.Description
	}
	if len(globalCfg.Agent.Skills) > 0 && (len(globalCfg.Agent.Skills) != 1 || globalCfg.Agent.Skills[0] != "general") {
		cfg.Agent.Skills = globalCfg.Agent.Skills
	}
	if globalCfg.Agent.CanDelegate {
		cfg.Agent.CanDelegate = true
	}
	// Auto-set can_delegate for coordinators
	if cfg.Agent.Role == "coordinator" {
		cfg.Agent.CanDelegate = true
	}

	// Write spore.toml
	tomlPath := filepath.Join(agentDir, "spore.toml")
	if err := cfg.Save(tomlPath); err != nil {
		return fmt.Errorf("saving spore.toml: %w", err)
	}

	// Write agent.yaml (OpenAgent Manifest)
	yamlPath := filepath.Join(agentDir, "agent.yaml")
	if err := writeAgentYAML(yamlPath, cfg); err != nil {
		return fmt.Errorf("saving agent.yaml: %w", err)
	}

	fmt.Printf("\n🌱 Agent initialized: %s\n", cfg.Agent.Name)
	fmt.Printf("   Identity: %s\n", id.PublicKeyHex()[:16])
	fmt.Printf("   Role:     %s\n", cfg.Agent.Role)
	fmt.Printf("   Skills:   %v\n", cfg.Agent.Skills)
	fmt.Printf("   Manifest: %s\n", yamlPath)
	fmt.Printf("   Config:   %s\n", tomlPath)
	fmt.Printf("   Data dir: %s\n", agentDir)

	if cfg.Agent.Role == "coordinator" {
		fmt.Println("\n💡 Coordinator agents decompose tasks and delegate to workers.")
		fmt.Println("   Use `spore swarm` to run with worker agents.")
	} else {
		fmt.Printf("\n💡 Run with: spore run -d %s\n", agentDir)
	}

	return nil
}

func writeAgentYAML(path string, cfg *agent.Config) error {
	desc := cfg.Agent.Description
	if desc == "" {
		desc = cfg.Agent.Name + " agent"
	}

	skillsYAML := ""
	for _, s := range cfg.Agent.Skills {
		skillsYAML += fmt.Sprintf("  - name: %q\n", s)
	}

	tier := "sonnet"
	m := strings.ToLower(cfg.LLM.Model)
	switch {
	case strings.Contains(m, "mini") || strings.Contains(m, "haiku") || strings.Contains(m, "0.6b"):
		tier = "haiku"
	case strings.Contains(m, "opus"):
		tier = "opus"
	}

	style := "practical and efficient"
	tone := "concise"
	if cfg.Agent.Role == "coordinator" {
		style = "strategic and organized"
		tone = "clear and directive"
	} else if cfg.Agent.Role == "specialist" {
		style = "deep expertise, analytical"
		tone = "precise and technical"
	}

	yaml := fmt.Sprintf(`id: %q
name: %q
version: "1.0.0"
description: %q

persona:
  style: %q
  tone: %q
  language: ["en"]

skills:
%s
collaboration:
  can_delegate: %v
  can_receive: %v
  protocols: ["spore/p2p"]

model:
  minimum: %q
  recommended: %q

marketplace:
  category: "general"
  tags: [%s]
`, cfg.Agent.Name, cfg.Agent.Name, desc, style, tone,
		skillsYAML, cfg.Agent.CanDelegate, cfg.Agent.CanReceive,
		tier, tier, quotedCSV(cfg.Agent.Skills))

	return os.WriteFile(path, []byte(yaml), 0644)
}

func quotedCSV(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return strings.Join(quoted, ", ")
}
