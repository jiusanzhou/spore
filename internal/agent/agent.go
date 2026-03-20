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
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.zoe.im/spore/internal/engine"
	"go.zoe.im/spore/internal/ethics"
	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/protocol"
	"go.zoe.im/spore/internal/runtime"

	"github.com/google/uuid"
)

// Status represents the agent's current state.
type Status string

const (
	StatusIdle      Status = "idle"
	StatusBusy      Status = "busy"
	StatusError     Status = "error"
	StatusStopped   Status = "stopped"
	StatusHibernate Status = "hibernate"
)

// PeerInfo holds discovered peer information from CapabilityAd.
type PeerInfo struct {
	AgentID      string
	PeerID       string
	Capabilities []string
	Capacity     float64
	Reputation   float64
	LastSeen     time.Time
}

// Info holds the agent's runtime information.
type Info struct {
	Name        string    `json:"name"`
	ID          string    `json:"id"`
	Role        string    `json:"role"`
	Skills      []string  `json:"skills,omitempty"`
	Description string    `json:"description,omitempty"`
	Model       string    `json:"model"`
	Runtime     string    `json:"runtime"`
	Status      Status    `json:"status"`
	TaskCount   int       `json:"task_count"`
	StartedAt   time.Time `json:"started_at"`
	Balance     float64   `json:"balance"`
}

// ethicsAdapter wraps *ethics.Engine to satisfy engine.EthicsChecker.
type ethicsAdapter struct {
	e *ethics.Engine
}

func (a *ethicsAdapter) Check(agentID, taskID, action string) (string, string, string) {
	dec, lvl, reason := a.e.Check(agentID, taskID, action)
	return string(dec), string(lvl), reason
}

// Agent is the core runtime for a single spore agent.
type Agent struct {
	cfg      *Config
	identity *Identity
	llm      llm.Provider
	memory   memory.Store
	engine   *engine.Engine
	ethics   *ethics.Engine
	bus      network.Bus
	registry *runtime.Registry

	status    Status
	taskQueue chan *taskEntry
	mu        sync.RWMutex
	taskCount int
	startedAt time.Time

	// Peer registry from CapabilityAd messages
	peersMu  sync.RWMutex
	peers    map[string]*PeerInfo

	// Task lifecycle callback (set by Swarm)
	onTaskUpdate func(taskID, status, runtime, result, errMsg string)

	// Coordinator state for tracking delegated subtasks
	coordStates map[string]*coordinatorState
}

// SetOnTaskUpdate registers a callback for task lifecycle events.
func (a *Agent) SetOnTaskUpdate(fn func(taskID, status, runtime, result, errMsg string)) {
	a.onTaskUpdate = fn
}

// taskEntry wraps a task with optional runtime preference.
type taskEntry struct {
	ID          string
	Description string
	Runtime     string // preferred runtime name, empty = auto
	WorkDir     string
	CreatedAt   time.Time
}

// New creates a new Agent from config.
func New(cfg *Config) (*Agent, error) {
	id, err := NewIdentity(cfg.Agent.Name)
	if err != nil {
		return nil, fmt.Errorf("creating identity: %w", err)
	}

	provider, err := llm.NewProvider(cfg.LLM.Provider, llm.ProviderConfig{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
		Headers: cfg.LLM.Headers,
	})
	if err != nil {
		return nil, fmt.Errorf("creating LLM provider: %w", err)
	}

	store, err := memory.NewStore(cfg.Memory.Backend, cfg.Memory.Path, memory.WithIPFSEndpoint(cfg.Memory.IPFSEndpoint))
	if err != nil {
		return nil, fmt.Errorf("creating memory store: %w", err)
	}

	ethicsEngine, err := ethics.New("", &ethics.Config{
		MaxBudgetPerTask: cfg.Ethics.MaxBudgetPerTask,
	})
	if err != nil {
		return nil, fmt.Errorf("creating ethics engine: %w", err)
	}

	eng := engine.New(provider, store)
	eng.SetEthics(&ethicsAdapter{e: ethicsEngine})
	eng.SetAgentID(id.PublicKeyHex()[:16])
	eng.RegisterTool(&engine.ShellTool{})
	eng.RegisterTool(&engine.WebSearchTool{})

	// Setup runtime registry
	reg := runtime.NewRegistry()

	// Always register builtin
	reg.Register(runtime.NewBuiltin(provider, store))

	// Setup based on config
	switch cfg.Runtime.Type {
	case "auto", "":
		// Auto-discover available CLIs (native + agentbox adapters)
		discovered := reg.AutoDiscover(context.Background())
		if len(discovered) > 0 {
			fmt.Printf("   Discovered runtimes: %v\n", discovered)
		}
	case "openclaw":
		reg.Register(runtime.NewOpenClaw())
	case "http":
		if cfg.Runtime.URL != "" {
			reg.Register(runtime.NewHTTPRuntime(cfg.Runtime.URL))
		}
	case "exec":
		if cfg.Runtime.Command != "" {
			reg.Register(runtime.NewExecRuntime(runtime.ExecConfig{
				Name:     cfg.Agent.Name + "-exec",
				Command:  cfg.Runtime.Command,
				Args:     cfg.Runtime.Args,
				TaskFlag: cfg.Runtime.TaskFlag,
				Tags:     cfg.Runtime.Tags,
			}))
		}
	case "builtin":
		// already registered
	default:
		// Try agentbox adapter for known runtimes (claude-code, codex, gemini, etc.)
		// Map common aliases: "claude-code" → "claude"
		aboxName := cfg.Runtime.Type
		if aboxName == "claude-code" {
			aboxName = "claude"
		}
		for _, adapter := range runtime.DefaultAboxAdapters() {
			if adapter.Info().Name == aboxName {
				reg.Register(adapter)
				break
			}
		}
	}

	return &Agent{
		cfg:       cfg,
		identity:  id,
		llm:       provider,
		memory:    store,
		engine:    eng,
		ethics:    ethicsEngine,
		registry:  reg,
		status:    StatusIdle,
		taskQueue: make(chan *taskEntry, 50),
		peers:     make(map[string]*PeerInfo),
	}, nil
}

