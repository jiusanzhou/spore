/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 * Licensed under the Apache License 2.0
 */

package sessions

import (
	"strings"
	"testing"
)

func TestCreateAndList(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a, err := s.Create("agent-0", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.Agent != "agent-0" {
		t.Fatalf("bad session: %+v", a)
	}

	b, _ := s.Create("agent-1", "named")
	if b.Title != "named" {
		t.Fatalf("title not stored: %q", b.Title)
	}

	list, err := s.List(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(list))
	}
}

func TestAppendTurnsAndAutoTitle(t *testing.T) {
	s, _ := New(":memory:")
	defer s.Close()

	sess, _ := s.Create("agent-0", "")
	if _, err := s.AppendUserTurn(sess.ID, "Hello agent, how are you today?", "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistantTurn(sess.ID, "I am well, thanks!", "task-1", "builtin"); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(sess.ID)
	if got.TurnCount != 2 {
		t.Fatalf("turn_count = %d, want 2", got.TurnCount)
	}
	if got.Title != "Hello agent, how are you today?" {
		t.Fatalf("auto-title not applied: %q", got.Title)
	}

	turns, _ := s.Turns(sess.ID)
	if len(turns) != 2 || turns[0].Role != "user" || turns[1].Role != "assistant" {
		t.Fatalf("turn order/roles wrong: %+v", turns)
	}
}

func TestAutoTitleTruncates(t *testing.T) {
	s, _ := New(":memory:")
	defer s.Close()

	sess, _ := s.Create("agent-0", "")
	long := strings.Repeat("a", 100)
	s.AppendUserTurn(sess.ID, long, "")

	got, _ := s.Get(sess.ID)
	if !strings.HasSuffix(got.Title, "…") {
		t.Fatalf("long title should be truncated with ellipsis, got %q", got.Title)
	}
	if len([]rune(got.Title)) > 65 {
		t.Fatalf("title too long: %d runes", len([]rune(got.Title)))
	}
}

func TestTaskLinkConsumesOnce(t *testing.T) {
	s, _ := New(":memory:")
	defer s.Close()

	s.LinkTaskToSession("task-x", "sess-1")
	if got := s.SessionForTask("task-x"); got != "sess-1" {
		t.Fatalf("first lookup = %q, want sess-1", got)
	}
	// second lookup should return "" — link is consumed.
	if got := s.SessionForTask("task-x"); got != "" {
		t.Fatalf("link not consumed, got %q", got)
	}
}

func TestFormatHistory(t *testing.T) {
	turns := []*Turn{
		{Role: RoleUser, Content: "Q1"},
		{Role: RoleAssistant, Content: "A1"},
		{Role: RoleUser, Content: "Q2"},
	}
	out := FormatHistory(turns, 0)
	want := "User: Q1\n\nAssistant: A1\n\nUser: Q2\n\n"
	if out != want {
		t.Fatalf("FormatHistory mismatch:\n got: %q\nwant: %q", out, want)
	}

	// maxTurns trims oldest first.
	out2 := FormatHistory(turns, 2)
	want2 := "Assistant: A1\n\nUser: Q2\n\n"
	if out2 != want2 {
		t.Fatalf("FormatHistory(maxTurns=2) mismatch:\n got: %q\nwant: %q", out2, want2)
	}

	if got := FormatHistory(nil, 0); got != "" {
		t.Fatalf("empty history should return empty string, got %q", got)
	}
}

func TestDeleteCascades(t *testing.T) {
	s, _ := New(":memory:")
	defer s.Close()

	sess, _ := s.Create("agent-0", "")
	s.AppendUserTurn(sess.ID, "hi", "")
	s.AppendAssistantTurn(sess.ID, "hello", "task-1", "builtin")

	if err := s.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(sess.ID); got != nil {
		t.Fatalf("session should be deleted, got %+v", got)
	}
	turns, _ := s.Turns(sess.ID)
	if len(turns) != 0 {
		t.Fatalf("turns should cascade-delete, got %d", len(turns))
	}
}
