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
	"fmt"
	"sync"
	"time"

	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/protocol"
)

// Token economy: tokens are oxygen for agents.
// Agents earn tokens by completing tasks, lose tokens by using LLM.
// Without tokens, agents can't think — they go into survival mode.

// TokenConfig defines the economic parameters for the token lifecycle.
type TokenConfig struct {
	// InitialBalance is the seed tokens each agent starts with (birth capital).
	InitialBalance float64 `toml:"initial_balance" yaml:"initial_balance" json:"initial_balance"`

	// TaskReward is the base reward for completing a task successfully.
	TaskReward float64 `toml:"task_reward" yaml:"task_reward" json:"task_reward"`

	// FailurePenalty is tokens lost when a task fails (0 = no penalty).
	FailurePenalty float64 `toml:"failure_penalty" yaml:"failure_penalty" json:"failure_penalty"`

	// DelegationFee is the fraction of reward coordinator pays to worker (0-1).
	DelegationFee float64 `toml:"delegation_fee" yaml:"delegation_fee" json:"delegation_fee"`

	// HeartbeatCost is the tiny cost of staying alive each heartbeat (metabolism).
	HeartbeatCost float64 `toml:"heartbeat_cost" yaml:"heartbeat_cost" json:"heartbeat_cost"`

	// ThinkCost multiplier — actual LLM cost × this factor (default 1.0).
	ThinkCostMultiplier float64 `toml:"think_cost_multiplier" yaml:"think_cost_multiplier" json:"think_cost_multiplier"`

	// ShareReward is earned when other agents absorb your shared experience.
	ShareReward float64 `toml:"share_reward" yaml:"share_reward" json:"share_reward"`

	// StarvationThreshold — below this, agent enters panic mode.
	StarvationThreshold float64 `toml:"starvation_threshold" yaml:"starvation_threshold" json:"starvation_threshold"`

	// CriticalThreshold — below this, agent can only do survival actions.
	CriticalThreshold float64 `toml:"critical_threshold" yaml:"critical_threshold" json:"critical_threshold"`
}

// DefaultTokenConfig returns a balanced token economy.
func DefaultTokenConfig() TokenConfig {
	return TokenConfig{
		InitialBalance:      10.0,  // enough for ~10 tasks to bootstrap
		TaskReward:          1.0,   // earn 1 token per successful task
		FailurePenalty:      0.2,   // small penalty for failure
		DelegationFee:       0.5,   // coordinator pays 50% of reward to worker
		HeartbeatCost:       0.01,  // metabolism: slowly burns tokens
		ThinkCostMultiplier: 1.0,   // 1:1 mapping to LLM cost
		ShareReward:         0.1,   // small reward for knowledge sharing
		StarvationThreshold: 2.0,   // below 2 → panic
		CriticalThreshold:   0.5,   // below 0.5 → survival only
	}
}

// TokenLedger tracks all token transactions for an agent.
type TokenLedger struct {
	mu      sync.RWMutex
	agent   *Agent
	config  TokenConfig
	entries []LedgerEntry
	stats   TokenStats
}

