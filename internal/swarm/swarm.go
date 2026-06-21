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
	"sort"
	"sync"
	"time"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/runtime"
	"go.zoe.im/spore/internal/sessions"
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

	// Swarm-level subsystems (optional, initialized by Supervisor)
	changelog *Changelog
	feedback  *FeedbackChannel

	// sessions holds the chat-session store. Lazily opened the first time
	// it's requested so non-API callers (CLI demos / tests) don't pay the
	// sqlite open cost. Methods on *sessions.Store are safe for concurrent
	// use; we just guard the open-once with a mutex.
	sessionStore   *sessions.Store
	sessionStoreMu sync.Mutex

	// taskEvents fans out runtime.StreamEvent values to per-task SSE
	// subscribers. Populated by agents via PublishTaskEvent; consumed by
	// the API's /api/tasks/:id/stream handler.
	taskEvents *taskEventBroadcaster
}

// New creates a new swarm with a local in-process bus.
func New(baseDir string, maxAgents int) *Swarm {
	return &Swarm{
		bus:        network.NewLocalBus(),
		agents:     make(map[string]*agent.Agent),
		spawner:    spawner.New(baseDir, maxAgents),
		baseDir:    baseDir,
		startedAt:  time.Now(),
		taskEvents: newTaskEventBroadcaster(),
	}
}

