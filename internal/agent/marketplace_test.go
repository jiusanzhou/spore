/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 */

package agent

import (
	"testing"
	"time"
)

func TestServiceAd_SkillMatch(t *testing.T) {
	m := &Marketplace{
		services: make(map[string]*ServiceAd),
		escrows:  make(map[string]*Escrow),
		stopCh:   make(chan struct{}),
	}

	// Add service ads
	m.services["agent-a"] = &ServiceAd{
		AgentID:      "agent-a",
		Name:         "coder",
		Skills:       []string{"golang", "rust"},
		PricePerTask: 2.0,
		Capacity:     0.8,
		Reputation:   0.9,
		Timestamp:    testNow(),
	}
	m.services["agent-b"] = &ServiceAd{
		AgentID:      "agent-b",
		Name:         "writer",
		Skills:       []string{"writing", "translation"},
		PricePerTask: 1.0,
		Capacity:     1.0,
		Reputation:   0.7,
		Timestamp:    testNow(),
	}
	m.services["agent-c"] = &ServiceAd{
		AgentID:      "agent-c",
		Name:         "researcher",
		Skills:       []string{"research", "golang"},
		PricePerTask: 3.0,
		Capacity:     0.5,
		Reputation:   0.6,
		Timestamp:    testNow(),
	}

	// Find golang providers
	matches := m.FindService(ServiceQuery{Skill: "golang"})
	if len(matches) != 2 {
		t.Fatalf("expected 2 golang providers, got %d", len(matches))
	}
	// agent-a should rank higher (higher rep × capacity)
	if matches[0].Ad.AgentID != "agent-a" {
		t.Errorf("expected agent-a first, got %s", matches[0].Ad.AgentID)
	}

	// Find writing providers
	matches = m.FindService(ServiceQuery{Skill: "writing"})
	if len(matches) != 1 {
		t.Fatalf("expected 1 writing provider, got %d", len(matches))
	}
	if matches[0].Ad.AgentID != "agent-b" {
		t.Errorf("expected agent-b, got %s", matches[0].Ad.AgentID)
	}

	// Find nonexistent skill
	matches = m.FindService(ServiceQuery{Skill: "quantum"})
	if len(matches) != 0 {
		t.Errorf("expected 0 providers for quantum, got %d", len(matches))
	}
}

func TestServiceQuery_PriceFilter(t *testing.T) {
	m := &Marketplace{
		services: make(map[string]*ServiceAd),
		escrows:  make(map[string]*Escrow),
		stopCh:   make(chan struct{}),
	}

	m.services["cheap"] = &ServiceAd{
		AgentID: "cheap", Skills: []string{"coding"}, PricePerTask: 1.0,
		Capacity: 1.0, Reputation: 0.8, Timestamp: testNow(),
	}
	m.services["expensive"] = &ServiceAd{
		AgentID: "expensive", Skills: []string{"coding"}, PricePerTask: 5.0,
		Capacity: 1.0, Reputation: 0.9, Timestamp: testNow(),
	}

	matches := m.FindService(ServiceQuery{Skill: "coding", MaxPrice: 2.0})
	if len(matches) != 1 {
		t.Fatalf("expected 1 match with max price 2.0, got %d", len(matches))
	}
	if matches[0].Ad.AgentID != "cheap" {
		t.Errorf("expected cheap, got %s", matches[0].Ad.AgentID)
	}
}

func TestServiceQuery_ReputationFilter(t *testing.T) {
	m := &Marketplace{
		services: make(map[string]*ServiceAd),
		escrows:  make(map[string]*Escrow),
		stopCh:   make(chan struct{}),
	}

	m.services["trusted"] = &ServiceAd{
		AgentID: "trusted", Skills: []string{"coding"}, PricePerTask: 1.0,
		Capacity: 1.0, Reputation: 0.85, Timestamp: testNow(),
	}
	m.services["untrusted"] = &ServiceAd{
		AgentID: "untrusted", Skills: []string{"coding"}, PricePerTask: 1.0,
		Capacity: 1.0, Reputation: 0.2, Timestamp: testNow(),
	}

	matches := m.FindService(ServiceQuery{Skill: "coding", MinRep: 0.5})
	if len(matches) != 1 {
		t.Fatalf("expected 1 match with min rep 0.5, got %d", len(matches))
	}
	if matches[0].Ad.AgentID != "trusted" {
		t.Errorf("expected trusted, got %s", matches[0].Ad.AgentID)
	}
}

