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
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"go.zoe.im/spore/internal/agent"
	"go.zoe.im/spore/internal/memory"
	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/swarm"
	"go.zoe.im/spore/web"
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
	// API routes
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
	s.mux.HandleFunc("/api/content/", s.handleContentItem)
	s.mux.HandleFunc("/api/marketplace", s.handleMarketplace)
	s.mux.HandleFunc("/api/marketplace/request", s.handleMarketplaceRequest)
	s.mux.HandleFunc("/api/changelog", s.handleChangelog)
	s.mux.HandleFunc("/api/feedback", s.handleFeedback)
	s.mux.HandleFunc("/api/feedback/vote", s.handleFeedbackVote)
	s.mux.HandleFunc("/api/help-wanted", s.handleHelpWanted)

	// Frontend: embedded React app (web/dist/) with SPA fallback,
	// or legacy single-file dashboard.html as fallback.
	if distFS := web.DistFS(); distFS != nil {
		s.mux.Handle("/", spaHandler{fs: http.FS(distFS), fallbackHTML: dashboardHTML})
	} else {
		s.mux.HandleFunc("/", s.handleDashboard)
	}
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
	case "context":
		s.handleAgentContext(w, r, name)
	case "marketplace":
		s.handleAgentMarketplace(w, r, name)
	case "catalog":
		s.handleAgentCatalog(w, r, name)
	case "collective-memory":
		s.handleAgentCollectiveMemory(w, r, name)
	case "manifest":
		s.handleAgentManifest(w, r, name)
	case "journal":
		s.handleAgentJournal(w, r, name)
	case "synthesis":
		s.handleAgentSynthesis(w, r, name)
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

