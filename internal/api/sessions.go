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
	"net/http"
	"strings"

	"go.zoe.im/spore/internal/sessions"
)

// handleSessions handles /api/sessions:
//   - GET  → list all sessions, newest first
//   - POST → create a new session ({agent, title?})
//
// The chat UI hits these on app load (list) and "New chat" button (create).
// We deliberately don't expose pagination yet — early dogfood, sessions per
// user count is small. Add LIMIT/offset when someone hits 100+.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	store, err := s.sw.Sessions()
	if err != nil {
		http.Error(w, "session store unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		list, err := store.List(0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Always return an empty array (never null) so the frontend can
		// map() over the response without a null guard.
		if list == nil {
			list = []*sessions.Session{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": list})

	case http.MethodPost:
		var req struct {
			Agent string `json:"agent"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		// If no agent specified, default to the first available — matches
		// /api/tasks behaviour and keeps "new chat" one-click for the demo.
		if req.Agent == "" {
			agents := s.sw.List()
			if len(agents) == 0 {
				http.Error(w, "no agents available", http.StatusServiceUnavailable)
				return
			}
			req.Agent = agents[0].Name
		}
		sess, err := store.Create(req.Agent, req.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, sess)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSessionRoute handles /api/sessions/<id> and /api/sessions/<id>/turns:
//   - GET    /api/sessions/<id>         → session metadata
//   - GET    /api/sessions/<id>/turns   → ordered list of turns
//   - DELETE /api/sessions/<id>         → drop session + cascade turns
//
// We hand-route here instead of pulling in chi/gorilla because the API
// surface is tiny and the rest of the server uses raw http.ServeMux.
func (s *Server) handleSessionRoute(w http.ResponseWriter, r *http.Request) {
	store, err := s.sw.Sessions()
	if err != nil {
		http.Error(w, "session store unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	subpath := ""
	if len(parts) > 1 {
		subpath = parts[1]
	}

	switch subpath {
	case "":
		switch r.Method {
		case http.MethodGet:
			sess, err := store.Get(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if sess == nil {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, sess)
		case http.MethodDelete:
			if err := store.Delete(id); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "turns":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		turns, err := store.Turns(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if turns == nil {
			turns = []*sessions.Turn{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"turns": turns})
	default:
		http.NotFound(w, r)
	}
}
