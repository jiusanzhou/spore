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
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
)

// ─────────────────────────────────────────────────────────────────────────────
// Inline Skill Curator
//
// Hermes-style "use it, fix it" loop: when the LLM consults a SKILL.md and
// notices that something is stale, missing, or outright wrong, it can call
// the skill_patch tool right then — the curator does NOT need to wait for
// the next post-task evolution cycle.
//
// Why a tool, not a post-task probe?
//
//   - The LLM already has the symptom in working memory at use-time. Asking
//     it again N seconds later, in a separate "review" call, costs another
//     round trip and routinely loses the specific text that needed editing.
//   - Tool-calls slot into the existing engine.Engine ACT phase with no new
//     control-flow surface area. Same retry/timeout/audit paths as every
//     other tool.
//   - Bounded blast radius: each call is a single string Replace through
//     SkillFS.Patch, atomic, recorded as a SkillFS revision with its own
//     content hash and IPFS CID. Reverts are trivial.
//
// Two tools are exposed together:
//
//   - skill_patch  — apply a targeted edit (preferred; like patch())
//   - skill_note   — append a "pitfalls" / "lessons" line to the body when
//                    the LLM has wisdom but no specific text to replace
// ─────────────────────────────────────────────────────────────────────────────

// SkillPatchTool implements engine.Tool. It is constructed by the agent at
// startup and registered with the engine so the LLM can call it during a
// task. Every successful patch is reflected immediately on disk and via
// IPFS (writeSkillLocked publishes new content), so subsequent agents
// pulling the skill see the fix.
type SkillPatchTool struct {
	fsFn     func() *SkillFS // lazy lookup — SkillFS is created after SetWorkDir
	patches  atomic.Int64    // monitoring: how many patches landed this lifetime
}

// NewSkillPatchTool wires the tool to a fixed SkillFS. Useful in tests where
// the FS is built up-front. Production code should prefer NewSkillPatchToolFn
// because the agent's SkillFS is created lazily after SetWorkDir runs.
func NewSkillPatchTool(fs *SkillFS) *SkillPatchTool {
	return &SkillPatchTool{fsFn: func() *SkillFS { return fs }}
}

// NewSkillPatchToolFn defers the FS lookup to call time. Pass a closure that
// returns the agent's *SkillFS; nil is treated as "skill system not ready"
// and the tool returns a clear error message rather than panicking.
func NewSkillPatchToolFn(fsFn func() *SkillFS) *SkillPatchTool {
	if fsFn == nil {
		fsFn = func() *SkillFS { return nil }
	}
	return &SkillPatchTool{fsFn: fsFn}
}

// fs resolves the underlying store; may return nil before SetWorkDir runs.
func (t *SkillPatchTool) fs() *SkillFS {
	if t.fsFn == nil {
		return nil
	}
	return t.fsFn()
}

// Name implements engine.Tool. Single colon-free identifier so the LLM
// prompt's `ACTION: <tool> <input>` parsing stays unambiguous.
func (t *SkillPatchTool) Name() string { return "skill_patch" }

// Description implements engine.Tool. Spelled out in detail because this is
// the LLM's only documentation for how to call it.
func (t *SkillPatchTool) Description() string {
	return `Patch a SKILL.md you just used when you noticed it was wrong, ` +
		`out of date, or missing a step. Use this the moment you see the ` +
		`problem — do not wait for a later evolution cycle.

Input: JSON object with three fields:
  {"skill": "<name>", "old": "<exact substring to replace>", "new": "<replacement>"}

Rules:
  - "old" must appear exactly once in the skill body.
  - To insert without removing, set "new" to "<old>\n<new content>".
  - To delete a stale line, set "new" to "" (empty string).
  - Keep edits minimal; one focused fix per call beats a sweeping rewrite.
  - You may call this multiple times in a single task; each call is recorded
    as its own revision with a content hash and IPFS CID.

Returns: a one-line summary of the change (skill name, generation, CID).`
}

// Execute implements engine.Tool. Input MUST be JSON; we deliberately do
// NOT accept a bare string here because the three fields are too easy to
// confuse positionally and a botched edit corrupts a skill.
func (t *SkillPatchTool) Execute(ctx context.Context, input string) (string, error) {
	fs := t.fs()
	if fs == nil {
		return "", fmt.Errorf("skill_patch: skill system not initialized yet (workdir not set?)")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("skill_patch: empty input; expected JSON {skill, old, new}")
	}

	var req struct {
		Skill string `json:"skill"`
		Old   string `json:"old"`
		New   string `json:"new"`
	}
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return "", fmt.Errorf("skill_patch: invalid JSON: %w", err)
	}

	req.Skill = strings.TrimSpace(req.Skill)
	if req.Skill == "" {
		return "", fmt.Errorf("skill_patch: 'skill' is required")
	}
	if req.Old == "" {
		return "", fmt.Errorf("skill_patch: 'old' is required (use skill_note for additions without anchor)")
	}
	if req.Old == req.New {
		return "", fmt.Errorf("skill_patch: 'old' and 'new' are identical — no-op")
	}

	// Reject patches whose anchor isn't unique. SkillFS.Patch already does
	// strings.Contains, but only the first occurrence is replaced. We want
	// the LLM to widen its anchor instead of silently editing the wrong
	// occurrence.
	if existing, ok := fs.Get(req.Skill); ok {
		count := strings.Count(existing.Body, req.Old)
		if count == 0 {
			return "", fmt.Errorf("skill_patch: 'old' text not found in skill %q", req.Skill)
		}
		if count > 1 {
			return "", fmt.Errorf("skill_patch: 'old' text appears %d times in skill %q; widen the anchor with surrounding context to make it unique", count, req.Skill)
		}
	}

	skill, err := fs.Patch(req.Skill, req.Old, req.New)
	if err != nil {
		return "", fmt.Errorf("skill_patch: %w", err)
	}
	t.patches.Add(1)

	cidHint := skill.Meta.IPFSCID
	if cidHint == "" {
		cidHint = "(pending publish)"
	}
	return fmt.Sprintf("✏️ patched %s → generation %d, cid=%s",
		skill.Meta.Name, skill.Meta.Generation, cidHint), nil
}