// NewWithBus creates an agent attached to a shared message bus.
func NewWithBus(cfg *Config, bus network.Bus) (*Agent, error) {
	a, err := New(cfg)
	if err != nil {
		return nil, err
	}
	a.bus = bus

	a.engine.RegisterTool(&engine.DelegateTool{
		SendFunc: func(to, taskDesc string) error {
			msg, err := protocol.NewMessage(
				a.identity.PublicKeyHex(),
				to,
				protocol.MsgTaskRequest,
				protocol.TaskRequest{Description: taskDesc},
			)
			if err != nil {
				return err
			}
			return bus.Send(msg)
		},
	})

	bus.Subscribe(a.identity.PublicKeyHex()[:16], a.handleMessage)
	return a, nil
}

// Run starts the agent's main loop.
func (a *Agent) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	a.startedAt = time.Now()
	a.status = StatusIdle

	runtimeName := "builtin"
	if rts := a.registry.List(); len(rts) > 0 {
		// pick the first non-builtin if available
		for _, rt := range rts {
			if rt.Name != "builtin" {
				runtimeName = rt.Name
				break
			}
		}
	}

	fmt.Printf("🦠 Agent %s running (id: %s)\n", a.cfg.Agent.Name, a.identity.PublicKeyHex()[:16])
	fmt.Printf("   Runtime: %s\n", runtimeName)
	fmt.Printf("   Model:   %s/%s\n", a.cfg.LLM.Provider, a.cfg.LLM.Model)
	fmt.Printf("   Role:    %s\n", a.cfg.Agent.Role)
	fmt.Printf("   Balance: %.4f\n", a.identity.Balance)

	go a.taskWorker(ctx)

	// Publish initial CapabilityAd
	a.publishCapabilityAd()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Println("\n🛑 Shutting down...")
			return a.shutdown()
		case <-ticker.C:
			a.heartbeat()
		}
	}
}

// SubmitTask queues a task for execution.
func (a *Agent) SubmitTask(description string) string {
	return a.SubmitTaskWithRuntime(description, "", "")
}

// SubmitTaskWithRuntime queues a task with a specific runtime preference.
func (a *Agent) SubmitTaskWithRuntime(description, runtimeName, workDir string) string {
	entry := &taskEntry{
		ID:          uuid.New().String()[:8],
		Description: description,
		Runtime:     runtimeName,
		WorkDir:     workDir,
		CreatedAt:   time.Now(),
	}
	a.taskQueue <- entry
	return entry.ID
}

// Info returns the agent's current info.
func (a *Agent) Info() Info {
	a.mu.RLock()
	defer a.mu.RUnlock()

	rtName := a.cfg.Runtime.Type
	if rtName == "" {
		rtName = "auto"
	}

	return Info{
		Name:        a.cfg.Agent.Name,
		ID:          a.identity.PublicKeyHex()[:16],
		Role:        a.cfg.Agent.Role,
		Skills:      a.cfg.Agent.Skills,
		Description: a.cfg.Agent.Description,
		Model:       a.cfg.LLM.Model,
		Runtime:     rtName,
		Status:      a.status,
		TaskCount:   a.taskCount,
		StartedAt:   a.startedAt,
		Balance:     a.identity.Balance,
	}
}

// Runtimes returns info about all available runtimes.
func (a *Agent) Runtimes() []runtime.Info {
	return a.registry.List()
}

// Identity returns the agent's identity.
func (a *Agent) Identity() *Identity { return a.identity }