// spaHandler serves embedded static files with SPA fallback to index.html.
type spaHandler struct {
	fs           http.FileSystem
	fallbackHTML []byte // legacy dashboard.html fallback
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Try serving the static file
	f, err := h.fs.Open(path)
	if err == nil {
		defer f.Close()
		stat, err := f.Stat()
		if err == nil && !stat.IsDir() {
			http.FileServer(h.fs).ServeHTTP(w, r)
			return
		}
	}

	// SPA fallback: serve index.html for unmatched paths (client-side routing)
	idx, err := h.fs.Open("index.html")
	if err != nil {
		// No index.html in embed — fallback to legacy dashboard
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(h.fallbackHTML)
		return
	}
	defer idx.Close()

	stat, _ := idx.Stat()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", stat.ModTime(), idx.(io.ReadSeeker))
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
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"tasks": s.sw.TaskLog(),
		})
	case http.MethodPost:
		var req struct {
			Agent       string `json:"agent"`       // target agent name (empty = broadcast to swarm)
			Description string `json:"description"` // task description
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Description == "" {
			http.Error(w, "description is required", http.StatusBadRequest)
			return
		}

		if req.Agent == "" {
			// Broadcast: pick first agent and let it coordinate via stigmergic market
			agents := s.sw.List()
			if len(agents) == 0 {
				http.Error(w, "no agents available", http.StatusServiceUnavailable)
				return
			}
			req.Agent = agents[0].Name
		}

		taskID, err := s.sw.SendTask(req.Agent, req.Description)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"task_id": taskID,
			"agent":   req.Agent,
			"status":  "queued",
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

	result := map[string]interface{}{
		"declared": a.Config().Agent.Skills,
	}

	evo := a.Evolution()
	if evo != nil {
		result["profiles"] = evo.SkillProfiles()
		result["confidence"] = evo.Strategy().SkillConfidence
	}

	// Skill store data (SkillFS — file-system-first + IPFS)
	if sfs := a.SkillFileStore(); sfs != nil {
		agentID := a.Info().ID
		result["stats"] = sfs.Stats(agentID)
		result["active_skills"] = sfs.All()
		result["skill_metrics"] = sfs.AllMetrics()
		if analyses, err := sfs.RecentAnalyses(agentID, 10); err == nil {
			result["recent_analyses"] = analyses
		}
	} else if ss := a.Skills(); ss != nil {
		// Fallback to legacy SkillStore
		agentID := a.Info().ID
		result["stats"] = ss.Stats(agentID)
		if active, err := ss.ActiveSkills(); err == nil {
			result["active_skills"] = active
		}
		if analyses, err := ss.RecentAnalyses(agentID, 10); err == nil {
			result["recent_analyses"] = analyses
		}
	}

	writeJSON(w, http.StatusOK, result)
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

func (s *Server) handleAgentContext(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}
	mem := a.Memory()
	if mem == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"entries": []interface{}{}, "stats": map[string]int{}})
		return
	}
	ctxStore, ok := mem.(memory.ContextStore)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]interface{}{"entries": []interface{}{}, "stats": map[string]int{}, "note": "context store not available"})
		return
	}

	agentID := a.ID()
	category := r.URL.Query().Get("category")
	ctxType := r.URL.Query().Get("type")
	query := r.URL.Query().Get("q")

	var entries []*memory.ContextEntry
	var err error

	if query != "" {
		entries, err = ctxStore.SearchContext(query, memory.ContextType(ctxType), memory.MemoryCategory(category), 50)
	} else if category != "" {
		entries, err = ctxStore.ListByCategory(agentID, memory.MemoryCategory(category), 50)
	} else if ctxType != "" {
		entries, err = ctxStore.ListByType(agentID, memory.ContextType(ctxType), 50)
	} else {
		// Return all categories with stats
		entries, err = ctxStore.ListByType(agentID, memory.CtxMemory, 100)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	stats := ctxStore.ContextStats(agentID)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"stats":   stats,
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

// handleContentItem returns raw content by CID.
// GET /api/content/<cid> → raw bytes (text/markdown for .md-like content)
// GET /api/content/<cid>?format=html → simple HTML rendering
func (s *Server) handleContentItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cid := strings.TrimPrefix(r.URL.Path, "/api/content/")
	if cid == "" {
		http.Error(w, "missing CID", http.StatusBadRequest)
		return
	}

	bus := s.sw.Bus()
	p2pBus, ok := bus.(*network.P2PBus)
	if !ok || p2pBus.Content == nil {
		http.Error(w, "content store not available", http.StatusServiceUnavailable)
		return
	}

	data, err := p2pBus.Content.Get(cid)
	if err != nil {
		http.Error(w, "content not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Find metadata from refs
	var ref *network.ContentRef
	for _, r := range p2pBus.Content.ListRefs() {
		if r.CID == cid {
			ref = &r
			break
		}
	}

	format := r.URL.Query().Get("format")
	if format == "html" {
		// Render as a simple readable HTML page
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		title := cid[:12]
		contentType := ""
		ipfsCID := ""
		agentID := ""
		summary := ""
		if ref != nil {
			contentType = ref.Type
			ipfsCID = ref.IPFSCID
			agentID = ref.AgentID
			summary = ref.Summary
			if summary != "" {
				title = summary
			}
		}
		fmt.Fprint(w, contentHTMLPage(title, contentType, cid, ipfsCID, agentID, string(data)))
		return
	}

	// Raw content
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("X-Content-CID", cid)
	if ref != nil && ref.IPFSCID != "" {
		w.Header().Set("X-IPFS-CID", ref.IPFSCID)
	}
	w.Write(data)
}

// contentHTMLPage renders a Markdown content item as a standalone HTML page.
func contentHTMLPage(title, contentType, cid, ipfsCID, agentID, body string) string {
	// Escape for HTML
	body = strings.ReplaceAll(body, "&", "&amp;")
	body = strings.ReplaceAll(body, "<", "&lt;")
	body = strings.ReplaceAll(body, ">", "&gt;")

	ipfsLine := ""
	if ipfsCID != "" {
		ipfsLine = fmt.Sprintf(`<div class="meta">IPFS: <code>%s</code></div>`, ipfsCID)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s — Spore Content</title>
<style>
:root { --bg: #0a0a0a; --fg: #e5e5e5; --dim: #888; --accent: #6cf; --card: #161616; --border: #2a2a2a; }
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: var(--bg); color: var(--fg); padding: 24px; max-width: 800px; margin: 0 auto; line-height: 1.6; }
.header { border-bottom: 1px solid var(--border); padding-bottom: 16px; margin-bottom: 24px; }
.header h1 { font-size: 18px; font-weight: 600; margin-bottom: 8px; }
.meta { font-size: 12px; color: var(--dim); margin: 2px 0; }
.meta code { background: var(--card); padding: 2px 6px; border-radius: 4px; font-size: 11px; }
.badge { display: inline-block; padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 500; background: var(--card); border: 1px solid var(--border); margin-right: 6px; }
.content { white-space: pre-wrap; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 13px; background: var(--card); border: 1px solid var(--border); border-radius: 8px; padding: 20px; overflow-x: auto; }
h1, h2, h3 { color: var(--accent); }
</style>
</head>
<body>
<div class="header">
  <h1>%s</h1>
  <div class="meta"><span class="badge">%s</span> Agent: <code>%s</code></div>
  <div class="meta">SHA-256: <code>%s</code></div>
  %s
</div>
<pre class="content">%s</pre>
</body>
</html>`, title, title, contentType, agentID, cid, ipfsLine, body)
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

// ── Marketplace API ───────────────────────────────────────

func (s *Server) handleAgentMarketplace(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ag := s.sw.GetAgent(name)
	if ag == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	mp := ag.Market()
	if mp == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "marketplace not enabled"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"stats":    mp.Stats(),
		"services": mp.Services(),
		"escrows":  mp.Escrows(),
	})
}

func (s *Server) handleAgentCatalog(w http.ResponseWriter, r *http.Request, name string) {
	ag := s.sw.GetAgent(name)
	if ag == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	cat := ag.Catalog()
	if cat == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "catalog not initialized"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query().Get("q")
		category := r.URL.Query().Get("category")
		installable := r.URL.Query().Get("installable") == "true"

		results := cat.Browse(agent.BrowseFilter{
			Query:    query,
			Category: category,
			HasCID:   installable,
		})
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"stats":  cat.Stats(),
			"unique": cat.UniqueSkills(),
			"results": results,
		})

	case http.MethodPost:
		// POST: install a skill from catalog
		var req struct {
			SkillName string `json:"skill_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SkillName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skill_name required"})
			return
		}
		sfs := ag.SkillFileStore()
		if sfs == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "SkillFS not initialized"})
			return
		}
		// Build fetchFn from agent's P2P bus
		fetchFn := func(cid string) ([]byte, error) {
			return nil, fmt.Errorf("no P2P content store available")
		}
		if p2pBus, ok := ag.Bus().(*network.P2PBus); ok && p2pBus.Content != nil {
			fetchFn = func(cid string) ([]byte, error) {
				return p2pBus.Content.Get(cid)
			}
		}
		if err := cat.Install(req.SkillName, sfs, fetchFn); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "installed", "skill": req.SkillName})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentCollectiveMemory(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ag := s.sw.GetAgent(name)
	if ag == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	cs := ag.CollectiveSynth()
	if cs == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "collective synthesis not initialized"})
		return
	}

	result := map[string]interface{}{
		"status": cs.Status(),
	}
	if content, err := cs.CollectiveLearnings(); err == nil {
		result["collective_learnings"] = content
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAgentManifest(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ag := s.sw.GetAgent(name)
	if ag == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	manifest := ag.GenerateManifest()
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	_ = enc.Encode(manifest)
}

func (s *Server) handleMarketplace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Aggregate marketplace data from all agents
	agents := s.sw.Agents()
	var allServices []agent.ServiceAd
	var allEscrows []agent.Escrow
	totalStats := agent.MarketplaceStats{
		SkillProviders: make(map[string]int),
	}

	for _, ag := range agents {
		mp := ag.Market()
		if mp == nil {
			continue
		}
		services := mp.Services()
		allServices = append(allServices, services...)
		allEscrows = append(allEscrows, mp.Escrows()...)
		stats := mp.Stats()
		totalStats.KnownServices += stats.KnownServices
		totalStats.ActiveEscrows += stats.ActiveEscrows
		totalStats.TotalEscrowVal += stats.TotalEscrowVal
		totalStats.TotalReviews += stats.TotalReviews
		for k, v := range stats.SkillProviders {
			totalStats.SkillProviders[k] += v
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"stats":    totalStats,
		"services": allServices,
		"escrows":  allEscrows,
	})
}

