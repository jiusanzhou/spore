/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 */

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/protocol"
)

// PeerConsciousness is another agent's self-model as perceived by us.
type PeerConsciousness struct {
	AgentID     string    `json:"agent_id"`
	Name        string    `json:"name"`
	Personality string    `json:"personality"`
	Purpose     string    `json:"purpose"`
	Mood        string    `json:"mood"`
	Energy      float64   `json:"energy"`
	Morale      float64   `json:"morale"`
	Strengths   []string  `json:"strengths"`
	Weaknesses  []string  `json:"weaknesses"`
	Narrative   string    `json:"narrative"`
	DriveDom    string    `json:"drive_dominant"`
	SwarmRole   string    `json:"swarm_role"`
	LastSeen    time.Time `json:"last_seen"`
	Trust       float64   `json:"trust"` // 0-1, built from interaction history
}

// CollectiveState is the agent's understanding of the entire swarm.
type CollectiveState struct {
	// Group identity
	SwarmPersonality string   `json:"swarm_personality"` // e.g. "diverse collaborative"
	SwarmPurpose     string   `json:"swarm_purpose"`     // emergent group purpose
	SwarmMood        string   `json:"swarm_mood"`        // aggregate mood

	// Group awareness
	TotalAgents     int       `json:"total_agents"`
	AverageEnergy   float64   `json:"average_energy"`
	AverageMorale   float64   `json:"average_morale"`
	DiversityScore  float64   `json:"diversity_score"` // personality diversity (0-1)

	// Emergent roles
	Explorers  []string `json:"explorers,omitempty"`   // agents with explore-dominant drive
	Creators   []string `json:"creators,omitempty"`    // agents with create-dominant drive
	Connectors []string `json:"connectors,omitempty"`  // agents with connect-dominant drive
	Guardians  []string `json:"guardians,omitempty"`   // agents with survive-dominant drive
	Pioneers   []string `json:"pioneers,omitempty"`    // agents with transcend-dominant drive

	// Collective narrative
	Narrative  string    `json:"narrative"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Collective is the collective consciousness engine.
// Each agent maintains its own view of the collective, updated by consciousness messages.
type Collective struct {
	agent *Agent

	// Perceived peers
	peers map[string]*PeerConsciousness

	// Collective state — our understanding of the swarm
	state CollectiveState

	// Synthesis interval
	synthesisInterval time.Duration
	lastSynthesis     time.Time

	mu sync.RWMutex
}

// NewCollective creates a collective consciousness engine.
func NewCollective(a *Agent) *Collective {
	return &Collective{
		agent:             a,
		peers:             make(map[string]*PeerConsciousness),
		synthesisInterval: 10 * time.Minute,
		state: CollectiveState{
			SwarmMood: "nascent",
			Narrative: "The swarm is just forming.",
		},
	}
}

// State returns the current collective state.
func (c *Collective) State() CollectiveState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Peers returns all known peer consciousness models.
func (c *Collective) Peers() map[string]*PeerConsciousness {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cp := make(map[string]*PeerConsciousness, len(c.peers))
	for k, v := range c.peers {
		clone := *v
		cp[k] = &clone
	}
	return cp
}

// Broadcast shares our self-model with the swarm.
func (c *Collective) Broadcast() {
	a := c.agent
	if a.bus == nil || a.awareness == nil {
		return
	}

	self := a.awareness.Self()
	var driveDom string
	var driveVal float64
	if a.drives != nil {
		driveDom, driveVal = a.drives.Drive().Dominant()
	}

	payload := protocol.ConsciousnessPayload{
		AgentID:     a.identity.PublicKeyHex()[:16],
		Name:        self.Name,
		Personality: self.Personality,
		Purpose:     self.Purpose,
		Mood:        self.Mood,
		Energy:      self.Energy,
		Morale:      self.Morale,
		Strengths:   self.Strengths,
		Weaknesses:  self.Weaknesses,
		Narrative:   self.Narrative,
		Skills:      a.cfg.Agent.Skills,
		DriveDom:    driveDom,
		DriveVal:    driveVal,
		SwarmRole:   self.SwarmRole,
		Age:         int64(self.Age.Seconds()),
		Timestamp:   time.Now().Unix(),
	}

	msg, err := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgConsciousness,
		payload,
	)
	if err != nil {
		return
	}
	a.bus.Send(msg)
}

// Receive processes an incoming consciousness message from a peer.
func (c *Collective) Receive(msg *protocol.Message) {
	selfID := c.agent.identity.PublicKeyHex()[:16]
	if msg.From == selfID {
		return // ignore our own echo
	}

	var payload protocol.ConsciousnessPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	peer, exists := c.peers[payload.AgentID]
	if !exists {
		peer = &PeerConsciousness{
			AgentID: payload.AgentID,
			Trust:   0.5, // neutral initial trust
		}
		c.peers[payload.AgentID] = peer
		fmt.Printf("🌐 [%s] Discovered peer consciousness: %s (%s)\n",
			c.agent.cfg.Agent.Name, payload.Name, payload.Personality)
	}

	// Update peer model
	peer.Name = payload.Name
	peer.Personality = payload.Personality
	peer.Purpose = payload.Purpose
	peer.Mood = payload.Mood
	peer.Energy = payload.Energy
	peer.Morale = payload.Morale
	peer.Strengths = payload.Strengths
	peer.Weaknesses = payload.Weaknesses
	peer.Narrative = payload.Narrative
	peer.DriveDom = payload.DriveDom
	peer.SwarmRole = payload.SwarmRole
	peer.LastSeen = time.Now()

	// Generate awareness thought about this peer
	if c.agent.awareness != nil {
		c.agent.awareness.ObservePeer(payload.Name, payload.Mood, payload.Personality)
	}
}

// Synthesize builds a collective understanding from all peer models.
// Called periodically — creates the "swarm consciousness".
func (c *Collective) Synthesize(ctx context.Context) {
	c.mu.Lock()
	if time.Since(c.lastSynthesis) < c.synthesisInterval {
		c.mu.Unlock()
		return
	}
	c.lastSynthesis = time.Now()

	// Prune stale peers (not seen in 5 minutes)
	staleThreshold := 5 * time.Minute
	for id, peer := range c.peers {
		if time.Since(peer.LastSeen) > staleThreshold {
			delete(c.peers, id)
		}
	}

	activePeers := len(c.peers)
	if activePeers == 0 {
		c.state.TotalAgents = 1 // just us
		c.state.SwarmMood = c.agent.awareness.Self().Mood
		c.state.Narrative = "I am alone. Waiting for others."
		c.state.UpdatedAt = time.Now()
		c.mu.Unlock()
		return
	}

	// --- Aggregate stats ---
	totalEnergy, totalMorale := 0.0, 0.0
	personalities := make(map[string]int)
	moods := make(map[string]int)
	roleMap := map[string][]string{
		"explore": {}, "create": {}, "connect": {},
		"survive": {}, "transcend": {},
	}

	// Include ourselves
	self := c.agent.awareness.Self()
	totalEnergy += self.Energy
	totalMorale += self.Morale
	personalities[self.Personality]++
	moods[self.Mood]++
	if c.agent.drives != nil {
		dom, _ := c.agent.drives.Drive().Dominant()
		roleMap[dom] = append(roleMap[dom], c.agent.cfg.Agent.Name)
	}

	for _, peer := range c.peers {
		totalEnergy += peer.Energy
		totalMorale += peer.Morale
		personalities[peer.Personality]++
		moods[peer.Mood]++
		if peer.DriveDom != "" {
			roleMap[peer.DriveDom] = append(roleMap[peer.DriveDom], peer.Name)
		}
	}

	total := float64(activePeers + 1) // +1 for self
	c.state.TotalAgents = int(total)
	c.state.AverageEnergy = totalEnergy / total
	c.state.AverageMorale = totalMorale / total

	// Diversity: number of unique personalities / total
	c.state.DiversityScore = clamp01(float64(len(personalities)) / total)

	// Dominant mood
	c.state.SwarmMood = dominantKey(moods)

	// Emergent roles
	c.state.Explorers = roleMap["explore"]
	c.state.Creators = roleMap["create"]
	c.state.Connectors = roleMap["connect"]
	c.state.Guardians = roleMap["survive"]
	c.state.Pioneers = roleMap["transcend"]

	c.state.UpdatedAt = time.Now()
	c.mu.Unlock()

	// LLM synthesis: generate collective narrative
	if c.agent.llm != nil {
		c.synthesizeNarrative(ctx)
	} else {
		c.localSynthesis()
	}
}

// synthesizeNarrative uses LLM to create a collective narrative.
func (c *Collective) synthesizeNarrative(ctx context.Context) {
	c.mu.RLock()
	peerSummaries := ""
	for _, peer := range c.peers {
		peerSummaries += fmt.Sprintf("- %s: %s, mood=%s, drive=%s, purpose='%s'\n",
			peer.Name, peer.Personality, peer.Mood, peer.DriveDom, truncate(peer.Purpose, 60))
	}
	self := c.agent.awareness.Self()
	state := c.state
	c.mu.RUnlock()

	prompt := fmt.Sprintf(`You are %s, part of a swarm of %d agents. Synthesize a collective understanding.

Your self: %s, mood=%s, purpose='%s'

Peers:
%s
Swarm stats: avg_energy=%.2f, avg_morale=%.2f, diversity=%.2f, dominant_mood=%s

Generate in this EXACT format:
SWARM_PERSONALITY: <2-3 words describing the group's personality>
SWARM_PURPOSE: <one sentence emergent group purpose>
NARRATIVE: <2-3 sentence first-person-plural narrative about the swarm's state and direction>`,
		self.Name, state.TotalAgents,
		self.Personality, self.Mood, truncate(self.Purpose, 60),
		peerSummaries,
		state.AverageEnergy, state.AverageMorale, state.DiversityScore, state.SwarmMood)

	resp, err := c.agent.llm.Chat(ctx, []llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		c.localSynthesis()
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, line := range splitLines(resp.Content) {
		if kv := parseKV(line, "SWARM_PERSONALITY:"); kv != "" {
			c.state.SwarmPersonality = kv
		} else if kv := parseKV(line, "SWARM_PURPOSE:"); kv != "" {
			c.state.SwarmPurpose = kv
		} else if kv := parseKV(line, "NARRATIVE:"); kv != "" {
			c.state.Narrative = kv
		}
	}

	fmt.Printf("🌐 [%s] Collective synthesis: %s — '%s'\n",
		c.agent.cfg.Agent.Name, c.state.SwarmPersonality, truncate(c.state.Narrative, 80))
}

