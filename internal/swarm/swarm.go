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
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/spawner"
)

// TaskEvent records a task lifecycle event.
type TaskEvent struct {
	ID          string    `json:"id"`
	Agent       string    `json:"agent"`
	Description string    `json:"description"`
	Status      string    `json:"status"` // queued, running, completed, failed
	Runtime     string    `json:"runtime"`
	Result      string    `json:"result,omitempty"`
	Error       string    `json:"error,omitempty"`
	SubmittedAt time.Time `json:"submitted_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

const maxTaskLog = 50

// Swarm manages multiple agents on the same node.
type Swarm struct {
	bus     network.Bus
	agents  map[string]*agent.Agent
	spawner *spawner.Spawner
	baseDir string
	mu      sync.RWMutex

	startedAt      time.Time
	taskLog        []TaskEvent
	taskLogMu      sync.RWMutex
	tasksCompleted int
	tasksFailed    int
	tasksQueued    int
}

// New creates a new swarm with a local in-process bus.
func New(baseDir string, maxAgents int) *Swarm {
	return &Swarm{
		bus:       network.NewLocalBus(),
		agents:    make(map[string]*agent.Agent),
		spawner:   spawner.New(baseDir, maxAgents),
		baseDir:   baseDir,
		startedAt: time.Now(),
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
		bus:       bus,
		agents:    make(map[string]*agent.Agent),
		spawner:   spawner.New(baseDir, maxAgents),
		baseDir:   baseDir,
		startedAt: time.Now(),
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

	// Set working directory for file-based evolution (OpenAgent layout)
	agentDir := filepath.Join(s.baseDir, cfg.Agent.Name)
	os.MkdirAll(agentDir, 0755)
	a.SetWorkDir(agentDir)

	// Register task lifecycle callback
	agentName := cfg.Agent.Name
	a.SetOnTaskUpdate(func(taskID, status, runtime, result, errMsg string) {
		s.LogTask(TaskEvent{
			ID:          taskID,
			Agent:       agentName,
			Status:      status,
			Runtime:     runtime,
			Result:      result,
			Error:       errMsg,
			CompletedAt: time.Now(),
		})
	})

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

	childCfg, _, err := s.spawner.SpawnWithBalance(parent.Config(), parent.Identity(), req, 0)
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

	s.LogTask(TaskEvent{
		ID:          taskID,
		Agent:       agentName,
		Description: description,
		Status:      "queued",
		SubmittedAt: time.Now(),
	})

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

// GetAgent returns a specific agent by name.
func (s *Swarm) GetAgent(name string) *agent.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[name]
}

// Bus returns the shared message bus.
func (s *Swarm) Bus() network.Bus {
	return s.bus
}

// PeerID returns the P2P peer ID if using P2PBus, empty string otherwise.
func (s *Swarm) PeerID() string {
	if p2p, ok := s.bus.(*network.P2PBus); ok {
		return p2p.PeerID()
	}
	return ""
}

// LogTask records a task event in the swarm's task log.
func (s *Swarm) LogTask(evt TaskEvent) {
	s.taskLogMu.Lock()
	defer s.taskLogMu.Unlock()

	// Update or append
	found := false
	for i := len(s.taskLog) - 1; i >= 0; i-- {
		if s.taskLog[i].ID == evt.ID {
			// Merge: keep original fields, update new ones
			if evt.Status != "" {
				s.taskLog[i].Status = evt.Status
			}
			if evt.Runtime != "" {
				s.taskLog[i].Runtime = evt.Runtime
			}
			if evt.Result != "" {
				s.taskLog[i].Result = evt.Result
			}
			if evt.Error != "" {
				s.taskLog[i].Error = evt.Error
			}
			if !evt.CompletedAt.IsZero() {
				s.taskLog[i].CompletedAt = evt.CompletedAt
			}
			found = true
			break
		}
	}
	if !found {
		s.taskLog = append(s.taskLog, evt)
		if len(s.taskLog) > maxTaskLog {
			s.taskLog = s.taskLog[len(s.taskLog)-maxTaskLog:]
		}
	}

	// Update counters
	switch evt.Status {
	case "queued":
		s.tasksQueued++
	case "completed":
		s.tasksCompleted++
		s.tasksQueued--
	case "failed":
		s.tasksFailed++
		s.tasksQueued--
	}
}

// TaskLog returns the recent task events (newest first).
func (s *Swarm) TaskLog() []TaskEvent {
	s.taskLogMu.RLock()
	defer s.taskLogMu.RUnlock()

	result := make([]TaskEvent, len(s.taskLog))
	for i, evt := range s.taskLog {
		result[len(s.taskLog)-1-i] = evt
	}
	return result
}

// Stats returns swarm-level statistics.
type SwarmStats struct {
	TotalAgents       int     `json:"total_agents"`
	ActiveAgents      int     `json:"active_agents"`
	TotalCompleted    int     `json:"total_tasks_completed"`
	TotalFailed       int     `json:"total_tasks_failed"`
	TotalQueued       int     `json:"total_tasks_queued"`
	UptimeSeconds     int64   `json:"uptime_seconds"`
	NetworkTransport  string  `json:"network_transport"`
	AverageCapacity   float64 `json:"average_capacity"`
}

// Stats returns the current swarm statistics.
func (s *Swarm) Stats() SwarmStats {
	infos := s.List()
	active := 0
	var totalCapacity float64
	for _, info := range infos {
		if info.Status == agent.StatusIdle || info.Status == agent.StatusBusy {
			active++
		}
		if info.Status == agent.StatusBusy {
			totalCapacity += 0.0
		} else if info.Status == agent.StatusIdle {
			totalCapacity += 1.0
		}
	}
	avgCap := 0.0
	if len(infos) > 0 {
		avgCap = totalCapacity / float64(len(infos))
	}

	transport := "local"
	if _, ok := s.bus.(*network.P2PBus); ok {
		transport = "libp2p"
	}

	s.taskLogMu.RLock()
	completed := s.tasksCompleted
	failed := s.tasksFailed
	queued := s.tasksQueued
	s.taskLogMu.RUnlock()

	return SwarmStats{
		TotalAgents:      len(infos),
		ActiveAgents:     active,
		TotalCompleted:   completed,
		TotalFailed:      failed,
		TotalQueued:      queued,
		UptimeSeconds:    int64(time.Since(s.startedAt).Seconds()),
		NetworkTransport: transport,
		AverageCapacity:  avgCap,
	}
}

// Close shuts down the swarm.
func (s *Swarm) Close() error {
	return s.bus.Close()
}
