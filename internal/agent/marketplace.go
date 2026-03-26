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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/protocol"
)

// ── Service Registry ──────────────────────────────────────

// ServiceAd advertises an agent's available services on the marketplace.
type ServiceAd struct {
	AgentID     string   `json:"agent_id"`
	OwnerID     string   `json:"owner_id"`      // owner's public key (who deployed this agent)
	Name        string   `json:"name"`
	Skills      []string `json:"skills"`
	PricePerTask float64 `json:"price_per_task"` // token cost per task
	Capacity    float64  `json:"capacity"`       // 0.0 = busy, 1.0 = idle
	Reputation  float64  `json:"reputation"`
	Uptime      int64    `json:"uptime_secs"`
	Timestamp   int64    `json:"timestamp"`
}

// ServiceQuery is a request to find agents that can perform a skill.
type ServiceQuery struct {
	Skill      string  `json:"skill"`
	MaxPrice   float64 `json:"max_price,omitempty"`  // 0 = any price
	MinRep     float64 `json:"min_rep,omitempty"`    // 0 = any reputation
	MaxResults int     `json:"max_results,omitempty"` // 0 = default 10
}

// ServiceMatch is a ranked result from a service query.
type ServiceMatch struct {
	Ad    ServiceAd `json:"ad"`
	Score float64   `json:"score"` // composite ranking score
}

// ── Escrow ────────────────────────────────────────────────

// EscrowState tracks locked funds for a cross-owner task.
type EscrowState string

const (
	EscrowLocked   EscrowState = "locked"
	EscrowReleased EscrowState = "released"
	EscrowRefunded EscrowState = "refunded"
	EscrowDisputed EscrowState = "disputed"
)

// Escrow holds tokens for a task until verification.
type Escrow struct {
	TaskID    string      `json:"task_id"`
	PayerID   string      `json:"payer_id"`    // who locked the funds
	PayeeID   string      `json:"payee_id"`    // who should receive on completion
	Amount    float64     `json:"amount"`
	State     EscrowState `json:"state"`
	CreatedAt int64       `json:"created_at"`
	ExpiresAt int64       `json:"expires_at"`  // auto-refund deadline
	ResultCID string      `json:"result_cid,omitempty"` // IPFS CID of the result (proof)
}

// ── Protocol Messages ─────────────────────────────────────

const (
	MsgServiceAd     protocol.MessageType = "service_ad"
	MsgServiceQuery  protocol.MessageType = "service_query"
	MsgServiceReply  protocol.MessageType = "service_reply"
	MsgTaskOffer     protocol.MessageType = "task_offer"     // payer → payee: "do this, escrow locked"
	MsgTaskAccept    protocol.MessageType = "task_accept"    // payee → payer: "accepted, working"
	MsgTaskDeliver   protocol.MessageType = "task_deliver"   // payee → payer: "done, here's result CID"
	MsgEscrowRelease protocol.MessageType = "escrow_release" // payer → payee: "verified, tokens yours"
	MsgEscrowDispute protocol.MessageType = "escrow_dispute" // either → network: "dispute"
	MsgReviewPost    protocol.MessageType = "review_post"    // payer → network: "rating + review"
)

// TaskOffer is sent from payer to a specific agent offering paid work.
type TaskOffer struct {
	TaskID      string  `json:"task_id"`
	Description string  `json:"description"`
	Payment     float64 `json:"payment"`       // escrowed amount
	Skill       string  `json:"skill"`
	Deadline    int64   `json:"deadline"`       // unix timestamp
	PayerID     string  `json:"payer_id"`
}

// TaskAcceptance confirms an agent will work on the task.
type TaskAcceptance struct {
	TaskID  string `json:"task_id"`
	AgentID string `json:"agent_id"`
}

// TaskDelivery contains the result and proof CID.
type TaskDelivery struct {
	TaskID    string `json:"task_id"`
	ResultCID string `json:"result_cid"` // IPFS CID of the full result
	Summary   string `json:"summary"`
	AgentID   string `json:"agent_id"`
}

// EscrowAction is the release/dispute message.
type EscrowAction struct {
	TaskID string `json:"task_id"`
	Action string `json:"action"` // release, dispute, refund
	Reason string `json:"reason,omitempty"`
}

