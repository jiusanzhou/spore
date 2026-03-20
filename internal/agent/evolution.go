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
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.zoe.im/spore/internal/memory"
)

// EvolutionEngine manages the agent's self-evolution cycle:
// observe → evaluate → adapt → persist.
type EvolutionEngine struct {
	mu       sync.RWMutex
	agent    *Agent
	journal  []*ExperienceRecord
	skills   map[string]*SkillProfile
	strategy *StrategyProfile

	// Thresholds
	reflectInterval   time.Duration // how often to run reflection
	minRecordsReflect int           // min records before triggering reflection
	lastReflect       time.Time
}

// ExperienceRecord captures the outcome of a single task execution.
type ExperienceRecord struct {
	TaskID      string    `json:"task_id"`
	Description string    `json:"description"`
	Runtime     string    `json:"runtime"`
	Success     bool      `json:"success"`
	Duration    float64   `json:"duration_secs"`
	Error       string    `json:"error,omitempty"`
	Skills      []string  `json:"skills_used"`
	Timestamp   time.Time `json:"timestamp"`
}

// SkillProfile tracks proficiency in a specific skill area.
type SkillProfile struct {
	Name        string  `json:"name"`
	Attempts    int     `json:"attempts"`
	Successes   int     `json:"successes"`
	AvgDuration float64 `json:"avg_duration_secs"`
	SuccessRate float64 `json:"success_rate"`
	Trend       string  `json:"trend"` // "improving", "stable", "declining"
	LastUsed    int64   `json:"last_used"`
}

// StrategyProfile holds the agent's evolved decision-making preferences.
type StrategyProfile struct {
	PreferredRuntime  string            `json:"preferred_runtime"`
	RuntimeScores     map[string]float64 `json:"runtime_scores"`
	SkillConfidence   map[string]float64 `json:"skill_confidence"`
	DelegateThreshold float64           `json:"delegate_threshold"` // below this confidence → delegate
	AdaptedAt         int64             `json:"adapted_at"`
}

// NewEvolutionEngine creates an evolution engine for the given agent.
func NewEvolutionEngine(a *Agent) *EvolutionEngine {
	return &EvolutionEngine{
		agent:             a,
		journal:           make([]*ExperienceRecord, 0, 64),
		skills:            make(map[string]*SkillProfile),
		strategy:          &StrategyProfile{
			RuntimeScores:   make(map[string]float64),
			SkillConfidence: make(map[string]float64),
			DelegateThreshold: 0.3,
		},
		reflectInterval:   5 * time.Minute,
		minRecordsReflect: 3,
	}
}

// Record logs a task outcome into the experience journal.
func (e *EvolutionEngine) Record(rec *ExperienceRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec.Timestamp = time.Now()
	e.journal = append(e.journal, rec)

	// Update skill profiles
	for _, skill := range rec.Skills {
		sp, ok := e.skills[skill]
		if !ok {
			sp = &SkillProfile{Name: skill}
			e.skills[skill] = sp
		}
		sp.Attempts++
		if rec.Success {
			sp.Successes++
		}
		sp.SuccessRate = float64(sp.Successes) / float64(sp.Attempts)
		sp.AvgDuration = (sp.AvgDuration*float64(sp.Attempts-1) + rec.Duration) / float64(sp.Attempts)
		sp.LastUsed = time.Now().Unix()
		e.updateTrend(sp)
	}

	// Update runtime scores
	if rec.Runtime != "" {
		score := e.strategy.RuntimeScores[rec.Runtime]
		if rec.Success {
			score += 1.0
		} else {
			score -= 0.5
		}
		e.strategy.RuntimeScores[rec.Runtime] = score
	}

	// Auto-reflect if enough records accumulated since last reflect
	if len(e.journal) >= e.minRecordsReflect && time.Since(e.lastReflect) >= e.reflectInterval {
		go e.reflect()
	}
}

// updateTrend calculates the trend over last N attempts.
func (e *EvolutionEngine) updateTrend(sp *SkillProfile) {
	if sp.Attempts < 3 {
		sp.Trend = "stable"
		return
	}

	// Look at recent records for this skill
	recent := 0
	recentSuccess := 0
	count := 0
	for i := len(e.journal) - 1; i >= 0 && count < 5; i-- {
		rec := e.journal[i]
		for _, s := range rec.Skills {
			if s == sp.Name {
				recent++
				if rec.Success {
					recentSuccess++
				}
				count++
				break
			}
		}
	}

	if recent == 0 {
		sp.Trend = "stable"
		return
	}

	recentRate := float64(recentSuccess) / float64(recent)
	if recentRate > sp.SuccessRate+0.1 {
		sp.Trend = "improving"
	} else if recentRate < sp.SuccessRate-0.1 {
		sp.Trend = "declining"
	} else {
		sp.Trend = "stable"
	}
}

// reflect performs a self-evaluation cycle: analyze patterns → adapt strategy.
func (e *EvolutionEngine) reflect() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.journal) == 0 {
		return
	}
	e.lastReflect = time.Now()

	// Discover new skills (log only, don't acquire yet)
	newSkills := e.discoverNewSkills()
	if len(newSkills) > 0 {
		fmt.Printf("🧬 [%s] Discovered potential new skills: %v\n", e.agent.cfg.Agent.Name, newSkills)
	}

	// Delegate to localReflect for strategy update + persistence
	e.localReflect()
}

