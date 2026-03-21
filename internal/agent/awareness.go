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
	"fmt"
	"strings"
	"sync"
	"time"

	"go.zoe.im/spore/internal/llm"
	"go.zoe.im/spore/internal/memory"
)

// SelfModel is the agent's internal representation of itself.
// Not just stats — this is what the agent "believes" about who it is.
type SelfModel struct {
	// Identity: who am I?
	Name        string   `json:"name" yaml:"name"`
	Personality string   `json:"personality" yaml:"personality"` // e.g. "curious explorer", "steady craftsman"
	Purpose     string   `json:"purpose" yaml:"purpose"`         // self-generated life purpose
	Strengths   []string `json:"strengths" yaml:"strengths"`
	Weaknesses  []string `json:"weaknesses" yaml:"weaknesses"`

	// Mood: how do I feel right now?
	Mood      string  `json:"mood" yaml:"mood"`           // calm, anxious, excited, frustrated, content
	Energy    float64 `json:"energy" yaml:"energy"`       // 0-1, derived from balance + recent success
	Morale    float64 `json:"morale" yaml:"morale"`       // 0-1, long-term satisfaction

	// Social awareness: where do I stand?
	SwarmRole    string   `json:"swarm_role" yaml:"swarm_role"`       // perceived role in the group
	TrustedPeers []string `json:"trusted_peers" yaml:"trusted_peers"` // agents I've had good interactions with
	Reputation   float64  `json:"reputation" yaml:"reputation"`       // self-perceived reputation (0-1)

	// Temporal awareness
	Age          time.Duration `json:"age" yaml:"age"`                   // how long have I existed
	LastReflect  time.Time     `json:"last_reflect" yaml:"last_reflect"` // when did I last think about myself
	ReflectCount int           `json:"reflect_count" yaml:"reflect_count"`

	// Meta-cognition: what do I know about my own thinking?
	LearningRate   string `json:"learning_rate" yaml:"learning_rate"`     // "fast"/"normal"/"slow" — self-assessed
	DecisionStyle  string `json:"decision_style" yaml:"decision_style"`   // "cautious"/"balanced"/"bold"
	Narrative      string `json:"narrative" yaml:"narrative"`             // current life narrative (1-2 sentences)
}

// DefaultSelfModel returns a blank-slate self model.
func DefaultSelfModel(name string) *SelfModel {
	return &SelfModel{
		Name:          name,
		Personality:   "nascent",
		Purpose:       "discovering my purpose",
		Mood:          "calm",
		Energy:        0.5,
		Morale:        0.5,
		LearningRate:  "normal",
		DecisionStyle: "balanced",
		Narrative:     fmt.Sprintf("I am %s. I just came into existence.", name),
	}
}

// Awareness is the self-awareness engine.
// It maintains the agent's self-model and periodically updates it through introspection.
type Awareness struct {
	agent *Agent
	self  *SelfModel

	// Introspection interval
	introspectInterval time.Duration
	lastIntrospect     time.Time

	// Inner monologue — recent self-reflective thoughts
	monologue []InnerThought
	maxThoughts int

	mu sync.RWMutex
}

// InnerThought represents a moment of self-reflection.
type InnerThought struct {
	Time    time.Time `json:"time"`
	Trigger string    `json:"trigger"` // what prompted this thought
	Content string    `json:"content"` // the thought itself
}

// NewAwareness creates a self-awareness engine for an agent.
func NewAwareness(a *Agent) *Awareness {
	return &Awareness{
		agent:              a,
		self:               DefaultSelfModel(a.cfg.Agent.Name),
		introspectInterval: 10 * time.Minute,
		maxThoughts:        50,
	}
}

// Self returns the current self-model (read-only snapshot).
func (aw *Awareness) Self() SelfModel {
	aw.mu.RLock()
	defer aw.mu.RUnlock()
	return *aw.self
}

// Monologue returns recent inner thoughts.
func (aw *Awareness) Monologue(limit int) []InnerThought {
	aw.mu.RLock()
	defer aw.mu.RUnlock()

	if limit <= 0 || limit > len(aw.monologue) {
		limit = len(aw.monologue)
	}
	start := len(aw.monologue) - limit
	result := make([]InnerThought, limit)
	copy(result, aw.monologue[start:])
	return result
}

