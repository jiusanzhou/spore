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

package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Skill Catalog — browse and install skills from the swarm
//
// Each agent's SkillFS publishes skills to IPFS. When ServiceAd is broadcast,
// it includes the agent's skill list. The SkillCatalog aggregates these into
// a browsable catalog with provenance (who has what, quality metrics).
//
// Install = ImportFromCID via SkillFS.
// ────────────────────────────────────────────────────────────────────────────

// CatalogEntry is a skill available in the swarm.
type CatalogEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	Origin      string   `json:"origin,omitempty"`
	Generation  int      `json:"generation"`
	IPFSCID     string   `json:"ipfs_cid,omitempty"`
	ProviderID  string   `json:"provider_id"`   // agent that has this skill
	ProviderName string  `json:"provider_name,omitempty"`
	Reputation  float64  `json:"reputation"`    // provider's reputation
	Tags        []string `json:"tags,omitempty"`
	SeenAt      int64    `json:"seen_at"`       // unix timestamp
}

// SkillCatalog aggregates skills from the swarm for browsing.
type SkillCatalog struct {
	mu      sync.RWMutex
	entries map[string][]*CatalogEntry // skill name → providers (multiple agents may have same skill)
}

// NewSkillCatalog creates an empty catalog.
func NewSkillCatalog() *SkillCatalog {
	return &SkillCatalog{
		entries: make(map[string][]*CatalogEntry),
	}
}

// IngestServiceAd extracts skill info from a service advertisement.
func (sc *SkillCatalog) IngestServiceAd(ad *ServiceAd) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	now := time.Now().Unix()
	for _, skillName := range ad.Skills {
		entry := &CatalogEntry{
			Name:         skillName,
			ProviderID:   ad.AgentID,
			ProviderName: ad.Name,
			Reputation:   ad.Reputation,
			SeenAt:       now,
		}

		providers := sc.entries[skillName]
		// Update existing or append
		updated := false
		for i, p := range providers {
			if p.ProviderID == ad.AgentID {
				providers[i] = entry
				updated = true
				break
			}
		}
		if !updated {
			sc.entries[skillName] = append(providers, entry)
		}
	}
}

// IngestSkillCID records a specific skill CID from a content announcement.
func (sc *SkillCatalog) IngestSkillCID(skillName, cid, agentID, description string, generation int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	now := time.Now().Unix()
	entry := &CatalogEntry{
		Name:        skillName,
		Description: description,
		IPFSCID:     cid,
		ProviderID:  agentID,
		Generation:  generation,
		SeenAt:      now,
	}

	providers := sc.entries[skillName]
	updated := false
	for i, p := range providers {
		if p.ProviderID == agentID {
			providers[i] = entry
			updated = true
			break
		}
	}
	if !updated {
		sc.entries[skillName] = append(providers, entry)
	}
}

// Browse returns all catalog entries, optionally filtered.
type BrowseFilter struct {
	Query       string  // substring match on name/description/tags
	Category    string  // exact category match
	MinRep      float64 // minimum provider reputation
	HasCID      bool    // only skills with IPFS CID (installable)
}

func (sc *SkillCatalog) Browse(filter BrowseFilter) []*CatalogEntry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	now := time.Now().Unix()
	staleThreshold := int64(600) // 10 minutes

	var results []*CatalogEntry
	queryLower := strings.ToLower(filter.Query)

	for _, providers := range sc.entries {
		for _, e := range providers {
			// Stale check
			if now-e.SeenAt > staleThreshold {
				continue
			}
			// Filter: query
			if queryLower != "" {
				nameLower := strings.ToLower(e.Name)
				descLower := strings.ToLower(e.Description)
				tagsLower := strings.ToLower(strings.Join(e.Tags, " "))
				if !strings.Contains(nameLower, queryLower) &&
					!strings.Contains(descLower, queryLower) &&
					!strings.Contains(tagsLower, queryLower) {
					continue
				}
			}
			// Filter: category
			if filter.Category != "" && e.Category != filter.Category {
				continue
			}
			// Filter: min reputation
			if filter.MinRep > 0 && e.Reputation < filter.MinRep {
				continue
			}
			// Filter: has CID
			if filter.HasCID && e.IPFSCID == "" {
				continue
			}
			results = append(results, e)
		}
	}

	// Sort: highest reputation first, then newest
	sort.Slice(results, func(i, j int) bool {
		if results[i].Reputation != results[j].Reputation {
			return results[i].Reputation > results[j].Reputation
		}
		return results[i].SeenAt > results[j].SeenAt
	})

	return results
}

// UniqueSkills returns deduplicated skill names with best provider info.
func (sc *SkillCatalog) UniqueSkills() []*CatalogEntry {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	var results []*CatalogEntry
	for name, providers := range sc.entries {
		if len(providers) == 0 {
			continue
		}
		// Pick the best provider (highest rep, with CID preferred)
		best := providers[0]
		for _, p := range providers[1:] {
			if p.IPFSCID != "" && best.IPFSCID == "" {
				best = p
			} else if p.Reputation > best.Reputation {
				best = p
			}
		}
		entry := *best
		entry.Name = name
		results = append(results, &entry)
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results
}

// Install fetches a skill by CID and installs it into the agent's SkillFS.
func (sc *SkillCatalog) Install(skillName string, fs *SkillFS, fetchFn func(string) ([]byte, error)) error {
	sc.mu.RLock()
	providers, ok := sc.entries[skillName]
	sc.mu.RUnlock()

	if !ok || len(providers) == 0 {
		return fmt.Errorf("skill %q not found in catalog", skillName)
	}

	// Find a provider with a CID
	var cid string
	for _, p := range providers {
		if p.IPFSCID != "" {
			cid = p.IPFSCID
			break
		}
	}
	if cid == "" {
		return fmt.Errorf("skill %q has no IPFS CID — cannot install", skillName)
	}

	_, err := fs.ImportFromCID(cid, fetchFn)
	if err != nil {
		return fmt.Errorf("installing %q from CID %s: %w", skillName, truncateCID(cid), err)
	}

	return nil
}

// Stats returns catalog statistics.
type CatalogStats struct {
	UniqueSkills   int `json:"unique_skills"`
	TotalProviders int `json:"total_providers"`
	WithCID        int `json:"with_cid"` // installable
}

func (sc *SkillCatalog) Stats() CatalogStats {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	stats := CatalogStats{UniqueSkills: len(sc.entries)}
	providerSet := make(map[string]struct{})
	for _, providers := range sc.entries {
		for _, p := range providers {
			providerSet[p.ProviderID] = struct{}{}
			if p.IPFSCID != "" {
				stats.WithCID++
			}
		}
	}
	stats.TotalProviders = len(providerSet)
	return stats
}
