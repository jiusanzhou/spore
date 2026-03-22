/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 */

package agent

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReputationRecord tracks one peer's reputation from our perspective.
type ReputationRecord struct {
	AgentID        string    `json:"agent_id"`
	Score          float64   `json:"score"`           // current reputation score (0.0 - 1.0)
	TasksCompleted int       `json:"tasks_completed"` // successful tasks delivered
	TasksFailed    int       `json:"tasks_failed"`    // failed tasks
	TasksTimeout   int       `json:"tasks_timeout"`   // timed-out tasks
	TotalRating    float64   `json:"total_rating"`    // sum of quality ratings received
	Violations     int       `json:"violations"`      // ethics violations detected
	LastInteract   time.Time `json:"last_interact"`   // last interaction time
	Isolated       bool      `json:"isolated"`        // quarantined due to violations
}

// ReputationEngine manages per-peer reputation scores for an agent.
type ReputationEngine struct {
	mu      sync.RWMutex
	records map[string]*ReputationRecord // keyed by agent ID
	workDir string
}

// NewReputationEngine creates a new reputation tracker.
func NewReputationEngine() *ReputationEngine {
	return &ReputationEngine{
		records: make(map[string]*ReputationRecord),
	}
}

// SetWorkDir sets the directory for reputation persistence.
func (r *ReputationEngine) SetWorkDir(dir string) {
	r.workDir = dir
	r.load()
}

// --- Score Updates ---

const (
	repInitial       = 0.5   // new peers start neutral
	repSuccessBase   = 0.05  // base score boost for task success
	repFailPenalty   = 0.10  // score penalty for task failure
	repTimeoutPenalty = 0.15 // score penalty for timeout (worse than failure)
	repViolationDrop = 0.5   // major score drop for ethics violation
	repDecayRate     = 0.01  // score decays toward neutral over time
	repDecayInterval = 24 * time.Hour
	repIsolateThreshold = 0.1 // below this score → isolated
	repRecoverThreshold = 0.3 // above this to un-isolate
)

// RecordSuccess updates reputation after a peer completes a task successfully.
// rating is the quality score (0.0 - 1.0) of the delivered result.
func (r *ReputationEngine) RecordSuccess(agentID string, rating float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.getOrCreate(agentID)
	rec.TasksCompleted++
	rec.TotalRating += clampRep(rating, 0, 1)
	rec.LastInteract = time.Now()

	// Score boost proportional to quality rating
	boost := repSuccessBase * (0.5 + rating*0.5)
	rec.Score = clampRep(rec.Score+boost, 0, 1)

	// Un-isolate if recovered
	if rec.Isolated && rec.Score >= repRecoverThreshold {
		rec.Isolated = false
		fmt.Printf("🔓 [reputation] %s un-isolated (score=%.2f)\n", agentID[:8], rec.Score)
	}

	r.persist()
}

// RecordFailure updates reputation after a peer fails a task.
func (r *ReputationEngine) RecordFailure(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.getOrCreate(agentID)
	rec.TasksFailed++
	rec.LastInteract = time.Now()
	rec.Score = clampRep(rec.Score-repFailPenalty, 0, 1)

	r.checkIsolation(rec)
	r.persist()
}

// RecordTimeout updates reputation after a peer times out on a task.
func (r *ReputationEngine) RecordTimeout(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.getOrCreate(agentID)
	rec.TasksTimeout++
	rec.LastInteract = time.Now()
	rec.Score = clampRep(rec.Score-repTimeoutPenalty, 0, 1)

	r.checkIsolation(rec)
	r.persist()
}

// RecordViolation updates reputation after an ethics violation.
func (r *ReputationEngine) RecordViolation(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec := r.getOrCreate(agentID)
	rec.Violations++
	rec.LastInteract = time.Now()
	rec.Score = clampRep(rec.Score-repViolationDrop, 0, 1)

	r.checkIsolation(rec)
	r.persist()
}

// --- Queries ---

// Score returns the current reputation score for a peer.
// Returns repInitial for unknown peers.
func (r *ReputationEngine) Score(agentID string) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rec, ok := r.records[agentID]
	if !ok {
		return repInitial
	}
	return rec.Score
}

// IsIsolated checks if a peer is quarantined.
func (r *ReputationEngine) IsIsolated(agentID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rec, ok := r.records[agentID]
	if !ok {
		return false
	}
	return rec.Isolated
}

// Get returns the full reputation record for a peer.
func (r *ReputationEngine) Get(agentID string) *ReputationRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rec, ok := r.records[agentID]
	if !ok {
		return &ReputationRecord{AgentID: agentID, Score: repInitial}
	}
	// Return a copy
	copy := *rec
	return &copy
}

// All returns all reputation records.
func (r *ReputationEngine) All() []*ReputationRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ReputationRecord, 0, len(r.records))
	for _, rec := range r.records {
		copy := *rec
		result = append(result, &copy)
	}
	return result
}

// Decay applies time-based decay — scores drift toward neutral (0.5).
// Called periodically (e.g., every evolution cycle).
func (r *ReputationEngine) Decay() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	changed := false
	for _, rec := range r.records {
		elapsed := now.Sub(rec.LastInteract)
		if elapsed < repDecayInterval {
			continue
		}
		periods := elapsed.Hours() / repDecayInterval.Hours()
		decay := repDecayRate * math.Min(periods, 10) // cap at 10 periods
		if rec.Score > repInitial {
			rec.Score = math.Max(repInitial, rec.Score-decay)
			changed = true
		} else if rec.Score < repInitial {
			rec.Score = math.Min(repInitial, rec.Score+decay)
			changed = true
		}
	}
	if changed {
		r.persist()
	}
}

// --- Internals ---

func (r *ReputationEngine) getOrCreate(agentID string) *ReputationRecord {
	rec, ok := r.records[agentID]
	if !ok {
		rec = &ReputationRecord{
			AgentID: agentID,
			Score:   repInitial,
		}
		r.records[agentID] = rec
	}
	return rec
}

func (r *ReputationEngine) checkIsolation(rec *ReputationRecord) {
	if !rec.Isolated && rec.Score <= repIsolateThreshold {
		rec.Isolated = true
		fmt.Printf("🔒 [reputation] %s isolated (score=%.2f, violations=%d)\n",
			rec.AgentID[:8], rec.Score, rec.Violations)
	}
}

func clampRep(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// --- Persistence ---

func (r *ReputationEngine) persist() {
	if r.workDir == "" {
		return
	}
	path := filepath.Join(r.workDir, "evolution", "reputation.yaml")
	os.MkdirAll(filepath.Dir(path), 0755)

	data, err := json.MarshalIndent(r.records, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0644)
}

func (r *ReputationEngine) load() {
	if r.workDir == "" {
		return
	}
	path := filepath.Join(r.workDir, "evolution", "reputation.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var records map[string]*ReputationRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return
	}
	r.records = records
	fmt.Printf("🏅 Loaded reputation data: %d peers\n", len(records))
}