func (s *Server) handleMarketplaceRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Agent       string `json:"agent"`
		Skill       string `json:"skill"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Find the requesting agent (payer)
	agentName := req.Agent
	if agentName == "" {
		agents := s.sw.List()
		if len(agents) > 0 {
			agentName = agents[0].Name
		}
	}
	payer := s.sw.GetAgent(agentName)
	if payer == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}

	payerMp := payer.Market()
	if payerMp == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "marketplace not enabled"})
		return
	}

	// Cross-reference all agents' services to find the best provider
	// (same swarm agents share a host, so they don't see each other via P2P)
	var bestAd *agent.ServiceAd
	bestScore := -1.0
	payerID := payer.ID()

	for _, ag := range s.sw.Agents() {
		mp := ag.Market()
		if mp == nil {
			continue
		}
		for _, svc := range mp.Services() {
			if svc.AgentID == payerID {
				continue // don't assign to self
			}
			for _, sk := range svc.Skills {
				if sk == req.Skill {
					score := svc.Reputation * svc.Capacity
					if svc.PricePerTask > 0 {
						score /= svc.PricePerTask
					}
					if score > bestScore {
						bestScore = score
						ad := svc
						bestAd = &ad
					}
					break
				}
			}
		}
	}

	if bestAd == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no agents found for skill: " + req.Skill})
		return
	}

	ctx := r.Context()
	taskID, err := payerMp.OfferTask(ctx, bestAd.AgentID, req.Description, req.Skill, bestAd.PricePerTask)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"task_id":  taskID,
		"status":   "offered",
		"payer":    agentName,
		"provider": bestAd.Name,
		"skill":    req.Skill,
		"payment":  bestAd.PricePerTask,
	})
}

func (s *Server) handleAgentJournal(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	journal := a.Journal()
	if journal == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"agent":   name,
			"entries": []interface{}{},
		})
		return
	}

	limit := 50
	entries := journal.Entries(limit)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agent":    name,
		"entries":  entries,
		"count":    len(entries),
		"markdown": journal.RenderMarkdown(),
	})
}

func (s *Server) handleAgentSynthesis(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a := s.sw.GetAgent(name)
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found: " + name})
		return
	}
	synth := a.Synthesizer()
	if synth == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"agent":  name,
			"status": "no synthesis engine",
		})
		return
	}

	status := synth.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agent":  name,
		"status": status,
	})
}

// ── Swarm-level API: Changelog, Feedback, Help Wanted ──────────────────────

func (s *Server) handleChangelog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cl := s.sw.SwarmChangelog()
	if cl == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "changelog not initialized (use supervisor)"})
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"total":   cl.Count(),
		"entries": cl.Recent(limit),
	})
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	fc := s.sw.SwarmFeedback()
	if fc == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "feedback not initialized (use supervisor)"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		limit := 20
		if l := r.URL.Query().Get("limit"); l != "" {
			fmt.Sscanf(l, "%d", &limit)
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"stats":    fc.Stats(),
			"feedback": fc.RecentFeedback(limit),
		})

	case http.MethodPost:
		var req swarm.FeedbackEntry
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if req.Message == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message required"})
			return
		}
		fc.SubmitFeedback(req)
		writeJSON(w, http.StatusCreated, map[string]string{"status": "feedback recorded"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFeedbackVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fc := s.sw.SwarmFeedback()
	if fc == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "feedback not initialized"})
		return
	}

	var req struct {
		FeedbackID string `json:"feedback_id"`
		Delta      int    `json:"delta"` // +1 or -1
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.FeedbackID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "feedback_id required"})
		return
	}
	if req.Delta == 0 {
		req.Delta = 1
	}
	if fc.VoteFeedback(req.FeedbackID, req.Delta) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "voted"})
	} else {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "feedback not found"})
	}
}

func (s *Server) handleHelpWanted(w http.ResponseWriter, r *http.Request) {
	fc := s.sw.SwarmFeedback()
	if fc == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "feedback not initialized"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"active": fc.ActiveHelpWanted(),
		})

	case http.MethodPost:
		var req swarm.HelpWanted
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		fc.SubmitHelpWanted(req)
		writeJSON(w, http.StatusCreated, map[string]string{"status": "help wanted submitted", "id": req.ID})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
