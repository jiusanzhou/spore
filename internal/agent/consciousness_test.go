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
	"testing"
	"time"

	"go.zoe.im/spore/internal/protocol"
)

func TestCollective_DefaultState(t *testing.T) {
	cfg := DefaultConfig("test-collective", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	state := a.collective.State()
	if state.SwarmMood != "nascent" {
		t.Errorf("initial swarm mood: got %q, want 'nascent'", state.SwarmMood)
	}
}

func TestCollective_ReceivePeer(t *testing.T) {
	cfg := DefaultConfig("test-recv", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Simulate receiving a consciousness message from another agent
	payload := protocol.ConsciousnessPayload{
		AgentID:     "peer-abc12345",
		Name:        "explorer-1",
		Personality: "curious explorer",
		Purpose:     "discover new domains",
		Mood:        "excited",
		Energy:      0.8,
		Morale:      0.9,
		Strengths:   []string{"coding", "research"},
		Narrative:   "I am explorer-1, venturing into unknown territory.",
		DriveDom:    "explore",
		DriveVal:    0.85,
		Timestamp:   time.Now().Unix(),
	}

	payloadBytes, _ := json.Marshal(payload)
	msg := &protocol.Message{
		From:    "peer-abc12345",
		Type:    protocol.MsgConsciousness,
		Payload: payloadBytes,
	}

	a.collective.Receive(msg)

	peers := a.collective.Peers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}

	peer := peers["peer-abc12345"]
	if peer == nil {
		t.Fatal("peer not found")
	}
	if peer.Personality != "curious explorer" {
		t.Errorf("peer personality: got %q", peer.Personality)
	}
	if peer.Mood != "excited" {
		t.Errorf("peer mood: got %q", peer.Mood)
	}
	if peer.Trust != 0.5 {
		t.Errorf("initial trust: got %.2f, want 0.50", peer.Trust)
	}
}

func TestCollective_IgnoreSelf(t *testing.T) {
	cfg := DefaultConfig("test-self-ignore", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	selfID := a.identity.PublicKeyHex()[:16]
	payload := protocol.ConsciousnessPayload{
		AgentID: selfID,
		Name:    "myself",
	}

	payloadBytes, _ := json.Marshal(payload)
	msg := &protocol.Message{
		From:    selfID,
		Type:    protocol.MsgConsciousness,
		Payload: payloadBytes,
	}

	a.collective.Receive(msg)

	peers := a.collective.Peers()
	if len(peers) != 0 {
		t.Errorf("should ignore self, got %d peers", len(peers))
	}
}

func TestCollective_SynthesizeLocal(t *testing.T) {
	cfg := DefaultConfig("test-synth", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Add some peers
	for _, name := range []string{"alpha", "beta", "gamma"} {
		payload := protocol.ConsciousnessPayload{
			AgentID:     "peer-" + name,
			Name:        name,
			Personality: "worker",
			Mood:        "calm",
			Energy:      0.6,
			Morale:      0.7,
			DriveDom:    "explore",
			Timestamp:   time.Now().Unix(),
		}
		payloadBytes, _ := json.Marshal(payload)
		a.collective.Receive(&protocol.Message{
			From:    "peer-" + name,
			Type:    protocol.MsgConsciousness,
			Payload: payloadBytes,
		})
	}

	// Force synthesis
	a.collective.synthesisInterval = 0
	a.collective.Synthesize(context.Background())

	state := a.collective.State()
	if state.TotalAgents != 4 { // 3 peers + self
		t.Errorf("total agents: got %d, want 4", state.TotalAgents)
	}
	if state.Narrative == "" {
		t.Error("narrative should not be empty after synthesis")
	}
	if state.SwarmPersonality == "" {
		t.Error("swarm personality should not be empty")
	}
	if state.DiversityScore == 0 {
		t.Error("diversity score should be > 0")
	}
}

func TestCollective_StalePeerPrune(t *testing.T) {
	cfg := DefaultConfig("test-prune", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Add peer with old timestamp
	a.collective.mu.Lock()
	a.collective.peers["stale-peer"] = &PeerConsciousness{
		AgentID:  "stale-peer",
		Name:     "stale",
		LastSeen: time.Now().Add(-10 * time.Minute), // 10 min ago
	}
	a.collective.mu.Unlock()

	// Synthesize should prune stale peer
	a.collective.synthesisInterval = 0
	a.collective.Synthesize(context.Background())

	peers := a.collective.Peers()
	if _, exists := peers["stale-peer"]; exists {
		t.Error("stale peer should have been pruned")
	}
}

func TestCollective_EmergentRoles(t *testing.T) {
	cfg := DefaultConfig("test-roles", "gpt-4o-mini")
	cfg.Memory.Path = ""
	cfg.Drive = &Drive{Explore: 0.9, Survive: 0.1, Connect: 0.2, Transcend: 0.3, Create: 0.2}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Add peers with different drives
	peers := []struct {
		name     string
		driveDom string
	}{
		{"creator-1", "create"},
		{"guardian-1", "survive"},
		{"connector-1", "connect"},
	}

	for _, p := range peers {
		payload := protocol.ConsciousnessPayload{
			AgentID:  "peer-" + p.name,
			Name:     p.name,
			Mood:     "calm",
			Energy:   0.5,
			Morale:   0.5,
			DriveDom: p.driveDom,
		}
		payloadBytes, _ := json.Marshal(payload)
		a.collective.Receive(&protocol.Message{
			From:    "peer-" + p.name,
			Type:    protocol.MsgConsciousness,
			Payload: payloadBytes,
		})
	}

	a.collective.synthesisInterval = 0
	a.collective.Synthesize(context.Background())

	state := a.collective.State()
	if len(state.Explorers) != 1 {
		t.Errorf("explorers: got %d, want 1 (self)", len(state.Explorers))
	}
	if len(state.Creators) != 1 {
		t.Errorf("creators: got %d, want 1", len(state.Creators))
	}
	if len(state.Guardians) != 1 {
		t.Errorf("guardians: got %d, want 1", len(state.Guardians))
	}
	if len(state.Connectors) != 1 {
		t.Errorf("connectors: got %d, want 1", len(state.Connectors))
	}
}

func TestCollective_PeerTrigersThought(t *testing.T) {
	cfg := DefaultConfig("test-peer-thought", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	// Send anxious peer consciousness
	payload := protocol.ConsciousnessPayload{
		AgentID:     "peer-anxious",
		Name:        "worried-one",
		Personality: "cautious",
		Mood:        "anxious",
		Energy:      0.2,
		Morale:      0.2,
	}
	payloadBytes, _ := json.Marshal(payload)
	a.collective.Receive(&protocol.Message{
		From:    "peer-anxious",
		Type:    protocol.MsgConsciousness,
		Payload: payloadBytes,
	})

	// Should have generated a thought
	thoughts := a.awareness.Monologue(10)
	found := false
	for _, t := range thoughts {
		if t.Trigger == "peer_observation" {
			found = true
			break
		}
	}
	if !found {
		t.Error("receiving anxious peer should trigger a thought")
	}
}

func TestCollective_InfoIncluded(t *testing.T) {
	cfg := DefaultConfig("test-info-coll", "gpt-4o-mini")
	cfg.Memory.Path = ""

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	defer a.memory.Close()

	info := a.Info()
	if info.Collective == nil {
		t.Fatal("Info should include collective state")
	}
}

func TestJoinComma(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{nil, "finding our way"},
		{[]string{"explorers"}, "explorers"},
		{[]string{"explorers", "creators"}, "creators and explorers"},
		{[]string{"a", "b", "c"}, "a, b and c"},
	}
	for _, tt := range tests {
		got := joinComma(tt.in)
		if got != tt.want {
			t.Errorf("joinComma(%v): got %q, want %q", tt.in, got, tt.want)
		}
	}
}
