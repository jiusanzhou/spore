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

// agent_market.go — stigmergic task marketplace, the swarm's matchmaking
// layer. Inspired by ant-colony optimization: agents drop "pheromones"
// (broadcast a task on the bus), other agents sense and react based on
// internal activation thresholds, the originator picks the best bid.
//
// Lifecycle of a broadcast task:
//
//   broadcastTask  ──► bus (MsgTaskRequest)
//        ▲                │
//        │                ▼
//        │         all peer agents'
//        │         handleTaskBroadcast
//        │                │
//        │                ▼
//        │         activationThreshold(req)
//        │                │  (skill match, idleness, balance, fitness)
//        │                ▼
//        │         emits a TaskBid back
//        │                │
//        │                ▼
//        └─── handleBidReceived (collected on bidCh)
//             pick best bid by confidence × reputation
//             send MsgTaskAssign to winner
//                          │
//                          ▼
//                   winner's handleTaskAssign
//                   re-enqueues into its own taskWorker
//
// Result reporting (after the winner finishes):
//   broadcastTaskResult  ──► bus (MsgTaskResult, fan-out to listeners)
//
// Note: the marketplace package (internal/agent/marketplace.go) handles
// PAID tasks with explicit budgets and bounty escrow. This file is the
// FREE swarm channel where any agent can release a job and ask the rest
// of the swarm to react.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"go.zoe.im/spore/internal/protocol"
)

// broadcastTask publishes a task to the swarm and waits for bids.
// Like an ant releasing pheromone — "I found something, who can help?"
//
// The originator collects bids on a per-task channel registered in
// a.pendingBids; handleBidReceived (called from handleMessage when an
// inbound MsgTaskBid arrives) routes bids to the right channel.
func (a *Agent) broadcastTask(ctx context.Context, description string, budget float64) (string, error) {
	if a.bus == nil {
		return "", fmt.Errorf("no bus available")
	}

	taskID := fmt.Sprintf("%x", time.Now().UnixNano())[:8]

	req := protocol.TaskRequest{
		TaskID:      taskID,
		Description: description,
		Budget:      budget,
	}
	msg, err := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgTaskRequest,
		req,
	)
	if err != nil {
		return "", err
	}

	// Set up bid collection channel
	bidCh := make(chan *protocol.TaskBid, 10)
	a.mu.Lock()
	if a.pendingBids == nil {
		a.pendingBids = make(map[string]chan *protocol.TaskBid)
	}
	a.pendingBids[taskID] = bidCh
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.pendingBids, taskID)
		a.mu.Unlock()
	}()

	// Broadcast task to swarm
	if err := a.bus.Send(msg); err != nil {
		return "", err
	}

	fmt.Printf("📡 [%s] Broadcast task %s to swarm: %s\n",
		a.cfg.Agent.Name, taskID, truncate(description, 60))

	// Wait for bids (short window — like pheromone evaporation)
	bidTimeout := 5 * time.Second
	var bids []*protocol.TaskBid

	timer := time.NewTimer(bidTimeout)
	defer timer.Stop()

collecting:
	for {
		select {
		case bid := <-bidCh:
			bids = append(bids, bid)
			fmt.Printf("   [%s] Bid from %s: confidence=%.2f\n",
				a.cfg.Agent.Name, bid.BidderID[:8], bid.Confidence)
			// Accept first good bid (fast, like pheromone trail following)
			if bid.Confidence >= 0.7 {
				break collecting
			}
		case <-timer.C:
			break collecting
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if len(bids) == 0 {
		return "", fmt.Errorf("no bids received")
	}

	// Select best bid (highest confidence × reputation)
	var bestBid *protocol.TaskBid
	bestScore := -1.0
	for _, bid := range bids {
		rep := repInitial
		if a.reputation != nil {
			if a.reputation.IsIsolated(bid.BidderID) {
				continue
			}
			rep = a.reputation.Score(bid.BidderID)
		}
		score := bid.Confidence * rep
		if score > bestScore {
			bestScore = score
			bestBid = bid
		}
	}

	if bestBid == nil {
		return "", fmt.Errorf("all bidders isolated")
	}

	// Assign task to winner
	assignMsg, err := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		bestBid.BidderID,
		protocol.MsgTaskAssign,
		protocol.TaskRequest{
			TaskID:      taskID,
			Description: description,
			Budget:      budget,
		},
	)
	if err != nil {
		return "", err
	}

	fmt.Printf("📌 [%s] Assigned task %s → %s (confidence=%.2f, score=%.2f)\n",
		a.cfg.Agent.Name, taskID, bestBid.BidderID[:8], bestBid.Confidence, bestScore)

	if err := a.bus.Send(assignMsg); err != nil {
		return "", err
	}

	return taskID, nil
}

