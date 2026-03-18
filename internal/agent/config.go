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
	Agent   AgentConfig    `toml:"agent" yaml:"agent" json:"agent"`
	Runtime RuntimeConfig  `toml:"runtime" yaml:"runtime" json:"runtime"`
	LLM     LLMConfig      `toml:"llm" yaml:"llm" json:"llm"`
	Memory  MemoryConfig   `toml:"memory" yaml:"memory" json:"memory"`
	Network NetworkConfig  `toml:"network" yaml:"network" json:"network"`
	Ethics  EthicsConfig   `toml:"ethics" yaml:"ethics" json:"ethics"`
	Economy EconomyConfig  `toml:"economy" yaml:"economy" json:"economy"`
	Privacy PrivacyConfig  `toml:"privacy" yaml:"privacy" json:"privacy"`
	Spawner SpawnerConfig  `toml:"spawner" yaml:"spawner" json:"spawner"`
}

// AgentConfig defines the agent's basic identity and behavior.
type AgentConfig struct {
	Name string `toml:"name" yaml:"name" json:"name"`
	Role string `toml:"role" yaml:"role" json:"role"` // coordinator, worker, specialist
}

// RuntimeConfig defines which execution backend to use.
type RuntimeConfig struct {
	// Type selects the runtime: "builtin", "claude-code", "codex", "openclaw", "exec", "http", "auto"
	// "auto" will probe available CLIs and pick the best one.
	Type string `toml:"type" yaml:"type" json:"type"`

	// Exec-specific config (when type = "exec")
	Command  string   `toml:"command" yaml:"command" json:"command"`
	Args     []string `toml:"args" yaml:"args" json:"args"`
	TaskFlag string   `toml:"task_flag" yaml:"task_flag" json:"task_flag"`
	Tags     []string `toml:"tags" yaml:"tags" json:"tags"`

	// HTTP-specific config (when type = "http")
	URL string `toml:"url" yaml:"url" json:"url"`
}

// LLMConfig defines the LLM provider settings.
type LLMConfig struct {
	Provider string            `toml:"provider" yaml:"provider" json:"provider"`
	Model    string            `toml:"model" yaml:"model" json:"model"`
	BaseURL  string            `toml:"base_url" yaml:"base_url" json:"base_url"`
	APIKey   string            `toml:"api_key" yaml:"api_key" json:"api_key"`     // prefer env: SPORE_LLM_API_KEY
	Headers  map[string]string `toml:"headers" yaml:"headers" json:"headers" opts:"-"`     // custom headers (e.g. x-api-key)
	Router   map[string]string `toml:"router" yaml:"router" json:"router" opts:"-"`        // task_type -> model
}

// MemoryConfig defines memory storage settings.
type MemoryConfig struct {
	Backend      string `toml:"backend" yaml:"backend" json:"backend"`                   // sqlite, ipfs
	Path         string `toml:"path" yaml:"path" json:"path"`
	IPFSEndpoint string `toml:"ipfs_endpoint" yaml:"ipfs_endpoint" json:"ipfs_endpoint"` // IPFS API endpoint
}

// NetworkConfig defines networking settings.
type NetworkConfig struct {
	Transport string   `toml:"transport" yaml:"transport" json:"transport"` // local, libp2p
	Listen    []string `toml:"listen" yaml:"listen" json:"listen"`
	Bootstrap []string `toml:"bootstrap" yaml:"bootstrap" json:"bootstrap"`
}

// EthicsConfig defines ethics engine parameters.
type EthicsConfig struct {
	MaxSpawnChildren int     `toml:"max_spawn_children" yaml:"max_spawn_children" json:"max_spawn_children"`
	MaxBudgetPerTask float64 `toml:"max_budget_per_task" yaml:"max_budget_per_task" json:"max_budget_per_task"`
}

// SpawnerConfig defines spawning parameters.
type SpawnerConfig struct {
	MaxChildren          int     `toml:"max_children" yaml:"max_children" json:"max_children"`
	MinBalanceToSpawn    float64 `toml:"min_balance_to_spawn" yaml:"min_balance_to_spawn" json:"min_balance_to_spawn"`
	DefaultResourceShare float64 `toml:"default_resource_share" yaml:"default_resource_share" json:"default_resource_share"`
}

// EconomyConfig defines economic parameters for the agent.
type EconomyConfig struct {
	HibernateThreshold float64 `toml:"hibernate_threshold" yaml:"hibernate_threshold" json:"hibernate_threshold"`
	MinTaskBalance     float64 `toml:"min_task_balance" yaml:"min_task_balance" json:"min_task_balance"`
}

// PrivacyConfig defines privacy filter settings.
type PrivacyConfig struct {
	Enabled bool   `toml:"enabled" yaml:"enabled" json:"enabled"`
	Mode    string `toml:"mode" yaml:"mode" json:"mode"` // warn, sanitize, reject
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
			MinTaskBalance:     0.0, // no gate by default; set > 0 to require balance
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
