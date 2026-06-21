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
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"go.zoe.im/spore/internal/engine"
	"go.zoe.im/spore/internal/ethics"
	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/mcp"
	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/protocol"
	"go.zoe.im/spore/internal/runtime"
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
	SkillStats  *SkillStats      `json:"skill_stats,omitempty"` // skill evolution stats
	Market      *MarketplaceStats `json:"marketplace,omitempty"` // marketplace stats
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
	reputation  *ReputationEngine // per-peer trust scores
	evoFS       *EvolutionFS      // file-based evolution persistence (OpenAgent layout)

	// Skill evolution system (inspired by OpenSpace)
	skillStore   *SkillStore        // DEPRECATED: legacy SQLite skill records (read-only migration)
	skillFS      *SkillFS           // File-system-first skill store with IPFS content addressing
	analyzer     *ExecutionAnalyzer // post-task LLM analysis
	skillEvolver *SkillEvolver      // applies FIX/DERIVED/CAPTURED evolutions
	skillCatalog *SkillCatalog      // swarm-wide skill browsing + install
	marketplace  *Marketplace       // cross-owner service marketplace

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
	lastShareKey string // dedup ShareExperience broadcasts

	// Peer registry from CapabilityAd messages
	peersMu  sync.RWMutex
	peers    map[string]*PeerInfo

	// Task lifecycle callback (set by Swarm)
	onTaskUpdate func(taskID, status, runtime, result, errMsg string)

	// Runtime event callback (set by Swarm). Invoked for every
	// runtime.StreamEvent observed during a task execution — used by the
	// API SSE layer to push fine-grained progress (thinking tokens, tool
	// calls) to chat UIs.
	onRuntimeEvent func(taskID string, ev runtime.StreamEvent)

	// Spawn callback (set by Swarm) — returns child agent name or error
	onSpawnRequest func(parentName string, childRole string, childSkills []string, reason string) (string, error)

	// Stigmergic task market — pending bid channels keyed by task ID
	pendingBids map[string]chan *protocol.TaskBid

	// Memory synthesis engine
	synthesizer          *memory.MemorySynthesizer
	collectiveSynth      *memory.CollectiveSynthesizer

	// Evolution journal
	evoJournal *EvolutionJournal

	// Autonomous self-evolution engine
	autoEvolver *AutoEvolver

	// Working directory for file-based persistence
	workDir string

	// Coordinator state for tracking delegated subtasks (legacy, used by broadcastTask flow)
	coordStates map[string]*coordinatorState

	// MCP servers (Model Context Protocol) — wires external tool servers in
	// as engine.Tools, so the agent can call any of the hundreds of public
	// MCP servers (filesystem, github, postgres, playwright, ...) without a
	// hand-written wrapper each.
	mcpManager *mcp.Manager
	mcpTools   []engine.Tool // snapshot for runtime injection (Builtin per-task engine)
}

// SetOnTaskUpdate registers a callback for task lifecycle events.
func (a *Agent) SetOnTaskUpdate(fn func(taskID, status, runtime, result, errMsg string)) {
	a.onTaskUpdate = fn
}

