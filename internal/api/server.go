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

package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/swarm"
)

//go:embed dashboard.html
var dashboardHTML []byte

// Server is the HTTP API server for the swarm.
type Server struct {
	sw   *swarm.Swarm
	port int
	mux  *http.ServeMux
	srv  *http.Server

	// SSE
	sseClients   map[chan []byte]struct{}
	sseMu        sync.Mutex
	sseCtx       context.Context
	sseCancel    context.CancelFunc
}

// NewServer creates a new API server.
func NewServer(sw *swarm.Swarm, port int) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		sw:         sw,
		port:       port,
		mux:        http.NewServeMux(),
		sseClients: make(map[chan []byte]struct{}),
		sseCtx:     ctx,
		sseCancel:  cancel,
	}
	s.routes()
	s.srv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // SSE needs no write timeout
	}
	go s.sseBroadcastLoop()
	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

// Close gracefully shuts down the server.
func (s *Server) Close() error {
	s.sseCancel()
	return s.srv.Close()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleDashboard)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/agents", s.handleAgents)
	// Pattern: /api/agents/<name>/tasks or /api/agents/<name>/info
	s.mux.HandleFunc("/api/agents/", s.handleAgentRoute)
	s.mux.HandleFunc("/api/peers", s.handlePeers)
	s.mux.HandleFunc("/api/peers/connect", s.handlePeerConnect)
	s.mux.HandleFunc("/api/stats", s.handleStats)
	s.mux.HandleFunc("/api/tasks", s.handleTasks)
	s.mux.HandleFunc("/api/events", s.handleSSE)
	s.mux.HandleFunc("/api/content", s.handleContent)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"version": "0.1.0-dev",
		"time":    time.Now().Unix(),
	})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	infos := s.sw.List()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agents": infos,
		"count":  len(infos),
	})
}

func (s *Server) handleAgentRoute(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/agents/<name>/tasks or /api/agents/<name>/info
	path := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		// /api/agents/<name> — agent info
		s.handleAgentInfo(w, r, parts[0])
		return
	}

	name := parts[0]
	action := parts[1]

	switch action {
	case "tasks":
		s.handleAgentTasks(w, r, name)
	case "info":
		s.handleAgentInfo(w, r, name)
	case "evolution":
		s.handleAgentEvolution(w, r, name)
	case "skills":
		s.handleAgentSkills(w, r, name)
	case "experience":
		s.handleAgentExperience(w, r, name)
	case "peers":
		s.handleAgentPeerFitness(w, r, name)
	case "awareness":
		s.handleAgentAwareness(w, r, name)
	case "monologue":
		s.handleAgentMonologue(w, r, name)
	case "collective":
		s.handleAgentCollective(w, r, name)
	case "economy":
		s.handleAgentEconomy(w, r, name)
	case "reputation":
		s.handleAgentReputation(w, r, name)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) handleAgentInfo(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	infos := s.sw.List()
	for _, info := range infos {
		if info.Name == name {
			writeJSON(w, http.StatusOK, info)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
}

func (s *Server) handleAgentTasks(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Description == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description is required"})
		return
	}

	taskID, err := s.sw.SendTask(name, req.Description)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"task_id": taskID,
		"agent":   name,
		"status":  "queued",
	})
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bus := s.sw.Bus()
	p2pBus, ok := bus.(*network.P2PBus)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"peer_id": "",
			"peers":   []string{},
			"count":   0,
			"note":    "not using P2P transport",
		})
		return
	}
	peers := p2pBus.ConnectedPeers()
	result := map[string]interface{}{
		"peer_id": p2pBus.PeerID(),
		"peers":   peers,
		"count":   len(peers),
	}
	if p2pBus.Content != nil {
		result["content_store"] = p2pBus.Content.Stats()
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePeerConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr := r.URL.Query().Get("addr")
	if addr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "addr parameter required"})
		return
	}
	bus := s.sw.Bus()
	p2pBus, ok := bus.(*network.P2PBus)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not using P2P transport"})
		return
	}
	if err := p2pBus.Connect(addr); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "connected", "addr": addr})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.sw.Stats())
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tasks": s.sw.TaskLog(),
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// --- Evolution API Handlers ---

func (s *Server) handleAgentEvolution(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	evo := a.Evolution()
	if evo == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no evolution engine"})
		return
	}

	strategy := evo.Strategy()
	skills := evo.SkillProfiles()
	stats := evo.Stats()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agent":    name,
		"stats":    stats,
		"strategy": strategy,
		"skills":   skills,
	})
}