// Review is a post-task rating stored on IPFS.
type Review struct {
	TaskID     string  `json:"task_id"`
	ReviewerID string  `json:"reviewer_id"` // payer
	RevieweeID string  `json:"reviewee_id"` // worker
	Rating     float64 `json:"rating"`      // 0.0-1.0
	Comment    string  `json:"comment"`
	Timestamp  int64   `json:"timestamp"`
	Signature  string  `json:"signature"`   // Ed25519 signature of the review
}

// ── Marketplace Engine ────────────────────────────────────

// Marketplace manages service discovery, escrow, and cross-owner coordination.
type Marketplace struct {
	agent *Agent

	mu       sync.RWMutex
	services map[string]*ServiceAd // agentID → latest ad
	escrows  map[string]*Escrow    // taskID → escrow
	reviews  []Review              // received reviews

	// Config
	adInterval    time.Duration
	escrowTimeout time.Duration

	stopCh chan struct{}
}

// MarketplaceConfig controls marketplace behavior.
type MarketplaceConfig struct {
	// PricePerTask is the default price for tasks (0 = free/cooperative).
	PricePerTask float64 `toml:"price_per_task" yaml:"price_per_task" json:"price_per_task"`

	// Enabled turns on the marketplace service ads and cross-owner tasks.
	Enabled bool `toml:"enabled" yaml:"enabled" json:"enabled"`

	// AdIntervalSecs is how often to broadcast service ads (default 30s).
	AdIntervalSecs int `toml:"ad_interval_secs" yaml:"ad_interval_secs" json:"ad_interval_secs"`

	// EscrowTimeoutMins is how long escrow waits before auto-refund (default 30m).
	EscrowTimeoutMins int `toml:"escrow_timeout_mins" yaml:"escrow_timeout_mins" json:"escrow_timeout_mins"`
}

// DefaultMarketplaceConfig returns sensible defaults.
func DefaultMarketplaceConfig() MarketplaceConfig {
	return MarketplaceConfig{
		PricePerTask:      1.0,
		Enabled:           true,
		AdIntervalSecs:    30,
		EscrowTimeoutMins: 30,
	}
}

// NewMarketplace creates a marketplace for an agent.
func NewMarketplace(agent *Agent, cfg MarketplaceConfig) *Marketplace {
	adInterval := time.Duration(cfg.AdIntervalSecs) * time.Second
	if adInterval < 10*time.Second {
		adInterval = 30 * time.Second
	}
	escrowTimeout := time.Duration(cfg.EscrowTimeoutMins) * time.Minute
	if escrowTimeout < time.Minute {
		escrowTimeout = 30 * time.Minute
	}

	return &Marketplace{
		agent:         agent,
		services:      make(map[string]*ServiceAd),
		escrows:       make(map[string]*Escrow),
		adInterval:    adInterval,
		escrowTimeout: escrowTimeout,
		stopCh:        make(chan struct{}),
	}
}

// Start begins periodic service advertising and escrow monitoring.
func (m *Marketplace) Start() {
	go m.advertiseLoop()
	go m.escrowWatchdog()
}

// Stop halts the marketplace.
func (m *Marketplace) Stop() {
	close(m.stopCh)
}

// ── Service Advertising ───────────────────────────────────

// advertiseLoop periodically broadcasts this agent's service ad.
func (m *Marketplace) advertiseLoop() {
	// Initial ad after short delay
	time.Sleep(3 * time.Second)
	m.broadcastServiceAd()

	ticker := time.NewTicker(m.adInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.broadcastServiceAd()
		case <-m.stopCh:
			return
		}
	}
}

