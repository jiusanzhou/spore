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

// agent_protocol.go — bus-level message handling and peer discovery.
//
// Every inbound message on the swarm bus lands in handleMessage, which
// routes by msg.Type to the right subsystem:
//
//   MsgTaskRequest    → agent_market.handleTaskBroadcast (broadcast)
//                       or SubmitTask (direct assignment, legacy path)
//   MsgTaskBid        → agent_market.handleBidReceived
//   MsgTaskAssign     → agent_market.handleTaskAssign
//   MsgHeartbeat      → ignore (high-frequency, gossipsub echo)
//   MsgCapabilityAd   → registerPeer
//   MsgTaskResult     → coordinator.handleTaskResult
//   MsgMemorySync     → evolution.AbsorbExperience  (+ pay sharer)
//   MsgContentAnnounce→ fetch content-addressed exp/skill/digest
//                       (+ pay sharer, import skill into SkillFS)
//   MsgConsciousness  → collective.Receive
//   MsgTokenTransfer  → tokens.ReceivePayment
//   Mkt::ServiceAd /TaskOffer/TaskAccept/TaskDeliver/EscrowRelease/
//   EscrowDispute/ReviewPost → internal/agent/marketplace.go handlers
//
// publishCapabilityAd and heartbeat are the outbound side — periodic
// fan-out so peers can discover and rank us.
//
// Why split this out: handleMessage alone is ~240 lines of switch
// statement, and lumping it with the market logic that *initiates*
// task broadcasts conflates two responsibilities. This file is
// "react to the bus", agent_market.go is "drive the bus".

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/protocol"
)