// NewP2PSwarm creates a swarm backed by a libp2p P2P bus.
func NewP2PSwarm(baseDir string, maxAgents int, privKey ed25519.PrivateKey, listenAddrs, bootstrapPeers []string) (*Swarm, error) {
	bus, err := network.NewP2PBus(network.P2PConfig{
		ListenAddrs:    listenAddrs,
		BootstrapPeers: bootstrapPeers,
		PrivateKey:     privKey,
		DataDir:        baseDir,
	})
	if err != nil {
		return nil, fmt.Errorf("create p2p bus: %w", err)
	}
	return &Swarm{
		bus:        bus,
		agents:     make(map[string]*agent.Agent),
		spawner:    spawner.New(baseDir, maxAgents),
		baseDir:    baseDir,
		startedAt:  time.Now(),
		taskEvents: newTaskEventBroadcaster(),
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

	// Register task lifecycle callback. We pull session linkage out of the
	// store synchronously here — SessionForTask is consume-once, so even if
	// the same task somehow surfaces multiple completion events (retries,
	// duplicate broadcasts), we only append the assistant turn the first
	// time. Failures and bare in-progress updates skip the chat write.
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

		// Close per-task SSE subscribers on terminal status so browsers
		// don't sit on the stream after the task ends.
		if status == "completed" || status == "failed" {
			s.CloseTaskEvents(taskID)
		}

		// Chat session glue: if this task originated from a chat session,
		// land the agent's reply back into that session so the user sees a
		// continuous transcript on the next /api/sessions/<id> read or SSE
		// state push. We deliberately gate on terminal statuses + non-empty
		// content to avoid littering chats with empty "running" markers.
		if s.sessionStore == nil {
			return
		}
		sid := s.sessionStore.SessionForTask(taskID)
		if sid == "" {
			return
		}
		switch status {
		case "completed", "success":
			if result != "" {
				_, _ = s.sessionStore.AppendAssistantTurn(sid, result, taskID, runtime)
			}
		case "failed":
			msg := errMsg
			if msg == "" {
				msg = "(task failed with no error message)"
			}
			_, _ = s.sessionStore.AppendAssistantTurn(sid, "⚠️ "+msg, taskID, runtime)
		}
	})

	// Register runtime stream event callback — fans every per-event chunk
	// (thinking text, tool calls, completion) into the per-task pub/sub so
	// the API SSE handler can push them to chat UIs in real time.
	a.SetOnRuntimeEvent(func(taskID string, ev runtime.StreamEvent) {
		s.PublishTaskEvent(taskID, ev)
	})

	// Register spawn callback — allows agents to spawn children at runtime
	a.SetOnSpawnRequest(func(parentName, childRole string, childSkills []string, reason string) (string, error) {
		return s.handleSpawnRequest(parentName, childRole, childSkills, reason)
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

// Sessions returns the chat-session store, opening it on first call.
// dataDir is used to anchor the sqlite file under <baseDir>/sessions.db; we
// pass it through Swarm.baseDir so callers don't need to know the layout.
// Returns nil + error if the open fails — callers should treat that as
// "chat unavailable" and fall back to direct task submission.
func (s *Swarm) Sessions() (*sessions.Store, error) {
	s.sessionStoreMu.Lock()
	defer s.sessionStoreMu.Unlock()
	if s.sessionStore != nil {
		return s.sessionStore, nil
	}
	path := ""
	if s.baseDir != "" {
		path = filepath.Join(s.baseDir, "sessions.db")
	}
	store, err := sessions.New(path)
	if err != nil {
		return nil, err
	}
	s.sessionStore = store
	return store, nil
}

// PublishTaskEvent forwards a runtime.StreamEvent to every subscriber of
// taskID. Safe to call from runtime goroutines; non-blocking — slow
// subscribers drop events rather than stall the runtime.
//
// Called by agent.makeRuntimeEventHandler for every event seen during a
// streaming task execution, so the API SSE handler can fan it out to the
// browser.
func (s *Swarm) PublishTaskEvent(taskID string, ev runtime.StreamEvent) {
	if s.taskEvents == nil {
		return
	}
	s.taskEvents.publish(taskID, ev)
}

// SubscribeTaskEvents returns a channel of runtime.StreamEvent values for the
// given task and a cancel function the caller MUST invoke when finished. The
// channel closes when the task completes (CloseTaskEvents) or when cancel is
// called, whichever comes first.
func (s *Swarm) SubscribeTaskEvents(taskID string) (<-chan runtime.StreamEvent, func()) {
	if s.taskEvents == nil {
		// Defensive: return an already-closed channel + no-op cancel so
		// callers handle "no events available" the same as "task done".
		ch := make(chan runtime.StreamEvent)
		close(ch)
		return ch, func() {}
	}
	return s.taskEvents.subscribe(taskID)
}

// CloseTaskEvents signals to all subscribers of taskID that the task has
// completed; subscriber channels are closed. Idempotent.
func (s *Swarm) CloseTaskEvents(taskID string) {
	if s.taskEvents == nil {
		return
	}
	s.taskEvents.closeTask(taskID)
}

// SendTaskWithSession is the chat-aware variant of SendTask. It looks up the
// session, formats prior turns into a prompt prefix, submits the combined
// description to the target agent, then records the user turn and links the
// task back to the session so the assistant reply lands in the same thread.
//
// On any failure (unknown session, agent missing, store error) it falls back
// to the original behaviour: submit the raw description and surface the
// error so the API caller can decide whether to soft-degrade.
func (s *Swarm) SendTaskWithSession(sessionID, description string) (taskID, agentName string, err error) {
	store, err := s.Sessions()
	if err != nil {
		return "", "", fmt.Errorf("session store unavailable: %w", err)
	}
	sess, err := store.Get(sessionID)
	if err != nil {
		return "", "", fmt.Errorf("loading session: %w", err)
	}
	if sess == nil {
		return "", "", fmt.Errorf("session not found: %s", sessionID)
	}
	agentName = sess.Agent

	s.mu.RLock()
	a, ok := s.agents[agentName]
	s.mu.RUnlock()
	if !ok {
		return "", agentName, fmt.Errorf("agent not found: %s", agentName)
	}

	// Build prompt: prior turns prefixed, then the new user message. Cap at
	// 20 turns so a long-running session doesn't blow context windows; we
	// keep the most recent 20 since recency matters more than origin for
	// most chat use cases.
	turns, err := store.Turns(sessionID)
	if err != nil {
		return "", agentName, fmt.Errorf("loading turns: %w", err)
	}
	prompt := description
	if hist := sessions.FormatHistory(turns, 20); hist != "" {
		prompt = hist + "User: " + description
	}

	taskID = a.SubmitTask(prompt)

	// Record the user turn now (so it shows up immediately even before the
	// agent finishes), and link the task so the completion callback can
	// append the assistant reply.
	if _, err := store.AppendUserTurn(sessionID, description, taskID); err != nil {
		// Non-fatal: task is already queued. Log via the task error field.
		fmt.Printf("⚠️ session %s: failed to record user turn: %v\n", sessionID, err)
	}
	store.LinkTaskToSession(taskID, sessionID)

	s.LogTask(TaskEvent{
		ID:          taskID,
		Agent:       agentName,
		Description: description, // log only the new user message, not history
		Status:      "queued",
		SubmittedAt: time.Now(),
	})

	return taskID, agentName, nil
}

// List returns info about all agents.
func (s *Swarm) List() []agent.Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos := make([]agent.Info, 0, len(s.agents))
	for _, a := range s.agents {
		infos = append(infos, a.Info())
	}

	// Stable sort: by name
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos
}

// GetAgent returns a specific agent by name.
func (s *Swarm) GetAgent(name string) *agent.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agents[name]
}

