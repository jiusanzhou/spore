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
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
)

// Agent is the core runtime for a single spore agent.
type Agent struct {
	cfg      *Config
	identity *Identity
	llm      llm.Provider
	memory   memory.Store
}

// New creates a new Agent from config.
func New(cfg *Config) (*Agent, error) {
	// load or create identity
	// TODO: resolve identity path from config/dir
	id, err := NewIdentity(cfg.Agent.Name)
	if err != nil {
		return nil, fmt.Errorf("creating identity: %w", err)
	}

	// init LLM provider
	provider, err := llm.NewProvider(cfg.LLM.Provider, llm.ProviderConfig{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("creating LLM provider: %w", err)
	}

	// init memory store
	store, err := memory.NewStore(cfg.Memory.Backend, cfg.Memory.Path)
	if err != nil {
		return nil, fmt.Errorf("creating memory store: %w", err)
	}

	return &Agent{
		cfg:      cfg,
		identity: id,
		llm:      provider,
		memory:   store,
	}, nil
}

// Run starts the agent's main loop.
func (a *Agent) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	fmt.Printf("🦠 Agent %s running (id: %s)\n", a.cfg.Agent.Name, a.identity.PublicKeyHex()[:16]+"...")
	fmt.Printf("   Model: %s/%s\n", a.cfg.LLM.Provider, a.cfg.LLM.Model)
	fmt.Printf("   Role:  %s\n", a.cfg.Agent.Role)

	// main loop: Observe → Think → Act → Reflect
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Println("\n🛑 Shutting down...")
			return a.shutdown()
		case <-ticker.C:
			if err := a.tick(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  tick error: %v\n", err)
			}
		}
	}
}

// tick executes one cycle of the agent loop.
func (a *Agent) tick(ctx context.Context) error {
	// Phase 1: just heartbeat
	// TODO: implement Observe → Think → Act → Reflect
	_ = ctx
	return nil
}

// shutdown gracefully stops the agent.
func (a *Agent) shutdown() error {
	if a.memory != nil {
		return a.memory.Close()
	}
	return nil
}