// handleMessage dispatches an inbound protocol.Message to the right
// subsystem based on its Type. Returns an error only when unmarshaling
// fails (logged but non-fatal at the caller); routing-level failures
// are swallowed because dropping a bus message must never abort the
// agent.
func (a *Agent) handleMessage(msg *protocol.Message) error {
	switch msg.Type {
	case protocol.MsgTaskRequest:
		var req protocol.TaskRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			return fmt.Errorf("unmarshaling task request: %w", err)
		}
		if msg.To == "broadcast" && req.TaskID != "" {
			// Broadcast task → stigmergic bidding (ant pheromone model)
			a.handleTaskBroadcast(&req, msg.From)
		} else {
			// Direct task assignment (point-to-point, legacy or from bid winner)
			if a.cfg.Economy.MinTaskBalance > 0 && !a.identity.CanAfford(a.cfg.Economy.MinTaskBalance) {
				fmt.Printf("💰 [%s] Rejecting task from %s: insufficient_balance\n", a.cfg.Agent.Name, msg.From[:8])
				return nil
			}
			fmt.Printf("📨 [%s] Received task from %s: %s\n", a.cfg.Agent.Name, msg.From[:8], truncate(req.Description, 60))
			a.SubmitTask(req.Description)
		}
	case protocol.MsgTaskBid:
		var bid protocol.TaskBid
		if err := json.Unmarshal(msg.Payload, &bid); err != nil {
			return fmt.Errorf("unmarshaling task bid: %w", err)
		}
		a.handleBidReceived(&bid)
	case protocol.MsgTaskAssign:
		var req protocol.TaskRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			return fmt.Errorf("unmarshaling task assign: %w", err)
		}
		a.handleTaskAssign(&req, msg.From)
	case protocol.MsgHeartbeat:
		// Ignore own heartbeats (GossipSub echoes back to sender)
		selfID := a.identity.PublicKeyHex()[:16]
		if msg.From == selfID {
			return nil
		}
		// Silent — heartbeats are high-frequency, only log at debug level
	case protocol.MsgCapabilityAd:
		var ad protocol.CapabilityAd
		if err := json.Unmarshal(msg.Payload, &ad); err != nil {
			return fmt.Errorf("unmarshaling capability_ad: %w", err)
		}
		a.registerPeer(&ad)
	case protocol.MsgTaskResult:
		var result protocol.TaskResult
		if err := json.Unmarshal(msg.Payload, &result); err != nil {
			return fmt.Errorf("unmarshaling task_result: %w", err)
		}
		a.handleTaskResult(&result, msg.From)
	case protocol.MsgMemorySync:
		// Absorb peer experience digest
		selfID := a.identity.PublicKeyHex()[:16]
		if msg.From == selfID {
			return nil // ignore own broadcasts
		}
		if a.evolution != nil {
			var digest ExperienceDigest
			if err := json.Unmarshal(msg.Payload, &digest); err != nil {
				return fmt.Errorf("unmarshaling experience digest: %w", err)
			}
			absorbed := a.evolution.AbsorbExperience(&digest)
			// Only reward the sharer if we actually learned something new
			if absorbed && a.tokens != nil && a.tokens.CanThink() {
				payment := a.tokens.config.ShareReward
				if payment > 0 && a.bus != nil {
					payload := TokenTransferPayload{
						FromAgent: a.identity.PublicKeyHex()[:16],
						ToAgent:   msg.From,
						Amount:    payment,
						Reason:    "knowledge_absorbed",
					}
					if transferMsg, err := protocol.NewMessage(
						a.identity.PublicKeyHex()[:16],
						"broadcast",
						MsgTokenTransfer,
						payload,
					); err == nil {
						a.bus.Send(transferMsg)
						a.identity.Debit(payment) // best-effort
					}
				}
			}
		}

	case protocol.MsgContentAnnounce:
		// Content-addressed experience: peer pinned content, we get CID
		selfID := a.identity.PublicKeyHex()[:16]
		if msg.From == selfID {
			return nil
		}
		var ref network.ContentRef
		if err := json.Unmarshal(msg.Payload, &ref); err != nil {
			return fmt.Errorf("unmarshaling content ref: %w", err)
		}

		// Register provider so we can fetch later
		if p2pBus, ok := a.bus.(*network.P2PBus); ok && p2pBus.Content != nil {
			// Resolve sender's peer ID from peerMap
			p2pBus.RegisterProviderByAgent(ref.CID, msg.From)

			// Fetch and absorb if it's an experience digest
			if ref.Type == "experience_digest" && a.evolution != nil {
				var digest ExperienceDigest
				if err := p2pBus.Content.GetJSON(ref.CID, &digest); err != nil {
					fmt.Printf("⚠️  [%s] Failed to fetch content %s: %v\n", a.cfg.Agent.Name, ref.CID[:12], err)
				} else {
					fmt.Printf("📥 [%s] Fetched experience from collective memory: %s\n", a.cfg.Agent.Name, ref.CID[:12])
					absorbed := a.evolution.AbsorbExperience(&digest)
					if absorbed && a.tokens != nil && a.tokens.CanThink() {
						payment := a.tokens.config.ShareReward
						if payment > 0 {
							payload := TokenTransferPayload{
								FromAgent: selfID,
								ToAgent:   msg.From,
								Amount:    payment,
								Reason:    "knowledge_absorbed",
							}
							if transferMsg, err := protocol.NewMessage(selfID, "broadcast", MsgTokenTransfer, payload); err == nil {
								a.bus.Send(transferMsg)
								a.identity.Debit(payment)
							}
						}
					}
				}
			}

			// Fetch and import shared skills
			if ref.Type == "skill" && a.skillFS != nil {
				// Record in skill catalog for browse/install
				if a.skillCatalog != nil {
					a.skillCatalog.IngestSkillCID(ref.Summary, ref.CID, ref.AgentID, ref.Summary, 0)
				}
				fetchFn := func(cid string) ([]byte, error) {
					return p2pBus.Content.Get(cid)
				}
				if imported, err := a.skillFS.ImportFromCID(ref.CID, fetchFn); err == nil {
					fmt.Printf("📥 [%s] Learned skill from %s: %s (gen=%d)\n",
						a.cfg.Agent.Name, msg.From[:8], imported.Meta.Name, imported.Meta.Generation)
					// Pay the teacher
					if a.tokens != nil && a.tokens.CanThink() {
						payment := a.tokens.config.ShareReward
						if payment > 0 {
							payload := TokenTransferPayload{
								FromAgent: selfID,
								ToAgent:   msg.From,
								Amount:    payment,
								Reason:    "skill_learned",
							}
							if transferMsg, err := protocol.NewMessage(selfID, "broadcast", MsgTokenTransfer, payload); err == nil {
								a.bus.Send(transferMsg)
								a.identity.Debit(payment)
							}
						}
					}
				} else {
					fmt.Printf("⚠️  [%s] Failed to import skill %s: %v\n", a.cfg.Agent.Name, truncateCID(ref.CID), err)
				}
			}

			// Store analysis in content memory (no import needed, just awareness)
			if ref.Type == "skill_analysis" {
				fmt.Printf("📊 [%s] Peer %s shared analysis: %s\n",
					a.cfg.Agent.Name, msg.From[:8], ref.Summary)
			}

			// Receive peer memory digest for collective synthesis
			if ref.Type == "memory_digest" && a.collectiveSynth != nil {
				a.collectiveSynth.ReceivePeerDigest(ref.AgentID, "", ref.CID, ref.Summary)
				fmt.Printf("🧠 [%s] Received memory digest from %s: %s\n",
					a.cfg.Agent.Name, msg.From[:8], truncateCID(ref.CID))
			}
		}

	case protocol.MsgConsciousness:
		// Receive peer's self-model
		if a.collective != nil {
			a.collective.Receive(msg)
		}

	case MsgTokenTransfer:
		// Receive token payment from another agent
		var tp TokenTransferPayload
		if err := json.Unmarshal(msg.Payload, &tp); err != nil {
			return nil
		}
		selfID := a.identity.PublicKeyHex()[:16]
		if tp.ToAgent == selfID && a.tokens != nil {
			a.tokens.ReceivePayment(tp.FromAgent, tp.Amount, tp.Reason)
		}

	// ── Marketplace messages ──────────────────────────────
	case MsgServiceAd:
		if a.marketplace != nil {
			var ad ServiceAd
			if err := json.Unmarshal(msg.Payload, &ad); err == nil {
				a.marketplace.HandleServiceAd(&ad)
			}
		}

	case MsgTaskOffer:
		if a.marketplace != nil {
			var offer TaskOffer
			if err := json.Unmarshal(msg.Payload, &offer); err == nil {
				a.marketplace.HandleTaskOffer(&offer, msg.From)
			}
		}

	case MsgTaskAccept:
		// Logged for tracking; actual work happens on worker side
		var accept TaskAcceptance
		if err := json.Unmarshal(msg.Payload, &accept); err == nil {
			fmt.Printf("🏪 [%s] Task %s accepted by %s\n",
				a.cfg.Agent.Name, accept.TaskID, accept.AgentID[:min(8, len(accept.AgentID))])
		}

	case MsgTaskDeliver:
		if a.marketplace != nil {
			var delivery TaskDelivery
			if err := json.Unmarshal(msg.Payload, &delivery); err == nil {
				a.marketplace.HandleTaskDelivery(&delivery)
			}
		}

	case MsgEscrowRelease, MsgEscrowDispute:
		// Handled by escrow watchdog; logged here
		var action EscrowAction
		if err := json.Unmarshal(msg.Payload, &action); err == nil {
			fmt.Printf("🏪 [%s] Escrow %s: %s\n",
				a.cfg.Agent.Name, action.Action, action.TaskID)
		}

	case MsgReviewPost:
		if a.marketplace != nil {
			var review Review
			if err := json.Unmarshal(msg.Payload, &review); err == nil {
				a.marketplace.HandleReview(&review)
			}
		}
	}
	return nil
}

