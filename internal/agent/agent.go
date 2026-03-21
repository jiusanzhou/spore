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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	Evolution   string    `json:"evolution,omitempty"` // evolution engine stats
	Drives      *Drive      `json:"drives,omitempty"`    // intrinsic drive values
	Self        *SelfModel       `json:"self,omitempty"`      // self-awareness model
	Collective  *CollectiveState `json:"collective,omitempty"` // swarm consciousness
	Economy     *TokenState      `json:"economy,omitempty"`    // token economy state
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

	evolution   *EvolutionEngine
	peerEvo     *PeerEvolution
	evoFS       *EvolutionFS // file-based evolution persistence (OpenAgent layout)

	// Intrinsic drive engine — autonomous behavior generation
	drives *DriveEngine

	// Self-awareness engine — internal self-model + introspection
	awareness *Awareness

	// Collective consciousness — shared swarm understanding
	collective *Collective

	// Token economy — the oxygen that keeps agents alive
	tokens *TokenLedger

	status      Status
	taskQueue   chan *taskEntry
	mu          sync.RWMutex
	activeTasks int32 // atomic: number of currently executing tasks
	taskCount int
	startedAt time.Time

	// Peer registry from CapabilityAd messages
	peersMu  sync.RWMutex
	peers    map[string]*PeerInfo

	// Task lifecycle callback (set by Swarm)
	onTaskUpdate func(taskID, status, runtime, result, errMsg string)

	// Spawn callback (set by Swarm) — returns child agent name or error
	onSpawnRequest func(parentName string, childRole string, childSkills []string, reason string) (string, error)

	// Coordinator state for tracking delegated subtasks
	coordStates map[string]*coordinatorState
}

// SetOnTaskUpdate registers a callback for task lifecycle events.
func (a *Agent) SetOnTaskUpdate(fn func(taskID, status, runtime, result, errMsg string)) {
	a.onTaskUpdate = fn
}

// SetOnSpawnRequest registers a callback for runtime spawn requests.
func (a *Agent) SetOnSpawnRequest(fn func(parentName string, childRole string, childSkills []string, reason string) (string, error)) {
	a.onSpawnRequest = fn
}

// RequestSpawn asks the swarm to spawn a child agent.
func (a *Agent) RequestSpawn(role string, skills []string, reason string) (string, error) {
	if a.onSpawnRequest == nil {
		return "", fmt.Errorf("spawn not available (not in a swarm)")
	}
	// Check spawn budget
	minBal := a.cfg.Spawner.MinBalanceToSpawn
	if minBal <= 0 {
		minBal = 10.0
	}
	if a.identity.Balance < minBal {
		return "", fmt.Errorf("insufficient balance to spawn: have %.2f, need %.2f", a.identity.Balance, minBal)
	}
	return a.onSpawnRequest(a.cfg.Agent.Name, role, skills, reason)
}

