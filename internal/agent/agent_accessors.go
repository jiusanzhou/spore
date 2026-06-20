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

// agent_accessors.go — public read-only getters on *Agent.
//
// These are the entry points other packages (mcpserver, gateway, api,
// cmd) use to look at agent state without going through the agent's
// own task loop. They MUST stay one-liners or trivial copies — anything
// with logic belongs in agent.go (Info, Close) or in its own file.
//
// Why split them off: agent.go was 2.3k lines and the getter cluster
// was 240 of those, all skimming-but-no-logic. Splitting halves the
// time it takes to find anything when reading agent.go, and these
// rarely change so the new file should stay quiet.

package agent

import (
	"go.zoe.im/spore/internal/ethics"
	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/mcp"
	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/runtime"
)

// ID returns the agent's stable identifier (first 16 hex chars of the
// public key — short enough to read, long enough to be unique inside
// any realistic swarm).
func (a *Agent) ID() string {
	return a.identity.PublicKeyHex()[:16]
}

// Memory returns the agent's memory store.
func (a *Agent) Memory() memory.Store {
	return a.memory
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

// Tokens returns the agent's token ledger.
func (a *Agent) Tokens() *TokenLedger { return a.tokens }

// PeerEvo returns the agent's peer evolution tracker (may be nil).
func (a *Agent) PeerEvo() *PeerEvolution { return a.peerEvo }

// Reputation returns the agent's reputation engine (may be nil).
func (a *Agent) Reputation() *ReputationEngine { return a.reputation }

// Ethics returns the agent's ethics engine (may be nil). External callers
// (e.g. the MCP server) use it to vet content proposed by remote clients
// before it touches the agent's state.
func (a *Agent) Ethics() *ethics.Engine { return a.ethics }

// Skills returns the agent's legacy skill store (DEPRECATED, may be nil).
func (a *Agent) Skills() *SkillStore { return a.skillStore }

// SkillFileStore returns the agent's SkillFS (file-system-first, may be nil).
func (a *Agent) SkillFileStore() *SkillFS { return a.skillFS }

// Market returns the marketplace engine.
func (a *Agent) Market() *Marketplace { return a.marketplace }

// Bus returns the agent's network bus (may be nil).
func (a *Agent) Bus() network.Bus { return a.bus }

// MCPManager returns the agent's MCP manager (may be nil when MCP is disabled).
// Callers can use it to inspect connected servers or close the manager early.
func (a *Agent) MCPManager() *mcp.Manager { return a.mcpManager }

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

// Peers returns a copy of the current peer registry. Callers may
// mutate the returned map without affecting the agent's internal
// state.
func (a *Agent) Peers() map[string]*PeerInfo {
	a.peersMu.RLock()
	defer a.peersMu.RUnlock()
	cp := make(map[string]*PeerInfo, len(a.peers))
	for k, v := range a.peers {
		cp[k] = v
	}
	return cp
}