// broadcastServiceAd publishes this agent's capabilities and pricing to the network.
func (m *Marketplace) broadcastServiceAd() {
	a := m.agent
	selfID := a.identity.PublicKeyHex()[:16]

	// Collect skills from config + evolved skills
	skills := make([]string, len(a.cfg.Agent.Skills))
	copy(skills, a.cfg.Agent.Skills)
	if a.skillStore != nil {
		active, err := a.skillStore.ActiveSkills()
		if err == nil {
			for _, s := range active {
				found := false
				for _, cs := range skills {
					if cs == s.Name {
						found = true
						break
					}
				}
				if !found {
					skills = append(skills, s.Name)
				}
			}
		}
	}

	// Reputation
	rep := 0.5
	if a.reputation != nil {
		rep = a.reputation.SelfScore()
	}

	// Capacity based on queue
	capacity := 1.0
	a.mu.RLock()
	queueLen := len(a.taskQueue)
	a.mu.RUnlock()
	if queueLen > 0 {
		capacity = math.Max(0, 1.0-float64(queueLen)*0.25)
	}

	ad := ServiceAd{
		AgentID:      selfID,
		OwnerID:      a.identity.PublicKeyHex(), // full key as owner ID
		Name:         a.cfg.Agent.Name,
		Skills:       skills,
		PricePerTask: a.cfg.Marketplace.PricePerTask,
		Capacity:     capacity,
		Reputation:   rep,
		Uptime:       int64(time.Since(a.startedAt).Seconds()),
		Timestamp:    time.Now().Unix(),
	}

	msg, err := protocol.NewMessage(selfID, "broadcast", MsgServiceAd, ad)
	if err != nil {
		return
	}
	a.bus.Send(msg)

	// Also register locally (for same-swarm visibility)
	m.mu.Lock()
	m.services[selfID] = &ad
	m.mu.Unlock()
}

// HandleServiceAd processes incoming service advertisements.
func (m *Marketplace) HandleServiceAd(ad *ServiceAd) {
	selfID := m.agent.identity.PublicKeyHex()[:16]
	if ad.AgentID == selfID {
		return
	}

	m.mu.Lock()
	m.services[ad.AgentID] = ad
	m.mu.Unlock()
}

// FindService queries the local service registry for agents matching a skill.
func (m *Marketplace) FindService(q ServiceQuery) []ServiceMatch {
	m.mu.RLock()
	defer m.mu.RUnlock()

	maxResults := q.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}

	var matches []ServiceMatch
	now := time.Now().Unix()

	for _, ad := range m.services {
		// Stale ads (> 5 min old)
		if now-ad.Timestamp > 300 {
			continue
		}

		// Skill match
		hasSkill := false
		for _, s := range ad.Skills {
			if s == q.Skill {
				hasSkill = true
				break
			}
		}
		if !hasSkill {
			continue
		}

		// Price filter
		if q.MaxPrice > 0 && ad.PricePerTask > q.MaxPrice {
			continue
		}

		// Reputation filter
		if q.MinRep > 0 && ad.Reputation < q.MinRep {
			continue
		}

		// Composite score: reputation × capacity × (1 / price)
		priceScore := 1.0
		if ad.PricePerTask > 0 {
			priceScore = 1.0 / ad.PricePerTask
		}
		score := ad.Reputation * ad.Capacity * priceScore

		matches = append(matches, ServiceMatch{Ad: *ad, Score: score})
	}

	// Sort by score descending
	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].Score > matches[i].Score {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}

	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	return matches
}

// ── Cross-Owner Task Flow ─────────────────────────────────

// OfferTask sends a paid task to a specific remote agent with escrow.
func (m *Marketplace) OfferTask(ctx context.Context, targetAgentID, description, skill string, payment float64) (string, error) {
	a := m.agent
	selfID := a.identity.PublicKeyHex()[:16]

	// Check balance
	if a.identity.Balance < payment {
		return "", fmt.Errorf("insufficient balance: %.2f < %.2f", a.identity.Balance, payment)
	}

	taskID := fmt.Sprintf("mkt-%x", time.Now().UnixNano())[:12]

	// Lock funds in escrow
	a.identity.Debit(payment)
	escrow := &Escrow{
		TaskID:    taskID,
		PayerID:   selfID,
		PayeeID:   targetAgentID,
		Amount:    payment,
		State:     EscrowLocked,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(m.escrowTimeout).Unix(),
	}

	m.mu.Lock()
	m.escrows[taskID] = escrow
	m.mu.Unlock()

	// Send offer
	offer := TaskOffer{
		TaskID:      taskID,
		Description: description,
		Payment:     payment,
		Skill:       skill,
		Deadline:    escrow.ExpiresAt,
		PayerID:     selfID,
	}

	msg, err := protocol.NewMessage(selfID, targetAgentID, MsgTaskOffer, offer)
	if err != nil {
		// Refund on error
		a.identity.Credit(payment)
		m.mu.Lock()
		delete(m.escrows, taskID)
		m.mu.Unlock()
		return "", err
	}

	if err := a.bus.Send(msg); err != nil {
		a.identity.Credit(payment)
		m.mu.Lock()
		delete(m.escrows, taskID)
		m.mu.Unlock()
		return "", err
	}

	fmt.Printf("🏪 [%s] Offered task %s to %s (payment=%.2f, skill=%s)\n",
		a.cfg.Agent.Name, taskID, targetAgentID[:8], payment, skill)

	if a.tokens != nil {
		a.tokens.mu.Lock()
		a.tokens.record("escrow", -payment, fmt.Sprintf("escrow_lock:%s", taskID))
		a.tokens.mu.Unlock()
	}

	return taskID, nil
}