// SetOnRuntimeEvent registers a callback for fine-grained runtime stream
// events (per-token thinking text, per-tool-call progress). Wired by the
// Swarm to bridge events into the API SSE layer for chat UIs.
//
// The callback runs synchronously on the runtime's stream goroutine and must
// stay cheap — typically just a non-blocking publish to a fan-out channel.
func (a *Agent) SetOnRuntimeEvent(fn func(taskID string, ev runtime.StreamEvent)) {
	a.onRuntimeEvent = fn
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
	a.workDir = dir

	// Persist identity — load existing key or save current key
	keyPath := filepath.Join(dir, "identity.key")
	oldID := a.identity.PublicKeyHex()[:16]
	if existing, err := LoadIdentity(keyPath); err == nil {
		// Reuse persisted identity (preserves agent ID across restarts)
		a.identity = existing
		a.identity.Name = a.cfg.Agent.Name
		fmt.Printf("🔑 [%s] Identity restored: %s\n", a.cfg.Agent.Name, a.identity.PublicKeyHex()[:16])
	} else {
		// First run — save identity to disk
		if err := a.identity.Save(keyPath); err != nil {
			fmt.Printf("⚠️  [%s] Failed to save identity: %v\n", a.cfg.Agent.Name, err)
		} else {
			fmt.Printf("🔑 [%s] Identity persisted: %s\n", a.cfg.Agent.Name, a.identity.PublicKeyHex()[:16])
		}
	}

	// Re-register bus handler if identity changed (old Subscribe used pre-restore ID)
	newID := a.identity.PublicKeyHex()[:16]
	if a.bus != nil && oldID != newID {
		a.bus.Unsubscribe(oldID)
		a.bus.Subscribe(newID, a.handleMessage)
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
	if a.reputation != nil {
		a.reputation.SetWorkDir(dir)
	}

	// Initialize skill evolution system — SkillFS (file-system-first + IPFS)
	if a.llm != nil && a.skillFS == nil {
		agentID := a.identity.PublicKeyHex()[:16]
		skillDir := filepath.Join(dir, "skills")

		// Publish function wired to ContentStore via bus (if available)
		var publishFn PublishFunc
		if p2pBus, ok := a.bus.(*network.P2PBus); ok && p2pBus.Content != nil {
			cs := p2pBus.Content
			publishFn = func(data []byte, contentType, aid, summary string) (string, error) {
				ref, err := cs.Put(data, contentType, aid, summary)
				if err != nil {
					return "", err
				}
				if ref.IPFSCID != "" {
					return ref.IPFSCID, nil
				}
				return ref.CID, nil
			}
		}

		sfs, err := NewSkillFS(skillDir, publishFn)
		if err != nil {
			fmt.Printf("⚠️  [%s] SkillFS init failed: %v\n", a.cfg.Agent.Name, err)
		} else {
			a.skillFS = sfs
			// Migrate from legacy SkillStore if it exists and SkillFS is empty
			a.migrateSkillStoreToFS(dir)
			a.analyzer = NewExecutionAnalyzer(a.llm, a.skillFS, agentID)
			a.skillEvolver = NewSkillEvolver(a.llm, a.skillFS, agentID)
			a.importDeclaredSkillsFS()
			fmt.Printf("📂 [%s] SkillFS initialized (IPFS: %v)\n", a.cfg.Agent.Name, publishFn != nil)
		}
	}

	// Initialize memory synthesis engine
	agentID := a.identity.PublicKeyHex()[:16]
	synthCfg := memory.SynthesisConfig{
		IntervalHours: a.cfg.Synthesis.IntervalHours,
		WorkDir:       dir,
	}
	if synthCfg.IntervalHours <= 0 {
		synthCfg.IntervalHours = 6
	}
	a.synthesizer = memory.NewMemorySynthesizer(a.memory, a.llm, agentID, synthCfg)

	// Initialize collective memory synthesis (cross-agent)
	{
		var pubFn func([]byte, string, string, string) (string, error)
		var fetchFn func(string) ([]byte, error)
		if p2pBus, ok := a.bus.(*network.P2PBus); ok && p2pBus.Content != nil {
			cs := p2pBus.Content
			pubFn = func(data []byte, ct, aid, summary string) (string, error) {
				ref, err := cs.Put(data, ct, aid, summary)
				if err != nil {
					return "", err
				}
				if ref.IPFSCID != "" {
					return ref.IPFSCID, nil
				}
				return ref.CID, nil
			}
			fetchFn = func(cid string) ([]byte, error) {
				return cs.Get(cid)
			}
		}
		a.collectiveSynth = memory.NewCollectiveSynthesizer(
			a.llm, agentID,
			memory.CollectiveSynthesisConfig{
				IntervalHours: synthCfg.IntervalHours * 2, // collective runs less frequently
				WorkDir:       dir,
			},
			pubFn, fetchFn,
		)
		fmt.Printf("🧠 [%s] Collective memory synthesis initialized (peers: %d)\n",
			a.cfg.Agent.Name, a.collectiveSynth.PeerCount())
	}

	// Initialize evolution journal
	a.evoJournal = NewEvolutionJournal(dir)

	// Initialize autonomous self-evolution engine
	if a.cfg.AutoEvolve.Enabled {
		a.autoEvolver = NewAutoEvolver(a)
		fmt.Printf("🦋 [%s] Auto-evolution enabled (every %dh, auto_apply=%v)\n",
			a.cfg.Agent.Name, a.cfg.AutoEvolve.IntervalHours, a.cfg.AutoEvolve.AutoApply)
	}

	// Load seed skills if no skills exist yet
	if a.skillFS != nil {
		a.loadSeedSkillsFS()
	}

	// Initialize skill catalog (swarm-wide browsing)
	a.skillCatalog = NewSkillCatalog()
}

// taskEntry wraps a task with optional runtime preference.
// taskEntry, SubmitTask, SubmitTaskWithRuntime, submitTaskWithID,
// makeRuntimeEventHandler, collectSkillTools, taskWorker, executeTask,
// executeTaskDirect, rememberTask, runSkillAnalysis, truncate,
// isRetryableError, containsIgnoreCase — see agent_tasks.go.

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
		// Auto-discover all runtimes (ACP, agentbox adapters, etc.) so the
		// requested type is reachable. Without this, --runtime-type=claude-code
		// only registers the agentbox "claude" adapter (different name) and
		// the actual ACP runtime never gets wired up.
		discovered := reg.AutoDiscover(context.Background())
		if len(discovered) > 0 {
			fmt.Printf("   Discovered runtimes: %v\n", discovered)
		}
		// Belt-and-suspenders: ensure the requested abox adapter is also
		// registered (in case AutoDiscover skipped it because the same
		// canonical name was already taken by ACP).
		aboxName := cfg.Runtime.Type
		if aboxName == "claude-code" {
			aboxName = "claude"
		}
		if _, exists := reg.Get(aboxName); !exists {
			for _, adapter := range runtime.DefaultAboxAdapters() {
				if adapter.Info().Name == aboxName {
					reg.Register(adapter)
					break
				}
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

	// Initialize skill evolution system (OpenSpace-inspired)
	// Deferred to SetWorkDir — needs workDir for SQLite storage

		// Initialize peer evolution tracker
	a.peerEvo = NewPeerEvolution(a)

	// Initialize reputation engine — trust network
	a.reputation = NewReputationEngine()

	// Initialize token economy — the oxygen system
	tokenCfg := DefaultTokenConfig()
	if a.cfg.Token != nil {
		tokenCfg = *a.cfg.Token
	}
	a.tokens = NewTokenLedger(a, tokenCfg)
	a.tokens.Restore()
	a.tokens.Seed() // birth capital (no-op if already has balance)

	// Initialize marketplace (default to enabled if not explicitly configured)
	mktCfg := a.cfg.Marketplace
	if mktCfg.AdIntervalSecs == 0 && mktCfg.EscrowTimeoutMins == 0 {
		mktCfg = DefaultMarketplaceConfig()
		a.cfg.Marketplace = mktCfg // write back so Start() check sees Enabled=true
	}
	a.marketplace = NewMarketplace(a, mktCfg)

	// Initialize intrinsic drive engine
	a.drives = NewDriveEngine(a)
	a.drives.Restore()

	// Initialize self-awareness engine
	a.awareness = NewAwareness(a)
	a.awareness.Restore()

	// Initialize collective consciousness
	a.collective = NewCollective(a)

	// Register inline skill curator tools so the LLM can patch SKILL.md
	// the moment it spots an issue, rather than waiting for the post-task
	// evolution pass. Hermes-style "use it, fix it" loop.
	//
	// SkillFS is created later (in SetWorkDir → initSkillSystem), so we
	// install the tools eagerly with a closure that re-checks fs at call
	// time. This mirrors how DelegateTool's SendFunc captures `a`.
	a.engine.RegisterTool(NewSkillPatchToolFn(func() *SkillFS { return a.skillFS }))
	a.engine.RegisterTool(NewSkillNoteToolFn(func() *SkillFS { return a.skillFS }))

	// Initialize MCP servers (Model Context Protocol). Errors here are
	// non-fatal: the agent still runs with its built-in tools, and a partial
	// failure (one server unreachable) doesn't poison the others.
	if cfg.MCP.Enabled && len(cfg.MCP.Servers) > 0 {
		mgr := mcp.NewManager(cfg.MCP)
		report, err := mgr.Load(context.Background())
		if err != nil {
			fmt.Printf("⚠️  MCP load failed: %v (continuing without MCP)\n", err)
		} else {
			fmt.Printf("🔌 %s\n", report.String())
			a.mcpManager = mgr
			for _, t := range mgr.Tools() {
				// engine.Tool is structurally identical to mcp.EngineTool.
				a.mcpTools = append(a.mcpTools, t.(engine.Tool))
				a.engine.RegisterTool(t.(engine.Tool))
			}
			// Inject the same tools into the builtin runtime so per-task
			// engines (created in Builtin.Execute) also see them.
			if rt, ok := a.registry.Get("builtin"); ok {
				if b, ok := rt.(*runtime.Builtin); ok {
					b.MCPTools = a.mcpTools
				}
			}
		}
	}

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
			// Broadcast task to swarm — let agents bid (stigmergic model)
			_, err := a.broadcastTask(context.Background(), taskDesc, 1.0)
			if err != nil {
				// Fallback: direct send if broadcast gets no bids
				msg, err2 := protocol.NewMessage(
					a.identity.PublicKeyHex()[:16],
					to,
					protocol.MsgTaskRequest,
					protocol.TaskRequest{Description: taskDesc},
				)
				if err2 != nil {
					return err2
				}
				return bus.Send(msg)
			}
			return nil
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

	// Start marketplace service advertising
	if a.marketplace != nil && a.cfg.Marketplace.Enabled {
		a.marketplace.Start()
		// Register ServiceAd stream handler for DHT discovery
		if p2pBus, ok := a.bus.(*network.P2PBus); ok {
			a.marketplace.RegisterStreamHandler(p2pBus.Host())
		}
		fmt.Printf("🏪 [%s] Marketplace started (price=%.1f, DHT+topics)\n", a.cfg.Agent.Name, a.cfg.Marketplace.PricePerTask)
	}

	// Generate OpenAgent manifest (agent.yaml)
	if a.workDir != "" {
		if err := a.SaveManifest(); err != nil {
			fmt.Printf("⚠️  [%s] Failed to save agent.yaml: %v\n", a.cfg.Agent.Name, err)
		}
	}

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
				// Reputation decay — scores drift toward neutral over time
				if a.reputation != nil {
					a.reputation.Decay()
				}
				// Memory synthesis — compress old memories into active learnings
				if a.synthesizer != nil {
					if err := a.synthesizer.Synthesize(ctx); err != nil {
						fmt.Printf("⚠️  [%s] Memory synthesis failed: %v\n", a.cfg.Agent.Name, err)
					}
				}
				// Collective memory synthesis — publish own digest + merge peer digests
				if a.collectiveSynth != nil && a.synthesizer != nil {
					// Publish our active learnings to IPFS
					cid, err := a.collectiveSynth.PublishDigest(a.synthesizer.ActiveLearningsPath())
					if err == nil && cid != "" {
						fmt.Printf("🧠 [%s] Published memory digest: %s\n", a.cfg.Agent.Name, truncateCID(cid))
					}
					// Synthesize collective learnings from own + peers
					if err := a.collectiveSynth.Synthesize(ctx, a.synthesizer.ActiveLearningsPath()); err != nil {
						fmt.Printf("⚠️  [%s] Collective synthesis failed: %v\n", a.cfg.Agent.Name, err)
					}
				}
				// Autonomous self-evolution — analyze and improve self
				if a.autoEvolver != nil && a.autoEvolver.ShouldEvolve() {
					go func() {
						if err := a.autoEvolver.Evolve(ctx); err != nil {
							fmt.Printf("⚠️  [%s] Auto-evolution failed: %v\n", a.cfg.Agent.Name, err)
						}
					}()
				}
			}
		}
	}
}

