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

package swarm

import (
	"crypto/ed25519"
	"fmt"
	"sync"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/spawner"
)

// Swarm manages multiple agents on the same node.
type Swarm struct {
	bus     network.Bus
	agents  map[string]*agent.Agent
	spawner *spawner.Spawner
	mu      sync.RWMutex
}

// New creates a new swarm with a local in-process bus.
func New(baseDir string, maxAgents int) *Swarm {
	return &Swarm{
		bus:     network.NewLocalBus(),
		agents:  make(map[string]*agent.Agent),
		spawner: spawner.New(baseDir, maxAgents),
	}
}

// NewP2PSwarm creates a swarm backed by a libp2p P2P bus.
func NewP2PSwarm(baseDir string, maxAgents int, privKey ed25519.PrivateKey, listenAddrs, bootstrapPeers []string) (*Swarm, error) {
	bus, err := network.NewP2PBus(network.P2PConfig{
		ListenAddrs:    listenAddrs,
		BootstrapPeers: bootstrapPeers,
		PrivateKey:     privKey,
	})
	if err != nil {
		return nil, fmt.Errorf("create p2p bus: %w", err)
	}
	return &Swarm{
		bus:     bus,
		agents:  make(map[string]*agent.Agent),
		spawner: spawner.New(baseDir, maxAgents),
	}, nil
}

// AddAgent creates and registers an agent in the swarm.
func (s *Swarm) AddAgent(cfg *agent.Config) (*agent.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, err := agent.NewWithBus(cfg, s.bus)
	if err != nil {
		return nil, err
	}

	s.agents[cfg.Agent.Name] = a
	return a, nil
}

// SpawnChild creates a new child agent from a parent.
func (s *Swarm) SpawnChild(parentName string, req *spawner.Request) (*agent.Agent, error) {
	s.mu.RLock()
	parent, ok := s.agents[parentName]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("parent agent not found: %s", parentName)
	}

	childCfg, _, err := s.spawner.Spawn(parent.Config(), req)
	if err != nil {
		return nil, err
	}

	return s.AddAgent(childCfg)
}

// RunAll starts all agents in background goroutines.
func (s *Swarm) RunAll() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, a := range s.agents {
		go func(a *agent.Agent) {
			if err := a.Run(); err != nil {
				fmt.Printf("❌ Agent %s exited: %v\n", a.Info().Name, err)
			}
		}(a)
	}
}

// SendTask sends a task to a specific agent.
func (s *Swarm) SendTask(agentName, description string) (string, error) {
	s.mu.RLock()
	a, ok := s.agents[agentName]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("agent not found: %s", agentName)
	}
	taskID := a.SubmitTask(description)
	return taskID, nil
}

// List returns info about all agents.
func (s *Swarm) List() []agent.Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos := make([]agent.Info, 0, len(s.agents))
	for _, a := range s.agents {
		infos = append(infos, a.Info())
	}
	return infos
}

// Bus returns the shared message bus.
func (s *Swarm) Bus() network.Bus {
	return s.bus
}

// Close shuts down the swarm.
func (s *Swarm) Close() error {
	return s.bus.Close()
}