// Config returns the agent's config.
func (a *Agent) Config() *Config { return a.cfg }

// Peers returns the current peer registry.
func (a *Agent) Peers() map[string]*PeerInfo {
	a.peersMu.RLock()
	defer a.peersMu.RUnlock()
	cp := make(map[string]*PeerInfo, len(a.peers))
	for k, v := range a.peers {
		cp[k] = v
	}
	return cp
}

func (a *Agent) taskWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-a.taskQueue:
			// Check hibernate state
			a.mu.RLock()
			status := a.status
			a.mu.RUnlock()
			if status == StatusHibernate {
				fmt.Printf("💤 [%s] Rejecting task (hibernate): %s\n", a.cfg.Agent.Name, entry.Description)
				continue
			}

			// Check minimum balance for task acceptance
			if a.cfg.Economy.MinTaskBalance > 0 && !a.identity.CanAfford(a.cfg.Economy.MinTaskBalance) {
				fmt.Printf("💰 [%s] Rejecting task (insufficient_balance): %.4f < %.4f\n",
					a.cfg.Agent.Name, a.identity.Balance, a.cfg.Economy.MinTaskBalance)
				continue
			}

			a.mu.Lock()
			a.status = StatusBusy
			a.mu.Unlock()

			fmt.Printf("📋 [%s] Starting task: %s\n", a.cfg.Agent.Name, entry.Description)
			if a.onTaskUpdate != nil {
				a.onTaskUpdate(entry.ID, "running", "", "", "")
			}

			err := a.executeTask(ctx, entry)
			if err != nil {
				fmt.Printf("❌ [%s] Task failed: %s\n", a.cfg.Agent.Name, err)
			}

			a.mu.Lock()
			a.taskCount++
			// Check if we should hibernate
			if a.cfg.Economy.HibernateThreshold > 0 && a.identity.Balance <= 0 {
				a.status = StatusHibernate
				fmt.Printf("💤 [%s] Entering hibernate (balance depleted)\n", a.cfg.Agent.Name)
			} else {
				a.status = StatusIdle
			}
			a.mu.Unlock()
		}
	}
}

func (a *Agent) executeTask(ctx context.Context, entry *taskEntry) error {
	// Coordinator agents decompose and delegate
	if a.cfg.Agent.Role == "coordinator" && a.cfg.Agent.CanDelegate && a.bus != nil {
		return a.coordinatorExecute(ctx, entry)
	}

	return a.executeTaskDirect(ctx, entry)
}

// executeTaskDirect runs a task via runtime without coordinator decomposition.
func (a *Agent) executeTaskDirect(ctx context.Context, entry *taskEntry) error {
	// Worker/specialist: direct execution
	var rt runtime.Runtime
	var err error

	if entry.Runtime != "" {
		// Explicit runtime requested
		var ok bool
		rt, ok = a.registry.Get(entry.Runtime)
		if !ok {
			return fmt.Errorf("runtime not found: %s", entry.Runtime)
		}
	} else {
		// Auto-route based on task tags (for now, just use first non-builtin)
		rt, err = a.registry.Route(nil)
		if err != nil {
			return fmt.Errorf("no runtime available: %w", err)
		}
	}

	fmt.Printf("   [%s] Using runtime: %s\n", a.cfg.Agent.Name, rt.Info().Name)

	input := runtime.TaskInput{
		ID:          entry.ID,
		Description: entry.Description,
		WorkDir:     entry.WorkDir,
	}

	output, err := rt.Execute(ctx, input)
	if err != nil {
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "failed", rt.Info().Name, "", err.Error())
		}
		return err
	}

	if output.Success {
		fmt.Printf("✅ [%s] Task completed via %s: %s\n", a.cfg.Agent.Name, rt.Info().Name, truncate(output.Result, 200))
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "completed", rt.Info().Name, output.Result, "")
		}
		// Debit cost from balance after task completion
		if output.Cost > 0 {
			if err := a.identity.Debit(output.Cost); err != nil {
				fmt.Printf("⚠️  [%s] Balance debit failed: %v\n", a.cfg.Agent.Name, err)
			}
		}
		// Broadcast result to bus (for coordinator collection)
		a.broadcastTaskResult(entry.ID, output.Result, true, "")
	} else {
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "failed", rt.Info().Name, "", output.Error)
		}
		a.broadcastTaskResult(entry.ID, "", false, output.Error)
		return fmt.Errorf("task failed: %s", output.Error)
	}
	return nil
}

