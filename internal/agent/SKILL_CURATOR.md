# Inline Skill Curator

> Hermes-style "use it, fix it" loop for SKILL.md files.

## What it adds

Two new tools registered with every agent's engine:

| Tool          | When to use                                                          |
| ------------- | -------------------------------------------------------------------- |
| `skill_patch` | The skill has wrong/stale text and you have the exact substring.     |
| `skill_note`  | The skill is missing a pitfall/lesson; you have wisdom but no anchor. |

Both run synchronously inside the engine ACT phase, alongside `shell`,
`search`, `fetch`, `mcp:*`, and so on. No separate review pass, no extra
LLM round trip.

## Why "inline" and not "after the task"

Spore already has post-task evolution: `agent.runSkillAnalysis` calls
`SkillEvolver.Evolve` with the full task transcript. That path is great
for **structural** changes — promoting a one-off success into a brand-new
skill (CAPTURE), or generalising a fixed skill into a broader one (DERIVE).

It is **not** the right place for "I just used skill X and noticed it
mentions a deprecated flag." That signal is loudest the moment the LLM
sees the deprecated flag; by the time the post-task pass starts, the
specific text often has to be reconstructed from logs, and the model is
liable to over-edit ("this whole section feels old, let me rewrite it").

The curator gives the LLM a **scalpel** at use-time, while still letting
the post-task evolution pass do the broader refactors later.

## How the LLM uses it

These are real patterns the engine prompt encourages once the tools are
registered:

```text
ACTION: skill_patch {"skill":"github-pr-workflow","old":"gh pr review --merge","new":"gh pr merge"}
RESULT: ✏️ patched github-pr-workflow → generation 4, cid=Qm...

ACTION: skill_note {"skill":"systematic-debugging","note":"On macOS, lsof needs sudo to see other users' fds"}
RESULT: 📝 noted on systematic-debugging → generation 5, cid=Qm...
```

Notes accumulate under a single `## Pitfalls (auto-curated)` section so
they stay together and don't pollute the rest of the body.

## Safety rails

- **Unique-anchor enforcement.** If `old` appears more than once in the
  body, the patch is **rejected** with a clear hint to widen the anchor.
  Without this, `SkillFS.Patch`'s `strings.Replace(..., 1)` would silently
  edit the wrong occurrence — a much worse failure mode than rejection.
- **No bare strings.** Both tools require JSON input. Three positional
  arguments are too easy to confuse.
- **Versioned writes.** Every successful call goes through
  `SkillFS.writeSkillLocked`, which bumps the generation counter, hashes
  the new content, and (if a `PublishFunc` is wired) publishes to IPFS.
  Reverts are trivial because the previous revision is still in the
  filesystem-level backup the rest of the evolution system already uses.
- **Lazy FS resolution.** The tools are registered in `agent.New()`, but
  `SkillFS` is created later in `SetWorkDir`. We pass a closure
  (`NewSkillPatchToolFn(func() *SkillFS { return a.skillFS })`) so the
  tools resolve the FS at call time and return a clear error when called
  before the workdir is set.

## Where it sits in the architecture

```
                         ┌─────────────────────┐
                         │ engine.Engine       │
                         │   tools = {         │
                         │     shell, search,  │
                         │     fetch, mcp:*,   │
                         │     skill_patch,    │  ◀── this package
                         │     skill_note,     │  ◀── this package
                         │     ...             │
                         │   }                 │
                         └──────────┬──────────┘
                                    │ Execute
                                    ▼
                         ┌─────────────────────┐
                         │ SkillFS.Patch /     │
                         │ SkillFS.Update      │
                         └──────────┬──────────┘
                                    │ writeSkillLocked
                                    ▼
                         ┌─────────────────────┐
                         │ disk + IPFS publish │
                         └─────────────────────┘
```

The post-task evolution pipeline (analyzer → SkillEvolver) is unchanged
and runs alongside this. They share the SkillFS substrate, so a patch
from one is visible to the other immediately.

## Testing

```bash
go test ./internal/agent/ -run TestSkill
```

Unit tests cover: validation errors, success path, ambiguous-anchor
rejection, note appending (single + multiple), pitfalls section creation,
and lazy nil-FS handling.

## Future work

- **Approve-on-broadcast.** When a patched skill broadcasts its new CID
  to peers, peer reputation scoring should consider whether the patch
  improved or regressed the skill's success rate. This is a hook the
  reputation engine already has — wiring is a follow-up PR.
- **Patch budget.** Cap how many patches the curator can apply per task
  so a runaway model can't rewrite a skill into oblivion in one session.
  Easy to add (atomic counter on the tool); deferred until we observe
  a real instance of the problem.
