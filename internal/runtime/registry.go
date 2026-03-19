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

package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Registry manages available runtimes and routes tasks to them.
type Registry struct {
	mu       sync.RWMutex
	runtimes map[string]Runtime
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		runtimes: make(map[string]Runtime),
	}
}

// Register adds a runtime to the registry.
func (r *Registry) Register(rt Runtime) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes[rt.Info().Name] = rt
}

// Get returns a runtime by name.
func (r *Registry) Get(name string) (Runtime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.runtimes[name]
	return rt, ok
}

// List returns info about all registered runtimes.
func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	infos := make([]Info, 0, len(r.runtimes))
	for _, rt := range r.runtimes {
		infos = append(infos, rt.Info())
	}
	return infos
}

// Healthy returns only the runtimes that are currently available.
func (r *Registry) Healthy(ctx context.Context) []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var infos []Info
	for _, rt := range r.runtimes {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := rt.Healthy(checkCtx); err == nil {
			infos = append(infos, rt.Info())
		}
		cancel()
	}
	return infos
}

// Route picks the best runtime for a task based on required tags.
// Falls back to "builtin" if no match.
func (r *Registry) Route(tags []string) (Runtime, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(tags) == 0 {
		return r.fallback()
	}

	// Score each runtime by tag overlap
	var best Runtime
	bestScore := 0

	for _, rt := range r.runtimes {
		score := 0
		for _, cap := range rt.Info().Capabilities {
			for _, capTag := range cap.Tags {
				for _, want := range tags {
					if capTag == want {
						score++
					}
				}
			}
		}
		if score > bestScore {
			bestScore = score
			best = rt
		}
	}

	if best != nil {
		return best, nil
	}
	return r.fallback()
}

func (r *Registry) fallback() (Runtime, error) {
	if rt, ok := r.runtimes["builtin"]; ok {
		return rt, nil
	}
	if len(r.runtimes) > 0 {
		for _, rt := range r.runtimes {
			return rt, nil
		}
	}
	return nil, fmt.Errorf("no runtimes available")
}

// AutoDiscover probes common agent CLIs and registers those that are available.
// It checks both native Spore runtimes and agentbox-backed adapters.
func (r *Registry) AutoDiscover(ctx context.Context) []string {
	// Native Spore runtimes (kept for openclaw which has custom Execute logic)
	natives := []Runtime{
		NewOpenClaw(),
	}

	var discovered []string
	for _, rt := range natives {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := rt.Healthy(checkCtx); err == nil {
			r.Register(rt)
			discovered = append(discovered, rt.Info().Name)
		}
		cancel()
	}

	// agentbox-backed adapters (claude, codex, opencode, gemini, aider, goose, openhands)
	for _, rt := range DefaultAboxAdapters() {
		name := rt.Info().Name
		if _, exists := r.Get(name); exists {
			continue // don't override native implementations
		}
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := rt.Healthy(checkCtx); err == nil {
			r.Register(rt)
			discovered = append(discovered, name)
		}
		cancel()
	}

	return discovered
}

// Close shuts down all runtimes.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rt := range r.runtimes {
		rt.Close()
	}
	return nil
}
