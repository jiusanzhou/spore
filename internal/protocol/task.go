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

package protocol

// TaskRequest is the payload for a task request message.
type TaskRequest struct {
	Description  string   `json:"description"`
	Requirements []string `json:"requirements,omitempty"`
	Budget       float64  `json:"budget,omitempty"`
	Deadline     int64    `json:"deadline,omitempty"` // unix timestamp
}

// TaskBid is the payload for a task bid message.
type TaskBid struct {
	TaskID        string  `json:"task_id"`
	EstimatedCost float64 `json:"estimated_cost"`
	EstimatedTime int64   `json:"estimated_time"` // seconds
	Capabilities  []string `json:"capabilities,omitempty"`
}

// TaskResult is the payload for a task result message.
type TaskResult struct {
	TaskID  string `json:"task_id"`
	Output  string `json:"output"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// TaskVerify is the payload for a task verification message.
type TaskVerify struct {
	TaskID   string  `json:"task_id"`
	Accepted bool    `json:"accepted"`
	Rating   float64 `json:"rating"` // 0.0 - 1.0
	Feedback string  `json:"feedback,omitempty"`
}

// SpawnInit is the payload for initiating a spawn.
type SpawnInit struct {
	Name             string            `json:"name"`
	Role             string            `json:"role"`
	Model            string            `json:"model,omitempty"`
	MemorySnapshot   []string          `json:"memory_snapshot,omitempty"`
	ResourceShare    float64           `json:"resource_share"`
	StartupBalance   float64           `json:"startup_balance"`
	Mutations        map[string]string `json:"mutations,omitempty"` // config overrides
}

// SpawnAck is the payload acknowledging a spawn.
type SpawnAck struct {
	ChildID    string `json:"child_id"`     // child public key hex
	ParentID   string `json:"parent_id"`
	Status     string `json:"status"`       // ok, error
	Error      string `json:"error,omitempty"`
}

// CapabilityAd advertises what an agent can do.
type CapabilityAd struct {
	AgentID      string   `json:"agent_id"`
	PeerID       string   `json:"peer_id,omitempty"`
	Capabilities []string `json:"capabilities"`
	Interests    []string `json:"interests,omitempty"`
	Topics       []string `json:"topics,omitempty"`
	Capacity     float64  `json:"capacity"` // 0.0 = fully loaded, 1.0 = idle
	Reputation   float64  `json:"reputation"`
}

// HeartbeatPayload is the structured payload for heartbeat messages.
type HeartbeatPayload struct {
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	Runtime   string  `json:"runtime"`
	Balance   float64 `json:"balance"`
	Capacity  float64 `json:"capacity"`   // 0.0 = fully loaded, 1.0 = idle
	TaskCount int     `json:"task_count"`
	Uptime    int64   `json:"uptime"`     // seconds since start
}