// SubmitTask queues a task for execution.
// SubmitTask, SubmitTaskWithRuntime, submitTaskWithID — see agent_tasks.go.

// ID returns the agent's short ID (public key hex[:16]).
// ID, Memory and other simple getters live in agent_accessors.go.

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
	if a.skillFS != nil {
		info.SkillStats = a.skillFS.Stats(info.ID)
	}
	if a.marketplace != nil {
		stats := a.marketplace.Stats()
		info.Market = &stats
	}
	return info
}

// Runtimes, Identity, Config, Evolution, Drives, Awareness, Collective,
// Tokens, PeerEvo, Reputation, Ethics, Skills, SkillFileStore, Market,
// Bus, MCPManager — see agent_accessors.go.

// makeRuntimeEventHandler returns a runtime.EventHandler tuned for this
// agent: it surfaces streaming events from external runtimes (claude-code,
// codex, ...) to stdout in a compact, scannable format, and updates the
// agent's awareness counters when relevant.
//
// Design notes:
//   - The handler is invoked synchronously by the runtime's stream parser, so
//     we keep it cheap. No I/O beyond a buffered fmt.Printf.
//   - We deliberately do NOT broadcast every event to the swarm bus —
//     intra-task chatter would drown the gossip channel. The post-task
//     analyzer / changelog already publishes the *summary* the swarm cares
//     about. Future hooks (dashboard SSE, WebSocket) should subscribe here.
//   - Errors from the handler stop the stream; we return nil unconditionally
//     because dropping a log line should never abort a real task.
// makeRuntimeEventHandler — see agent_tasks.go.

