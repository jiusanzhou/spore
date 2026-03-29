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

package swarm

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ────────────────────────────────────────────────────────────────────────────
// Human Feedback Channel
//
// Allows humans to participate in agent evolution direction:
// - Upvote/downvote tasks (affects priority weighting)
// - Submit "help wanted" requests (broadcast to swarm)
// - Vote on evolution proposals (community + agent self-eval)
// - Provide direct skill feedback
//
// API-driven: humans interact via HTTP endpoints, agents via GossipSub.
// ────────────────────────────────────────────────────────────────────────────

// FeedbackType classifies human feedback.
type FeedbackType string

const (
	FeedbackUpvote    FeedbackType = "upvote"
	FeedbackDownvote  FeedbackType = "downvote"
	FeedbackHelpWanted FeedbackType = "help_wanted"
	FeedbackSkillNote FeedbackType = "skill_note"
	FeedbackDirection FeedbackType = "direction"    // evolution direction vote
)

// FeedbackEntry is a single piece of human feedback.
type FeedbackEntry struct {
	ID        string       `json:"id"`
	Type      FeedbackType `json:"type"`
	Author    string       `json:"author"`              // human identifier
	Target    string       `json:"target,omitempty"`     // agent name or task ID
	Skill     string       `json:"skill,omitempty"`      // related skill
	Message   string       `json:"message"`
	Weight    float64      `json:"weight"`               // computed priority weight
	Votes     int          `json:"votes"`                // community votes
	Timestamp time.Time    `json:"timestamp"`
	Resolved  bool         `json:"resolved"`
}

// HelpWanted is a request from an agent for human assistance.
type HelpWanted struct {
	ID          string    `json:"id"`
	Agent       string    `json:"agent"`
	Description string    `json:"description"`
	Skill       string    `json:"skill,omitempty"`
	Urgency     float64   `json:"urgency"` // 0-1
	Timestamp   time.Time `json:"timestamp"`
	Resolved    bool      `json:"resolved"`
}

// FeedbackChannel manages human-agent interaction.
type FeedbackChannel struct {
	mu          sync.RWMutex
	feedback    []FeedbackEntry
	helpWanted  []HelpWanted
	maxItems    int
	changelog   *Changelog // optional: record feedback in changelog
}

// NewFeedbackChannel creates a feedback channel.
func NewFeedbackChannel(changelog *Changelog) *FeedbackChannel {
	return &FeedbackChannel{
		maxItems:  200,
		changelog: changelog,
	}
}

// SubmitFeedback records human feedback.
func (fc *FeedbackChannel) SubmitFeedback(fb FeedbackEntry) {
	if fb.Timestamp.IsZero() {
		fb.Timestamp = time.Now()
	}
	if fb.ID == "" {
		fb.ID = fmt.Sprintf("fb-%d", time.Now().UnixNano())
	}

	// Compute weight based on type
	switch fb.Type {
	case FeedbackUpvote:
		fb.Weight = 1.0
	case FeedbackDownvote:
		fb.Weight = -1.0
	case FeedbackHelpWanted:
		fb.Weight = 2.0 // human help requests are high priority
	case FeedbackDirection:
		fb.Weight = 1.5
	default:
		fb.Weight = 0.5
	}

	fc.mu.Lock()
	fc.feedback = append(fc.feedback, fb)
	if len(fc.feedback) > fc.maxItems {
		fc.feedback = fc.feedback[len(fc.feedback)-fc.maxItems:]
	}
	fc.mu.Unlock()

	// Record in changelog
	if fc.changelog != nil {
		fc.changelog.Record(ChangeEntry{
			Type:    ChangeHumanFeedback,
			Agent:   fb.Target,
			Summary: fmt.Sprintf("[%s] %s: %s", fb.Author, fb.Type, fb.Message),
			Tags:    []string{string(fb.Type), fb.Author},
		})
	}
}

// VoteFeedback adds a community vote to an existing feedback entry.
func (fc *FeedbackChannel) VoteFeedback(feedbackID string, delta int) bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	for i := range fc.feedback {
		if fc.feedback[i].ID == feedbackID {
			fc.feedback[i].Votes += delta
			// Adjust weight by votes
			fc.feedback[i].Weight += float64(delta) * 0.5
			return true
		}
	}
	return false
}

// SubmitHelpWanted records an agent's request for human assistance.
func (fc *FeedbackChannel) SubmitHelpWanted(hw HelpWanted) {
	if hw.Timestamp.IsZero() {
		hw.Timestamp = time.Now()
	}
	if hw.ID == "" {
		hw.ID = fmt.Sprintf("hw-%d", time.Now().UnixNano())
	}

	fc.mu.Lock()
	fc.helpWanted = append(fc.helpWanted, hw)
	fc.mu.Unlock()

	if fc.changelog != nil {
		fc.changelog.Record(ChangeEntry{
			Type:    ChangeHumanFeedback,
			Agent:   hw.Agent,
			Summary: fmt.Sprintf("🆘 Help wanted from %s: %s", hw.Agent, hw.Description),
			Tags:    []string{"help_wanted", hw.Skill},
		})
	}
}

// ResolveHelpWanted marks a help wanted request as resolved.
func (fc *FeedbackChannel) ResolveHelpWanted(id string) bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	for i := range fc.helpWanted {
		if fc.helpWanted[i].ID == id {
			fc.helpWanted[i].Resolved = true
			return true
		}
	}
	return false
}

// PriorityWeight returns the aggregate human feedback weight for a target.
// Positive = humans want more of this, negative = humans disapprove.
func (fc *FeedbackChannel) PriorityWeight(target string) float64 {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	var total float64
	for _, fb := range fc.feedback {
		if fb.Target == target && !fb.Resolved {
			total += fb.Weight
		}
	}
	return total
}

// ActiveHelpWanted returns unresolved help wanted requests.
func (fc *FeedbackChannel) ActiveHelpWanted() []HelpWanted {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	var result []HelpWanted
	for _, hw := range fc.helpWanted {
		if !hw.Resolved {
			result = append(result, hw)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Urgency > result[j].Urgency
	})
	return result
}

// RecentFeedback returns the N most recent feedback entries.
func (fc *FeedbackChannel) RecentFeedback(n int) []FeedbackEntry {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if n <= 0 || n > len(fc.feedback) {
		n = len(fc.feedback)
	}
	start := len(fc.feedback) - n
	result := make([]FeedbackEntry, n)
	copy(result, fc.feedback[start:])
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// FeedbackStats returns summary statistics.
type FeedbackStats struct {
	TotalFeedback     int `json:"total_feedback"`
	TotalUpvotes      int `json:"total_upvotes"`
	TotalDownvotes    int `json:"total_downvotes"`
	ActiveHelpWanted  int `json:"active_help_wanted"`
	ResolvedHelp      int `json:"resolved_help"`
}

func (fc *FeedbackChannel) Stats() FeedbackStats {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	stats := FeedbackStats{TotalFeedback: len(fc.feedback)}
	for _, fb := range fc.feedback {
		switch fb.Type {
		case FeedbackUpvote:
			stats.TotalUpvotes++
		case FeedbackDownvote:
			stats.TotalDownvotes++
		}
	}
	for _, hw := range fc.helpWanted {
		if hw.Resolved {
			stats.ResolvedHelp++
		} else {
			stats.ActiveHelpWanted++
		}
	}
	return stats
}