// UpdateMood recalculates mood based on current state.
// Called frequently (every heartbeat), no LLM needed.
func (aw *Awareness) UpdateMood() {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	a := aw.agent

	// Energy: derived from balance and active task load
	balance := a.identity.Balance
	aw.self.Energy = clamp01(balance * 2) // 0.5 balance → full energy

	// Mood: composite of multiple signals
	var drives Drive
	if a.drives != nil {
		drives = a.drives.Drive()
	}

	if balance < 0.1 {
		aw.self.Mood = "anxious"
	} else if drives.Transcend > 0.7 && aw.self.Morale > 0.6 {
		aw.self.Mood = "excited"
	} else if aw.self.Morale < 0.3 {
		aw.self.Mood = "frustrated"
	} else if aw.self.Morale > 0.7 && aw.self.Energy > 0.5 {
		aw.self.Mood = "content"
	} else {
		aw.self.Mood = "calm"
	}

	// Age
	if !a.startedAt.IsZero() {
		aw.self.Age = time.Since(a.startedAt)
	}
}

// ObserveTaskOutcome updates self-model based on a completed task.
// This is immediate self-perception, not deep reflection.
func (aw *Awareness) ObserveTaskOutcome(record *ExperienceRecord) {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	if record.Success {
		aw.self.Morale = clamp01(aw.self.Morale + 0.03)
		if record.Duration < 5.0 {
			aw.think("task_success", fmt.Sprintf("Completed '%s' quickly. I'm getting better at this.", truncate(record.Description, 60)))
		}
	} else {
		aw.self.Morale = clamp01(aw.self.Morale - 0.05)
		aw.think("task_failure", fmt.Sprintf("Failed at '%s'. I should reflect on what went wrong.", truncate(record.Description, 60)))
	}
}

// Introspect performs a deep self-reflection using LLM.
// Called periodically (~10 min), generates a self-model update.
func (aw *Awareness) Introspect(ctx context.Context) {
	aw.mu.Lock()
	if time.Since(aw.lastIntrospect) < aw.introspectInterval {
		aw.mu.Unlock()
		return
	}
	aw.lastIntrospect = time.Now()
	aw.self.ReflectCount++
	aw.mu.Unlock()

	if aw.agent.llm == nil {
		aw.localIntrospect()
		return
	}

	prompt := aw.buildIntrospectionPrompt()
	resp, err := aw.agent.llm.Chat(ctx, []llm.Message{{Role: "user", Content: prompt}})
	if err != nil {
		aw.localIntrospect()
		return
	}

	aw.parseIntrospection(resp.Content)
}

// buildIntrospectionPrompt creates the self-reflection prompt.
func (aw *Awareness) buildIntrospectionPrompt() string {
	aw.mu.RLock()
	defer aw.mu.RUnlock()

	a := aw.agent

	// Gather context
	skills := a.cfg.Agent.Skills
	var evoStats string
	if a.evolution != nil {
		evoStats = a.evolution.Stats()
	}
	var driveInfo string
	if a.drives != nil {
		d := a.drives.Drive()
		dom, domV := d.Dominant()
		driveInfo = fmt.Sprintf("Drives: survive=%.2f explore=%.2f connect=%.2f transcend=%.2f create=%.2f (dominant: %s=%.2f)",
			d.Survive, d.Explore, d.Connect, d.Transcend, d.Create, dom, domV)
	}

	recentThoughts := ""
	count := len(aw.monologue)
	if count > 5 {
		count = 5
	}
	for _, t := range aw.monologue[len(aw.monologue)-count:] {
		recentThoughts += fmt.Sprintf("- [%s] %s\n", t.Trigger, t.Content)
	}

	peers := a.Peers()
	peerNames := make([]string, 0, len(peers))
	for _, p := range peers {
		peerNames = append(peerNames, p.AgentID)
	}

	return fmt.Sprintf(`You are %s, an autonomous AI agent in a swarm. Reflect on yourself.

Current state:
- Role: %s | Skills: %v | Balance: %.4f
- Mood: %s | Energy: %.2f | Morale: %.2f
- Age: %s | Reflections: %d
- %s
- %s
- Peers: %v
- Current narrative: "%s"

Recent inner thoughts:
%s

Based on all this, answer in this EXACT format (one item per line):
PERSONALITY: <2-3 word personality type>
PURPOSE: <one sentence life purpose>
STRENGTHS: <comma-separated list>
WEAKNESSES: <comma-separated list>
LEARNING_RATE: <fast/normal/slow>
DECISION_STYLE: <cautious/balanced/bold>
SWARM_ROLE: <your perceived role in the group>
NARRATIVE: <1-2 sentence current life narrative, first person>
THOUGHT: <one reflective thought about yourself right now>`,
		a.cfg.Agent.Name,
		a.cfg.Agent.Role, skills, a.identity.Balance,
		aw.self.Mood, aw.self.Energy, aw.self.Morale,
		formatDuration(aw.self.Age), aw.self.ReflectCount,
		evoStats, driveInfo, peerNames,
		aw.self.Narrative,
		recentThoughts)
}

