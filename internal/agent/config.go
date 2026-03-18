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

	"github.com/BurntSushi/toml"
)

// Config is the top-level agent configuration.
type Config struct {
	Agent   AgentConfig    `toml:"agent"`
	Runtime RuntimeConfig  `toml:"runtime"`
	LLM     LLMConfig      `toml:"llm"`
	Memory  MemoryConfig   `toml:"memory"`
	Network NetworkConfig  `toml:"network"`
	Ethics  EthicsConfig   `toml:"ethics"`
	Economy EconomyConfig  `toml:"economy"`
	Privacy PrivacyConfig  `toml:"privacy"`
	Spawner SpawnerConfig  `toml:"spawner"`
}

// AgentConfig defines the agent's basic identity and behavior.
type AgentConfig struct {
	Name string `toml:"name"`
	Role string `toml:"role"` // coordinator, worker, specialist
}

// RuntimeConfig defines which execution backend to use.
type RuntimeConfig struct {
	// Type selects the runtime: "builtin", "claude-code", "codex", "openclaw", "exec", "http", "auto"
	// "auto" will probe available CLIs and pick the best one.
	Type string `toml:"type"`

	// Exec-specific config (when type = "exec")
	Command  string   `toml:"command"`
	Args     []string `toml:"args"`
	TaskFlag string   `toml:"task_flag"`
	Tags     []string `toml:"tags"`

	// HTTP-specific config (when type = "http")
	URL string `toml:"url"`
}

// LLMConfig defines the LLM provider settings.
type LLMConfig struct {
	Provider string            `toml:"provider"`
	Model    string            `toml:"model"`
	BaseURL  string            `toml:"base_url"`
	APIKey   string            `toml:"api_key"` // prefer env: SPORE_LLM_API_KEY
	Router   map[string]string `toml:"router"`  // task_type -> model
}

// MemoryConfig defines memory storage settings.
type MemoryConfig struct {
	Backend      string `toml:"backend"` // sqlite, ipfs
	Path         string `toml:"path"`
	IPFSEndpoint string `toml:"ipfs_endpoint"` // IPFS API endpoint (default: localhost:5001)
}

// NetworkConfig defines networking settings.
type NetworkConfig struct {
	Transport string   `toml:"transport"` // local, libp2p
	Listen    []string `toml:"listen"`
	Bootstrap []string `toml:"bootstrap"`
}

// EthicsConfig defines ethics engine parameters.
type EthicsConfig struct {
	MaxSpawnChildren int     `toml:"max_spawn_children"`
	MaxBudgetPerTask float64 `toml:"max_budget_per_task"`
}

// SpawnerConfig defines spawning parameters.
type SpawnerConfig struct {
	MaxChildren          int     `toml:"max_children"`
	MinBalanceToSpawn    float64 `toml:"min_balance_to_spawn"`
	DefaultResourceShare float64 `toml:"default_resource_share"`
}

// EconomyConfig defines economic parameters for the agent.
type EconomyConfig struct {
	HibernateThreshold float64 `toml:"hibernate_threshold"` // balance below which agent stops accepting tasks
	MinTaskBalance     float64 `toml:"min_task_balance"`    // minimum balance to accept a new task
}

// PrivacyConfig defines privacy filter settings.
type PrivacyConfig struct {
	Enabled bool   `toml:"enabled"`
	Mode    string `toml:"mode"` // warn, sanitize, reject
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig(name, model string) *Config {
	return &Config{
		Agent: AgentConfig{
			Name: name,
			Role: "worker",
		},
		Runtime: RuntimeConfig{
			Type: "auto", // auto-discover available runtimes
		},
		LLM: LLMConfig{
			Provider: "openai",
			Model:    model,
			BaseURL:  "https://api.openai.com/v1",
		},
		Memory: MemoryConfig{
			Backend: "sqlite",
			Path:    "memory.db",
		},
		Network: NetworkConfig{
			Transport: "local",
		},
		Ethics: EthicsConfig{
			MaxSpawnChildren: 5,
			MaxBudgetPerTask: 1.0,
		},
		Economy: EconomyConfig{
			HibernateThreshold: 0.0,
			MinTaskBalance:     1.0,
		},
		Privacy: PrivacyConfig{
			Enabled: true,
			Mode:    "warn",
		},
		Spawner: SpawnerConfig{
			MaxChildren:          5,
			MinBalanceToSpawn:    10.0,
			DefaultResourceShare: 0.2,
		},
	}
}

// Save writes the config as TOML to the given path.
func (c *Config) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// LoadConfig reads config from a file. It tries cfgPath first,
// then falls back to <dir>/spore.toml.
func LoadConfig(cfgPath, dir string) (*Config, error) {
	path := cfgPath
	if path == "" && dir != "" {
		path = filepath.Join(dir, "spore.toml")
	}
	if path == "" {
		// try default location
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine config path: %w", err)
		}
		path = filepath.Join(home, ".spore", "agent-0", "spore.toml")
	}

	cfg := &Config{}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("decoding config %s: %w", path, err)
	}

	// override API key from env if not set
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = os.Getenv("SPORE_LLM_API_KEY")
	}

	return cfg, nil
}
