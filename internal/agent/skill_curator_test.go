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
	"context"
	"strings"
	"testing"
)

// newTestSkillFS spins up a SkillFS rooted in t.TempDir() and seeds one skill.
// Returns the FS and the seed skill's name.
func newTestSkillFS(t *testing.T) (*SkillFS, string) {
	t.Helper()

	// nil PublishFunc — we don't exercise IPFS publishing in unit tests.
	fs, err := NewSkillFS(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewSkillFS: %v", err)
	}

	body := "# Heading\n" +
		"Step 1: do the thing.\n" +
		"Step 2: do the other thing.\n"

	skill, err := fs.Create(SkillMeta{
		Name:        "demo-skill",
		Description: "A skill for tests",
	}, body)
	if err != nil {
		t.Fatalf("Create skill: %v", err)
	}
	return fs, skill.Meta.Name
}

func TestSkillPatchTool_Name_and_Description(t *testing.T) {
	tool := NewSkillPatchTool(nil)
	if tool.Name() != "skill_patch" {
		t.Errorf("Name = %q, want skill_patch", tool.Name())
	}
	if !strings.Contains(tool.Description(), "JSON object") {
		t.Errorf("Description should mention JSON; got %q", tool.Description())
	}
}

func TestSkillPatchTool_NoFS(t *testing.T) {
	tool := NewSkillPatchTool(nil)
	_, err := tool.Execute(context.Background(), `{"skill":"x","old":"a","new":"b"}`)
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected 'not initialized' error, got %v", err)
	}
}

func TestSkillPatchTool_InvalidInputs(t *testing.T) {
	fs, _ := newTestSkillFS(t)
	tool := NewSkillPatchTool(fs)
	ctx := context.Background()

	cases := []struct {
		name   string
		input  string
		errSub string
	}{
		{"empty", "", "empty input"},
		{"bad json", "not json", "invalid JSON"},
		{"missing skill", `{"old":"a","new":"b"}`, "'skill' is required"},
		{"missing old", `{"skill":"demo-skill","new":"b"}`, "'old' is required"},
		{"identical", `{"skill":"demo-skill","old":"x","new":"x"}`, "no-op"},
		{"unknown skill", `{"skill":"nope","old":"x","new":"y"}`, "not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := tool.Execute(ctx, c.input)
			if err == nil {
				t.Fatalf("expected error containing %q", c.errSub)
			}
			if !strings.Contains(err.Error(), c.errSub) {
				t.Fatalf("error %q missing substring %q", err.Error(), c.errSub)
			}
		})
	}
}

func TestSkillPatchTool_Success(t *testing.T) {
	fs, name := newTestSkillFS(t)
	tool := NewSkillPatchTool(fs)
	ctx := context.Background()

	out, err := tool.Execute(ctx, `{"skill":"`+name+`","old":"do the thing","new":"do the better thing"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "patched") {
		t.Errorf("output %q should announce a patch", out)
	}
	if tool.PatchCount() != 1 {
		t.Errorf("PatchCount = %d, want 1", tool.PatchCount())
	}

	skill, ok := fs.Get(name)
	if !ok {
		t.Fatal("skill disappeared after patch")
	}
	if !strings.Contains(skill.Body, "do the better thing") {
		t.Errorf("body did not pick up replacement: %q", skill.Body)
	}
	if strings.Contains(skill.Body, "do the thing.\n") {
		t.Errorf("original line should be gone: %q", skill.Body)
	}
}

func TestSkillPatchTool_AmbiguousAnchorRejected(t *testing.T) {
	fs, _ := newTestSkillFS(t)

	// Add a second occurrence of "Step" so the anchor is ambiguous.
	body := "# h\nStep alpha.\nStep beta.\n"
	if _, err := fs.Create(SkillMeta{Name: "amb"}, body); err != nil {
		t.Fatal(err)
	}

	tool := NewSkillPatchTool(fs)
	_, err := tool.Execute(context.Background(),
		`{"skill":"amb","old":"Step","new":"Stage"}`)
	if err == nil || !strings.Contains(err.Error(), "appears 2 times") {
		t.Fatalf("expected ambiguous-anchor rejection, got %v", err)
	}
	if tool.PatchCount() != 0 {
		t.Errorf("PatchCount should still be 0 after rejection")
	}
}

func TestSkillNoteTool_AppendsBullet(t *testing.T) {
	fs, name := newTestSkillFS(t)
	tool := NewSkillNoteTool(fs)
	ctx := context.Background()

	out, err := tool.Execute(ctx,
		`{"skill":"`+name+`","note":"Watch out for symlink loops"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "noted") {
		t.Errorf("output should announce a note, got %q", out)
	}

	skill, _ := fs.Get(name)
	if !strings.Contains(skill.Body, pitfallsHeader) {
		t.Errorf("body missing pitfalls header:\n%s", skill.Body)
	}
	if !strings.Contains(skill.Body, "- Watch out for symlink loops") {
		t.Errorf("body missing bullet:\n%s", skill.Body)
	}

	// Second note should sit under the same header, not create a new one.
	if _, err := tool.Execute(ctx,
		`{"skill":"`+name+`","note":"Don't forget to chmod"}`); err != nil {
		t.Fatal(err)
	}
	skill, _ = fs.Get(name)
	if strings.Count(skill.Body, pitfallsHeader) != 1 {
		t.Errorf("expected exactly one pitfalls header, got %d:\n%s",
			strings.Count(skill.Body, pitfallsHeader), skill.Body)
	}
	if !strings.Contains(skill.Body, "- Don't forget to chmod") {
		t.Errorf("second bullet missing:\n%s", skill.Body)
	}
	if tool.NoteCount() != 2 {
		t.Errorf("NoteCount = %d, want 2", tool.NoteCount())
	}
}

func TestSkillNoteTool_StripsLeadingDash(t *testing.T) {
	fs, name := newTestSkillFS(t)
	tool := NewSkillNoteTool(fs)
	ctx := context.Background()

	if _, err := tool.Execute(ctx,
		`{"skill":"`+name+`","note":"- already has dash"}`); err != nil {
		t.Fatal(err)
	}

	skill, _ := fs.Get(name)
	if strings.Contains(skill.Body, "- - already has dash") {
		t.Errorf("note tool should strip a leading '- ': %q", skill.Body)
	}
	if !strings.Contains(skill.Body, "- already has dash") {
		t.Errorf("note line missing: %q", skill.Body)
	}
}

func TestSkillCurator_LazyFn(t *testing.T) {
	// Demonstrates that NewSkillPatchToolFn doesn't panic on a nil resolver
	// and produces a clear error message that hints at the cause.
	tool := NewSkillPatchToolFn(func() *SkillFS { return nil })
	_, err := tool.Execute(context.Background(),
		`{"skill":"x","old":"a","new":"b"}`)
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected init error, got %v", err)
	}

	noteTool := NewSkillNoteToolFn(nil) // also nil-safe
	_, err = noteTool.Execute(context.Background(), `{"skill":"x","note":"y"}`)
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected init error, got %v", err)
	}
}