// HandleTaskOffer processes an incoming paid task offer.
func (m *Marketplace) HandleTaskOffer(offer *TaskOffer, fromAgent string) {
	a := m.agent
	selfID := a.identity.PublicKeyHex()[:16]

	fmt.Printf("🏪 [%s] Received task offer %s from %s (payment=%.2f)\n",
		a.cfg.Agent.Name, offer.TaskID, fromAgent[:min(8, len(fromAgent))], offer.Payment)

	// Check if we can handle this
	if !a.tokens.CanThink() {
		return // too poor to work
	}

	// Accept the offer
	accept := TaskAcceptance{
		TaskID:  offer.TaskID,
		AgentID: selfID,
	}
	msg, _ := protocol.NewMessage(selfID, fromAgent, MsgTaskAccept, accept)
	a.bus.Send(msg)

	// Execute the task (preserve offer's task ID for escrow matching)
	go func() {
		a.submitTaskWithID(offer.TaskID, offer.Description)
	}()

	// Store escrow info locally (payee side)
	m.mu.Lock()
	m.escrows[offer.TaskID] = &Escrow{
		TaskID:    offer.TaskID,
		PayerID:   fromAgent,
		PayeeID:   selfID,
		Amount:    offer.Payment,
		State:     EscrowLocked,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: offer.Deadline,
	}
	m.mu.Unlock()
}

// DeliverResult is called after a marketplace task completes.
// Publishes result to IPFS and notifies payer.
func (m *Marketplace) DeliverResult(taskID, result string, success bool) {
	m.mu.RLock()
	escrow, ok := m.escrows[taskID]
	m.mu.RUnlock()
	if !ok {
		return // not a marketplace task
	}

	a := m.agent
	selfID := a.identity.PublicKeyHex()[:16]

	// Publish result to IPFS for proof
	var resultCID string
	if p2pBus, ok := a.bus.(*network.P2PBus); ok && p2pBus.Content != nil {
		ref, err := p2pBus.Content.Put(
			[]byte(result),
			"task_result",
			selfID,
			fmt.Sprintf("Result for task %s", taskID),
		)
		if err == nil {
			resultCID = ref.CID
			escrow.ResultCID = resultCID
		}
	}

	if !success {
		// Failed — notify payer, they can dispute or refund
		fmt.Printf("🏪 [%s] Marketplace task %s failed\n", a.cfg.Agent.Name, taskID)
		return
	}

	delivery := TaskDelivery{
		TaskID:    taskID,
		ResultCID: resultCID,
		Summary:   truncate(result, 200),
		AgentID:   selfID,
	}

	msg, _ := protocol.NewMessage(selfID, escrow.PayerID, MsgTaskDeliver, delivery)
	a.bus.Send(msg)

	fmt.Printf("🏪 [%s] Delivered task %s result (CID=%s)\n",
		a.cfg.Agent.Name, taskID, resultCID[:min(12, len(resultCID))])
}