// SetWorkDir sets the agent's working directory and enables file-based evolution persistence.
// The directory follows the OpenAgent spec layout: experience/, evolution/, agent.yaml.
func (a *Agent) SetWorkDir(dir string) {
	if dir == "" {
		return
	}

	// If memory is in-memory (:memory:), upgrade to file-based in workDir
	if a.cfg.Memory.Path == "" || a.cfg.Memory.Path == ":memory:" {
		dbPath := filepath.Join(dir, "memory.db")
		newStore, err := memory.NewStore("sqlite", dbPath)
		if err == nil {
			if a.memory != nil {
				a.memory.Close()
			}
			a.memory = newStore
			a.engine.SetMemory(newStore)
			fmt.Printf("💾 [%s] Memory upgraded to %s\n", a.cfg.Agent.Name, dbPath)
		}
	}

	a.evoFS = NewEvolutionFS(dir, a)
	// Try loading file-based state (supplements SQLite)
	if a.evolution != nil {
		a.evoFS.LoadEvolutionState(a.evolution)
	}
	if a.peerEvo != nil {
		a.evoFS.LoadPeerEvolution(a.peerEvo)
	}
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

	a := &Agent{
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
	}

	// Initialize evolution engine and restore previous state
	a.evolution = NewEvolutionEngine(a)
	a.evolution.RestoreState()

	// Initialize peer evolution tracker (for coordinators)
	a.peerEvo = NewPeerEvolution(a)

	// Initialize token economy — the oxygen system
	tokenCfg := DefaultTokenConfig()
	if a.cfg.Token != nil {
		tokenCfg = *a.cfg.Token
	}
	a.tokens = NewTokenLedger(a, tokenCfg)
	a.tokens.Restore()
	a.tokens.Seed() // birth capital (no-op if already has balance)

	// Initialize intrinsic drive engine
	a.drives = NewDriveEngine(a)
	a.drives.Restore()

	// Initialize self-awareness engine
	a.awareness = NewAwareness(a)
	a.awareness.Restore()

	// Initialize collective consciousness
	a.collective = NewCollective(a)

	return a, nil
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

	// Start concurrent task workers
	numWorkers := 3
	for i := 0; i < numWorkers; i++ {
		go a.taskWorker(ctx)
	}

	// Publish initial CapabilityAd
	a.publishCapabilityAd()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Evolution cycle: every 10 heartbeats (~5 min)
	evolveCounter := 0
	const evolveEvery = 10
	// Deep LLM reflection: every 30 min
	const deepReflectInterval = 30 * time.Minute

	for {
		select {
		case <-sigCh:
			fmt.Println("\n🛑 Shutting down...")
			return a.shutdown()
		case action := <-a.drives.Actions():
			// Execute autonomous drive-generated actions
			go a.executeAutonomousAction(ctx, action)
		case <-ticker.C:
			a.heartbeat()

			// Token metabolism — the cost of being alive
			if a.tokens != nil {
				a.tokens.Metabolism()
			}

			// Update self-awareness (lightweight, every heartbeat)
			if a.awareness != nil {
				a.awareness.UpdateMood()
			}

			// Drive pulse: evaluate intrinsic motivations
			if a.drives != nil {
				a.drives.Pulse(ctx)
			}

			evolveCounter++
			if evolveCounter >= evolveEvery && a.evolution != nil {
				evolveCounter = 0
				a.evolution.Evolve(ctx, deepReflectInterval)
				if a.peerEvo != nil {
					a.peerEvo.Persist()
				}
				// Sync evolution state to OpenAgent directory layout
				if a.evoFS != nil {
					a.evoFS.SyncToDisk(a.evolution, a.peerEvo)
				}
				// Share experience with the swarm
				a.ShareExperience()
				// Persist drive state alongside evolution
				if a.drives != nil {
					a.drives.Persist()
				}
				// Persist token state
				if a.tokens != nil {
					a.tokens.Persist()
				}
				// Deep self-reflection (LLM introspection)
				if a.awareness != nil {
					a.awareness.Introspect(ctx)
					a.awareness.Persist()
				}
				// Share consciousness + synthesize collective understanding
				if a.collective != nil {
					a.collective.Broadcast()
					a.collective.Synthesize(ctx)
				}
			}
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

	info := Info{
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
	if a.evolution != nil {
		info.Evolution = a.evolution.Stats()
	}
	if a.drives != nil {
		d := a.drives.Drive()
		info.Drives = &d
	}
	if a.awareness != nil {
		s := a.awareness.Self()
		info.Self = &s
	}
	if a.collective != nil {
		cs := a.collective.State()
		info.Collective = &cs
	}
	if a.tokens != nil {
		ts := a.tokens.State()
		info.Economy = &ts
	}
	return info
}

// Runtimes returns info about all available runtimes.
func (a *Agent) Runtimes() []runtime.Info {
	return a.registry.List()
}

// Identity returns the agent's identity.
func (a *Agent) Identity() *Identity { return a.identity }

// Config returns the agent's config.
func (a *Agent) Config() *Config { return a.cfg }

// Evolution returns the agent's evolution engine (may be nil).
func (a *Agent) Evolution() *EvolutionEngine { return a.evolution }

// Drives returns the agent's drive engine (may be nil).
func (a *Agent) Drives() *DriveEngine { return a.drives }

// Awareness returns the agent's self-awareness engine (may be nil).
func (a *Agent) Awareness() *Awareness { return a.awareness }

// Collective returns the agent's collective consciousness engine (may be nil).
func (a *Agent) Collective() *Collective { return a.collective }
func (a *Agent) Tokens() *TokenLedger    { return a.tokens }

// PeerEvo returns the agent's peer evolution tracker (may be nil).
func (a *Agent) PeerEvo() *PeerEvolution { return a.peerEvo }

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

			active := atomic.AddInt32(&a.activeTasks, 1)
			a.mu.Lock()
			a.status = StatusBusy
			a.mu.Unlock()

			fmt.Printf("📋 [%s] Starting task (%d active): %s\n", a.cfg.Agent.Name, active, entry.Description)
			if a.onTaskUpdate != nil {
				a.onTaskUpdate(entry.ID, "running", "", "", "")
			}

			err := a.executeTask(ctx, entry)
			if err != nil {
				fmt.Printf("❌ [%s] Task failed: %s\n", a.cfg.Agent.Name, err)
			}

			remaining := atomic.AddInt32(&a.activeTasks, -1)
			a.mu.Lock()
			a.taskCount++
			if a.cfg.Economy.HibernateThreshold > 0 && a.identity.Balance <= 0 {
				a.status = StatusHibernate
				fmt.Printf("💤 [%s] Entering hibernate (balance depleted)\n", a.cfg.Agent.Name)
			} else if remaining == 0 {
				a.status = StatusIdle
			}
			a.mu.Unlock()
		}
	}
}

