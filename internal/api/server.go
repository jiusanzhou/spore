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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.zoe.im/spore/internal/network"
	"go.zoe.im/spore/internal/swarm"
)

// Server is the HTTP API server for the swarm.
type Server struct {
	sw   *swarm.Swarm
	port int
	mux  *http.ServeMux
	srv  *http.Server
}

// NewServer creates a new API server.
func NewServer(sw *swarm.Swarm, port int) *Server {
	s := &Server{
		sw:   sw,
		port: port,
		mux:  http.NewServeMux(),
	}
	s.routes()
	s.srv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return s
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}

// Close gracefully shuts down the server.
func (s *Server) Close() error {
	return s.srv.Close()
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/agents", s.handleAgents)
	// Pattern: /api/agents/<name>/tasks or /api/agents/<name>/info
	s.mux.HandleFunc("/api/agents/", s.handleAgentRoute)
	s.mux.HandleFunc("/api/peers", s.handlePeers)
	s.mux.HandleFunc("/api/peers/connect", s.handlePeerConnect)
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
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"peer_id": p2pBus.PeerID(),
		"peers":   peers,
		"count":   len(peers),
	})
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

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
