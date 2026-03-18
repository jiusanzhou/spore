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
	ID        string    `json:"id"` // public key hex (short)
	Role      string    `json:"role"`
	Model     string    `json:"model"`
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

	status    Status
	taskQueue chan *engine.Task
	mu        sync.RWMutex
	taskCount int
	startedAt time.Time
}

// New creates a new Agent from config.
func New(cfg *Config) (*Agent, error) {
	// load or create identity
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

	// init ethics engine
	ethicsEngine, err := ethics.New("", &ethics.Config{
		MaxBudgetPerTask: cfg.Ethics.MaxBudgetPerTask,
	})
	if err != nil {
		return nil, fmt.Errorf("creating ethics engine: %w", err)
	}

	// init task engine
	eng := engine.New(provider, store)
	eng.SetEthics(&ethicsAdapter{e: ethicsEngine})
	eng.SetAgentID(id.PublicKeyHex()[:16])

	// register built-in tools
	eng.RegisterTool(&engine.ShellTool{})
	eng.RegisterTool(&engine.WebSearchTool{})

	return &Agent{
		cfg:       cfg,
		identity:  id,
		llm:       provider,
		memory:    store,
		engine:    eng,
		ethics:    ethicsEngine,
		status:    StatusIdle,
		taskQueue: make(chan *engine.Task, 50),
	}, nil
}

// NewWithBus creates an agent attached to a shared message bus.
func NewWithBus(cfg *Config, bus network.Bus) (*Agent, error) {
	a, err := New(cfg)
	if err != nil {
		return nil, err
	}
	a.bus = bus

	// register delegate tool if bus is available
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

	// subscribe to messages
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

	fmt.Printf("🦠 Agent %s running (id: %s)\n", a.cfg.Agent.Name, a.identity.PublicKeyHex()[:16])
	fmt.Printf("   Model: %s/%s\n", a.cfg.LLM.Provider, a.cfg.LLM.Model)
	fmt.Printf("   Role:  %s\n", a.cfg.Agent.Role)

	// task worker
	go a.taskWorker(ctx)

	// heartbeat ticker
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
	task := &engine.Task{
		ID:          uuid.New().String()[:8],
		Description: description,
		State:       engine.TaskPending,
		CreatedAt:   time.Now(),
	}
	a.taskQueue <- task
	return task.ID
}

// Info returns the agent's current info.
func (a *Agent) Info() Info {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return Info{
		Name:      a.cfg.Agent.Name,
		ID:        a.identity.PublicKeyHex()[:16],
		Role:      a.cfg.Agent.Role,
		Model:     a.cfg.LLM.Model,
		Status:    a.status,
		TaskCount: a.taskCount,
		StartedAt: a.startedAt,
	}
}

// Identity returns the agent's identity.
func (a *Agent) Identity() *Identity {
	return a.identity
}

// Config returns the agent's config.
func (a *Agent) Config() *Config {
	return a.cfg
}

func (a *Agent) taskWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-a.taskQueue:
			a.mu.Lock()
			a.status = StatusBusy
			a.mu.Unlock()

			fmt.Printf("📋 [%s] Starting task: %s\n", a.cfg.Agent.Name, task.Description)

			if err := a.engine.Run(ctx, task); err != nil {
				fmt.Printf("❌ [%s] Task failed: %s\n", a.cfg.Agent.Name, err)
			} else {
				fmt.Printf("✅ [%s] Task completed: %s\n", a.cfg.Agent.Name, task.Result)
			}

			a.mu.Lock()
			a.status = StatusIdle
			a.taskCount++
			a.mu.Unlock()
		}
	}
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
		// acknowledge
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
			"name":   a.cfg.Agent.Name,
			"status": a.status,
		},
	)
	a.bus.Send(msg)
}

func (a *Agent) shutdown() error {
	a.status = StatusStopped
	if a.bus != nil {
		a.bus.Unsubscribe(a.identity.PublicKeyHex()[:16])
	}
	if a.ethics != nil {
		a.ethics.Close()
	}
	if a.memory != nil {
		return a.memory.Close()
	}
	return nil
}