func (a *Agent) handleMessage(msg *protocol.Message) error {
	switch msg.Type {
	case protocol.MsgTaskRequest:
		// Check balance before accepting
		if a.cfg.Economy.MinTaskBalance > 0 && !a.identity.CanAfford(a.cfg.Economy.MinTaskBalance) {
			fmt.Printf("💰 [%s] Rejecting task_request from %s: insufficient_balance\n", a.cfg.Agent.Name, msg.From[:8])
			return nil
		}
		var req protocol.TaskRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			return fmt.Errorf("unmarshaling task request: %w", err)
		}
		fmt.Printf("📨 [%s] Received task from %s: %s\n", a.cfg.Agent.Name, msg.From[:8], req.Description)
		a.SubmitTask(req.Description)
	case protocol.MsgHeartbeat:
		fmt.Printf("💓 [%s] Heartbeat from %s\n", a.cfg.Agent.Name, msg.From[:8])
	case protocol.MsgCapabilityAd:
		var ad protocol.CapabilityAd
		if err := json.Unmarshal(msg.Payload, &ad); err != nil {
			return fmt.Errorf("unmarshaling capability_ad: %w", err)
		}
		a.registerPeer(&ad)
	case protocol.MsgTaskResult:
		var result protocol.TaskResult
		if err := json.Unmarshal(msg.Payload, &result); err != nil {
			return fmt.Errorf("unmarshaling task_result: %w", err)
		}
		a.handleTaskResult(&result, msg.From)
	}
	return nil
}

func (a *Agent) registerPeer(ad *protocol.CapabilityAd) {
	a.peersMu.Lock()
	defer a.peersMu.Unlock()
	a.peers[ad.AgentID] = &PeerInfo{
		AgentID:      ad.AgentID,
		PeerID:       ad.PeerID,
		Capabilities: ad.Capabilities,
		Capacity:     ad.Capacity,
		Reputation:   ad.Reputation,
		LastSeen:     time.Now(),
	}

	// Auto-register peer mapping on P2PBus
	if p2pBus, ok := a.bus.(*network.P2PBus); ok && ad.PeerID != "" {
		pid, err := network.ParsePeerID(ad.PeerID)
		if err == nil {
			p2pBus.RegisterPeer(ad.AgentID, pid)
		}
	}
}

// broadcastTaskResult sends task result to the bus for coordinator collection.
func (a *Agent) broadcastTaskResult(taskID, output string, success bool, errMsg string) {
	if a.bus == nil {
		return
	}
	result := protocol.TaskResult{
		TaskID:  taskID,
		Output:  output,
		Success: success,
		Error:   errMsg,
	}
	msg, err := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgTaskResult,
		result,
	)
	if err != nil {
		return
	}
	a.bus.Send(msg)
}

func (a *Agent) publishCapabilityAd() {
	if a.bus == nil {
		return
	}
	a.mu.RLock()
	taskCount := a.taskCount
	a.mu.RUnlock()

	capacity := 1.0 // idle
	if taskCount > 0 {
		// Linearly decrease: 1 task → 0.7, 2 → 0.4, 3+ → 0.1
		capacity = 1.0 - float64(taskCount)*0.3
		if capacity < 0.1 {
			capacity = 0.1
		}
	}

	// Build capabilities from role + skills
	caps := append([]string{a.cfg.Agent.Role}, a.cfg.Agent.Skills...)

	ad := protocol.CapabilityAd{
		AgentID:      a.identity.PublicKeyHex()[:16],
		Capabilities: caps,
		Interests:    a.cfg.Agent.Interests,
		Capacity:     capacity,
		Reputation:   1.0,
	}

	// Set PeerID if using P2P bus
	if p2pBus, ok := a.bus.(*network.P2PBus); ok {
		ad.PeerID = p2pBus.PeerID()
	}

	msg, _ := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgCapabilityAd,
		ad,
	)
	a.bus.Send(msg)
}

func (a *Agent) heartbeat() {
	if a.bus == nil {
		return
	}
	a.mu.RLock()
	taskCount := a.taskCount
	status := a.status
	a.mu.RUnlock()

	capacity := 1.0
	if status == StatusBusy {
		capacity = 0.0
	} else if status == StatusHibernate {
		capacity = 0.0
	}

	payload := protocol.HeartbeatPayload{
		Name:      a.cfg.Agent.Name,
		Status:    string(status),
		Runtime:   a.cfg.Runtime.Type,
		Balance:   a.identity.Balance,
		Capacity:  capacity,
		TaskCount: taskCount,
		Uptime:    int64(time.Since(a.startedAt).Seconds()),
	}

	msg, _ := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgHeartbeat,
		payload,
	)
	a.bus.Send(msg)
}

func (a *Agent) shutdown() error {
	a.status = StatusStopped
	if a.bus != nil {
		a.bus.Unsubscribe(a.identity.PublicKeyHex()[:16])
	}
	if a.registry != nil {
		a.registry.Close()
	}
	if a.ethics != nil {
		a.ethics.Close()
	}
	if a.memory != nil {
		return a.memory.Close()
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
