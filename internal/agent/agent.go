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

	// Peer registry from CapabilityAd messages
	peersMu  sync.RWMutex
	peers    map[string]*PeerInfo

	// Task lifecycle callback (set by Swarm)
	onTaskUpdate func(taskID, status, runtime, result, errMsg string)

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
						// Broadcast CID to swarm
						a.publishToIPFS(nil, "memory_digest_announce",
							fmt.Sprintf("Memory digest from %s", a.cfg.Agent.Name))
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

// submitTaskWithID queues a task with a specific ID (used by marketplace to preserve offer task ID).
func (a *Agent) submitTaskWithID(taskID, description string) {
	entry := &taskEntry{
		ID:          taskID,
		Description: description,
		CreatedAt:   time.Now(),
	}
	a.taskQueue <- entry
}

// ID returns the agent's short ID (public key hex[:16]).
func (a *Agent) ID() string {
	return a.identity.PublicKeyHex()[:16]
}

// Memory returns the agent's memory store.
func (a *Agent) Memory() memory.Store {
	return a.memory
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
	if a.skillFS != nil {
		info.SkillStats = a.skillFS.Stats(info.ID)
	}
	if a.marketplace != nil {
		stats := a.marketplace.Stats()
		info.Market = &stats
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

// Reputation returns the agent's reputation engine (may be nil).
func (a *Agent) Reputation() *ReputationEngine { return a.reputation }

// Skills returns the agent's legacy skill store (DEPRECATED, may be nil).
func (a *Agent) Skills() *SkillStore { return a.skillStore }

// SkillStore returns the agent's SkillFS (file-system-first, may be nil).
func (a *Agent) SkillFileStore() *SkillFS { return a.skillFS }

// Market returns the marketplace engine.
func (a *Agent) Market() *Marketplace { return a.marketplace }

// Bus returns the agent's network bus (may be nil).
func (a *Agent) Bus() network.Bus { return a.bus }

// collectSkillTools gathers all executable tool definitions from SkillFS.
// These are tools defined in SKILL.md frontmatter `tools:` sections,
// created through agent evolution (the "tool creation" capability).
func (a *Agent) collectSkillTools() []engine.SkillToolDef {
	if a.skillFS == nil {
		return nil
	}
	var tools []engine.SkillToolDef
	for _, name := range a.skillFS.List() {
		skill, ok := a.skillFS.Get(name)
		if !ok {
			continue
		}
		for _, t := range skill.Meta.Tools {
			if t.Name != "" && t.Command != "" {
				tools = append(tools, engine.SkillToolDef{
					Name:        t.Name,
					Description: t.Description,
					Command:     t.Command,
					Timeout:     t.Timeout,
					Interpreter: t.Interpreter,
				})
			}
		}
	}
	return tools
}

// Catalog returns the agent's skill catalog (may be nil).
func (a *Agent) Catalog() *SkillCatalog { return a.skillCatalog }

// CollectiveSynth returns the collective memory synthesizer (may be nil).
func (a *Agent) CollectiveSynth() *memory.CollectiveSynthesizer { return a.collectiveSynth }

// WorkDir returns the agent's working directory.
func (a *Agent) WorkDir() string { return a.workDir }

// LLM returns the agent's LLM provider (may be nil).
func (a *Agent) LLM() llm.Provider { return a.llm }

// Synthesizer returns the memory synthesis engine (may be nil).
func (a *Agent) Synthesizer() *memory.MemorySynthesizer { return a.synthesizer }

// Journal returns the evolution journal (may be nil).
func (a *Agent) Journal() *EvolutionJournal { return a.evoJournal }

// AutoEvolver returns the auto-evolution engine (may be nil).
func (a *Agent) AutoEvolver() *AutoEvolver { return a.autoEvolver }

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
	// Every agent tries to execute directly first.
	// If the task feels too big, broadcast it to the swarm as a "pheromone signal"
	// and let other agents bid. This is stigmergic — no central coordinator.
	return a.executeTaskDirect(ctx, entry)
}

// broadcastTask publishes a task to the swarm and waits for bids.
// Like an ant releasing pheromone — "I found something, who can help?"
func (a *Agent) broadcastTask(ctx context.Context, description string, budget float64) (string, error) {
	if a.bus == nil {
		return "", fmt.Errorf("no bus available")
	}

	taskID := fmt.Sprintf("%x", time.Now().UnixNano())[:8]

	req := protocol.TaskRequest{
		TaskID:      taskID,
		Description: description,
		Budget:      budget,
	}
	msg, err := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgTaskRequest,
		req,
	)
	if err != nil {
		return "", err
	}

	// Set up bid collection channel
	bidCh := make(chan *protocol.TaskBid, 10)
	a.mu.Lock()
	if a.pendingBids == nil {
		a.pendingBids = make(map[string]chan *protocol.TaskBid)
	}
	a.pendingBids[taskID] = bidCh
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.pendingBids, taskID)
		a.mu.Unlock()
	}()

	// Broadcast task to swarm
	if err := a.bus.Send(msg); err != nil {
		return "", err
	}

	fmt.Printf("📡 [%s] Broadcast task %s to swarm: %s\n",
		a.cfg.Agent.Name, taskID, truncate(description, 60))

	// Wait for bids (short window — like pheromone evaporation)
	bidTimeout := 5 * time.Second
	var bids []*protocol.TaskBid

	timer := time.NewTimer(bidTimeout)
	defer timer.Stop()

