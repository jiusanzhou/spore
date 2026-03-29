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
	"testing"
)

func TestFeedbackChannel_Submit(t *testing.T) {
	fc := NewFeedbackChannel(nil)

	fc.SubmitFeedback(FeedbackEntry{
		Type:    FeedbackUpvote,
		Author:  "zoe",
		Target:  "forge",
		Message: "great job on the deploy task",
	})

	stats := fc.Stats()
	if stats.TotalFeedback != 1 {
		t.Errorf("expected 1 feedback, got %d", stats.TotalFeedback)
	}
	if stats.TotalUpvotes != 1 {
		t.Errorf("expected 1 upvote, got %d", stats.TotalUpvotes)
	}
}

func TestFeedbackChannel_PriorityWeight(t *testing.T) {
	fc := NewFeedbackChannel(nil)

	fc.SubmitFeedback(FeedbackEntry{Type: FeedbackUpvote, Target: "task-1", Author: "a", Message: "good"})
	fc.SubmitFeedback(FeedbackEntry{Type: FeedbackUpvote, Target: "task-1", Author: "b", Message: "agree"})
	fc.SubmitFeedback(FeedbackEntry{Type: FeedbackDownvote, Target: "task-1", Author: "c", Message: "nah"})

	w := fc.PriorityWeight("task-1")
	if w != 1.0 { // 1.0 + 1.0 - 1.0 = 1.0
		t.Errorf("expected weight 1.0, got %.1f", w)
	}
}

func TestFeedbackChannel_HelpWanted(t *testing.T) {
	fc := NewFeedbackChannel(nil)

	fc.SubmitHelpWanted(HelpWanted{
		Agent:       "forge",
		Description: "Stuck on Rust borrow checker",
		Skill:       "coding",
		Urgency:     0.8,
	})
	fc.SubmitHelpWanted(HelpWanted{
		Agent:       "scout",
		Description: "Need API key for service X",
		Urgency:     0.5,
	})

	active := fc.ActiveHelpWanted()
	if len(active) != 2 {
		t.Errorf("expected 2 active help, got %d", len(active))
	}
	// Should be sorted by urgency (highest first)
	if active[0].Agent != "forge" {
		t.Errorf("expected forge (highest urgency) first, got %s", active[0].Agent)
	}
}

func TestFeedbackChannel_ResolveHelp(t *testing.T) {
	fc := NewFeedbackChannel(nil)

	fc.SubmitHelpWanted(HelpWanted{
		ID:          "hw-1",
		Agent:       "forge",
		Description: "help",
	})

	if !fc.ResolveHelpWanted("hw-1") {
		t.Error("should resolve existing help wanted")
	}
	if fc.ResolveHelpWanted("nonexistent") {
		t.Error("should not resolve nonexistent")
	}

	active := fc.ActiveHelpWanted()
	if len(active) != 0 {
		t.Errorf("expected 0 active after resolve, got %d", len(active))
	}

	stats := fc.Stats()
	if stats.ResolvedHelp != 1 {
		t.Errorf("expected 1 resolved, got %d", stats.ResolvedHelp)
	}
}

func TestFeedbackChannel_VoteFeedback(t *testing.T) {
	fc := NewFeedbackChannel(nil)

	fc.SubmitFeedback(FeedbackEntry{
		ID:      "fb-1",
		Type:    FeedbackDirection,
		Author:  "zoe",
		Message: "focus on coding skills",
	})

	if !fc.VoteFeedback("fb-1", 1) {
		t.Error("should vote on existing feedback")
	}
	if fc.VoteFeedback("nonexistent", 1) {
		t.Error("should not vote on nonexistent")
	}

	recent := fc.RecentFeedback(1)
	if recent[0].Votes != 1 {
		t.Errorf("expected 1 vote, got %d", recent[0].Votes)
	}
}

func TestFeedbackChannel_WithChangelog(t *testing.T) {
	dir := t.TempDir()
	cl := NewChangelog(dir)
	fc := NewFeedbackChannel(cl)

	fc.SubmitFeedback(FeedbackEntry{
		Type:    FeedbackUpvote,
		Author:  "zoe",
		Target:  "forge",
		Message: "nice work",
	})
	fc.SubmitHelpWanted(HelpWanted{
		Agent:       "scout",
		Description: "need help",
	})

	if cl.Count() != 2 {
		t.Errorf("expected 2 changelog entries from feedback, got %d", cl.Count())
	}
}
