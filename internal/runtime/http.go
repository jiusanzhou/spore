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

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPRuntime connects to a remote agent via HTTP API.
// This enables any agent framework that exposes an HTTP endpoint
// to participate in the Spore network.
//
// Expected remote API:
//   GET  /info          → Info
//   POST /execute       → TaskOutput
//   GET  /health        → 200 OK
type HTTPRuntime struct {
	BaseURL string
	Client  *http.Client
	info    *Info // cached
}

// NewHTTPRuntime creates a runtime that delegates to a remote HTTP agent.
func NewHTTPRuntime(baseURL string) *HTTPRuntime {
	return &HTTPRuntime{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client: &http.Client{
			Timeout: 10 * time.Minute, // tasks can be long
		},
	}
}

func (h *HTTPRuntime) Info() Info {
	if h.info != nil {
		return *h.info
	}
	// try to fetch from remote
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", h.BaseURL+"/info", nil)
	resp, err := h.Client.Do(req)
	if err != nil {
		return Info{Name: "http-remote", Version: "unknown"}
	}
	defer resp.Body.Close()

	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{Name: "http-remote", Version: "unknown"}
	}
	h.info = &info
	return info
}

func (h *HTTPRuntime) Execute(ctx context.Context, task TaskInput) (*TaskOutput, error) {
	body, err := json.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("marshaling task: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", h.BaseURL+"/execute", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing task: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &TaskOutput{
			Success: false,
			Error:   fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody)),
		}, nil
	}

	var output TaskOutput
	if err := json.Unmarshal(respBody, &output); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &output, nil
}

func (h *HTTPRuntime) Healthy(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", h.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (h *HTTPRuntime) Close() error { return nil }