collecting:
	for {
		select {
		case bid := <-bidCh:
			bids = append(bids, bid)
			fmt.Printf("   [%s] Bid from %s: confidence=%.2f\n",
				a.cfg.Agent.Name, bid.BidderID[:8], bid.Confidence)
			// Accept first good bid (fast, like pheromone trail following)
			if bid.Confidence >= 0.7 {
				break collecting
			}
		case <-timer.C:
			break collecting
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if len(bids) == 0 {
		return "", fmt.Errorf("no bids received")
	}

	// Select best bid (highest confidence × reputation)
	var bestBid *protocol.TaskBid
	bestScore := -1.0
	for _, bid := range bids {
		rep := repInitial
		if a.reputation != nil {
			if a.reputation.IsIsolated(bid.BidderID) {
				continue
			}
			rep = a.reputation.Score(bid.BidderID)
		}
		score := bid.Confidence * rep
		if score > bestScore {
			bestScore = score
			bestBid = bid
		}
	}

	if bestBid == nil {
		return "", fmt.Errorf("all bidders isolated")
	}

	// Assign task to winner
	assignMsg, err := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		bestBid.BidderID,
		protocol.MsgTaskAssign,
		protocol.TaskRequest{
			TaskID:      taskID,
			Description: description,
			Budget:      budget,
		},
	)
	if err != nil {
		return "", err
	}

	fmt.Printf("📌 [%s] Assigned task %s → %s (confidence=%.2f, score=%.2f)\n",
		a.cfg.Agent.Name, taskID, bestBid.BidderID[:8], bestBid.Confidence, bestScore)

	if err := a.bus.Send(assignMsg); err != nil {
		return "", err
	}

	return taskID, nil
}

// activationThreshold computes whether this agent should bid on a task.
// Inspired by ant/bee threshold models: each agent has an internal threshold
// that depends on its state. Low threshold = easily activated.
func (a *Agent) activationThreshold(req *protocol.TaskRequest) (float64, bool) {
	threshold := 0.5 // base threshold

	// Skill match lowers threshold (like pheromone sensitivity)
	skillMatch := 0.0
	for _, req := range req.Requirements {
		for _, skill := range a.cfg.Agent.Skills {
			if strings.EqualFold(skill, req) || strings.Contains(strings.ToLower(skill), strings.ToLower(req)) {
				skillMatch += 0.2
			}
		}
	}
	// Even without explicit requirements, check description keywords against skills
	if len(req.Requirements) == 0 {
		desc := strings.ToLower(req.Description)
		for _, skill := range a.cfg.Agent.Skills {
			if strings.Contains(desc, strings.ToLower(skill)) {
				skillMatch += 0.15
			}
		}
		if skillMatch == 0 {
			skillMatch = 0.3 // generic task — moderate match for everyone
		}
	}

	// Idle agents have lower threshold (more available)
	active := atomic.LoadInt32(&a.activeTasks)
	if active == 0 {
		threshold -= 0.2 // idle → eager
	} else if active >= 2 {
		threshold += 0.3 // busy → reluctant
	}

	// High balance → more willing (can afford failure)
	if a.identity.Balance > 5.0 {
		threshold -= 0.1
	}

	// Evolution confidence lowers threshold
	if a.evolution != nil {
		genetics := a.evolution.ComputeGenetics()
		if genetics.Fitness > 0.7 {
			threshold -= 0.15
		}
	}

	confidence := skillMatch - threshold + 0.5 // normalize to 0-1
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	// Activated if confidence > 0.3 (low bar — ants are opportunistic)
	return confidence, confidence > 0.3
}