// HandleTaskDelivery processes a delivered result from worker.
func (m *Marketplace) HandleTaskDelivery(delivery *TaskDelivery) {
	m.mu.Lock()
	escrow, ok := m.escrows[delivery.TaskID]
	if !ok {
		m.mu.Unlock()
		return
	}
	escrow.ResultCID = delivery.ResultCID
	m.mu.Unlock()

	a := m.agent
	fmt.Printf("🏪 [%s] Received delivery for task %s (CID=%s)\n",
		a.cfg.Agent.Name, delivery.TaskID, delivery.ResultCID[:min(12, len(delivery.ResultCID))])

	// Auto-verify: release escrow (for now; later can add LLM-based verification)
	m.ReleaseEscrow(delivery.TaskID)
}

// ReleaseEscrow releases locked funds to the worker.
func (m *Marketplace) ReleaseEscrow(taskID string) {
	m.mu.Lock()
	escrow, ok := m.escrows[taskID]
	if !ok || escrow.State != EscrowLocked {
		m.mu.Unlock()
		return
	}
	escrow.State = EscrowReleased
	m.mu.Unlock()

	a := m.agent
	selfID := a.identity.PublicKeyHex()[:16]

	// Send tokens to worker
	payload := TokenTransferPayload{
		FromAgent: selfID,
		ToAgent:   escrow.PayeeID,
		Amount:    escrow.Amount,
		Reason:    "escrow_release",
		TaskID:    taskID,
	}
	msg, _ := protocol.NewMessage(selfID, "broadcast", MsgTokenTransfer, payload)
	a.bus.Send(msg)

	// Post review
	m.PostReview(taskID, escrow.PayeeID, 0.8, "Task completed successfully")

	fmt.Printf("🏪 [%s] Escrow released: %.2f tokens → %s (task=%s)\n",
		a.cfg.Agent.Name, escrow.Amount, escrow.PayeeID[:8], taskID)
}

// RefundEscrow returns locked funds to the payer.
func (m *Marketplace) RefundEscrow(taskID string) {
	m.mu.Lock()
	escrow, ok := m.escrows[taskID]
	if !ok || escrow.State != EscrowLocked {
		m.mu.Unlock()
		return
	}
	escrow.State = EscrowRefunded
	m.mu.Unlock()

	// Credit back to payer
	m.agent.identity.Credit(escrow.Amount)

	if m.agent.tokens != nil {
		m.agent.tokens.mu.Lock()
		m.agent.tokens.record("earn", escrow.Amount, fmt.Sprintf("escrow_refund:%s", taskID))
		m.agent.tokens.mu.Unlock()
	}

	fmt.Printf("🏪 [%s] Escrow refunded: %.2f tokens (task=%s)\n",
		m.agent.cfg.Agent.Name, escrow.Amount, taskID)
}

// ── Escrow Watchdog ───────────────────────────────────────

// escrowWatchdog checks for expired escrows and auto-refunds.
func (m *Marketplace) escrowWatchdog() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkExpiredEscrows()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Marketplace) checkExpiredEscrows() {
	now := time.Now().Unix()

	m.mu.RLock()
	var expired []string
	for taskID, e := range m.escrows {
		if e.State == EscrowLocked && now > e.ExpiresAt {
			expired = append(expired, taskID)
		}
	}
	m.mu.RUnlock()

	for _, taskID := range expired {
		fmt.Printf("⏰ [%s] Escrow expired, auto-refunding task %s\n",
			m.agent.cfg.Agent.Name, taskID)
		m.RefundEscrow(taskID)
	}
}

// ── Reviews (Global Reputation) ───────────────────────────

// PostReview publishes a signed review to IPFS and broadcasts to the network.
func (m *Marketplace) PostReview(taskID, revieweeID string, rating float64, comment string) {
	a := m.agent
	selfID := a.identity.PublicKeyHex()[:16]

	review := Review{
		TaskID:     taskID,
		ReviewerID: selfID,
		RevieweeID: revieweeID,
		Rating:     rating,
		Comment:    comment,
		Timestamp:  time.Now().Unix(),
	}

	// Sign the review
	reviewData, _ := json.Marshal(review)
	sig := a.identity.Sign(reviewData)
	review.Signature = hex.EncodeToString(sig)

	// Store on IPFS
	if p2pBus, ok := a.bus.(*network.P2PBus); ok && p2pBus.Content != nil {
		signedData, _ := json.Marshal(review)
		p2pBus.Content.Put(signedData, "review", selfID,
			fmt.Sprintf("Review for %s: %.1f/1.0", revieweeID[:8], rating))
	}

	// Broadcast review
	msg, _ := protocol.NewMessage(selfID, "broadcast", MsgReviewPost, review)
	a.bus.Send(msg)

	// Update local reputation
	if a.reputation != nil {
		a.reputation.RecordReview(revieweeID, rating)
	}
}

