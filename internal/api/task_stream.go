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
)

// handleTaskRoute dispatches /api/tasks/<id>/<action> requests.
//
// Currently the only sub-route is /stream (SSE per-task event feed). Any
// other path returns 404 so the precise /api/tasks endpoint (registered
// separately) keeps owning the collection-level handlers.
func (s *Server) handleTaskRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	taskID, action := parts[0], parts[1]
	switch action {
	case "stream":
		s.handleTaskStream(w, r, taskID)
	default:
		http.NotFound(w, r)
	}
}

// handleTaskStream serves Server-Sent Events for a single task ID.
//
// URL pattern: GET /api/tasks/<id>/stream
//
// On connect we subscribe to the per-task event broadcaster and forward each
// runtime.StreamEvent to the client as a `data: <json>\n\n` SSE frame. The
// stream terminates when:
//   - the task completes (broadcaster closes the subscription channel), OR
//   - the client disconnects (r.Context() is cancelled).
//
// We also send a periodic comment line (`: keepalive\n\n`) every 15s so
// intermediate proxies don't time out an idle stream during long runtime
// thinking phases.
func (s *Server) handleTaskStream(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if taskID == "" {
		http.Error(w, "missing task id", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// Disable buffering on intermediate proxies (e.g. nginx).
	w.Header().Set("X-Accel-Buffering", "no")

	events, cancel := s.sw.SubscribeTaskEvents(taskID)
	defer cancel()

	// Initial "open" frame so the browser's onopen fires deterministically
	// and the client knows the stream is wired up.
	fmt.Fprintf(w, "event: open\ndata: {\"task_id\":%q}\n\n", taskID)
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				// Task ended — send a terminal marker the client can use
				// to close the EventSource without a reconnect attempt.
				fmt.Fprintf(w, "event: end\ndata: {\"task_id\":%q}\n\n", taskID)
				flusher.Flush()
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			// SSE multiline data is allowed, but any embedded newlines in
			// the JSON would break the frame; json.Marshal escapes them so
			// a single `data: …\n\n` is safe.
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sseEventName(string(ev.Type)), payload)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// sseEventName sanitises a runtime event type for use as the SSE `event:` field.
// EventSource on the browser side picks it up via addEventListener(name, ...).
// Only ASCII letters / digits / hyphen / underscore are allowed.
func sseEventName(name string) string {
	if name == "" {
		return "message"
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, name)
	if clean == "" {
		return "message"
	}
	return clean
}
