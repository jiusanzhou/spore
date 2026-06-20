// Package agent — persistence + collective memory side of the Agent.
//
// This file holds methods that persist agent state across two stores:
//   - structured memory (preferences, events, evolution side-effects), and
//   - collective memory (IPFS / content-addressed store via the P2P bus).
//
// Split out of agent.go during the Phase 3 refactor to keep agent.go focused on
// lifecycle (New/Run/Close). All methods here are pure side-effect: they read
// agent state, format it, and push to a store. No control-flow or scheduling.
package agent

import (
	"fmt"
	"strings"
	"time"

	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/protocol"
	"go.zoe.im/spore/internal/runtime"
)

// recordEvolution updates the evolution engine, drives, awareness, and persists
// preferences + a milestone event after a task finishes. Also kicks off async
// skill analysis when an analyzer is wired in.
func (a *Agent) recordEvolution(entry *taskEntry, output *runtime.TaskOutput, rtName string, success bool, errMsg string) {
	if a.evolution == nil {
		return
	}
	duration := time.Since(entry.CreatedAt).Seconds()
	rec := &ExperienceRecord{
		TaskID:      entry.ID,
		Description: entry.Description,
		Runtime:     rtName,
		Success:     success,
		Duration:    duration,
		Error:       errMsg,
		Skills:      a.cfg.Agent.Skills, // use declared skills for now
	}
	a.evolution.Record(rec)

	// Adapt intrinsic drives based on experience
	if a.drives != nil {
		a.drives.Adapt(rec)
	}

	// Update self-awareness from task outcome
	if a.awareness != nil {
		a.awareness.ObserveTaskOutcome(rec)
	}

	// Store preferences after evolution cycle
	a.storePreferencesContext()

	// Store milestone event
	verb := "completed"
	if !success {
		verb = "failed"
	}
	a.storeEventContext(fmt.Sprintf("task_%s", verb), entry.ID,
		fmt.Sprintf("Task %s via %s: %s", verb, rtName, truncate(entry.Description, 60)))

	// Skill evolution: post-task analysis + evolution (async)
	if a.analyzer != nil && output != nil {
		go a.runSkillAnalysis(entry, output, rtName, duration)
	}
}

// storePreferencesContext persists the agent's runtime/strategy preferences as structured memory.
func (a *Agent) storePreferencesContext() {
	if a.evolution == nil || a.memory == nil {
		return
	}
	ctxStore, ok := a.memory.(memory.ContextStore)
	if !ok {
		return
	}

	agentID := a.identity.PublicKeyHex()[:16]
	strat := a.evolution.Strategy()

	// Build skill confidence summary
	var skillLines []string
	for name, sp := range a.evolution.SkillProfiles() {
		skillLines = append(skillLines, fmt.Sprintf("- %s: %.0f%% success (%d/%d), trend: %s",
			name, sp.SuccessRate*100, sp.Successes, sp.Attempts, sp.Trend))
	}

	l0 := fmt.Sprintf("Prefers %s runtime, %d skills tracked", strat.PreferredRuntime, len(skillLines))
	l1 := fmt.Sprintf("## Runtime Preferences\n\n**Preferred**: %s\n**Scores**: %v\n\n## Skill Confidence\n\n%s",
		strat.PreferredRuntime,
		strat.RuntimeScores,
		strings.Join(skillLines, "\n"))

	entry := &memory.ContextEntry{
		URI:      fmt.Sprintf("spore://%s/memory/preferences", agentID),
		AgentID:  agentID,
		Type:     memory.CtxMemory,
		Category: memory.CatPreferences,
		L0:       l0,
		L1:       l1,
		L2:       l1,
		Source:   "evolution",
	}
	ctxStore.PutContext(entry)
}