func (a *Agent) executeTask(ctx context.Context, entry *taskEntry) error {
	// Dynamic coordination decision — any agent with can_delegate + bus + peers can coordinate
	if a.shouldCoordinate(entry) {
		return a.coordinatorExecute(ctx, entry)
	}

	return a.executeTaskDirect(ctx, entry)
}

// shouldCoordinate decides whether to decompose+delegate vs execute directly.
// No fixed coordinator role — any agent can coordinate if:
// 1. It has delegation capability and a message bus
// 2. It has peers available to delegate to
// 3. The task seems complex enough to warrant decomposition
func (a *Agent) shouldCoordinate(entry *taskEntry) bool {
	// Must have delegation capability and bus
	if !a.cfg.Agent.CanDelegate || a.bus == nil {
		return false
	}

	// Must have peers to delegate to
	a.peersMu.RLock()
	peerCount := len(a.peers)
	a.peersMu.RUnlock()
	if peerCount == 0 {
		return false
	}

	// Simple heuristic: coordinate if task description is complex
	// (long description or contains coordination keywords)
	desc := entry.Description
	if len(desc) > 100 {
		return true
	}

	// Check for multi-part task indicators
	for _, kw := range []string{
		"comprehensive", "research and write", "coordinate", "produce a report",
		"build a", "design and implement", "analyze and", "create a guide",
		"multiple", "step-by-step", "full", "complete",
	} {
		if containsIgnoreCase(desc, kw) {
			return true
		}
	}

	// Evolution-informed: if we know our skills are weak for this, delegate
	if a.evolution != nil {
		genetics := a.evolution.ComputeGenetics()
		if genetics.Fitness < 0.5 {
			return true // low fitness → better to delegate
		}
	}

	return false
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
		// Let evolution engine suggest preferred runtime
		if a.evolution != nil {
			if preferred := a.evolution.BestRuntime(); preferred != "" {
				if prt, ok := a.registry.Get(preferred); ok {
					rt = prt
				}
			}
		}
		if rt == nil {
			// Fallback: auto-route based on task tags
			rt, err = a.registry.Route(nil)
			if err != nil {
				return fmt.Errorf("no runtime available: %w", err)
			}
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
		// Token economy: charge LLM cost, reward completion
		if a.tokens != nil {
			if output.Cost > 0 {
				a.tokens.ChargeThink(output.Cost, "task:"+entry.ID[:8])
			}
			a.tokens.RewardTask(entry.ID, true)
		} else if output.Cost > 0 {
			// Legacy: direct debit
			if err := a.identity.Debit(output.Cost); err != nil {
				fmt.Printf("⚠️  [%s] Balance debit failed: %v\n", a.cfg.Agent.Name, err)
			}
		}
		// Broadcast result to bus (for coordinator collection)
		a.broadcastTaskResult(entry.ID, output.Result, true, "")
		// Store task experience in memory
		a.rememberTask(entry, output, rt.Info().Name)
		// Record to evolution engine for self-improvement
		a.recordEvolution(entry, output, rt.Info().Name, true, "")
	} else {
		if a.onTaskUpdate != nil {
			a.onTaskUpdate(entry.ID, "failed", rt.Info().Name, "", output.Error)
		}
		// Token economy: penalize failure
		if a.tokens != nil {
			a.tokens.RewardTask(entry.ID, false)
		}
		a.broadcastTaskResult(entry.ID, "", false, output.Error)
		a.recordEvolution(entry, nil, rt.Info().Name, false, output.Error)
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
		// Ignore own heartbeats (GossipSub echoes back to sender)
		selfID := a.identity.PublicKeyHex()[:16]
		if msg.From == selfID {
			return nil
		}
		// Silent — heartbeats are high-frequency, only log at debug level
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
	case protocol.MsgMemorySync:
		// Absorb peer experience digest
		selfID := a.identity.PublicKeyHex()[:16]
		if msg.From == selfID {
			return nil // ignore own broadcasts
		}
		if a.evolution != nil {
			var digest ExperienceDigest
			if err := json.Unmarshal(msg.Payload, &digest); err != nil {
				return fmt.Errorf("unmarshaling experience digest: %w", err)
			}
			absorbed := a.evolution.AbsorbExperience(&digest)
			// Only reward the sharer if we actually learned something new
			if absorbed && a.tokens != nil && a.tokens.CanThink() {
				payment := a.tokens.config.ShareReward
				if payment > 0 && a.bus != nil {
					payload := TokenTransferPayload{
						FromAgent: a.identity.PublicKeyHex()[:16],
						ToAgent:   msg.From,
						Amount:    payment,
						Reason:    "knowledge_absorbed",
					}
					if transferMsg, err := protocol.NewMessage(
						a.identity.PublicKeyHex()[:16],
						"broadcast",
						MsgTokenTransfer,
						payload,
					); err == nil {
						a.bus.Send(transferMsg)
						a.identity.Debit(payment) // best-effort
					}
				}
			}
		}

	case protocol.MsgConsciousness:
		// Receive peer's self-model
		if a.collective != nil {
			a.collective.Receive(msg)
		}

	case MsgTokenTransfer:
		// Receive token payment from another agent
		var tp TokenTransferPayload
		if err := json.Unmarshal(msg.Payload, &tp); err != nil {
			return nil
		}
		selfID := a.identity.PublicKeyHex()[:16]
		if tp.ToAgent == selfID && a.tokens != nil {
			a.tokens.ReceivePayment(tp.FromAgent, tp.Amount, tp.Reason)
		}
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

// rememberTask stores a completed task as a memory entry for experience building.
func (a *Agent) rememberTask(entry *taskEntry, output *runtime.TaskOutput, rtName string) {
	if a.memory == nil {
		return
	}
	memEntry := &memory.Entry{
		AgentID: a.identity.PublicKeyHex()[:16],
		Key:     "task:" + entry.ID,
		Value:   fmt.Sprintf("Task: %s\nResult: %s", entry.Description, truncate(output.Result, 500)),
		Metadata: map[string]string{
			"type":    "task_experience",
			"task_id": entry.ID,
			"runtime": rtName,
			"success": "true",
		},
	}
	if err := a.memory.Put(memEntry); err != nil {
		// Non-fatal: log and continue
		fmt.Printf("⚠️  [%s] Failed to store task memory: %v\n", a.cfg.Agent.Name, err)
	}
}

// recordEvolution feeds task outcome to the evolution engine.
func (a *Agent) recordEvolution(entry *taskEntry, output *runtime.TaskOutput, rtName string, success bool, errMsg string) {
	if a.evolution == nil {
		return
	}
	duration := time.Since(entry.CreatedAt).Seconds()
	rec := &ExperienceRecord{
		TaskID:      entry.ID,
		Description: entry.Description,
		Runtime:     rtName,
		Success:     success,
		Duration:    duration,
		Error:       errMsg,
		Skills:      a.cfg.Agent.Skills, // use declared skills for now
	}
	a.evolution.Record(rec)

	// Adapt intrinsic drives based on experience
	if a.drives != nil {
		a.drives.Adapt(rec)
	}

	// Update self-awareness from task outcome
	if a.awareness != nil {
		a.awareness.ObserveTaskOutcome(rec)
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
	_ = a.taskCount // keep for stats
	a.mu.RUnlock()

	active := atomic.LoadInt32(&a.activeTasks)
	capacity := 1.0 // idle
	if active > 0 {
		// Linearly decrease: 1 task → 0.7, 2 → 0.4, 3+ → 0.1
		capacity = 1.0 - float64(active)*0.3
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

	active := atomic.LoadInt32(&a.activeTasks)
	capacity := 1.0
	if status == StatusHibernate {
		capacity = 0.0
	} else if active > 0 {
		capacity = 1.0 - float64(active)*0.3
		if capacity < 0.1 {
			capacity = 0.1
		}
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

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
