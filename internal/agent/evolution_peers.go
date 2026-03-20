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
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"go.zoe.im/spore/internal/memory"
)

// PeerEvolution tracks the evolutionary fitness of observed peers.
// Used by coordinators to prefer high-performing agents.
type PeerEvolution struct {
	mu    sync.RWMutex
	agent *Agent
	peers map[string]*PeerFitness
}

// PeerFitness tracks a peer's observed performance.
type PeerFitness struct {
	AgentID        string  `json:"agent_id"`
	TasksCompleted int     `json:"tasks_completed"`
	TasksFailed    int     `json:"tasks_failed"`
	AvgDuration    float64 `json:"avg_duration_secs"`
	SuccessRate    float64 `json:"success_rate"`
	Fitness        float64 `json:"fitness"` // composite score [0, 1]
	LastSeen       int64   `json:"last_seen"`
}

// NewPeerEvolution creates a peer evolution tracker.
func NewPeerEvolution(a *Agent) *PeerEvolution {
	pe := &PeerEvolution{
		agent: a,
		peers: make(map[string]*PeerFitness),
	}
	pe.restore()
	return pe
}

// ObserveSuccess records a successful task completion by a peer.
func (pe *PeerEvolution) ObserveSuccess(agentID string, duration float64) {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	pf := pe.getOrCreate(agentID)
	pf.TasksCompleted++
	pf.updateAvgDuration(duration)
	pf.recalcFitness()
	pf.LastSeen = time.Now().Unix()
}

// ObserveFailure records a failed task by a peer.
func (pe *PeerEvolution) ObserveFailure(agentID string, duration float64) {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	pf := pe.getOrCreate(agentID)
	pf.TasksFailed++
	pf.updateAvgDuration(duration)
	pf.recalcFitness()
	pf.LastSeen = time.Now().Unix()
}

// Fitness returns the fitness score for a peer (0 = unknown, higher = better).
func (pe *PeerEvolution) Fitness(agentID string) float64 {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	if pf, ok := pe.peers[agentID]; ok {
		return pf.Fitness
	}
	return 0.5 // neutral default for unknown peers
}

// Rankings returns all peers sorted by fitness (descending).
func (pe *PeerEvolution) Rankings() []*PeerFitness {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	result := make([]*PeerFitness, 0, len(pe.peers))
	for _, pf := range pe.peers {
		cp := *pf
		result = append(result, &cp)
	}

	// Sort by fitness descending
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].Fitness > result[i].Fitness {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// Persist saves peer evolution data to memory.
func (pe *PeerEvolution) Persist() {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	if pe.agent.memory == nil {
		return
	}
	data, _ := json.Marshal(pe.peers)
	pe.agent.memory.Put(&memory.Entry{
		AgentID: pe.agent.identity.PublicKeyHex()[:16],
		Key:     "evolution:peer_fitness",
		Value:   string(data),
		Metadata: map[string]string{
			"type": "peer_evolution",
		},
	})
}

// restore loads peer evolution data from memory.
func (pe *PeerEvolution) restore() {
	if pe.agent.memory == nil {
		return
	}
	entry, err := pe.agent.memory.Get("evolution:peer_fitness")
	if err != nil || entry == nil {
		return
	}
	var peers map[string]*PeerFitness
	if json.Unmarshal([]byte(entry.Value), &peers) == nil && peers != nil {
		pe.peers = peers
		fmt.Printf("🏆 [%s] Restored peer fitness data: %d peers\n", pe.agent.cfg.Agent.Name, len(peers))
	}
}

// getOrCreate returns existing or new PeerFitness (caller must hold lock).
func (pe *PeerEvolution) getOrCreate(agentID string) *PeerFitness {
	pf, ok := pe.peers[agentID]
	if !ok {
		pf = &PeerFitness{AgentID: agentID}
		pe.peers[agentID] = pf
	}
	return pf
}

func (pf *PeerFitness) updateAvgDuration(duration float64) {
	total := pf.TasksCompleted + pf.TasksFailed
	if total <= 1 {
		pf.AvgDuration = duration
	} else {
		pf.AvgDuration = (pf.AvgDuration*float64(total-1) + duration) / float64(total)
	}
}

// recalcFitness computes a composite fitness score.
// Formula: success_rate * (1 - time_decay) * speed_bonus
func (pf *PeerFitness) recalcFitness() {
	total := pf.TasksCompleted + pf.TasksFailed
	if total == 0 {
		pf.Fitness = 0.5
		return
	}

	pf.SuccessRate = float64(pf.TasksCompleted) / float64(total)

	// Speed bonus: faster agents get a small boost (log scale, capped at 1.2x)
	speedBonus := 1.0
	if pf.AvgDuration > 0 {
		speedBonus = math.Min(1.2, 1.0+1.0/math.Max(pf.AvgDuration, 0.1))
	}

	// Time decay: reduce fitness for stale peers
	daysSince := float64(time.Now().Unix()-pf.LastSeen) / 86400
	decay := 1.0
	if daysSince > 1 {
		decay = math.Max(0.5, 1.0-daysSince*0.05) // 5% per day, floor 50%
	}

	// Confidence: more data = more reliable score
	confidence := math.Min(1.0, float64(total)/10.0) // full confidence at 10+ tasks

	// Weighted composite
	rawFitness := pf.SuccessRate * speedBonus * decay
	// Blend with neutral (0.5) based on confidence
	pf.Fitness = rawFitness*confidence + 0.5*(1-confidence)

	// Clamp [0, 1]
	if pf.Fitness > 1 {
		pf.Fitness = 1
	}
	if pf.Fitness < 0 {
		pf.Fitness = 0
	}
}