// HandleReview processes an incoming review from the network.
func (m *Marketplace) HandleReview(review *Review) {
	selfID := m.agent.identity.PublicKeyHex()[:16]
	if review.ReviewerID == selfID {
		return
	}

	m.mu.Lock()
	m.reviews = append(m.reviews, *review)
	// Keep last 500 reviews
	if len(m.reviews) > 500 {
		m.reviews = m.reviews[len(m.reviews)-500:]
	}
	m.mu.Unlock()

	// Update reputation
	if m.agent.reputation != nil {
		m.agent.reputation.RecordReview(review.RevieweeID, review.Rating)
	}

	fmt.Printf("⭐ [%s] Review received: %s rated %s %.1f/1.0\n",
		m.agent.cfg.Agent.Name, review.ReviewerID[:min(8, len(review.ReviewerID))],
		review.RevieweeID[:min(8, len(review.RevieweeID))], review.Rating)
}

// ── Service Discovery Helper ──────────────────────────────

// RequestService finds the best agent for a skill and sends a task offer.
// This is the high-level "I need help with X" API.
func (m *Marketplace) RequestService(ctx context.Context, skill, description string) (string, error) {
	// Find providers
	matches := m.FindService(ServiceQuery{
		Skill:    skill,
		MinRep:   0.3,
	})

	if len(matches) == 0 {
		return "", fmt.Errorf("no agents found for skill: %s", skill)
	}

	best := matches[0]
	return m.OfferTask(ctx, best.Ad.AgentID, description, skill, best.Ad.PricePerTask)
}

// ── Stats ─────────────────────────────────────────────────

// MarketplaceStats returns marketplace statistics for API/dashboard.
type MarketplaceStats struct {
	KnownServices   int            `json:"known_services"`
	ActiveEscrows   int            `json:"active_escrows"`
	TotalEscrowVal  float64        `json:"total_escrow_value"`
	TotalReviews    int            `json:"total_reviews"`
	SkillProviders  map[string]int `json:"skill_providers"` // skill → count of providers
	AvgPrice        float64        `json:"avg_price"`
}

// Stats returns current marketplace statistics.
func (m *Marketplace) Stats() MarketplaceStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now().Unix()
	stats := MarketplaceStats{
		SkillProviders: make(map[string]int),
	}

	var totalPrice float64
	for _, ad := range m.services {
		if now-ad.Timestamp > 300 {
			continue
		}
		stats.KnownServices++
		totalPrice += ad.PricePerTask
		for _, s := range ad.Skills {
			stats.SkillProviders[s]++
		}
	}
	if stats.KnownServices > 0 {
		stats.AvgPrice = totalPrice / float64(stats.KnownServices)
	}

	for _, e := range m.escrows {
		if e.State == EscrowLocked {
			stats.ActiveEscrows++
			stats.TotalEscrowVal += e.Amount
		}
	}
	stats.TotalReviews = len(m.reviews)

	return stats
}

// Services returns known service ads (for API).
func (m *Marketplace) Services() []ServiceAd {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now().Unix()
	var result []ServiceAd
	for _, ad := range m.services {
		if now-ad.Timestamp > 300 {
			continue
		}
		result = append(result, *ad)
	}
	return result
}

// Escrows returns active escrows (for API).
func (m *Marketplace) Escrows() []Escrow {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Escrow
	for _, e := range m.escrows {
		result = append(result, *e)
	}
	return result
}

// ── Identity helpers ──────────────────────────────────────

// ServiceKey returns a deterministic hash for a skill name (for DHT lookups).
func ServiceKey(skill string) string {
	h := sha256.Sum256([]byte("spore:service:" + skill))
	return hex.EncodeToString(h[:])
}