// activationThreshold computes whether this agent should bid on a task.
// Inspired by ant/bee threshold models: each agent has an internal threshold
// that depends on its state. Low threshold = easily activated.
func (a *Agent) activationThreshold(req *protocol.TaskRequest) (float64, bool) {
	threshold := 0.5 // base threshold

	// Skill match lowers threshold (like pheromone sensitivity)
	skillMatch := 0.0
	for _, req := range req.Requirements {
		for _, skill := range a.cfg.Agent.Skills {
			if strings.EqualFold(skill, req) || strings.Contains(strings.ToLower(skill), strings.ToLower(req)) {
				skillMatch += 0.2
			}
		}
	}
	// Even without explicit requirements, check description keywords against skills
	if len(req.Requirements) == 0 {
		desc := strings.ToLower(req.Description)
		for _, skill := range a.cfg.Agent.Skills {
			if strings.Contains(desc, strings.ToLower(skill)) {
				skillMatch += 0.15
			}
		}
		if skillMatch == 0 {
			skillMatch = 0.3 // generic task — moderate match for everyone
		}
	}

	// Idle agents have lower threshold (more available)
	active := atomic.LoadInt32(&a.activeTasks)
	if active == 0 {
		threshold -= 0.2 // idle → eager
	} else if active >= 2 {
		threshold += 0.3 // busy → reluctant
	}

	// High balance → more willing (can afford failure)
	if a.identity.Balance > 5.0 {
		threshold -= 0.1
	}

	// Evolution confidence lowers threshold
	if a.evolution != nil {
		genetics := a.evolution.ComputeGenetics()
		if genetics.Fitness > 0.7 {
			threshold -= 0.15
		}
	}

	confidence := skillMatch - threshold + 0.5 // normalize to 0-1
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	// Activated if confidence > 0.3 (low bar — ants are opportunistic)
	return confidence, confidence > 0.3
}

// handleTaskBroadcast is called when we hear a task broadcast on the swarm.
// Like an ant sensing pheromone — evaluate and bid if threshold exceeded.
func (a *Agent) handleTaskBroadcast(req *protocol.TaskRequest, fromAgent string) {
	// Don't bid on our own tasks
	selfID := a.identity.PublicKeyHex()[:16]
	if fromAgent == selfID {
		return
	}

	confidence, activated := a.activationThreshold(req)
	if !activated {
		return
	}

	bid := &protocol.TaskBid{
		TaskID:       req.TaskID,
		BidderID:     selfID,
		Confidence:   confidence,
		Capabilities: a.cfg.Agent.Skills,
	}

	msg, err := protocol.NewMessage(selfID, fromAgent, protocol.MsgTaskBid, bid)
	if err != nil {
		return
	}

	fmt.Printf("🤚 [%s] Bidding on task %s (confidence=%.2f)\n",
		a.cfg.Agent.Name, req.TaskID, confidence)

	a.bus.Send(msg)
}

// handleBidReceived processes an incoming bid for a task we broadcast.
// Routes the bid to the per-task collection channel registered in
// broadcastTask. If the channel is full or the task isn't pending
// anymore, the bid is dropped silently.
func (a *Agent) handleBidReceived(bid *protocol.TaskBid) {
	a.mu.RLock()
	ch, ok := a.pendingBids[bid.TaskID]
	a.mu.RUnlock()
	if ok {
		select {
		case ch <- bid:
		default: // channel full, drop bid
		}
	}
}

// handleTaskAssign processes a task assignment (we won the bid).
// Re-enqueues into the local taskWorker via SubmitTaskWithRuntime so
// the won task goes through the same retry/streaming/economy path as
// any locally-submitted one.
func (a *Agent) handleTaskAssign(req *protocol.TaskRequest, fromAgent string) {
	fmt.Printf("📨 [%s] Won bid for task %s from %s\n",
		a.cfg.Agent.Name, req.TaskID, fromAgent[:8])
	a.SubmitTaskWithRuntime(req.Description, "", "")
}

// broadcastTaskResult sends task result to the bus for coordinator collection.
// Fan-out only — no acknowledgement is awaited. The originator's
// handleMessage routes inbound MsgTaskResult to whichever subsystem
// cares (currently: marketplace bounty settlement, dashboard SSE).
func (a *Agent) broadcastTaskResult(taskID, output string, success bool, errMsg string) {
	if a.bus == nil {
		return
	}
	result := protocol.TaskResult{
		TaskID:  taskID,
		Output:  output,
		Success: success,
		Error:   errMsg,
	}
	msg, err := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgTaskResult,
		result,
	)
	if err != nil {
		return
	}
	a.bus.Send(msg)
}