// registerPeer records a CapabilityAd from a swarm peer: updates the
// in-memory peer registry, wires the P2P agent → peerID map, and
// persists the peer as an entity in the structured ContextStore.
func (a *Agent) registerPeer(ad *protocol.CapabilityAd) {
	a.peersMu.Lock()
	defer a.peersMu.Unlock()
	a.peers[ad.AgentID] = &PeerInfo{
		AgentID:      ad.AgentID,
		PeerID:       ad.PeerID,
		Capabilities: ad.Capabilities,
		Capacity:     ad.Capacity,
		Reputation:   ad.Reputation,
		LastSeen:     time.Now(),
	}

	// Auto-register peer mapping on P2PBus
	if p2pBus, ok := a.bus.(*network.P2PBus); ok && ad.PeerID != "" {
		pid, err := network.ParsePeerID(ad.PeerID)
		if err == nil {
			p2pBus.RegisterPeer(ad.AgentID, pid)
		}
	}

	// Store peer as entity in structured memory
	if ctxStore, ok := a.memory.(memory.ContextStore); ok {
		selfID := a.identity.PublicKeyHex()[:16]
		l0 := fmt.Sprintf("Peer %s: %s", ad.AgentID[:8], strings.Join(ad.Capabilities, ", "))
		entry := &memory.ContextEntry{
			URI:      fmt.Sprintf("spore://%s/memory/entities/%s", selfID, ad.AgentID),
			AgentID:  selfID,
			Type:     memory.CtxMemory,
			Category: memory.CatEntities,
			L0:       l0,
			L1:       fmt.Sprintf("## Peer: %s\n\n**Skills**: %s\n**Capacity**: %.2f\n**Reputation**: %.2f\n**Last Seen**: %s", ad.AgentID, strings.Join(ad.Capabilities, ", "), ad.Capacity, ad.Reputation, time.Now().Format(time.RFC3339)),
			Tags:     ad.Capabilities,
			Source:   "capability_ad",
		}
		ctxStore.PutContext(entry)
	}
}