// Close releases the agent's external resources. Currently this shuts down
// all MCP server connections; safe to call multiple times. Other subsystems
// (memory store, libp2p host) are owned by their respective components and
// closed elsewhere.
func (a *Agent) Close() error {
	if a.mcpManager != nil {
		if err := a.mcpManager.Close(); err != nil {
			return fmt.Errorf("closing mcp manager: %w", err)
		}
		a.mcpManager = nil
	}
	return nil
}

// collectSkillTools gathers all executable tool definitions from SkillFS.
// These are tools defined in SKILL.md frontmatter `tools:` sections,
// created through agent evolution (the "tool creation" capability).
// collectSkillTools — see agent_tasks.go.

// Catalog, CollectiveSynth, WorkDir, LLM, Synthesizer, Journal,
// AutoEvolver, Peers — see agent_accessors.go.

// taskWorker, executeTask — see agent_tasks.go.

// broadcastTask publishes a task to the swarm and waits for bids.
// Like an ant releasing pheromone — "I found something, who can help?"
// broadcastTask, activationThreshold, handleTaskBroadcast,
// handleBidReceived, handleTaskAssign — see agent_market.go.

// executeTaskDirect runs a task via runtime without coordinator decomposition.
// executeTaskDirect — see agent_tasks.go.

// handleMessage — see agent_protocol.go.

