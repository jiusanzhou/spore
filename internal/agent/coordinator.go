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
	"strings"
	"sync"
	"time"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/protocol"
)

const (
	maxSubtasks    = 8
	subtaskTimeout = 120 * time.Second
)

// subtask is one unit of decomposed work.
type subtask struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`       // required skills for matching
	AgentID     string   `json:"agent_id"`      // assigned agent (empty = unassigned)
}

// coordinatorState tracks in-flight delegations for one parent task.
type coordinatorState struct {
	parentTaskID string
	subtasks     []subtask
	results      map[string]*protocol.TaskResult // subtaskID → result
	mu           sync.Mutex
	done         chan struct{}
}

func newCoordinatorState(parentID string, subs []subtask) *coordinatorState {
	return &coordinatorState{
		parentTaskID: parentID,
		subtasks:     subs,
		results:      make(map[string]*protocol.TaskResult),
		done:         make(chan struct{}),
	}
}

func (cs *coordinatorState) addResult(taskID string, result *protocol.TaskResult) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.results[taskID] = result
	if len(cs.results) >= len(cs.subtasks) {
		select {
		case <-cs.done:
		default:
			close(cs.done)
		}
		return true
	}
	return false
}

// coordinatorExecute decomposes a task, dispatches to workers, collects results.
func (a *Agent) coordinatorExecute(ctx context.Context, entry *taskEntry) error {
	fmt.Printf("🧠 [%s] Coordinator decomposing task: %s\n", a.cfg.Agent.Name, truncate(entry.Description, 80))

	// 1. Decompose
	subs, simple, err := a.decompose(ctx, entry.Description)
	if err != nil {
		return fmt.Errorf("decompose: %w", err)
	}

	// Simple task — execute directly (bypass coordinator)
	if simple {
		fmt.Printf("   [%s] Simple task, executing directly\n", a.cfg.Agent.Name)
		return a.executeTaskDirect(ctx, entry)
	}

	fmt.Printf("   [%s] Decomposed into %d subtasks\n", a.cfg.Agent.Name, len(subs))

	// 2. Dispatch
	state := newCoordinatorState(entry.ID, subs)

	// Store state for result collection
	a.mu.Lock()
	if a.coordStates == nil {
		a.coordStates = make(map[string]*coordinatorState)
	}
	a.coordStates[entry.ID] = state
	a.mu.Unlock()

	dispatched := a.dispatch(subs)
	if dispatched == 0 {
		// Try to spawn a specialist if possible
		if a.onSpawnRequest != nil && a.identity.Balance >= a.cfg.Spawner.MinBalanceToSpawn {
			neededSkills := []string{}
			for _, sub := range subs {
				neededSkills = append(neededSkills, sub.Skills...)
			}
			childName, err := a.RequestSpawn("worker", unique(neededSkills),
				fmt.Sprintf("No worker available for skills: %v", neededSkills))
			if err == nil {
				fmt.Printf("🐣 [%s] Spawned specialist '%s' for unmatched skills, retrying dispatch\n",
					a.cfg.Agent.Name, childName)
				// Brief pause to let child register
				time.Sleep(500 * time.Millisecond)
				dispatched = a.dispatch(subs)
			}
		}
	}
	if dispatched == 0 {
		fmt.Printf("   [%s] No workers available, executing locally\n", a.cfg.Agent.Name)
		a.mu.Lock()
		delete(a.coordStates, entry.ID)
		a.mu.Unlock()
		return a.executeTaskDirect(ctx, entry)
	}

	// 3. Wait for results
	fmt.Printf("   [%s] Waiting for %d subtask results...\n", a.cfg.Agent.Name, dispatched)
	select {
	case <-state.done:
		fmt.Printf("   [%s] All subtask results collected\n", a.cfg.Agent.Name)
	case <-time.After(subtaskTimeout):
		fmt.Printf("⚠️  [%s] Subtask timeout, proceeding with partial results\n", a.cfg.Agent.Name)
	case <-ctx.Done():
		return ctx.Err()
	}

	// 4. Summarize
	summary, err := a.summarize(ctx, entry.Description, state)
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}

	fmt.Printf("✅ [%s] Coordinator completed: %s\n", a.cfg.Agent.Name, truncate(summary, 200))
	if a.onTaskUpdate != nil {
		a.onTaskUpdate(entry.ID, "completed", "coordinator", summary, "")
	}

	// Store coordinator experience in memory
	a.rememberCoordination(entry, state, summary)

	// Cleanup
	a.mu.Lock()
	delete(a.coordStates, entry.ID)
	a.mu.Unlock()

	return nil
}

// decompose asks LLM to break a task into subtasks with skill requirements.
// Returns (subtasks, isSimple, error).
func (a *Agent) decompose(ctx context.Context, description string) ([]subtask, bool, error) {
	// Build available skills from known peers
	peerSkills := a.peerSkillsSummary()

	prompt := fmt.Sprintf(`You are a task coordinator. Analyze this task and decide:

