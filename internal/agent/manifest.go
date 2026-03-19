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

package agent

import (
	"fmt"
	"os"
	"path/filepath"

	manifest "go.zoe.im/agentbox/pkg/agent"
)

// LoadManifest reads an agent.yaml file and converts it to a Spore Config.
func LoadManifest(path string) (*Config, *manifest.Manifest, error) {
	m, err := manifest.ParseFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing agent.yaml: %w", err)
	}

	cfg := ManifestToConfig(m)
	return cfg, m, nil
}

// ManifestToConfig converts an OpenAgent Manifest to a Spore Config.
// Fields not representable in agent.yaml use sensible defaults.
func ManifestToConfig(m *manifest.Manifest) *Config {
	cfg := DefaultConfig(m.Name, "")

	// Identity
	cfg.Agent.Name = m.Name
	cfg.Agent.Description = m.Description

	// Skills from manifest
	if len(m.Skills) > 0 {
		skills := make([]string, len(m.Skills))
		for i, s := range m.Skills {
			skills[i] = s.Name
		}
		cfg.Agent.Skills = skills
	}

	// Collaboration
	if m.Collaboration != nil {
		cfg.Agent.CanDelegate = m.Collaboration.CanDelegate
		cfg.Agent.CanReceive = m.Collaboration.CanReceive
	}

	// Role inference from collaboration config
	if cfg.Agent.CanDelegate && cfg.Agent.CanReceive {
		cfg.Agent.Role = "coordinator"
	} else if cfg.Agent.CanReceive {
		cfg.Agent.Role = "worker"
	}

	// Model requirements → LLM config
	if m.Model != nil {
		if m.Model.Recommended != "" {
			cfg.LLM.Model = mapModelTier(m.Model.Recommended)
		} else if m.Model.Minimum != "" {
			cfg.LLM.Model = mapModelTier(m.Model.Minimum)
		}
	}

	// Runtime from preferred framework
	if fw := m.PreferredFramework(); fw != "" {
		cfg.Runtime.Type = mapFrameworkToRuntime(fw)
	}

	// Tags from marketplace
	if m.Marketplace != nil {
		cfg.Runtime.Tags = m.Marketplace.Tags
	}

	return cfg
}

// mapModelTier maps OpenAgent model tiers to concrete model names.
func mapModelTier(tier string) string {
	switch tier {
	case "haiku":
		return "gpt-4o-mini"
	case "sonnet":
		return "gpt-4o"
	case "opus":
		return "claude-opus-4-20250514"
	default:
		return tier // pass through concrete model names
	}
}

// mapFrameworkToRuntime maps framework names to Spore runtime types.
func mapFrameworkToRuntime(fw string) string {
	switch fw {
	case "openclaw":
		return "openclaw"
	case "claude-code":
		return "claude-code"
	case "codex":
		return "codex"
	default:
		return "auto"
	}
}

// FindManifest looks for agent.yaml in a directory.
func FindManifest(dir string) string {
	for _, name := range []string{"agent.yaml", "agent.yml"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