func (s *Server) handleAgentSkills(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	evo := a.Evolution()
	if evo == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"declared": a.Config().Agent.Skills,
			"profiles": map[string]interface{}{},
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"declared":   a.Config().Agent.Skills,
		"profiles":   evo.SkillProfiles(),
		"confidence": evo.Strategy().SkillConfidence,
	})
}

func (s *Server) handleAgentExperience(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	evo := a.Evolution()
	if evo == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"journal": []interface{}{}})
		return
	}

	// Return last N entries from journal
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	journal := evo.RecentJournal(limit)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total":   len(journal),
		"showing": len(journal),
		"journal": journal,
	})
}

func (s *Server) handleAgentPeerFitness(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	pe := a.PeerEvo()
	if pe == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"rankings": []interface{}{}})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"rankings": pe.Rankings(),
	})
}

func (s *Server) handleAgentAwareness(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	aw := a.Awareness()
	if aw == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no awareness engine"})
		return
	}
	writeJSON(w, http.StatusOK, aw.Self())
}

func (s *Server) handleAgentMonologue(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	aw := a.Awareness()
	if aw == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"thoughts": []interface{}{}})
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"thoughts": aw.Monologue(limit),
	})
}

func (s *Server) handleAgentCollective(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	col := a.Collective()
	if col == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no collective engine"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state": col.State(),
		"peers": col.Peers(),
	})
}

func (s *Server) handleAgentEconomy(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	tokens := a.Tokens()
	if tokens == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no token ledger"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"state":  tokens.State(),
		"ledger": tokens.RecentLedger(20),
	})
}

func (s *Server) handleAgentReputation(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	rep := a.Reputation()
	if rep == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no reputation engine"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"records": rep.All(),
	})
}

func (s *Server) handleContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bus := s.sw.Bus()
	p2pBus, ok := bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"items": []interface{}{},
			"stats": map[string]interface{}{},
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": p2pBus.Content.ListRefs(),
		"stats": p2pBus.Content.Stats(),
	})
}

// --- SSE (Server-Sent Events) ---

// handleSSE streams real-time state updates to the client.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []byte, 8)
	s.sseMu.Lock()
	s.sseClients[ch] = struct{}{}
	s.sseMu.Unlock()

	defer func() {
		s.sseMu.Lock()
		delete(s.sseClients, ch)
		s.sseMu.Unlock()
	}()

	// Send initial full state immediately
	s.sendFullState(ch)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.sseCtx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// sseBroadcastLoop periodically collects swarm state and pushes to all SSE clients.
func (s *Server) sseBroadcastLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.sseCtx.Done():
			return
		case <-ticker.C:
			s.sseMu.Lock()
			if len(s.sseClients) == 0 {
				s.sseMu.Unlock()
				continue
			}
			clients := make([]chan []byte, 0, len(s.sseClients))
			for ch := range s.sseClients {
				clients = append(clients, ch)
			}
			s.sseMu.Unlock()

			data := s.buildStatePayload()
			for _, ch := range clients {
				select {
				case ch <- data:
				default:
					// Client too slow, skip this frame
				}
			}
		}
	}
}

// sendFullState pushes current state into a single client channel.
func (s *Server) sendFullState(ch chan []byte) {
	data := s.buildStatePayload()
	select {
	case ch <- data:
	default:
	}
}

// buildStatePayload assembles the full dashboard state as JSON.
func (s *Server) buildStatePayload() []byte {
	infos := s.sw.List()
	stats := s.sw.Stats()
	tasks := s.sw.TaskLog()

	// Peers + content store
	var peerCount int
	var transport string
	var contentItems []network.ContentRef
	var contentStats map[string]interface{}
	bus := s.sw.Bus()
	if p2pBus, ok := bus.(*network.P2PBus); ok {
		peerCount = len(p2pBus.ConnectedPeers())
		transport = "p2p"
		if p2pBus.Content != nil {
			contentItems = p2pBus.Content.ListRefs()
			contentStats = p2pBus.Content.Stats()
		}
	} else {
		transport = "local"
	}

	payload := map[string]interface{}{
		"type":    "state",
		"agents":  infos,
		"stats":   stats,
		"tasks":   tasks,
		"network": map[string]interface{}{
			"transport": transport,
			"peers":     peerCount,
		},
		"content": map[string]interface{}{
			"items": contentItems,
			"stats": contentStats,
		},
		"ts": time.Now().UnixMilli(),
	}

	data, _ := json.Marshal(payload)
	return data
}