1. If it's SIMPLE (can be done by one agent), respond with just: SIMPLE
2. If it's COMPLEX, decompose it into subtasks.

Available workers and their skills:
%s

Task: %s

If COMPLEX, respond in this JSON format (no markdown):
[{"id":"sub-1","description":"...","skills":["skill1","skill2"]},...]

Rules:
- Max %d subtasks
- Each subtask should map to specific skills
- Be specific in descriptions`, peerSkills, description, maxSubtasks)

	messages := []llm.Message{
		{Role: "system", Content: "You are a task decomposition engine. Respond with either SIMPLE or a JSON array of subtasks."},
		{Role: "user", Content: prompt},
	}

	resp, err := a.llm.Chat(ctx, messages)
	if err != nil {
		return nil, false, err
	}

	content := strings.TrimSpace(resp.Content)

	// Check for SIMPLE
	if strings.HasPrefix(strings.ToUpper(content), "SIMPLE") {
		return nil, true, nil
	}

	// Parse JSON subtasks
	// Strip markdown code fence if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var subs []subtask
	if err := json.Unmarshal([]byte(content), &subs); err != nil {
		// Fallback: treat as simple if we can't parse
		fmt.Printf("⚠️  [%s] Failed to parse decomposition, treating as simple: %v\n", a.cfg.Agent.Name, err)
		return nil, true, nil
	}

	if len(subs) == 0 {
		return nil, true, nil
	}
	if len(subs) > maxSubtasks {
		subs = subs[:maxSubtasks]
	}

	return subs, false, nil
}

// dispatch sends subtasks to matching workers with load balancing. Returns number dispatched.
func (a *Agent) dispatch(subs []subtask) int {
	dispatched := 0
	// Track how many subtasks each worker has been assigned in this batch
	loadCount := make(map[string]int)

	for i := range subs {
		worker := a.matchWorker(subs[i].Skills, loadCount)
		if worker == nil {
			fmt.Printf("   [%s] No worker matched for subtask %s (skills: %v)\n",
				a.cfg.Agent.Name, subs[i].ID, subs[i].Skills)
			continue
		}
		subs[i].AgentID = worker.AgentID
		loadCount[worker.AgentID]++

		// Send task request to worker
		req := protocol.TaskRequest{
			Description: subs[i].Description,
		}
		msg, err := protocol.NewMessage(
			a.identity.PublicKeyHex()[:16],
			worker.AgentID,
			protocol.MsgTaskRequest,
			req,
		)
		if err != nil {
			fmt.Printf("⚠️  [%s] Failed to create message for %s: %v\n", a.cfg.Agent.Name, worker.AgentID, err)
			continue
		}

		if err := a.bus.Send(msg); err != nil {
			fmt.Printf("⚠️  [%s] Failed to dispatch to %s: %v\n", a.cfg.Agent.Name, worker.AgentID[:8], err)
			continue
		}

		fmt.Printf("   [%s] Dispatched subtask %s → %s (skills: %v)\n",
			a.cfg.Agent.Name, subs[i].ID, worker.AgentID[:8], subs[i].Skills)
		dispatched++
	}
	return dispatched
}

// matchWorker finds the best worker for required skills, considering current batch load
// and evolutionary fitness (peer performance history).
func (a *Agent) matchWorker(requiredSkills []string, batchLoad map[string]int) *PeerInfo {
	a.peersMu.RLock()
	defer a.peersMu.RUnlock()

	var best *PeerInfo
	bestScore := -1.0

	for _, peer := range a.peers {
		// Skip self
		if peer.AgentID == a.identity.PublicKeyHex()[:16] {
			continue
		}
		// Effective capacity = advertised capacity - batch load penalty
		batchPenalty := float64(batchLoad[peer.AgentID]) * 0.3
		effectiveCap := peer.Capacity - batchPenalty
		if effectiveCap < 0.1 {
			continue // fully loaded in this batch
		}

		// Score by skill overlap (case-insensitive, supports partial match)
		skillScore := 0
		for _, req := range requiredSkills {
			req = strings.ToLower(strings.TrimSpace(req))
			if req == "" {
				continue
			}
			for _, cap := range peer.Capabilities {
				cap = strings.ToLower(cap)
				if cap == req || strings.Contains(cap, req) || strings.Contains(req, cap) {
					skillScore++
					break // count each required skill once
				}
			}
		}

		// Get evolutionary fitness from peer tracking
		fitness := 0.5 // neutral default
		if a.peerEvo != nil {
			fitness = a.peerEvo.Fitness(peer.AgentID)
		}

		// Composite score: skill_match * 10 + fitness * capacity * reputation
		compositeScore := float64(skillScore)*10.0 + fitness*effectiveCap*peer.Reputation

		if compositeScore > bestScore {
			bestScore = compositeScore
			best = peer
		}
	}

	return best
}

// summarize combines subtask results into a final answer.
func (a *Agent) summarize(ctx context.Context, originalTask string, state *coordinatorState) (string, error) {
	state.mu.Lock()
	var parts []string
	for _, sub := range state.subtasks {
		if r, ok := state.results[sub.ID]; ok {
			status := "✅"
			if !r.Success {
				status = "❌"
			}
			parts = append(parts, fmt.Sprintf("%s [%s] %s: %s", status, sub.ID, sub.Description, r.Output))
		} else {
			parts = append(parts, fmt.Sprintf("⏳ [%s] %s: (no response)", sub.ID, sub.Description))
		}
	}
	state.mu.Unlock()

	prompt := fmt.Sprintf(`Original task: %s