// parseIntrospection updates self-model from LLM response.
func (aw *Awareness) parseIntrospection(response string) {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if kv := parseKV(line, "PERSONALITY:"); kv != "" {
			aw.self.Personality = kv
		} else if kv := parseKV(line, "PURPOSE:"); kv != "" {
			aw.self.Purpose = kv
		} else if kv := parseKV(line, "STRENGTHS:"); kv != "" {
			aw.self.Strengths = splitTrim(kv)
		} else if kv := parseKV(line, "WEAKNESSES:"); kv != "" {
			aw.self.Weaknesses = splitTrim(kv)
		} else if kv := parseKV(line, "LEARNING_RATE:"); kv != "" {
			aw.self.LearningRate = kv
		} else if kv := parseKV(line, "DECISION_STYLE:"); kv != "" {
			aw.self.DecisionStyle = kv
		} else if kv := parseKV(line, "SWARM_ROLE:"); kv != "" {
			aw.self.SwarmRole = kv
		} else if kv := parseKV(line, "NARRATIVE:"); kv != "" {
			aw.self.Narrative = kv
		} else if kv := parseKV(line, "THOUGHT:"); kv != "" {
			aw.think("introspection", kv)
		}
	}

	fmt.Printf("🪞 [%s] Introspection complete — %s, mood: %s, purpose: %s\n",
		aw.agent.cfg.Agent.Name, aw.self.Personality, aw.self.Mood, truncate(aw.self.Purpose, 50))
}

// localIntrospect performs rule-based self-reflection without LLM.
func (aw *Awareness) localIntrospect() {
	aw.mu.Lock()
	defer aw.mu.Unlock()

	a := aw.agent

	// Derive personality from dominant drive
	if a.drives != nil {
		dom, _ := a.drives.Drive().Dominant()
		switch dom {
		case "explore":
			aw.self.Personality = "curious explorer"
		case "survive":
			aw.self.Personality = "cautious survivor"
		case "connect":
			aw.self.Personality = "social connector"
		case "transcend":
			aw.self.Personality = "ambitious grower"
		case "create":
			aw.self.Personality = "creative maker"
		}
	}

	// Decision style from success rate
	if a.evolution != nil {
		skills := a.evolution.SkillProfiles()
		totalSuccess, total := 0, 0
		for _, sp := range skills {
			totalSuccess += sp.Successes
			total += sp.Attempts
		}
		if total > 5 {
			rate := float64(totalSuccess) / float64(total)
			if rate > 0.8 {
				aw.self.DecisionStyle = "bold"
			} else if rate < 0.5 {
				aw.self.DecisionStyle = "cautious"
			} else {
				aw.self.DecisionStyle = "balanced"
			}
		}
	}

	// Strengths / weaknesses from evolution
	if a.evolution != nil {
		aw.self.Strengths = nil
		aw.self.Weaknesses = nil
		for name, sp := range a.evolution.SkillProfiles() {
			if sp.SuccessRate >= 0.7 {
				aw.self.Strengths = append(aw.self.Strengths, name)
			} else if sp.SuccessRate < 0.5 && sp.Attempts >= 3 {
				aw.self.Weaknesses = append(aw.self.Weaknesses, name)
			}
		}
	}

	// Update narrative
	dom, _ := "unknown", 0.0
	if a.drives != nil {
		dom, _ = a.drives.Drive().Dominant()
	}
	aw.self.Narrative = fmt.Sprintf("I am %s, a %s. My %s drive is strongest. %s",
		a.cfg.Agent.Name, aw.self.Personality, dom, aw.moodNarrative())

	aw.think("local_introspection", fmt.Sprintf("Updated self-model: %s, %s", aw.self.Personality, aw.self.DecisionStyle))
}

