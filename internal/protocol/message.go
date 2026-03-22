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

import (
	"crypto/ed25519"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MessageType defines the type of inter-agent message.
type MessageType string

const (
	MsgTaskRequest   MessageType = "task_request"
	MsgTaskBid       MessageType = "task_bid"
	MsgTaskAssign    MessageType = "task_assign"
	MsgTaskResult    MessageType = "task_result"
	MsgTaskVerify    MessageType = "task_verify"
	MsgCapabilityAd  MessageType = "capability_ad"
	MsgMemorySync    MessageType = "memory_sync"
	MsgVote          MessageType = "vote"
	MsgHeartbeat     MessageType = "heartbeat"
	MsgConsciousness  MessageType = "consciousness" // self-model sharing
	MsgSpawnInit      MessageType = "spawn_init"
	MsgSpawnAck       MessageType = "spawn_ack"
	MsgContentAnnounce MessageType = "content_announce" // CID-based content available
)

// Message is the standard inter-agent message envelope.
type Message struct {
	Version   string          `json:"version"`
	ID        string          `json:"id"`
	From      string          `json:"from"`       // sender public key hex
	To        string          `json:"to"`         // recipient or "broadcast"
	Type      MessageType     `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp int64           `json:"timestamp"`
	Signature string          `json:"signature"`
}

// NewMessage creates a new unsigned message.
func NewMessage(from string, to string, typ MessageType, payload interface{}) (*Message, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &Message{
		Version:   "0.1.0",
		ID:        uuid.New().String(),
		From:      from,
		To:        to,
		Type:      typ,
		Payload:   data,
		Timestamp: time.Now().Unix(),
	}, nil
}

// SignableBytes returns the bytes to sign (everything except signature).
func (m *Message) SignableBytes() []byte {
	data, _ := json.Marshal(struct {
		Version   string          `json:"version"`
		ID        string          `json:"id"`
		From      string          `json:"from"`
		To        string          `json:"to"`
		Type      MessageType     `json:"type"`
		Payload   json.RawMessage `json:"payload"`
		Timestamp int64           `json:"timestamp"`
	}{m.Version, m.ID, m.From, m.To, m.Type, m.Payload, m.Timestamp})
	return data
}

// Sign signs the message with the given private key.
func (m *Message) Sign(priv ed25519.PrivateKey) {
	sig := ed25519.Sign(priv, m.SignableBytes())
	m.Signature = string(sig)
}