// Agents returns all agents.
func (s *Swarm) Agents() []*agent.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*agent.Agent, 0, len(s.agents))
	for _, a := range s.agents {
		result = append(result, a)
	}
	return result
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

// handleSpawnRequest processes a runtime spawn request from an agent.
func (s *Swarm) handleSpawnRequest(parentName, childRole string, childSkills []string, reason string) (string, error) {
	s.mu.RLock()
	parent, ok := s.agents[parentName]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("parent agent not found: %s", parentName)
	}

	// Generate child name
	childName := fmt.Sprintf("%s-child-%d", parentName, s.spawner.ChildCount()+1)

	// Calculate startup balance (transfer from parent)
	parentCfg := parent.Config()
	share := parentCfg.Spawner.DefaultResourceShare
	if share <= 0 {
		share = 0.2
	}
	startupBalance := parent.Identity().Balance * share

	// Build child config from parent
	childCfg := *parentCfg
	childCfg.Agent.Name = childName
	if childRole != "" {
		childCfg.Agent.Role = childRole
	}
	if len(childSkills) > 0 {
		childCfg.Agent.Skills = childSkills
	}
	childCfg.Agent.Description = fmt.Sprintf("Spawned by %s: %s", parentName, reason)
	childCfg.Agent.CanReceive = true
	childCfg.Agent.CanDelegate = (childRole == "coordinator")

	// Debit parent
	if err := parent.Identity().Debit(startupBalance); err != nil {
		return "", fmt.Errorf("parent balance insufficient: %w", err)
	}

	fmt.Printf("🐣 [%s] Spawning child '%s' (role=%s, skills=%v, balance=%.2f) — %s\n",
		parentName, childName, childCfg.Agent.Role, childCfg.Agent.Skills, startupBalance, reason)

	// Add to swarm (creates agent, sets up bus, workDir, etc.)
	child, err := s.AddAgent(&childCfg)
	if err != nil {
		// Refund parent
		parent.Identity().Credit(startupBalance)
		return "", fmt.Errorf("adding child to swarm: %w", err)
	}

	// Transfer startup balance to child
	child.Identity().Credit(startupBalance)

	// Inherit evolution from parent
	if parent.Evolution() != nil && child.Evolution() != nil {
		parent.Evolution().InheritEvolution(child.Evolution())
	}

	// Record in changelog
	if s.changelog != nil {
		s.changelog.RecordSpawn(parentName, childName, reason)
	}

	// Start child agent in background
	go func() {
		fmt.Printf("🦠 Child agent %s running (spawned by %s)\n", childName, parentName)
		if err := child.Run(); err != nil {
			fmt.Printf("❌ Child agent %s exited: %v\n", childName, err)
		}
	}()

	return childName, nil
}

// Close shuts down the swarm.
func (s *Swarm) Close() error {
	// Close every agent first so per-agent resources (MCP manager, future
	// inline-curator goroutines, etc.) shut down cleanly before the bus
	// goes away. Errors are collected but don't short-circuit; we want
	// every agent to get a chance to release its resources.
	var firstErr error
	for _, a := range s.Agents() {
		if err := a.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := s.bus.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// SetChangelog sets the swarm changelog (typically by Supervisor).
func (s *Swarm) SetChangelog(cl *Changelog) { s.changelog = cl }

// Changelog returns the swarm changelog (may be nil).
func (s *Swarm) SwarmChangelog() *Changelog { return s.changelog }

// SetFeedback sets the human feedback channel (typically by Supervisor).
func (s *Swarm) SetFeedback(fc *FeedbackChannel) { s.feedback = fc }

// SwarmFeedback returns the human feedback channel (may be nil).
func (s *Swarm) SwarmFeedback() *FeedbackChannel { return s.feedback }