// storeEventContext stores a key event/milestone in structured memory.
func (a *Agent) storeEventContext(eventType, eventID, summary string) {
	if a.memory == nil {
		return
	}
	ctxStore, ok := a.memory.(memory.ContextStore)
	if !ok {
		return
	}

	agentID := a.identity.PublicKeyHex()[:16]
	eid := eventID
	if len(eid) > 8 {
		eid = eid[:8]
	}
	entry := &memory.ContextEntry{
		URI:      fmt.Sprintf("spore://%s/memory/events/%s-%s", agentID, eventType, eid),
		AgentID:  agentID,
		Type:     memory.CtxMemory,
		Category: memory.CatEvents,
		L0:       summary,
		L1: fmt.Sprintf("## Event: %s\n\n**Type**: %s\n**ID**: %s\n**Time**: %s\n\n%s",
			summary, eventType, eventID, time.Now().Format(time.RFC3339), summary),
		Source: eventType,
		Metadata: map[string]string{
			"event_type": eventType,
			"event_id":   eventID,
		},
	}
	ctxStore.PutContext(entry)
}

// publishToIPFS stores content in the collective memory store (IPFS + SQLite).
func (a *Agent) publishToIPFS(data []byte, contentType, summary string) {
	if len(data) == 0 {
		return // nothing to publish
	}
	p2pBus, ok := a.bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		return
	}
	agentID := a.identity.PublicKeyHex()[:16]
	ref, err := p2pBus.Content.Put(data, contentType, agentID, summary)
	if err != nil {
		fmt.Printf("⚠️  [%s] IPFS publish failed: %v\n", a.cfg.Agent.Name, err)
		return
	}
	ipfsPart := ""
	if ref.IPFSCID != "" {
		ipfsPart = fmt.Sprintf(" ipfs=%s", ref.IPFSCID[:16])
	}
	fmt.Printf("📦 [%s] Published %s to collective memory: %s%s\n",
		a.cfg.Agent.Name, contentType, ref.CID[:12], ipfsPart)
}

// publishSkillToIPFS serializes a skill to Markdown, stores in IPFS, and broadcasts CID.
func (a *Agent) publishSkillToIPFS(rec *SkillRecord) {
	p2pBus, ok := a.bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		return
	}

	md := SkillToMarkdown(rec)
	agentID := a.identity.PublicKeyHex()[:16]
	summary := fmt.Sprintf("Skill: %s (origin=%s, gen=%d)", rec.Name, rec.Origin, rec.Generation)

	ref, err := p2pBus.Content.Put([]byte(md), "skill", agentID, summary)
	if err != nil {
		fmt.Printf("⚠️  [%s] Failed to publish skill %s: %v\n", a.cfg.Agent.Name, rec.Name, err)
		return
	}

	ipfsPart := ""
	if ref.IPFSCID != "" {
		ipfsPart = fmt.Sprintf(" ipfs=%s", ref.IPFSCID[:16])
	}
	fmt.Printf("📦 [%s] Skill published: %s → %s%s\n",
		a.cfg.Agent.Name, rec.Name, ref.CID[:12], ipfsPart)

	// Broadcast CID to swarm
	msg, err := protocol.NewMessage(agentID, "broadcast", protocol.MsgContentAnnounce, ref)
	if err == nil {
		a.bus.Send(msg)
	}
}

// broadcastSkillCID broadcasts a SkillFS skill's IPFS CID to the swarm.
func (a *Agent) broadcastSkillCID(skill *Skill) {
	p2pBus, ok := a.bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		return
	}

	agentID := a.identity.PublicKeyHex()[:16]
	ref := network.ContentRef{
		CID:       skill.Meta.ContentHash,
		IPFSCID:   skill.Meta.IPFSCID,
		AgentID:   agentID,
		Type:      "skill",
		Summary:   fmt.Sprintf("Skill: %s (origin=%s, gen=%d)", skill.Meta.Name, skill.Meta.Origin, skill.Meta.Generation),
		Timestamp: time.Now().Unix(),
	}

	msg, err := protocol.NewMessage(agentID, "broadcast", protocol.MsgContentAnnounce, &ref)
	if err == nil {
		a.bus.Send(msg)
		fmt.Printf("📡 [%s] Broadcast skill CID: %s → %s\n",
			a.cfg.Agent.Name, skill.Meta.Name, truncateCID(skill.Meta.IPFSCID))
	}
}