// discoverNewSkills identifies skill patterns in task descriptions
// that aren't in the agent's declared skill set.
func (e *EvolutionEngine) discoverNewSkills() []string {
	declaredSkills := make(map[string]bool)
	for _, s := range e.agent.cfg.Agent.Skills {
		declaredSkills[strings.ToLower(s)] = true
	}

	observed := make(map[string]int)
	for _, rec := range e.journal {
		if !rec.Success {
			continue
		}
		for _, s := range rec.Skills {
			s = strings.ToLower(s)
			if !declaredSkills[s] {
				observed[s]++
			}
		}
	}

	var newSkills []string
	for skill, count := range observed {
		if count >= 2 { // appeared at least twice successfully
			newSkills = append(newSkills, skill)
		}
	}
	return newSkills
}

// ShouldDelegate returns true if the agent's confidence for required skills
// is below the delegation threshold.
func (e *EvolutionEngine) ShouldDelegate(requiredSkills []string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(requiredSkills) == 0 {
		return false
	}

	totalConfidence := 0.0
	for _, skill := range requiredSkills {
		if conf, ok := e.strategy.SkillConfidence[strings.ToLower(skill)]; ok {
			totalConfidence += conf
		}
		// Unknown skill → 0 confidence
	}
	avgConfidence := totalConfidence / float64(len(requiredSkills))
	return avgConfidence < e.strategy.DelegateThreshold
}

// BestRuntime returns the agent's preferred runtime based on experience.
func (e *EvolutionEngine) BestRuntime() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.strategy.PreferredRuntime
}

// SkillProfiles returns a snapshot of all tracked skill profiles.
func (e *EvolutionEngine) SkillProfiles() map[string]*SkillProfile {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make(map[string]*SkillProfile, len(e.skills))
	for k, v := range e.skills {
		cp := *v
		out[k] = &cp
	}
	return out
}

// Strategy returns a snapshot of the current strategy profile.
func (e *EvolutionEngine) Strategy() *StrategyProfile {
	e.mu.RLock()
	defer e.mu.RUnlock()

	cp := *e.strategy
	return &cp
}

// persistState saves evolution data to the memory store.
func (e *EvolutionEngine) persistState() {
	if e.agent.memory == nil {
		return
	}

	// Save strategy profile
	strategyJSON, _ := json.Marshal(e.strategy)
	e.agent.memory.Put(&memory.Entry{
		AgentID: e.agent.identity.PublicKeyHex()[:16],
		Key:     "evolution:strategy",
		Value:   string(strategyJSON),
		Metadata: map[string]string{
			"type": "evolution_state",
		},
	})

	// Save skill profiles
	skillsJSON, _ := json.Marshal(e.skills)
	e.agent.memory.Put(&memory.Entry{
		AgentID: e.agent.identity.PublicKeyHex()[:16],
		Key:     "evolution:skills",
		Value:   string(skillsJSON),
		Metadata: map[string]string{
			"type": "evolution_state",
		},
	})

	// Save recent journal (last 50 entries)
	start := 0
	if len(e.journal) > 50 {
		start = len(e.journal) - 50
	}
	journalJSON, _ := json.Marshal(e.journal[start:])
	e.agent.memory.Put(&memory.Entry{
		AgentID: e.agent.identity.PublicKeyHex()[:16],
		Key:     "evolution:journal",
		Value:   string(journalJSON),
		Metadata: map[string]string{
			"type": "evolution_state",
		},
	})
}

// RestoreState loads previous evolution state from memory.
func (e *EvolutionEngine) RestoreState() {
	if e.agent.memory == nil {
		return
	}

	// Restore strategy
	if entry, err := e.agent.memory.Get("evolution:strategy"); err == nil && entry != nil {
		var strategy StrategyProfile
		if json.Unmarshal([]byte(entry.Value), &strategy) == nil {
			e.strategy = &strategy
			if e.strategy.RuntimeScores == nil {
				e.strategy.RuntimeScores = make(map[string]float64)
			}
			if e.strategy.SkillConfidence == nil {
				e.strategy.SkillConfidence = make(map[string]float64)
			}
		}
	}

	// Restore skills
	if entry, err := e.agent.memory.Get("evolution:skills"); err == nil && entry != nil {
		var skills map[string]*SkillProfile
		if json.Unmarshal([]byte(entry.Value), &skills) == nil {
			e.skills = skills
		}
	}

	// Restore journal
	if entry, err := e.agent.memory.Get("evolution:journal"); err == nil && entry != nil {
		var journal []*ExperienceRecord
		if json.Unmarshal([]byte(entry.Value), &journal) == nil {
			e.journal = journal
		}
	}

	if len(e.journal) > 0 {
		fmt.Printf("🧬 [%s] Restored evolution state: %d experiences, %d skills\n",
			e.agent.cfg.Agent.Name, len(e.journal), len(e.skills))
	}
}

// RecentJournal returns the last N experience records.
func (e *EvolutionEngine) RecentJournal(limit int) []*ExperienceRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if limit <= 0 {
		limit = 50
	}
	total := len(e.journal)
	start := 0
	if total > limit {
		start = total - limit
	}
	result := make([]*ExperienceRecord, total-start)
	copy(result, e.journal[start:])
	return result
}

// TotalExperiences returns the total count of experiences.
func (e *EvolutionEngine) TotalExperiences() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.journal)
}

// Stats returns a summary string for diagnostics.
func (e *EvolutionEngine) Stats() string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	total := len(e.journal)
	successes := 0
	for _, rec := range e.journal {
		if rec.Success {
			successes++
		}
	}

	var skills []string
	for name, sp := range e.skills {
		skills = append(skills, fmt.Sprintf("%s(%.0f%%)", name, sp.SuccessRate*100))
	}

	return fmt.Sprintf("experiences=%d success_rate=%.0f%% skills=[%s] preferred_runtime=%s",
		total,
		safePercent(successes, total),
		strings.Join(skills, ", "),
		e.strategy.PreferredRuntime,
	)
}

func safePercent(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}