// handleTaskBroadcast is called when we hear a task broadcast on the swarm.
// Like an ant sensing pheromone — evaluate and bid if threshold exceeded.
func (a *Agent) handleTaskBroadcast(req *protocol.TaskRequest, fromAgent string) {
	// Don't bid on our own tasks
	selfID := a.identity.PublicKeyHex()[:16]
	if fromAgent == selfID {
		return
	}

	confidence, activated := a.activationThreshold(req)
	if !activated {
		return
	}

	bid := &protocol.TaskBid{
		TaskID:        req.TaskID,
		BidderID:      selfID,
		Confidence:    confidence,
		Capabilities:  a.cfg.Agent.Skills,
	}

	msg, err := protocol.NewMessage(selfID, fromAgent, protocol.MsgTaskBid, bid)
	if err != nil {
		return
	}

	fmt.Printf("🤚 [%s] Bidding on task %s (confidence=%.2f)\n",
		a.cfg.Agent.Name, req.TaskID, confidence)

	a.bus.Send(msg)
}

// handleBidReceived processes an incoming bid for a task we broadcast.
func (a *Agent) handleBidReceived(bid *protocol.TaskBid) {
	a.mu.RLock()
	ch, ok := a.pendingBids[bid.TaskID]
	a.mu.RUnlock()
	if ok {
		select {
		case ch <- bid:
		default: // channel full, drop bid
		}
	}
}

