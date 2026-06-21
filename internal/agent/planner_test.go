package agent

import (
	"context"
	"strings"
	"testing"
)

func TestShouldPlan(t *testing.T) {
	cases := []struct {
		desc string
		want bool
	}{
		// action verbs trigger
		{"implement RFC-002 swarm protocol", true},
		{"fix bug in task_events broadcaster", true},
		{"refactor agent.go into smaller files", true},
		{"add feature: cross-agent skill install", true},
		{"build a CLI for managing skills", true},
		{"design a new evolution genome schema", true},

		// simple tasks stay on fast path
		{"what is 2+2?", false},
		{"explain how libp2p pubsub works", false},
		{"list all skills", false},
		{"hello", false},

		// long multi-clause without verbs still triggers
		{strings.Repeat("Some context. ", 30), true},
	}
	for _, c := range cases {
		got := shouldPlan(c.desc)
		if got != c.want {
			t.Errorf("shouldPlan(%q) = %v; want %v", truncate(c.desc, 40), got, c.want)
		}
	}
}

func TestParsePlan(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantSteps int
		wantErr   bool
	}{
		{
			name:      "bare json",
			raw:       `{"steps":[{"id":1,"description":"a","verify":"x"},{"id":2,"description":"b","verify":"y"}]}`,
			wantSteps: 2,
		},
		{
			name:      "fenced json",
			raw:       "```json\n{\"steps\":[{\"id\":1,\"description\":\"a\",\"verify\":\"x\"}]}\n```",
			wantSteps: 1,
		},
		{
			name:      "with prose prefix",
			raw:       "Here is the plan:\n```json\n{\"steps\":[{\"id\":1,\"description\":\"a\",\"verify\":\"x\"}]}\n```",
			wantSteps: 1,
		},
		{
			name:    "no json",
			raw:     "I cannot help with that.",
			wantErr: true,
		},
		{
			name:    "empty steps",
			raw:     `{"steps":[]}`,
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := parsePlan(c.raw)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got plan with %d steps", len(p.Steps))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(p.Steps) != c.wantSteps {
				t.Errorf("got %d steps, want %d", len(p.Steps), c.wantSteps)
			}
		})
	}
}

func TestParseVerdict(t *testing.T) {
	ok, reason, err := parseVerdict(`{"done":true,"reason":"all good"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || reason != "all good" {
		t.Errorf("got (ok=%v, reason=%q), want (true, \"all good\")", ok, reason)
	}

	ok, reason, err = parseVerdict("```json\n{\"done\":false,\"reason\":\"missing tests\"}\n```")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || !strings.Contains(reason, "missing") {
		t.Errorf("got (ok=%v, reason=%q), want (false, *missing*)", ok, reason)
	}

	if _, _, err := parseVerdict("nope"); err == nil {
		t.Error("expected error on non-JSON input")
	}
}

// fakePlanner is a deterministic Planner for testing the execution loop
// without spinning up a real LLM runtime. The script controls plan
// shape, step verdicts, and verifier errors per call.
type fakePlanner struct {
	plan         *Plan
	planErr      error
	verifyByStep map[int][]fakeVerdict // stepID → ordered verdicts (one per attempt)
	verifyCalls  map[int]int
}

type fakeVerdict struct {
	ok     bool
	reason string
	err    error
}

func (f *fakePlanner) Plan(_ context.Context, _ string) (*Plan, error) {
	if f.planErr != nil {
		return nil, f.planErr
	}
	return f.plan, nil
}

func (f *fakePlanner) Verify(_ context.Context, step Step, _ string) (bool, string, error) {
	if f.verifyCalls == nil {
		f.verifyCalls = map[int]int{}
	}
	idx := f.verifyCalls[step.ID]
	f.verifyCalls[step.ID] = idx + 1
	verdicts := f.verifyByStep[step.ID]
	if idx >= len(verdicts) {
		// default: pass
		return true, "", nil
	}
	v := verdicts[idx]
	return v.ok, v.reason, v.err
}

// We test parsing + heuristic logic directly; the full executeTaskWithPlan
// path requires a runtime.Runtime fake, which is out of scope for this
// commit (the e2e demo on a real runtime is the integration test). The
// next commit can add an in-package fake runtime if we want hermetic
// coverage of the loop itself.