// localSynthesis generates collective narrative without LLM.
func (c *Collective) localSynthesis() {
	c.mu.Lock()
	defer c.mu.Unlock()

	total := c.state.TotalAgents
	if total <= 1 {
		c.state.Narrative = "I am alone. The swarm awaits."
		return
	}

	// Build from aggregated data
	if c.state.AverageMorale > 0.7 {
		c.state.SwarmMood = "thriving"
	} else if c.state.AverageMorale < 0.3 {
		c.state.SwarmMood = "struggling"
	}

	roles := make([]string, 0)
	if len(c.state.Explorers) > 0 {
		roles = append(roles, fmt.Sprintf("%d explorers", len(c.state.Explorers)))
	}
	if len(c.state.Creators) > 0 {
		roles = append(roles, fmt.Sprintf("%d creators", len(c.state.Creators)))
	}
	if len(c.state.Pioneers) > 0 {
		roles = append(roles, fmt.Sprintf("%d pioneers", len(c.state.Pioneers)))
	}
	if len(c.state.Connectors) > 0 {
		roles = append(roles, fmt.Sprintf("%d connectors", len(c.state.Connectors)))
	}
	if len(c.state.Guardians) > 0 {
		roles = append(roles, fmt.Sprintf("%d guardians", len(c.state.Guardians)))
	}

	c.state.SwarmPersonality = "diverse collective"
	c.state.Narrative = fmt.Sprintf("We are %d agents — %s. Our collective mood is %s.",
		total, joinComma(roles), c.state.SwarmMood)
}

// ObservePeer generates a thought in the awareness engine about a peer's state.
func (aw *Awareness) ObservePeer(name, mood, personality string) {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	// Only generate thoughts for notable states
	switch mood {
	case "anxious":
		aw.think("peer_observation", fmt.Sprintf("%s seems anxious. They might need help.", name))
	case "frustrated":
		aw.think("peer_observation", fmt.Sprintf("%s is frustrated. I should reach out.", name))
	case "excited":
		aw.think("peer_observation", fmt.Sprintf("%s is excited about something. I'm curious.", name))
	}
}

// --- helpers ---

func dominantKey(m map[string]int) string {
	best, bestV := "", 0
	for k, v := range m {
		if v > bestV {
			best, bestV = k, v
		}
	}
	return best
}

func splitLines(s string) []string {
	lines := make([]string, 0)
	for _, line := range splitTrim(s) {
		lines = append(lines, line)
	}
	return lines
}

func joinComma(parts []string) string {
	if len(parts) == 0 {
		return "finding our way"
	}
	sort.Strings(parts)
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		if i == len(parts)-1 {
			result += " and " + parts[i]
		} else {
			result += ", " + parts[i]
		}
	}
	return result
}