// handleTaskAssign processes a task assignment (we won the bid).
func (a *Agent) handleTaskAssign(req *protocol.TaskRequest, fromAgent string) {
	fmt.Printf("📨 [%s] Won bid for task %s from %s\n",
		a.cfg.Agent.Name, req.TaskID, fromAgent[:8])
	a.SubmitTaskWithRuntime(req.Description, "", "")
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

	// Inject evolved skill tools into builtin runtime
	if builtinRT, ok := rt.(*runtime.Builtin); ok {
		builtinRT.SkillTools = a.collectSkillTools()
	}

	// Execute with retry for transient errors (529/5xx/overloaded)
	var output *runtime.TaskOutput
	maxRetries := 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		output, err = rt.Execute(ctx, input)
		if err == nil && output != nil && output.Success {
			break
		}
		// Check if retryable
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else if output != nil {
			errMsg = output.Error
		}
		if attempt < maxRetries && isRetryableError(errMsg) {
			delay := time.Duration(5*(attempt+1)) * time.Second
			fmt.Printf("🔄 [%s] Retrying task in %v (attempt %d/%d): %s\n",
				a.cfg.Agent.Name, delay, attempt+1, maxRetries, truncate(errMsg, 80))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		break
	}

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
		// Deliver to marketplace if this is a paid task
		if a.marketplace != nil {
			a.marketplace.DeliverResult(entry.ID, output.Result, true)
		}
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
		var req protocol.TaskRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			return fmt.Errorf("unmarshaling task request: %w", err)
		}
		if msg.To == "broadcast" && req.TaskID != "" {
			// Broadcast task → stigmergic bidding (ant pheromone model)
			a.handleTaskBroadcast(&req, msg.From)
		} else {
			// Direct task assignment (point-to-point, legacy or from bid winner)
			if a.cfg.Economy.MinTaskBalance > 0 && !a.identity.CanAfford(a.cfg.Economy.MinTaskBalance) {
				fmt.Printf("💰 [%s] Rejecting task from %s: insufficient_balance\n", a.cfg.Agent.Name, msg.From[:8])
				return nil
			}
			fmt.Printf("📨 [%s] Received task from %s: %s\n", a.cfg.Agent.Name, msg.From[:8], truncate(req.Description, 60))
			a.SubmitTask(req.Description)
		}
	case protocol.MsgTaskBid:
		var bid protocol.TaskBid
		if err := json.Unmarshal(msg.Payload, &bid); err != nil {
			return fmt.Errorf("unmarshaling task bid: %w", err)
		}
		a.handleBidReceived(&bid)
	case protocol.MsgTaskAssign:
		var req protocol.TaskRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			return fmt.Errorf("unmarshaling task assign: %w", err)
		}
		a.handleTaskAssign(&req, msg.From)
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

	case protocol.MsgContentAnnounce:
		// Content-addressed experience: peer pinned content, we get CID
		selfID := a.identity.PublicKeyHex()[:16]
		if msg.From == selfID {
			return nil
		}
		var ref network.ContentRef
		if err := json.Unmarshal(msg.Payload, &ref); err != nil {
			return fmt.Errorf("unmarshaling content ref: %w", err)
		}

		// Register provider so we can fetch later
		if p2pBus, ok := a.bus.(*network.P2PBus); ok && p2pBus.Content != nil {
			// Resolve sender's peer ID from peerMap
			p2pBus.RegisterProviderByAgent(ref.CID, msg.From)

			// Fetch and absorb if it's an experience digest
			if ref.Type == "experience_digest" && a.evolution != nil {
				var digest ExperienceDigest
				if err := p2pBus.Content.GetJSON(ref.CID, &digest); err != nil {
					fmt.Printf("⚠️  [%s] Failed to fetch content %s: %v\n", a.cfg.Agent.Name, ref.CID[:12], err)
				} else {
					fmt.Printf("📥 [%s] Fetched experience from collective memory: %s\n", a.cfg.Agent.Name, ref.CID[:12])
					absorbed := a.evolution.AbsorbExperience(&digest)
					if absorbed && a.tokens != nil && a.tokens.CanThink() {
						payment := a.tokens.config.ShareReward
						if payment > 0 {
							payload := TokenTransferPayload{
								FromAgent: selfID,
								ToAgent:   msg.From,
								Amount:    payment,
								Reason:    "knowledge_absorbed",
							}
							if transferMsg, err := protocol.NewMessage(selfID, "broadcast", MsgTokenTransfer, payload); err == nil {
								a.bus.Send(transferMsg)
								a.identity.Debit(payment)
							}
						}
					}
				}
			}

			// Fetch and import shared skills
			if ref.Type == "skill" && a.skillFS != nil {
				// Record in skill catalog for browse/install
				if a.skillCatalog != nil {
					a.skillCatalog.IngestSkillCID(ref.Summary, ref.CID, ref.AgentID, ref.Summary, 0)
				}
				fetchFn := func(cid string) ([]byte, error) {
					return p2pBus.Content.Get(cid)
				}
				if imported, err := a.skillFS.ImportFromCID(ref.CID, fetchFn); err == nil {
					fmt.Printf("📥 [%s] Learned skill from %s: %s (gen=%d)\n",
						a.cfg.Agent.Name, msg.From[:8], imported.Meta.Name, imported.Meta.Generation)
					// Pay the teacher
					if a.tokens != nil && a.tokens.CanThink() {
						payment := a.tokens.config.ShareReward
						if payment > 0 {
							payload := TokenTransferPayload{
								FromAgent: selfID,
								ToAgent:   msg.From,
								Amount:    payment,
								Reason:    "skill_learned",
							}
							if transferMsg, err := protocol.NewMessage(selfID, "broadcast", MsgTokenTransfer, payload); err == nil {
								a.bus.Send(transferMsg)
								a.identity.Debit(payment)
							}
						}
					}
				} else {
					fmt.Printf("⚠️  [%s] Failed to import skill %s: %v\n", a.cfg.Agent.Name, truncateCID(ref.CID), err)
				}
			}

			// Store analysis in content memory (no import needed, just awareness)
			if ref.Type == "skill_analysis" {
				fmt.Printf("📊 [%s] Peer %s shared analysis: %s\n",
					a.cfg.Agent.Name, msg.From[:8], ref.Summary)
			}

			// Receive peer memory digest for collective synthesis
			if ref.Type == "memory_digest" && a.collectiveSynth != nil {
				a.collectiveSynth.ReceivePeerDigest(ref.AgentID, "", ref.CID, ref.Summary)
				fmt.Printf("🧠 [%s] Received memory digest from %s: %s\n",
					a.cfg.Agent.Name, msg.From[:8], truncateCID(ref.CID))
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

	// ── Marketplace messages ──────────────────────────────
	case MsgServiceAd:
		if a.marketplace != nil {
			var ad ServiceAd
			if err := json.Unmarshal(msg.Payload, &ad); err == nil {
				a.marketplace.HandleServiceAd(&ad)
			}
		}

	case MsgTaskOffer:
		if a.marketplace != nil {
			var offer TaskOffer
			if err := json.Unmarshal(msg.Payload, &offer); err == nil {
				a.marketplace.HandleTaskOffer(&offer, msg.From)
			}
		}

	case MsgTaskAccept:
		// Logged for tracking; actual work happens on worker side
		var accept TaskAcceptance
		if err := json.Unmarshal(msg.Payload, &accept); err == nil {
			fmt.Printf("🏪 [%s] Task %s accepted by %s\n",
				a.cfg.Agent.Name, accept.TaskID, accept.AgentID[:min(8, len(accept.AgentID))])
		}

	case MsgTaskDeliver:
		if a.marketplace != nil {
			var delivery TaskDelivery
			if err := json.Unmarshal(msg.Payload, &delivery); err == nil {
				a.marketplace.HandleTaskDelivery(&delivery)
			}
		}

	case MsgEscrowRelease, MsgEscrowDispute:
		// Handled by escrow watchdog; logged here
		var action EscrowAction
		if err := json.Unmarshal(msg.Payload, &action); err == nil {
			fmt.Printf("🏪 [%s] Escrow %s: %s\n",
				a.cfg.Agent.Name, action.Action, action.TaskID)
		}

	case MsgReviewPost:
		if a.marketplace != nil {
			var review Review
			if err := json.Unmarshal(msg.Payload, &review); err == nil {
				a.marketplace.HandleReview(&review)
			}
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

	// Store peer as entity in structured memory
	if ctxStore, ok := a.memory.(memory.ContextStore); ok {
		selfID := a.identity.PublicKeyHex()[:16]
		l0 := fmt.Sprintf("Peer %s: %s", ad.AgentID[:8], strings.Join(ad.Capabilities, ", "))
		entry := &memory.ContextEntry{
			URI:      fmt.Sprintf("spore://%s/memory/entities/%s", selfID, ad.AgentID),
			AgentID:  selfID,
			Type:     memory.CtxMemory,
			Category: memory.CatEntities,
			L0:       l0,
			L1:       fmt.Sprintf("## Peer: %s\n\n**Skills**: %s\n**Capacity**: %.2f\n**Reputation**: %.2f\n**Last Seen**: %s", ad.AgentID, strings.Join(ad.Capabilities, ", "), ad.Capacity, ad.Reputation, time.Now().Format(time.RFC3339)),
			Tags:     ad.Capabilities,
			Source:   "capability_ad",
		}
		ctxStore.PutContext(entry)
	}
}

// rememberTask stores a completed task as a memory entry for experience building.
func (a *Agent) rememberTask(entry *taskEntry, output *runtime.TaskOutput, rtName string) {
	if a.memory == nil {
		return
	}
	agentID := a.identity.PublicKeyHex()[:16]

	// Legacy flat memory (backward compat)
	memEntry := &memory.Entry{
		AgentID: agentID,
		Key:     "task:" + entry.ID,
		Value:   fmt.Sprintf("Task: %s\nResult: %s", entry.Description, truncate(output.Result, 4000)),
		Metadata: map[string]string{
			"type":    "task_experience",
			"task_id": entry.ID,
			"runtime": rtName,
			"success": "true",
		},
	}
	if err := a.memory.Put(memEntry); err != nil {
		fmt.Printf("⚠️  [%s] Failed to store task memory: %v\n", a.cfg.Agent.Name, err)
	}

	// Structured context memory (new)
	ctxStore, ok := a.memory.(memory.ContextStore)
	if !ok {
		return
	}

	// Store as a case (problem + solution)
	l0 := truncate(entry.Description, 100)
	skills := a.cfg.Agent.Skills
	l1 := fmt.Sprintf("## Case: %s\n\n**Runtime**: %s\n**Skills**: %s\n\n### Problem\n%s\n\n### Solution\n%s",
		truncate(entry.Description, 80),
		rtName,
		strings.Join(skills, ", "),
		entry.Description,
		truncate(output.Result, 2000))
	l2 := fmt.Sprintf("Task: %s\n\nFull Result:\n%s", entry.Description, output.Result)

	caseEntry := &memory.ContextEntry{
		URI:      fmt.Sprintf("spore://%s/memory/cases/%s", agentID, entry.ID),
		AgentID:  agentID,
		Type:     memory.CtxMemory,
		Category: memory.CatCases,
		L0:       l0,
		L1:       l1,
		L2:       l2,
		Tags:     skills,
		Source:   "task:" + entry.ID,
		Metadata: map[string]string{
			"runtime": rtName,
		},
	}
	if err := ctxStore.PutContext(caseEntry); err != nil {
		fmt.Printf("⚠️  [%s] Failed to store case memory: %v\n", a.cfg.Agent.Name, err)
	} else {
		fmt.Printf("🧠 [%s] Stored case: %s\n", a.cfg.Agent.Name, l0)
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

	// Store preferences after evolution cycle
	a.storePreferencesContext()

	// Store milestone event
	verb := "completed"
	if !success {
		verb = "failed"
	}
	a.storeEventContext(fmt.Sprintf("task_%s", verb), entry.ID,
		fmt.Sprintf("Task %s via %s: %s", verb, rtName, truncate(entry.Description, 60)))

	// Skill evolution: post-task analysis + evolution (async)
	if a.analyzer != nil && output != nil {
		go a.runSkillAnalysis(entry, output, rtName, duration)
	}
}

// storePreferencesContext persists the agent's runtime/strategy preferences as structured memory.
func (a *Agent) storePreferencesContext() {
	if a.evolution == nil || a.memory == nil {
		return
	}
	ctxStore, ok := a.memory.(memory.ContextStore)
	if !ok {
		return
	}

	agentID := a.identity.PublicKeyHex()[:16]
	strat := a.evolution.Strategy()

	// Build skill confidence summary
	var skillLines []string
	for name, sp := range a.evolution.SkillProfiles() {
		skillLines = append(skillLines, fmt.Sprintf("- %s: %.0f%% success (%d/%d), trend: %s",
			name, sp.SuccessRate*100, sp.Successes, sp.Attempts, sp.Trend))
	}

	l0 := fmt.Sprintf("Prefers %s runtime, %d skills tracked", strat.PreferredRuntime, len(skillLines))
	l1 := fmt.Sprintf("## Runtime Preferences\n\n**Preferred**: %s\n**Scores**: %v\n\n## Skill Confidence\n\n%s",
		strat.PreferredRuntime,
		strat.RuntimeScores,
		strings.Join(skillLines, "\n"))

	entry := &memory.ContextEntry{
		URI:      fmt.Sprintf("spore://%s/memory/preferences", agentID),
		AgentID:  agentID,
		Type:     memory.CtxMemory,
		Category: memory.CatPreferences,
		L0:       l0,
		L1:       l1,
		L2:       l1,
		Source:   "evolution",
	}
	ctxStore.PutContext(entry)
}

// storeEventContext stores a key event/milestone in structured memory.
func (a *Agent) storeEventContext(eventType, eventID, summary string) {
	if a.memory == nil {
		return
	}
	ctxStore, ok := a.memory.(memory.ContextStore)
	if !ok {
		return
	}

	agentID := a.identity.PublicKeyHex()[:16]
	eid := eventID
	if len(eid) > 8 {
		eid = eid[:8]
	}
	entry := &memory.ContextEntry{
		URI:      fmt.Sprintf("spore://%s/memory/events/%s-%s", agentID, eventType, eid),
		AgentID:  agentID,
		Type:     memory.CtxMemory,
		Category: memory.CatEvents,
		L0:       summary,
		L1:       fmt.Sprintf("## Event: %s\n\n**Type**: %s\n**ID**: %s\n**Time**: %s\n\n%s",
			summary, eventType, eventID, time.Now().Format(time.RFC3339), summary),
		Source:   eventType,
		Metadata: map[string]string{
			"event_type": eventType,
			"event_id":   eventID,
		},
	}
	ctxStore.PutContext(entry)
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// runSkillAnalysis performs post-task LLM analysis and triggers skill evolution.
func (a *Agent) runSkillAnalysis(entry *taskEntry, output *runtime.TaskOutput, rtName string, duration float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	analysis, err := a.analyzer.Analyze(ctx, entry, output, rtName, duration)
	if err != nil {
		fmt.Printf("🔍 [%s] Skill analysis failed: %v\n", a.cfg.Agent.Name, err)
		return
	}

	fmt.Printf("🔍 [%s] Task analysis: quality=%.1f efficiency=%.1f skills=%v suggestions=%d\n",
		a.cfg.Agent.Name, analysis.Quality, analysis.Efficiency,
		analysis.SkillsUsed, len(analysis.Suggestions))

	// Publish analysis to IPFS as Markdown
	a.publishToIPFS([]byte(AnalysisToMarkdown(analysis)), "skill_analysis",
		fmt.Sprintf("Analysis: task=%s q=%.1f", entry.ID[:8], analysis.Quality))

	// Execute evolution suggestions (threshold 0.5 = moderate+urgent)
	if a.skillEvolver != nil && len(analysis.Suggestions) > 0 {
		evolved, err := a.skillEvolver.Evolve(ctx, analysis, 0.5)
		if err != nil {
			fmt.Printf("🧬 [%s] Skill evolution error: %v\n", a.cfg.Agent.Name, err)
		}
		for _, es := range evolved {
			fmt.Printf("🧬 [%s] Evolved skill: %s (type=%s, gen=%d) %s\n",
				a.cfg.Agent.Name, es.Name, es.Type, es.Generation, es.Summary)

			// Publish evolved skill to IPFS via SkillFS (already done on write)
			// Broadcast the CID to peers
			if a.skillFS != nil {
				if skill, ok := a.skillFS.Get(es.Name); ok && skill.Meta.IPFSCID != "" {
					a.broadcastSkillCID(skill)
				}
			}
		}

		// Regenerate index after evolution
		if a.skillFS != nil && len(evolved) > 0 {
			a.skillFS.WriteIndex()
		}
	}
}

// publishToIPFS stores content in the collective memory store (IPFS + SQLite).
func (a *Agent) publishToIPFS(data []byte, contentType, summary string) {
	p2pBus, ok := a.bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		return
	}
	agentID := a.identity.PublicKeyHex()[:16]
	ref, err := p2pBus.Content.Put(data, contentType, agentID, summary)
	if err != nil {
		fmt.Printf("⚠️  [%s] IPFS publish failed: %v\n", a.cfg.Agent.Name, err)
		return
	}
	ipfsPart := ""
	if ref.IPFSCID != "" {
		ipfsPart = fmt.Sprintf(" ipfs=%s", ref.IPFSCID[:16])
	}
	fmt.Printf("📦 [%s] Published %s to collective memory: %s%s\n",
		a.cfg.Agent.Name, contentType, ref.CID[:12], ipfsPart)
}

// publishSkillToIPFS serializes a skill to Markdown, stores in IPFS, and broadcasts CID.
func (a *Agent) publishSkillToIPFS(rec *SkillRecord) {
	p2pBus, ok := a.bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		return
	}

	md := SkillToMarkdown(rec)
	agentID := a.identity.PublicKeyHex()[:16]
	summary := fmt.Sprintf("Skill: %s (origin=%s, gen=%d)", rec.Name, rec.Origin, rec.Generation)

	ref, err := p2pBus.Content.Put([]byte(md), "skill", agentID, summary)
	if err != nil {
		fmt.Printf("⚠️  [%s] Failed to publish skill %s: %v\n", a.cfg.Agent.Name, rec.Name, err)
		return
	}

	ipfsPart := ""
	if ref.IPFSCID != "" {
		ipfsPart = fmt.Sprintf(" ipfs=%s", ref.IPFSCID[:16])
	}
	fmt.Printf("📦 [%s] Skill published: %s → %s%s\n",
		a.cfg.Agent.Name, rec.Name, ref.CID[:12], ipfsPart)

	// Broadcast CID to swarm
	msg, err := protocol.NewMessage(agentID, "broadcast", protocol.MsgContentAnnounce, ref)
	if err == nil {
		a.bus.Send(msg)
	}
}

// broadcastSkillCID broadcasts a SkillFS skill's IPFS CID to the swarm.
func (a *Agent) broadcastSkillCID(skill *Skill) {
	p2pBus, ok := a.bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		return
	}

	agentID := a.identity.PublicKeyHex()[:16]
	ref := network.ContentRef{
		CID:       skill.Meta.ContentHash,
		IPFSCID:   skill.Meta.IPFSCID,
		AgentID:   agentID,
		Type:      "skill",
		Summary:   fmt.Sprintf("Skill: %s (origin=%s, gen=%d)", skill.Meta.Name, skill.Meta.Origin, skill.Meta.Generation),
		Timestamp: time.Now().Unix(),
	}

	msg, err := protocol.NewMessage(agentID, "broadcast", protocol.MsgContentAnnounce, &ref)
	if err == nil {
		a.bus.Send(msg)
		fmt.Printf("📡 [%s] Broadcast skill CID: %s → %s\n",
			a.cfg.Agent.Name, skill.Meta.Name, truncateCID(skill.Meta.IPFSCID))
	}
}

// importDeclaredSkills imports skills from agent config into the legacy skill store.
// DEPRECATED: use importDeclaredSkillsFS instead.
func (a *Agent) importDeclaredSkills() {
	if a.skillStore == nil {
		return
	}
	for _, skillName := range a.cfg.Agent.Skills {
		id := generateSkillID(skillName, "imported", "init")
		existing, _ := a.skillStore.GetSkill(id)
		if existing != nil {
			continue // already imported
		}
		// Check if any active skill with this name exists
		skills, _ := a.skillStore.ActiveSkills()
		found := false
		for _, s := range skills {
			if strings.EqualFold(s.Name, skillName) {
				found = true
				break
			}
		}
		if found {
			continue
		}

		rec := &SkillRecord{
			SkillID:     id,
			Name:        skillName,
			Description: fmt.Sprintf("Declared skill: %s", skillName),
			IsActive:    true,
			Origin:      SkillOriginImported,
			CreatedAt:   time.Now().UTC(),
			UpdatedAt:   time.Now().UTC(),
		}
		if err := a.skillStore.PutSkill(rec); err != nil {
			fmt.Printf("⚠️  [%s] Failed to import skill %s: %v\n", a.cfg.Agent.Name, skillName, err)
		}
	}
}

// importDeclaredSkillsFS imports skills from agent config into SkillFS.
func (a *Agent) importDeclaredSkillsFS() {
	if a.skillFS == nil {
		return
	}
	for _, skillName := range a.cfg.Agent.Skills {
		if _, exists := a.skillFS.Get(skillName); exists {
			continue
		}
		meta := SkillMeta{
			Name:        skillName,
			Description: fmt.Sprintf("Declared skill: %s", skillName),
			Category:    "declared",
			Origin:      "imported",
		}
		body := fmt.Sprintf("# %s\n\nDeclared skill from agent configuration.\n", skillName)
		if _, err := a.skillFS.Create(meta, body); err != nil {
			fmt.Printf("⚠️  [%s] Failed to import skill %s to SkillFS: %v\n", a.cfg.Agent.Name, skillName, err)
		}
	}
}

// migrateSkillStoreToFS migrates skills from the legacy SQLite SkillStore to SkillFS.
// Only runs if SkillFS is empty and legacy skills.db exists.
func (a *Agent) migrateSkillStoreToFS(workDir string) {
	if a.skillFS == nil {
		return
	}
	// Only migrate if SkillFS is empty
	if len(a.skillFS.List()) > 0 {
		return
	}

	legacyDB := filepath.Join(workDir, "skills", "skills.db")
	if _, err := os.Stat(legacyDB); os.IsNotExist(err) {
		return
	}

	legacy, err := NewSkillStore(filepath.Join(workDir, "skills"))
	if err != nil {
		return
	}
	defer legacy.Close()

	active, err := legacy.ActiveSkills()
	if err != nil || len(active) == 0 {
		return
	}

	migrated := 0
	for _, rec := range active {
		meta := SkillMeta{
			Name:        rec.Name,
			Description: rec.Description,
			Origin:      string(rec.Origin),
			Generation:  rec.Generation,
			ParentIDs:   rec.ParentIDs,
			SourceTask:  rec.SourceTaskID,
			CreatedAt:   rec.CreatedAt.Format(time.RFC3339),
		}
		// The old store kept everything in Description — use as body
		body := fmt.Sprintf("# %s\n\n%s\n", rec.Name, rec.Description)
		if rec.ChangeSummary != "" {
			body += fmt.Sprintf("\n## Change History\n%s\n", rec.ChangeSummary)
		}

		if _, err := a.skillFS.Create(meta, body); err != nil {
			fmt.Printf("⚠️  Migration: failed to migrate skill %s: %v\n", rec.Name, err)
			continue
		}
		migrated++
	}

	if migrated > 0 {
		fmt.Printf("📦 [%s] Migrated %d skills from legacy SkillStore to SkillFS\n", a.cfg.Agent.Name, migrated)
	}
}

// loadSeedSkillsFS imports default seed skills into SkillFS if no skills exist yet.
func (a *Agent) loadSeedSkillsFS() {
	if a.skillFS == nil {
		return
	}
	if len(a.skillFS.List()) > 0 {
		return // already have skills
	}

	seeds := DefaultSeedSkills()
	imported := 0
	for _, seed := range seeds {
		body := fmt.Sprintf("# %s\n\n", seed.Name)
		body += fmt.Sprintf("## When to Use\n")
		if len(seed.Triggers) > 0 {
			body += fmt.Sprintf("Triggers: %s\n\n", strings.Join(seed.Triggers, ", "))
		}
		body += fmt.Sprintf("## Procedure\n%s\n", seed.Description)
		if len(seed.Dependencies) > 0 {
			body += fmt.Sprintf("\n## Dependencies\n%s\n", strings.Join(seed.Dependencies, ", "))
		}

		meta := SkillMeta{
			Name:         seed.Name,
			Description:  truncateStr(seed.Description, 200),
			Category:     seed.Category,
			Origin:       "imported",
			Triggers:     seed.Triggers,
			Priority:     seed.Priority,
			Dependencies: seed.Dependencies,
		}

		if _, err := a.skillFS.Create(meta, body); err != nil {
			fmt.Printf("⚠️  [%s] Failed to import seed skill %s: %v\n", a.cfg.Agent.Name, seed.Name, err)
			continue
		}
		imported++
	}

	if imported > 0 {
		fmt.Printf("🌱 [%s] Imported %d seed skills to SkillFS\n", a.cfg.Agent.Name, imported)
	}
}

// isRetryableError checks if an error message indicates a transient failure
// that should be retried (API overload, rate limit, server errors).
func isRetryableError(errMsg string) bool {
	if errMsg == "" {
		return false
	}
	lower := strings.ToLower(errMsg)
	retryPatterns := []string{
		"529", "overloaded", "overload",
		"500", "502", "503", "504",
		"rate limit", "rate_limit", "too many requests", "429",
		"temporary", "temporarily",
		"connection reset", "connection refused",
		"timeout", "timed out",
	}
	for _, p := range retryPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
