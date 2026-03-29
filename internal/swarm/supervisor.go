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
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"go.zoe.im/spore/internal/agent"
)

// ────────────────────────────────────────────────────────────────────────────
// Daemon Supervisor
//
// Wraps the Swarm with:
// - PID file management
// - Graceful shutdown on SIGINT/SIGTERM
// - Automatic agent restart on crash (with backoff)
// - Health monitoring
// ────────────────────────────────────────────────────────────────────────────

// SupervisorConfig configures the daemon supervisor.
type SupervisorConfig struct {
	PidFile         string        // PID file path (default: <baseDir>/spore.pid)
	MaxRestarts     int           // max restarts per agent before giving up (default 5)
	RestartBackoff  time.Duration // initial backoff between restarts (default 5s)
	MaxBackoff      time.Duration // maximum backoff (default 5min)
	HealthInterval  time.Duration // health check interval (default 30s)
}

// DefaultSupervisorConfig returns sensible defaults.
func DefaultSupervisorConfig(baseDir string) SupervisorConfig {
	return SupervisorConfig{
		PidFile:        filepath.Join(baseDir, "spore.pid"),
		MaxRestarts:    5,
		RestartBackoff: 5 * time.Second,
		MaxBackoff:     5 * time.Minute,
		HealthInterval: 30 * time.Second,
	}
}

// Supervisor manages agent lifecycle with automatic restarts.
type Supervisor struct {
	swarm     *Swarm
	cfg       SupervisorConfig
	mu        sync.Mutex
	restarts  map[string]int       // agent name → restart count
	ctx       context.Context
	cancel    context.CancelFunc
	changelog *Changelog
	feedback  *FeedbackChannel
}

// NewSupervisor creates a daemon supervisor for the given swarm.
func NewSupervisor(sw *Swarm, cfg SupervisorConfig) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())

	changelog := NewChangelog(sw.baseDir)
	changelog.Load()

	fb := NewFeedbackChannel(changelog)

	// Wire into swarm
	sw.SetChangelog(changelog)
	sw.SetFeedback(fb)

	return &Supervisor{
		swarm:     sw,
		cfg:       cfg,
		restarts:  make(map[string]int),
		ctx:       ctx,
		cancel:    cancel,
		changelog: changelog,
		feedback:  fb,
	}
}

// Changelog returns the swarm changelog.
func (sv *Supervisor) Changelog() *Changelog { return sv.changelog }

// Feedback returns the human feedback channel.
func (sv *Supervisor) Feedback() *FeedbackChannel { return sv.feedback }

// Run starts the supervisor with signal handling.
// Blocks until SIGINT/SIGTERM or context cancellation.
func (sv *Supervisor) Run() error {
	// Write PID file
	if err := sv.writePidFile(); err != nil {
		fmt.Printf("⚠️  Failed to write PID file: %v\n", err)
	}
	defer sv.removePidFile()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start all agents with supervision
	sv.startAllSupervised()

	// Health monitor
	healthTicker := time.NewTicker(sv.cfg.HealthInterval)
	defer healthTicker.Stop()

	fmt.Printf("🛡️  Supervisor started (pid=%d, agents=%d)\n", os.Getpid(), len(sv.swarm.agents))

	for {
		select {
		case sig := <-sigCh:
			fmt.Printf("\n🛑 Received %s, shutting down gracefully...\n", sig)
			sv.shutdown()
			return nil

		case <-sv.ctx.Done():
			sv.shutdown()
			return nil

		case <-healthTicker.C:
			sv.healthCheck()
		}
	}
}

// Stop signals the supervisor to shut down.
func (sv *Supervisor) Stop() {
	sv.cancel()
}

// startAllSupervised starts each agent in a supervised goroutine.
func (sv *Supervisor) startAllSupervised() {
	sv.swarm.mu.RLock()
	defer sv.swarm.mu.RUnlock()

	for name, a := range sv.swarm.agents {
		sv.startAgentSupervised(name, a)
	}
}

// startAgentSupervised runs an agent with automatic restart on crash.
func (sv *Supervisor) startAgentSupervised(name string, a *agent.Agent) {
	go func() {
		backoff := sv.cfg.RestartBackoff

		for {
			select {
			case <-sv.ctx.Done():
				return
			default:
			}

			err := a.Run()
			if err == nil {
				return // clean exit
			}

			sv.mu.Lock()
			sv.restarts[name]++
			count := sv.restarts[name]
			sv.mu.Unlock()

			if count > sv.cfg.MaxRestarts {
				fmt.Printf("💀 Agent %s exceeded max restarts (%d), giving up\n", name, sv.cfg.MaxRestarts)
				sv.changelog.Record(ChangeEntry{
					Type:    ChangeAgentStopped,
					Agent:   name,
					Summary: fmt.Sprintf("Agent %s crashed %d times, supervisor gave up", name, count),
					Details: err.Error(),
				})
				return
			}

			fmt.Printf("🔄 Agent %s crashed (attempt %d/%d): %v — restarting in %s\n",
				name, count, sv.cfg.MaxRestarts, err, backoff)

			select {
			case <-time.After(backoff):
				// Exponential backoff with cap
				backoff *= 2
				if backoff > sv.cfg.MaxBackoff {
					backoff = sv.cfg.MaxBackoff
				}
			case <-sv.ctx.Done():
				return
			}
		}
	}()
}

// healthCheck monitors agent health.
func (sv *Supervisor) healthCheck() {
	infos := sv.swarm.List()
	healthy := 0
	for _, info := range infos {
		if info.Status == agent.StatusIdle || info.Status == agent.StatusBusy {
			healthy++
		}
	}

	if healthy < len(infos) {
		fmt.Printf("⚠️  Health check: %d/%d agents healthy\n", healthy, len(infos))
	}
}

// shutdown performs graceful shutdown.
func (sv *Supervisor) shutdown() {
	sv.cancel()

	// Render changelog on exit
	if sv.changelog != nil {
		sv.changelog.RenderMarkdown()
	}

	// Close swarm
	if err := sv.swarm.Close(); err != nil {
		fmt.Printf("⚠️  Swarm close error: %v\n", err)
	}

	fmt.Println("👋 Supervisor shut down")
}

// PID file management

func (sv *Supervisor) writePidFile() error {
	dir := filepath.Dir(sv.cfg.PidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(sv.cfg.PidFile, []byte(strconv.Itoa(os.Getpid())), 0644)
}

func (sv *Supervisor) removePidFile() {
	os.Remove(sv.cfg.PidFile)
}

// ReadPidFile reads the PID from the pid file. Returns 0 if not found.
func ReadPidFile(pidFile string) int {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0
	}
	return pid
}

// IsRunning checks if a process with the given PID is alive.
func IsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 tests if process exists without actually sending a signal
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
