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
	StatusIdle    Status = "idle"
	StatusBusy    Status = "busy"
	StatusError   Status = "error"
	StatusStopped Status = "stopped"
)

// Info holds the agent's runtime information.
type Info struct {
	Name      string    `json:"name"`
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Model     string    `json:"model"`
	Runtime   string    `json:"runtime"`
	Status    Status    `json:"status"`
	TaskCount int       `json:"task_count"`
	StartedAt time.Time `json:"started_at"`
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
		// Auto-discover available CLIs
		discovered := reg.AutoDiscover(context.Background())
		if len(discovered) > 0 {
			fmt.Printf("   Discovered runtimes: %v\n", discovered)
		}
	case "claude-code":
		reg.Register(runtime.NewClaudeCode())
	case "codex":
		reg.Register(runtime.NewCodex())
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

	go a.taskWorker(ctx)

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
		Name:      a.cfg.Agent.Name,
		ID:        a.identity.PublicKeyHex()[:16],
		Role:      a.cfg.Agent.Role,
		Model:     a.cfg.LLM.Model,
		Runtime:   rtName,
		Status:    a.status,
		TaskCount: a.taskCount,
		StartedAt: a.startedAt,
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

func (a *Agent) taskWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-a.taskQueue:
			a.mu.Lock()
			a.status = StatusBusy
			a.mu.Unlock()

			fmt.Printf("📋 [%s] Starting task: %s\n", a.cfg.Agent.Name, entry.Description)

			err := a.executeTask(ctx, entry)
			if err != nil {
				fmt.Printf("❌ [%s] Task failed: %s\n", a.cfg.Agent.Name, err)
			}

			a.mu.Lock()
			a.status = StatusIdle
			a.taskCount++
			a.mu.Unlock()
		}
	}
}

func (a *Agent) executeTask(ctx context.Context, entry *taskEntry) error {
	// Determine which runtime to use
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
		return err
	}

	if output.Success {
		fmt.Printf("✅ [%s] Task completed via %s: %s\n", a.cfg.Agent.Name, rt.Info().Name, truncate(output.Result, 200))
	} else {
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
		fmt.Printf("📨 [%s] Received task from %s: %s\n", a.cfg.Agent.Name, msg.From[:8], req.Description)
		a.SubmitTask(req.Description)
	case protocol.MsgHeartbeat:
		fmt.Printf("💓 [%s] Heartbeat from %s\n", a.cfg.Agent.Name, msg.From[:8])
	}
	return nil
}

func (a *Agent) heartbeat() {
	if a.bus == nil {
		return
	}
	msg, _ := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgHeartbeat,
		map[string]interface{}{
			"name":    a.cfg.Agent.Name,
			"status":  a.status,
			"runtime": a.cfg.Runtime.Type,
		},
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