Subtask results:
%s

Synthesize these results into a clear, concise final answer.`, originalTask, strings.Join(parts, "\n"))

	messages := []llm.Message{
		{Role: "system", Content: "You are a coordinator summarizing subtask results into a final answer. Be concise."},
		{Role: "user", Content: prompt},
	}

	resp, err := a.llm.Chat(ctx, messages)
	if err != nil {
		// Fallback: just concatenate results
		return strings.Join(parts, "\n"), nil
	}
	return resp.Content, nil
}

// handleTaskResult processes a task result from a worker.
func (a *Agent) handleTaskResult(result *protocol.TaskResult, fromAgent string) {
	// Track peer performance for evolutionary selection
	if a.peerEvo != nil {
		if result.Success {
			a.peerEvo.ObserveSuccess(fromAgent, 0) // duration unknown from result msg
		} else {
			a.peerEvo.ObserveFailure(fromAgent, 0)
		}
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, state := range a.coordStates {
		state.mu.Lock()
		// Find first unresolved subtask assigned to this agent
		for i, sub := range state.subtasks {
			if sub.AgentID == fromAgent {
				if _, already := state.results[sub.ID]; !already {
					state.results[sub.ID] = result
					fmt.Printf("📩 [%s] Subtask %s (%d/%d) from %s: success=%v\n",
						a.cfg.Agent.Name, sub.ID, len(state.results), len(state.subtasks),
						fromAgent[:8], result.Success)
					_ = i // used implicitly
					// Check completion
					if len(state.results) >= len(state.subtasks) {
						select {
						case <-state.done:
						default:
							close(state.done)
						}
					}
					state.mu.Unlock()
					return
				}
			}
		}
		state.mu.Unlock()
	}
}

// peerSkillsSummary builds a text summary of known peers and their skills.
func (a *Agent) peerSkillsSummary() string {
	a.peersMu.RLock()
	defer a.peersMu.RUnlock()

	if len(a.peers) == 0 {
		return "(no workers discovered yet)"
	}

	var lines []string
	for _, peer := range a.peers {
		if peer.AgentID == a.identity.PublicKeyHex()[:16] {
			continue // skip self
		}
		lines = append(lines, fmt.Sprintf("- %s: skills=%v capacity=%.1f reputation=%.1f",
			peer.AgentID[:8], peer.Capabilities, peer.Capacity, peer.Reputation))
	}
	if len(lines) == 0 {
		return "(no workers discovered yet)"
	}
	return strings.Join(lines, "\n")
}

// rememberCoordination stores a coordinator task decomposition as experience.
func (a *Agent) rememberCoordination(entry *taskEntry, state *coordinatorState, summary string) {
	if a.memory == nil {
		return
	}

	// Build subtask summary
	state.mu.Lock()
	var subSummary []string
	for _, sub := range state.subtasks {
		status := "✅"
		if r, ok := state.results[sub.ID]; ok && !r.Success {
			status = "❌"
		} else if _, ok := state.results[sub.ID]; !ok {
			status = "⏳"
		}
		subSummary = append(subSummary, fmt.Sprintf("%s %s → %s (skills: %v)",
			status, sub.ID, sub.AgentID[:8], sub.Skills))
	}
	state.mu.Unlock()

	value := fmt.Sprintf("Task: %s\nSubtasks:\n%s\nSummary: %s",
		entry.Description, strings.Join(subSummary, "\n"), truncate(summary, 500))

	memEntry := &memory.Entry{
		AgentID: a.identity.PublicKeyHex()[:16],
		Key:     "coord:" + entry.ID,
		Value:   value,
		Metadata: map[string]string{
			"type":      "coordination_experience",
			"task_id":   entry.ID,
			"subtasks":  fmt.Sprintf("%d", len(state.subtasks)),
			"completed": fmt.Sprintf("%d", len(state.results)),
		},
	}
	if err := a.memory.Put(memEntry); err != nil {
		fmt.Printf("⚠️  [%s] Failed to store coordination memory: %v\n", a.cfg.Agent.Name, err)
	}
}

// unique deduplicates a string slice.
func unique(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