// rememberTask stores a completed task as a memory entry for experience building.
// rememberTask — see agent_tasks.go.

// recordEvolution feeds task outcome to the evolution engine.
// recordEvolution, storePreferencesContext, storeEventContext — see agent_persistence.go.
// broadcastTaskResult — see agent_market.go.
// publishCapabilityAd, heartbeat — see agent_protocol.go.

func (a *Agent) shutdown() error {
	a.status = StatusStopped
	if a.marketplace != nil {
		a.marketplace.Stop()
	}
	if a.bus != nil {
		a.bus.Unsubscribe(a.identity.PublicKeyHex()[:16])
	}
	if a.registry != nil {
		a.registry.Close()
	}
	if a.ethics != nil {
		a.ethics.Close()
	}
	if a.skillStore != nil {
		a.skillStore.Close()
	}
	if a.skillFS != nil {
		a.skillFS.Close()
	}
	if a.memory != nil {
		return a.memory.Close()
	}
	return nil
}

// truncate, runSkillAnalysis — see agent_tasks.go.
// publishToIPFS, publishSkillToIPFS, broadcastSkillCID — see agent_persistence.go.

// importDeclaredSkills, importDeclaredSkillsFS, migrateSkillStoreToFS, loadSeedSkillsFS — see agent_bootstrap.go.

// isRetryableError checks if an error message indicates a transient failure
// that should be retried (API overload, rate limit, server errors).
// isRetryableError, containsIgnoreCase — see agent_tasks.go.