// PatchCount returns how many successful patches this tool has applied.
// Useful for tests and dashboards.
func (t *SkillPatchTool) PatchCount() int64 { return t.patches.Load() }

// ─────────────────────────────────────────────────────────────────────────────
// SkillNoteTool — append a pitfalls/lessons line without an exact anchor.
// ─────────────────────────────────────────────────────────────────────────────

// SkillNoteTool implements engine.Tool. It appends a single line under a
// "## Pitfalls (auto-curated)" section, creating the section if missing.
// This is the right call when the LLM has wisdom to record but no precise
// "old text" to replace.
type SkillNoteTool struct {
	fsFn  func() *SkillFS
	notes atomic.Int64
}

// NewSkillNoteTool wires the tool to a fixed SkillFS (test convenience).
func NewSkillNoteTool(fs *SkillFS) *SkillNoteTool {
	return &SkillNoteTool{fsFn: func() *SkillFS { return fs }}
}

// NewSkillNoteToolFn defers FS lookup to call time. See NewSkillPatchToolFn.
func NewSkillNoteToolFn(fsFn func() *SkillFS) *SkillNoteTool {
	if fsFn == nil {
		fsFn = func() *SkillFS { return nil }
	}
	return &SkillNoteTool{fsFn: fsFn}
}

func (t *SkillNoteTool) fs() *SkillFS {
	if t.fsFn == nil {
		return nil
	}
	return t.fsFn()
}

// Name implements engine.Tool.
func (t *SkillNoteTool) Name() string { return "skill_note" }

// Description implements engine.Tool.
func (t *SkillNoteTool) Description() string {
	return `Append a short pitfalls/lessons-learned line to a SKILL.md when ` +
		`you don't have an exact text anchor to replace.

Input: JSON object:
  {"skill": "<name>", "note": "<one-line lesson>"}

The note is appended under "## Pitfalls (auto-curated)" — a section the ` +
		`curator creates on first use. Each note is a bullet line so you can ` +
		`call this multiple times to accumulate learnings.

Prefer skill_patch when you can identify the exact lines that are wrong. ` +
		`Use skill_note for "watch out for X" / "don't forget Y" guidance.`
}

const pitfallsHeader = "## Pitfalls (auto-curated)"

// Execute implements engine.Tool.
func (t *SkillNoteTool) Execute(ctx context.Context, input string) (string, error) {
	fs := t.fs()
	if fs == nil {
		return "", fmt.Errorf("skill_note: skill system not initialized yet (workdir not set?)")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("skill_note: empty input; expected JSON {skill, note}")
	}

	var req struct {
		Skill string `json:"skill"`
		Note  string `json:"note"`
	}
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return "", fmt.Errorf("skill_note: invalid JSON: %w", err)
	}
	req.Skill = strings.TrimSpace(req.Skill)
	req.Note = strings.TrimSpace(req.Note)
	if req.Skill == "" || req.Note == "" {
		return "", fmt.Errorf("skill_note: both 'skill' and 'note' are required")
	}
	// Sanitise: collapse newlines so a single note stays on one bullet.
	req.Note = strings.ReplaceAll(req.Note, "\n", " ")
	req.Note = strings.TrimPrefix(req.Note, "- ")

	existing, ok := fs.Get(req.Skill)
	if !ok {
		return "", fmt.Errorf("skill_note: skill %q not found", req.Skill)
	}

	bullet := "- " + req.Note

	var newBody string
	if idx := strings.Index(existing.Body, pitfallsHeader); idx >= 0 {
		// Section exists — append below the header. Find header's line end
		// and insert directly after it, before any subsequent content.
		insertAt := idx + len(pitfallsHeader)
		// Skip any single newline immediately after the header.
		if insertAt < len(existing.Body) && existing.Body[insertAt] == '\n' {
			insertAt++
		}
		newBody = existing.Body[:insertAt] + bullet + "\n" + existing.Body[insertAt:]
	} else {
		// Section missing — append it at end of body.
		trimmed := strings.TrimRight(existing.Body, "\n")
		newBody = trimmed + "\n\n" + pitfallsHeader + "\n" + bullet + "\n"
	}

	skill, err := fs.Update(req.Skill, existing.Meta, newBody, "skill_note: appended pitfall")
	if err != nil {
		return "", fmt.Errorf("skill_note: %w", err)
	}
	t.notes.Add(1)

	cidHint := skill.Meta.IPFSCID
	if cidHint == "" {
		cidHint = "(pending publish)"
	}
	return fmt.Sprintf("📝 noted on %s → generation %d, cid=%s",
		skill.Meta.Name, skill.Meta.Generation, cidHint), nil
}

// NoteCount returns how many notes this tool has appended.
func (t *SkillNoteTool) NoteCount() int64 { return t.notes.Load() }