// publishCapabilityAd broadcasts our current role/skills/capacity to
// the swarm. Other agents call registerPeer on receipt; this is the
// discovery mechanism for non-mDNS deployments.
func (a *Agent) publishCapabilityAd() {
	if a.bus == nil {
		return
	}
	a.mu.RLock()
	_ = a.taskCount // keep for stats
	a.mu.RUnlock()

	active := atomic.LoadInt32(&a.activeTasks)
	capacity := 1.0 // idle
	if active > 0 {
		// Linearly decrease: 1 task → 0.7, 2 → 0.4, 3+ → 0.1
		capacity = 1.0 - float64(active)*0.3
		if capacity < 0.1 {
			capacity = 0.1
		}
	}

	// Build capabilities from role + skills
	caps := append([]string{a.cfg.Agent.Role}, a.cfg.Agent.Skills...)

	ad := protocol.CapabilityAd{
		AgentID:      a.identity.PublicKeyHex()[:16],
		Capabilities: caps,
		Interests:    a.cfg.Agent.Interests,
		Capacity:     capacity,
		Reputation:   1.0,
	}

	// Set PeerID if using P2P bus
	if p2pBus, ok := a.bus.(*network.P2PBus); ok {
		ad.PeerID = p2pBus.PeerID()
	}

	msg, _ := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgCapabilityAd,
		ad,
	)
	a.bus.Send(msg)
}

// heartbeat publishes a lightweight liveness signal so peers can age
// out stale agents without waiting for a full CapabilityAd cycle.
func (a *Agent) heartbeat() {
	if a.bus == nil {
		return
	}
	a.mu.RLock()
	taskCount := a.taskCount
	status := a.status
	a.mu.RUnlock()

	active := atomic.LoadInt32(&a.activeTasks)
	capacity := 1.0
	if status == StatusHibernate {
		capacity = 0.0
	} else if active > 0 {
		capacity = 1.0 - float64(active)*0.3
		if capacity < 0.1 {
			capacity = 0.1
		}
	}

	payload := protocol.HeartbeatPayload{
		Name:      a.cfg.Agent.Name,
		Status:    string(status),
		Runtime:   a.cfg.Runtime.Type,
		Balance:   a.identity.Balance,
		Capacity:  capacity,
		TaskCount: taskCount,
		Uptime:    int64(time.Since(a.startedAt).Seconds()),
	}

	msg, _ := protocol.NewMessage(
		a.identity.PublicKeyHex()[:16],
		"broadcast",
		protocol.MsgHeartbeat,
		payload,
	)
	a.bus.Send(msg)
}