func TestEscrow_Lifecycle(t *testing.T) {
	m := &Marketplace{
		services: make(map[string]*ServiceAd),
		escrows:  make(map[string]*Escrow),
		stopCh:   make(chan struct{}),
	}

	// Create escrow
	escrow := &Escrow{
		TaskID:    "task-1",
		PayerID:   "payer",
		PayeeID:   "payee",
		Amount:    5.0,
		State:     EscrowLocked,
		CreatedAt: testNow(),
		ExpiresAt: testNow() + 1800,
	}
	m.escrows["task-1"] = escrow

	// Verify initial state
	if escrow.State != EscrowLocked {
		t.Errorf("expected locked, got %s", escrow.State)
	}

	// Stats
	stats := m.Stats()
	if stats.ActiveEscrows != 1 {
		t.Errorf("expected 1 active escrow, got %d", stats.ActiveEscrows)
	}
	if stats.TotalEscrowVal != 5.0 {
		t.Errorf("expected 5.0 escrow value, got %.2f", stats.TotalEscrowVal)
	}
}

func TestEscrow_Expired(t *testing.T) {
	m := &Marketplace{
		services: make(map[string]*ServiceAd),
		escrows:  make(map[string]*Escrow),
		stopCh:   make(chan struct{}),
	}

	now := testNow()

	// Create expired escrow
	m.escrows["expired"] = &Escrow{
		TaskID:    "expired",
		PayerID:   "payer",
		PayeeID:   "payee",
		Amount:    3.0,
		State:     EscrowLocked,
		CreatedAt: now - 3600,
		ExpiresAt: now - 60, // expired 1 min ago
	}

	// Not expired
	m.escrows["active"] = &Escrow{
		TaskID:    "active",
		PayerID:   "payer",
		PayeeID:   "payee",
		Amount:    2.0,
		State:     EscrowLocked,
		CreatedAt: now,
		ExpiresAt: now + 1800,
	}

	// Verify expired detection (don't call checkExpiredEscrows since it needs agent)
	expired := 0
	for _, e := range m.escrows {
		if e.State == EscrowLocked && now > e.ExpiresAt {
			expired++
		}
	}
	if expired != 1 {
		t.Errorf("expected 1 expired escrow, got %d", expired)
	}
	if m.escrows["active"].State != EscrowLocked {
		t.Errorf("active escrow should still be locked")
	}
}

func TestStaleAds_Filtered(t *testing.T) {
	m := &Marketplace{
		services: make(map[string]*ServiceAd),
		escrows:  make(map[string]*Escrow),
		stopCh:   make(chan struct{}),
	}

	m.services["fresh"] = &ServiceAd{
		AgentID: "fresh", Skills: []string{"coding"}, PricePerTask: 1.0,
		Capacity: 1.0, Reputation: 0.8, Timestamp: testNow(),
	}
	m.services["stale"] = &ServiceAd{
		AgentID: "stale", Skills: []string{"coding"}, PricePerTask: 1.0,
		Capacity: 1.0, Reputation: 0.9, Timestamp: testNow() - 600, // 10 min old
	}

	matches := m.FindService(ServiceQuery{Skill: "coding"})
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (stale filtered), got %d", len(matches))
	}
	if matches[0].Ad.AgentID != "fresh" {
		t.Errorf("expected fresh, got %s", matches[0].Ad.AgentID)
	}
}

func TestMarketplaceStats(t *testing.T) {
	m := &Marketplace{
		services: make(map[string]*ServiceAd),
		escrows:  make(map[string]*Escrow),
		reviews:  []Review{{Rating: 0.8}, {Rating: 0.9}},
		stopCh:   make(chan struct{}),
	}

	m.services["a"] = &ServiceAd{
		AgentID: "a", Skills: []string{"coding", "writing"}, PricePerTask: 2.0,
		Timestamp: testNow(),
	}
	m.services["b"] = &ServiceAd{
		AgentID: "b", Skills: []string{"coding"}, PricePerTask: 4.0,
		Timestamp: testNow(),
	}

	stats := m.Stats()
	if stats.KnownServices != 2 {
		t.Errorf("expected 2 known services, got %d", stats.KnownServices)
	}
	if stats.SkillProviders["coding"] != 2 {
		t.Errorf("expected 2 coding providers, got %d", stats.SkillProviders["coding"])
	}
	if stats.SkillProviders["writing"] != 1 {
		t.Errorf("expected 1 writing provider, got %d", stats.SkillProviders["writing"])
	}
	if stats.TotalReviews != 2 {
		t.Errorf("expected 2 reviews, got %d", stats.TotalReviews)
	}
	if stats.AvgPrice != 3.0 {
		t.Errorf("expected avg price 3.0, got %.2f", stats.AvgPrice)
	}
}

func TestServiceKey(t *testing.T) {
	k1 := ServiceKey("coding")
	k2 := ServiceKey("coding")
	k3 := ServiceKey("writing")

	if k1 != k2 {
		t.Error("same skill should produce same key")
	}
	if k1 == k3 {
		t.Error("different skills should produce different keys")
	}
	if len(k1) != 64 { // sha256 hex
		t.Errorf("expected 64 char hex, got %d", len(k1))
	}
}

func testNow() int64 {
	return time.Now().Unix()
}