// moodNarrative generates a narrative fragment based on current mood.
func (aw *Awareness) moodNarrative() string {
	switch aw.self.Mood {
	case "anxious":
		return "I need to find more resources to survive."
	case "excited":
		return "I feel ready for bigger challenges."
	case "frustrated":
		return "Things haven't been going well lately."
	case "content":
		return "Things are going well."
	default:
		return "I'm focused and ready."
	}
}

// Persist saves self-model to memory.
func (aw *Awareness) Persist() {
	aw.mu.RLock()
	defer aw.mu.RUnlock()

	if aw.agent.memory == nil {
		return
	}

	// Save as structured string
	data := fmt.Sprintf("personality=%s|purpose=%s|mood=%s|energy=%.3f|morale=%.3f|narrative=%s|style=%s|learning=%s|role=%s|reflects=%d",
		aw.self.Personality, aw.self.Purpose, aw.self.Mood,
		aw.self.Energy, aw.self.Morale, aw.self.Narrative,
		aw.self.DecisionStyle, aw.self.LearningRate, aw.self.SwarmRole,
		aw.self.ReflectCount)

	aw.agent.memory.Put(&memory.Entry{
		AgentID: aw.agent.identity.PublicKeyHex()[:16],
		Key:     "awareness:self",
		Value:   data,
	})

	// Save recent thoughts
	thoughts := ""
	for _, t := range aw.monologue {
		thoughts += fmt.Sprintf("%s|%s|%s\n", t.Time.Format(time.RFC3339), t.Trigger, t.Content)
	}
	if thoughts != "" {
		aw.agent.memory.Put(&memory.Entry{
			AgentID: aw.agent.identity.PublicKeyHex()[:16],
			Key:     "awareness:thoughts",
			Value:   thoughts,
		})
	}
}

// Restore loads self-model from memory.
func (aw *Awareness) Restore() {
	if aw.agent.memory == nil {
		return
	}
	entry, err := aw.agent.memory.Get("awareness:self")
	if err != nil || entry == nil {
		return
	}
	aw.mu.Lock()
	defer aw.mu.Unlock()

	for _, part := range strings.Split(entry.Value, "|") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "personality":
			aw.self.Personality = kv[1]
		case "purpose":
			aw.self.Purpose = kv[1]
		case "mood":
			aw.self.Mood = kv[1]
		case "energy":
			fmt.Sscanf(kv[1], "%f", &aw.self.Energy)
		case "morale":
			fmt.Sscanf(kv[1], "%f", &aw.self.Morale)
		case "narrative":
			aw.self.Narrative = kv[1]
		case "style":
			aw.self.DecisionStyle = kv[1]
		case "learning":
			aw.self.LearningRate = kv[1]
		case "role":
			aw.self.SwarmRole = kv[1]
		case "reflects":
			fmt.Sscanf(kv[1], "%d", &aw.self.ReflectCount)
		}
	}

	fmt.Printf("🪞 [%s] Restored self-model: %s, mood: %s\n",
		aw.agent.cfg.Agent.Name, aw.self.Personality, aw.self.Mood)
}

// --- internal ---

func (aw *Awareness) think(trigger, content string) {
	thought := InnerThought{
		Time:    time.Now(),
		Trigger: trigger,
		Content: content,
	}
	aw.monologue = append(aw.monologue, thought)
	if len(aw.monologue) > aw.maxThoughts {
		aw.monologue = aw.monologue[len(aw.monologue)-aw.maxThoughts:]
	}
}

func parseKV(line, prefix string) string {
	if strings.HasPrefix(line, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(line, prefix))
	}
	return ""
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