// LedgerEntry is a single token transaction.
type LedgerEntry struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`    // earn/spend/transfer/penalty/metabolism
	Amount  float64   `json:"amount"`  // positive = credit, negative = debit
	Balance float64   `json:"balance"` // balance after this transaction
	Reason  string    `json:"reason"`
	PeerID  string    `json:"peer_id,omitempty"`
}

// TokenStats aggregates token flow.
type TokenStats struct {
	TotalEarned     float64 `json:"total_earned"`
	TotalSpent      float64 `json:"total_spent"`
	TotalTransferred float64 `json:"total_transferred"` // paid to others
	TotalReceived   float64 `json:"total_received"`    // received from others
	TasksCompleted  int     `json:"tasks_completed"`
	TasksFailed     int     `json:"tasks_failed"`
	SharesRewarded  int     `json:"shares_rewarded"`
}

// TokenState represents current economic health.
type TokenState struct {
	Balance   float64    `json:"balance"`
	Health    string     `json:"health"` // thriving/stable/struggling/starving/critical
	Stats     TokenStats `json:"stats"`
	BurnRate  float64    `json:"burn_rate"`  // tokens/min over last 5 min
	EarnRate  float64    `json:"earn_rate"`  // tokens/min over last 5 min
	RunwayMin float64    `json:"runway_min"` // estimated minutes until bankruptcy
}

// MsgTokenTransfer is a P2P token payment message type.
const MsgTokenTransfer protocol.MessageType = "token_transfer"

// TokenTransferPayload is the wire format for token transfers.
type TokenTransferPayload struct {
	FromAgent string  `json:"from_agent"`
	ToAgent   string  `json:"to_agent"`
	Amount    float64 `json:"amount"`
	Reason    string  `json:"reason"` // task_payment, delegation_fee, gift, bounty
	TaskID    string  `json:"task_id,omitempty"`
}

// NewTokenLedger creates a token ledger and seeds initial balance.
func NewTokenLedger(agent *Agent, cfg TokenConfig) *TokenLedger {
	l := &TokenLedger{
		agent:  agent,
		config: cfg,
	}
	return l
}

// Seed gives the agent its birth capital (called once at startup).
func (l *TokenLedger) Seed() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.agent.identity.Balance > 0 {
		return // already has balance (restored from state)
	}

	amount := l.config.InitialBalance
	l.agent.identity.Credit(amount)
	l.record("earn", amount, "birth_capital")
	fmt.Printf("💰 [%s] Seeded with %.2f tokens (birth capital)\n", l.agent.cfg.Agent.Name, amount)
}

// RewardTask credits tokens for a completed task.
func (l *TokenLedger) RewardTask(taskID string, success bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if success {
		reward := l.config.TaskReward
		l.agent.identity.Credit(reward)
		l.record("earn", reward, fmt.Sprintf("task_complete:%s", taskID[:8]))
		l.stats.TotalEarned += reward
		l.stats.TasksCompleted++
	} else if l.config.FailurePenalty > 0 {
		penalty := l.config.FailurePenalty
		l.agent.identity.Debit(penalty) // ignore error if balance too low
		l.record("penalty", -penalty, fmt.Sprintf("task_fail:%s", taskID[:8]))
		l.stats.TotalSpent += penalty
		l.stats.TasksFailed++
	}
}

// ChargeThink debits the actual LLM cost.
func (l *TokenLedger) ChargeThink(cost float64, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	actual := cost * l.config.ThinkCostMultiplier
	if actual <= 0 {
		return
	}
	l.agent.identity.Debit(actual) // best-effort
	l.record("spend", -actual, "think:"+reason)
	l.stats.TotalSpent += actual
}

// Metabolism is called every heartbeat — the cost of being alive.
func (l *TokenLedger) Metabolism() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cost := l.config.HeartbeatCost
	if cost <= 0 {
		return
	}
	l.agent.identity.Debit(cost) // best-effort, can go to 0
	l.record("metabolism", -cost, "heartbeat")
	l.stats.TotalSpent += cost
}

// PayWorker transfers tokens from coordinator to worker for delegation.
func (l *TokenLedger) PayWorker(workerID string, taskID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	payment := l.config.TaskReward * l.config.DelegationFee
	if payment <= 0 {
		return
	}

	// Debit from self
	if err := l.agent.identity.Debit(payment); err != nil {
		return // can't afford
	}
	l.record("transfer", -payment, fmt.Sprintf("pay_worker:%s:%s", workerID[:8], taskID[:8]))
	l.stats.TotalTransferred += payment

	// Send token transfer message to worker
	payload := TokenTransferPayload{
		FromAgent: l.agent.identity.PublicKeyHex()[:16],
		ToAgent:   workerID,
		Amount:    payment,
		Reason:    "delegation_fee",
		TaskID:    taskID,
	}
	if l.agent.bus != nil {
		msg, err := protocol.NewMessage(
			l.agent.identity.PublicKeyHex()[:16],
			"broadcast",
			MsgTokenTransfer,
			payload,
		)
		if err == nil {
			l.agent.bus.Send(msg)
		}
	}
}

// ReceivePayment handles incoming token transfer.
func (l *TokenLedger) ReceivePayment(from string, amount float64, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.agent.identity.Credit(amount)
	l.record("earn", amount, fmt.Sprintf("received:%s:%s", from[:min(8, len(from))], reason))
	l.stats.TotalReceived += amount
	l.stats.TotalEarned += amount
	fmt.Printf("💰 [%s] Received %.4f tokens from %s (%s)\n",
		l.agent.cfg.Agent.Name, amount, from[:min(8, len(from))], reason)
}

// RewardShare credits a small reward when peers absorb your experience.
func (l *TokenLedger) RewardShare() {
	l.mu.Lock()
	defer l.mu.Unlock()

	reward := l.config.ShareReward
	if reward <= 0 {
		return
	}
	l.agent.identity.Credit(reward)
	l.record("earn", reward, "experience_shared")
	l.stats.TotalEarned += reward
	l.stats.SharesRewarded++
}

// State returns current economic health assessment.
func (l *TokenLedger) State() TokenState {
	l.mu.RLock()
	defer l.mu.RUnlock()

	balance := l.agent.identity.Balance
	health := l.assessHealth(balance)

	// Calculate burn/earn rate from last 5 minutes of entries
	burnRate, earnRate := l.calculateRates(5 * time.Minute)

	var runway float64
	if burnRate > earnRate && burnRate > 0 {
		netBurn := burnRate - earnRate
		runway = balance / netBurn // minutes until 0
	} else {
		runway = 999 // effectively infinite
	}

	return TokenState{
		Balance:   balance,
		Health:    health,
		Stats:     l.stats,
		BurnRate:  burnRate,
		EarnRate:  earnRate,
		RunwayMin: runway,
	}
}

func (l *TokenLedger) assessHealth(balance float64) string {
	switch {
	case balance <= 0:
		return "critical" // can't think at all
	case balance < l.config.CriticalThreshold:
		return "starving" // survival actions only
	case balance < l.config.StarvationThreshold:
		return "struggling" // reduce non-essential activity
	case balance < l.config.InitialBalance:
		return "stable" // operating normally
	default:
		return "thriving" // can afford exploration and creation
	}
}

func (l *TokenLedger) calculateRates(window time.Duration) (burn, earn float64) {
	now := time.Now()
	cutoff := now.Add(-window)
	var totalBurn, totalEarn float64

	for i := len(l.entries) - 1; i >= 0; i-- {
		e := l.entries[i]
		if e.Time.Before(cutoff) {
			break
		}
		if e.Amount < 0 {
			totalBurn += -e.Amount
		} else {
			totalEarn += e.Amount
		}
	}

	minutes := window.Minutes()
	return totalBurn / minutes, totalEarn / minutes
}

func (l *TokenLedger) record(typ string, amount float64, reason string) {
	entry := LedgerEntry{
		Time:    time.Now(),
		Type:    typ,
		Amount:  amount,
		Balance: l.agent.identity.Balance,
		Reason:  reason,
	}
	l.entries = append(l.entries, entry)

	// Keep last 500 entries
	if len(l.entries) > 500 {
		l.entries = l.entries[len(l.entries)-500:]
	}
}

// Persist saves token state to memory.
func (l *TokenLedger) Persist() {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.agent.memory == nil {
		return
	}

	data := fmt.Sprintf("balance=%.6f earned=%.6f spent=%.6f transferred=%.6f received=%.6f tasks_ok=%d tasks_fail=%d shares=%d",
		l.agent.identity.Balance,
		l.stats.TotalEarned, l.stats.TotalSpent,
		l.stats.TotalTransferred, l.stats.TotalReceived,
		l.stats.TasksCompleted, l.stats.TasksFailed,
		l.stats.SharesRewarded)

	l.agent.memory.Put(&memory.Entry{
		AgentID: l.agent.identity.PublicKeyHex()[:16],
		Key:     "token:state",
		Value:   data,
	})
}

// Restore loads token state from memory.
func (l *TokenLedger) Restore() {
	if l.agent.memory == nil {
		return
	}
	entry, err := l.agent.memory.Get("token:state")
	if err != nil || entry == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	var bal, earned, spent, transferred, received float64
	var tasksOK, tasksFail, shares int
	if _, err := fmt.Sscanf(entry.Value,
		"balance=%f earned=%f spent=%f transferred=%f received=%f tasks_ok=%d tasks_fail=%d shares=%d",
		&bal, &earned, &spent, &transferred, &received, &tasksOK, &tasksFail, &shares); err == nil {
		l.agent.identity.Balance = bal
		l.stats = TokenStats{
			TotalEarned:     earned,
			TotalSpent:      spent,
			TotalTransferred: transferred,
			TotalReceived:   received,
			TasksCompleted:  tasksOK,
			TasksFailed:     tasksFail,
			SharesRewarded:  shares,
		}
		fmt.Printf("💰 [%s] Restored token state: balance=%.4f (earned=%.4f spent=%.4f)\n",
			l.agent.cfg.Agent.Name, bal, earned, spent)
	}
}

// CanThink returns true if the agent has enough tokens for basic operations.
func (l *TokenLedger) CanThink() bool {
	return l.agent.identity.Balance > 0
}

// CanExplore returns true if the agent can afford non-essential activity.
func (l *TokenLedger) CanExplore() bool {
	return l.agent.identity.Balance > l.config.StarvationThreshold
}

// CanCreate returns true if the agent is wealthy enough for creative work.
func (l *TokenLedger) CanCreate() bool {
	return l.agent.identity.Balance > l.config.InitialBalance*0.5
}

// RecentLedger returns the last N ledger entries.
func (l *TokenLedger) RecentLedger(n int) []LedgerEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if n > len(l.entries) {
		n = len(l.entries)
	}
	result := make([]LedgerEntry, n)
	copy(result, l.entries[len(l.entries)-n:])
	return result
}
