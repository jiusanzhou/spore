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

package spawner

import (
	"fmt"
	"os"
	"path/filepath"

	"go.zoe.im/spore/internal/agent"
)

// Mode defines how a child agent is created.
type Mode string

const (
	ModeClone Mode = "clone" // exact copy
	ModeFork  Mode = "fork"  // inherit + mutate
)

// Request describes a spawn operation.
type Request struct {
	ParentName string
	ChildName  string
	Mode       Mode
	Role       string            // role override for the child
	Model      string            // model override
	Mutations  map[string]string // additional config overrides
}

// Spawner creates child agents from a parent.
type Spawner struct {
	baseDir      string
	maxChildren  int
	childCount   int
}

// New creates a new Spawner.
func New(baseDir string, maxChildren int) *Spawner {
	return &Spawner{
		baseDir:     baseDir,
		maxChildren: maxChildren,
	}
}

// Spawn creates a new child agent.
func (s *Spawner) Spawn(parentCfg *agent.Config, req *Request) (*agent.Config, *agent.Identity, error) {
	return s.SpawnWithBalance(parentCfg, nil, req, 0)
}

// SpawnWithBalance creates a new child agent with startup balance transferred from parent.
func (s *Spawner) SpawnWithBalance(parentCfg *agent.Config, parentID *agent.Identity, req *Request, startupBalance float64) (*agent.Config, *agent.Identity, error) {
	if s.childCount >= s.maxChildren {
		return nil, nil, fmt.Errorf("max children reached (%d/%d)", s.childCount, s.maxChildren)
	}

	// If startup balance requested, debit from parent
	if startupBalance > 0 && parentID != nil {
		if !parentID.CanAfford(startupBalance) {
			return nil, nil, fmt.Errorf("parent cannot afford startup balance: have %.4f, need %.4f", parentID.Balance, startupBalance)
		}
		if err := parentID.Debit(startupBalance); err != nil {
			return nil, nil, fmt.Errorf("debiting parent balance: %w", err)
		}
	}

	// Generate child identity
	childID, err := agent.NewIdentity(req.ChildName)
	if err != nil {
		// Refund parent if identity generation fails
		if startupBalance > 0 && parentID != nil {
			parentID.Credit(startupBalance)
		}
		return nil, nil, fmt.Errorf("generating child identity: %w", err)
	}

	// Set child's initial balance
	if startupBalance > 0 {
		childID.Credit(startupBalance)
	}

	// Create child config based on mode
	childCfg := s.deriveConfig(parentCfg, req)

	// Save to child directory
	childDir := filepath.Join(s.baseDir, req.ChildName)
	if err := os.MkdirAll(childDir, 0755); err != nil {
		return nil, nil, fmt.Errorf("creating child directory: %w", err)
	}
	if err := childCfg.Save(filepath.Join(childDir, "spore.toml")); err != nil {
		return nil, nil, fmt.Errorf("saving child config: %w", err)
	}
	if err := childID.Save(filepath.Join(childDir, "identity.key")); err != nil {
		return nil, nil, fmt.Errorf("saving child identity: %w", err)
	}

	s.childCount++
	return childCfg, childID, nil
}

func (s *Spawner) deriveConfig(parent *agent.Config, req *Request) *agent.Config {
	// Start with parent config
	child := *parent

	// Override name
	child.Agent.Name = req.ChildName

	// Apply role override
	if req.Role != "" {
		child.Agent.Role = req.Role
	}

	// Apply model override
	if req.Model != "" {
		child.LLM.Model = req.Model
	}

	// Fork mode: reduce resource share
	if req.Mode == ModeFork {
		child.Spawner.MaxChildren = parent.Spawner.MaxChildren / 2
	}

	return &child
}

// ChildCount returns the number of spawned children.
func (s *Spawner) ChildCount() int {
	return s.childCount
}
