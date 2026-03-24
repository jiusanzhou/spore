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

package memory

import (
	"fmt"
	"strings"
	"time"
)

// ContextType defines the three kinds of agent context (inspired by OpenViking).
type ContextType string

const (
	CtxMemory   ContextType = "memory"   // Agent's learned knowledge
	CtxResource ContextType = "resource" // External knowledge (docs, data)
	CtxSkill    ContextType = "skill"    // Callable capabilities
)

// MemoryCategory classifies memory entries into 6 types.
type MemoryCategory string

const (
	// User-facing memories (about the world/peers)
	CatProfile     MemoryCategory = "profile"     // Self identity, attributes
	CatPreferences MemoryCategory = "preferences" // Runtime preferences, strategy
	CatEntities    MemoryCategory = "entities"     // Known peers, agents, people
	CatEvents      MemoryCategory = "events"       // Key events, milestones, decisions

	// Agent-facing memories (learned from experience)
	CatCases    MemoryCategory = "cases"    // Problem + solution pairs
	CatPatterns MemoryCategory = "patterns" // Reusable workflows, strategies
)

// URI represents a Spore context URI: spore://<agent-id>/<type>/<category>/<path>
type URI struct {
	AgentID  string         // agent public key hex[:16], or "collective"
	Type     ContextType    // memory, resource, skill
	Category MemoryCategory // profile, preferences, entities, events, cases, patterns (for memory type)
	Path     string         // remaining path segments
}

// String returns the URI string representation.
func (u URI) String() string {
	parts := []string{"spore://", u.AgentID, "/", string(u.Type)}
	if u.Category != "" {
		parts = append(parts, "/", string(u.Category))
	}
	if u.Path != "" {
		parts = append(parts, "/", u.Path)
	}
	return strings.Join(parts, "")
}

// ParseURI parses a spore:// URI.
func ParseURI(s string) (URI, error) {
	if !strings.HasPrefix(s, "spore://") {
		return URI{}, fmt.Errorf("invalid spore URI: %s", s)
	}
	rest := strings.TrimPrefix(s, "spore://")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) < 2 {
		return URI{}, fmt.Errorf("invalid spore URI: needs at least agent/type: %s", s)
	}

	uri := URI{
		AgentID: parts[0],
		Type:    ContextType(parts[1]),
	}
	if len(parts) >= 3 {
		uri.Category = MemoryCategory(parts[2])
	}
	if len(parts) >= 4 {
		uri.Path = parts[3]
	}
	return uri, nil
}

// ContextEntry is the enriched memory entry with URI, layers, and category.
type ContextEntry struct {
	// Core identity
	URI      string         `json:"uri"`       // spore://<agent>/<type>/<category>/<path>
	AgentID  string         `json:"agent_id"`
	Type     ContextType    `json:"type"`      // memory, resource, skill
	Category MemoryCategory `json:"category"`  // profile, preferences, entities, events, cases, patterns

	// Three-layer content (OpenViking L0/L1/L2)
	L0 string `json:"l0"` // Abstract: ~100 tokens, one-liner
	L1 string `json:"l1"` // Overview: ~2k tokens, structured summary
	L2 string `json:"l2"` // Detail: full content, loaded on demand

	// Metadata
	Tags      []string          `json:"tags,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Source    string            `json:"source,omitempty"` // task_id, peer_id, etc.
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	AccessCnt int               `json:"access_cnt"`

	// Sharing
	CID    string `json:"cid,omitempty"`    // Content hash for P2P sharing
	Shared bool   `json:"shared,omitempty"` // Whether published to collective
}

// BuildURI constructs the URI from entry fields.
func (e *ContextEntry) BuildURI() string {
	u := URI{
		AgentID:  e.AgentID,
		Type:     e.Type,
		Category: e.Category,
	}
	return u.String()
}

// ContextStore extends Store with structured context operations.
type ContextStore interface {
	Store // backward compatible

	// PutContext stores a structured context entry.
	PutContext(entry *ContextEntry) error

	// GetContext retrieves by URI.
	GetContext(uri string) (*ContextEntry, error)

	// ListByCategory lists entries of a specific category.
	ListByCategory(agentID string, category MemoryCategory, limit int) ([]*ContextEntry, error)

	// ListByType lists entries of a specific context type.
	ListByType(agentID string, ctxType ContextType, limit int) ([]*ContextEntry, error)

	// SearchContext searches across all context with optional type/category filters.
	SearchContext(query string, ctxType ContextType, category MemoryCategory, limit int) ([]*ContextEntry, error)

	// Stats returns memory statistics by category.
	ContextStats(agentID string) map[MemoryCategory]int
}
