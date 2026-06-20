# RFC-001: ACP (Agent Client Protocol) Integration

**Status:** Implemented (2026-06)
**Author:** zoe
**Date:** 2026-06
**Related:** Phase 1 stream-json (commit `9417d24`), Phase 2 codex tests (commit `5ef20f7`)

**Implementation:** All three stages landed. See "Implementation Status" at the bottom
for per-stage commits, deliverables, and verification.

---

## TL;DR

Spore currently shells out to Claude Code / Codex / OpenCode etc. via `subprocess.run`-style
adapters. Phase 1 (stream-json parsing) made those calls **observable** but kept them
**unidirectional**: spore drives, the harness executes, returns. The harness can never
ask spore to do anything (delegate to a peer, query a skill, record an evolution).

ACP (Agent Client Protocol) — a JSON-RPC 2.0 stdio protocol introduced by Zed in Aug 2025,
now adopted by JetBrains, Neovim, and 50+ agent runtimes — fixes this. It is **bidirectional**:
the agent can call back into the client during a turn (request file I/O, request permissions,
spawn terminals, send streaming updates).

This RFC scopes a 3-stage ACP integration that turns spore from a "shell wrapper" into
a real **agent host**:

1. **Stage 1 (1-2 weeks): spore as ACP CLIENT** — replace claude_code/codex adapters with
   ACP transport. Cleaner, no fork-of-fork wrappers, and we get terminal/permission/diff
   primitives for free.
2. **Stage 2 (2-3 weeks): spore as ACP AGENT** — expose spore itself as an ACP-compliant
   agent. Now Zed/JetBrains/Neovim users can pick "Spore Network" from their agent menu
   and tap into the swarm.
3. **Stage 3 (1 week): MCP server bridge** — let any ACP-or-MCP capable agent reverse-call
   spore primitives (`delegate_to_swarm`, `query_skill`, `record_evolution`).

Stage 1 alone is the highest-ROI move: it's the path Hermes and OpenClaw both took, the
work has prior art we can read, and it lifts spore's "real integration" score from ~30%
to ~60%.

---

## Why ACP, why now

**The market consolidated faster than expected:**
- Claude Code published `@agentclientprotocol/claude-agent-acp` (~950k weekly downloads)
- Codex supports ACP via `acpx` and direct
- Gemini CLI, Cursor, GitHub Copilot, Goose, OpenCode — all in the ACP registry
- 50+ agents listed. JetBrains × Zed announced co-development Oct 2025

**For spore specifically:**
- Stage 1 deletes 4 of our 6 hand-rolled stream parsers. Those are maintenance debt
  the moment Anthropic/OpenAI rev their event schemas (which they do, often).
- Stage 2 gives spore distribution we cannot otherwise buy: every Zed/JetBrains user
  becomes a possible spore network entry-point, with no plugin to write.
- Stage 3 is what unlocks the real value prop ("collective skill memory accessible to
  every agent runtime"). Without it, spore's network features are unreachable from any
  agent that isn't the spore CLI itself.

**ACP vs the alternative (MCP only):**
- MCP = tool calls (one direction: agent → tool). Useful but doesn't solve "agent
  drives spore drives swarm".
- ACP = full session lifecycle, bidirectional, with permission/terminal/file primitives.
  Strict superset for our use case.
- Both are JSON-RPC 2.0 over stdio. They compose: an ACP agent can serve MCP tools.

---

## Current state (post Phase 1+2)

```
┌─────────────┐                                ┌─────────────┐
│   spore     │  exec.CommandContext(...)      │ claude / codex/│
│ (driver)    │ ─────────────────────────────► │  openhands     │
│             │                                │                │
│             │  ◄── stream-json (one-way) ────│                │
└─────────────┘                                └─────────────┘
```

- 6 runtime adapters in `internal/runtime/`, each ~100-300 lines.
- 2 (claude_code, codex) parse stream-json → StreamEvent (Phase 1+2). The other 4 still
  black-box.
- No way for the harness to call back into spore. The harness cannot:
  - request a peer agent to take over a subtask
  - ask spore for a skill it has but the harness doesn't
  - record an experience to the evolution journal
  - persist a learning
- Every harness has its own event schema we have to keep up with.

**Realistic integration depth:** ~30%.

---

## Target state

### Stage 1: spore as ACP CLIENT (1-2 weeks)

Replace `internal/runtime/claude_code.go` and `internal/runtime/codex.go` with a single
`internal/runtime/acp.go` that speaks ACP to any compliant agent.

```
┌─────────────┐                                ┌─────────────────────┐
│   spore     │  ACP: initialize → session/    │ claude-agent-acp    │
│ (ACP client)│  new → session/prompt           │ codex-acp           │
│             │ ◄────────────────────────────► │ gemini-cli (ACP)    │
│             │  session/update notifications  │ openhands-acp       │
│             │  fs/read_text_file (we serve)  │ ...                 │
│             │  terminal/create (we serve)    │                     │
│             │  request_permission (we serve) │                     │
└─────────────┘                                └─────────────────────┘
```

**Concrete deliverables:**
- New file: `internal/runtime/acp.go` — thin wrapper around `github.com/ironpark/acp-go`
  implementing `Runtime` + `StreamingRuntime`
- ACP `session_update` notifications map cleanly to existing `StreamEvent` types:
  - `agent_message_chunk` → `EventThinking`
  - `tool_call` (start) → `EventToolCall`
  - `tool_call_update` (completion with output) → `EventToolResult`
  - `plan` → currently we map this to thinking; could promote to `EventPlan`
- We implement client-side handlers (`fs/read_text_file`, `fs/write_text_file`,
  `terminal/create`) that delegate to spore's own sandbox / vfs / terminal subsystems.
  This is the new code; the rest is glue.
- Config gets `runtime.acp.command = "claude-agent-acp"` (or `codex-acp`, `acpx`, etc.)
  selecting which underlying agent to launch.
- claude_code.go and codex.go become thin shims: they now just configure ACP runtime
  with the right binary. Or we deprecate them — depends on whether non-ACP claude/codex
  invocations still have value (probably not after Phase 1; ACP supersedes).

**Risk:**
- ACP spec is at v1 but still evolving. Pin to a specific protocol version, write
  conformance tests against fixture transcripts (same pattern as Phase 1+2).
- `ironpark/acp-go` has 28 stars, 24 commits, MIT — small but actively maintained,
  has HTTP+SSE transport (useful for future remote agents). Alternative:
  `joshgarnett/agent-client-protocol-go` (smaller, code-gen from official schema).
- We absorb dependency on `claude-agent-acp` npm package being installed. Fall back to
  current stream-json parser when unavailable (graceful degradation, not hard requirement).

**Work breakdown (~50 hours):**
- 4h: read ironpark/acp-go source + run their examples against claude-agent-acp
- 8h: write `internal/runtime/acp.go` (Runtime + StreamingRuntime impl)
- 6h: implement client-side fs handler (delegate to existing spore sandbox)
- 4h: implement client-side terminal handler (delegate to existing exec runtime?)
- 6h: implement permission handler (interactive — can defer to "always allow" v1, then
  wire to telegram gateway for confirmation prompts)
- 6h: fixture-driven unit tests (mirror `claude_code_test.go` pattern)
- 6h: integration tests against real `claude-agent-acp` subprocess
- 4h: deprecate claude_code.go / codex.go OR keep as fallback (decision)
- 6h: wire ACP runtime into `runtime.Manager` discovery
- buffer: ~8h for protocol surprises

**Success criteria:**
- spore agent can invoke any ACP-compatible runtime via single config change
- All Phase 1+2 stream-json tests still pass (legacy adapters intact OR removed cleanly)
- End-to-end: spore CLI → ACP runtime → tool call → fs read served by spore → result
- Race detector clean

### Stage 2: spore as ACP AGENT (2-3 weeks)

Expose spore itself as an ACP-compliant agent. Run `spore acp` and it becomes a stdio
JSON-RPC server speaking ACP. Now Zed/JetBrains/Neovim/CodeCompanion users can pick
"Spore Network" as their agent and the swarm executes their prompts.

```
┌──────────────────┐  ACP: initialize/new/    ┌─────────────┐
│ Zed / JetBrains  │  prompt                  │ spore acp   │
│ Neovim/etc       │ ───────────────────────► │ (ACP agent) │
│ (ACP client)     │ ◄─── session_update ──── │             │
│                  │                          │   ↓         │
└──────────────────┘                          │ swarm peers │
                                              │ skill cache │
                                              │ evolution   │
                                              └─────────────┘
```

**Deliverables:**
- New: `cmd/spore/acp.go` — `spore acp` subcommand starts an ACP agent on stdio
- New: `internal/agent/acp_server.go` — implements `acp.Agent` interface from
  `ironpark/acp-go`. Routes:
  - `session/prompt` → spore's existing task pipeline (decide locally vs delegate)
  - `session_update` notifications stream back from runtime/swarm
  - Tool calls reported as `tool_call` updates with our internal tool_id
- Capability advertisement during `initialize`: declare we support load_session
  (resume from journal), MCP servers (forward to our MCP manager), prompt audio if
  Telegram gateway is wired
- Registry submission: PR to https://agentclientprotocol.com/get-started/agents to list
  spore (one-line manifest)

**Risk:**
- This is a larger surface area; ACP agents need to handle session lifecycle, cancellation,
  error reporting per spec. Plan for one full pass through the ACP spec doc set.
- Editor users have UX expectations (diffs, permission prompts) we don't natively render
  — but ACP defines the protocol; they render it. We just emit the right messages.

**Work breakdown (~80 hours):**
- 8h: read ACP v1 spec end-to-end + write a conformance checklist
- 12h: `internal/agent/acp_server.go` — agent-side handlers
- 8h: session lifecycle (new/load/list/cancel/close — most of these stabilized, listed
  in the LLM index above)
- 12h: stream agent execution → session_update notifications (reuse Phase 1+2 events as
  source-of-truth)
- 8h: tool call lifecycle (start, update, complete with content) — map from `StreamEvent`
- 8h: permission requests (when builtin runtime wants to do something risky, ask the
  client) — defer if needed for v1
- 8h: `cmd/spore/acp.go` subcommand + integration with existing daemon model
- 6h: fixtures + protocol conformance tests
- 6h: live test in Zed and JetBrains
- buffer: 12h for spec edge cases

**Success criteria:**
- `spore acp` registered in Zed's settings.json works end-to-end
- A prompt from Zed routes through spore swarm and returns streamed output
- Permission prompts in Zed work for sensitive operations
- Listed in https://agentclientprotocol.com/get-started/agents

### Stage 3: MCP bridge (1 week)

Already done in Phase 1 (commit `917f950` introduced the MCP manager). What's missing
is the **server side**: spore exposes spore primitives as MCP tools, callable by any
MCP-or-ACP agent.

**Deliverables:**
- New: `internal/mcp/server.go` — MCP server implementation (spore is currently MCP
  CLIENT only; this adds server)
- Tools to expose:
  - `delegate_to_swarm(task, capabilities[]) -> task_id` — fan out to peer agents
  - `query_skill(name) -> SkillProfile | null` — read from swarm-shared skill cache
  - `record_evolution(experience) -> ack` — write to evolution journal
  - `discover_peers(capability) -> Peer[]` — peer discovery via libp2p
- Activation: `spore mcp serve` subcommand (mirrors `spore acp`)

**Work breakdown (~30 hours):**
- 4h: scope which spore primitives make sense as MCP tools (some are too internal)
- 8h: implement MCP server using existing `mark3labs/mcp-go` lib
- 6h: wire to swarm/skill/evolution subsystems
- 4h: tests + fixtures
- 4h: integration test with claude-agent-acp configured to use spore as MCP server
- 4h: README + example configs (Zed, Claude Desktop, Cursor)

---

## Sequencing

Stage 1 → Stage 3 → Stage 2 (NOT Stage 1 → Stage 2 → Stage 3).

**Why:**
- Stage 1 is pure win, low risk, deletes code. Do first.
- Stage 3 is small, makes spore *useful from outside* even before Stage 2 ships
  (any MCP-capable agent can reach spore primitives). Critical for "is anyone using
  this" feedback.
- Stage 2 is the big one. Worth doing only if Stage 3 telemetry shows external agents
  actually call our primitives.

**Total scope if all three:** ~160 hours = 4-5 weeks of focused work.
**Stage 1 alone:** ~50 hours = 1.5 weeks.

---

## Decisions to make before starting

1. **Library: ironpark/acp-go vs joshgarnett/agent-client-protocol-go vs roll-our-own?**
   - Recommend ironpark — has middleware, HTTP transport, more commits, MIT
   - Fallback to roll-our-own if either lib has API churn during Stage 1
2. **Drop legacy adapters (claude_code.go / codex.go) post-Stage-1, or keep as fallback?**
   - Recommend keep for one release cycle as `legacy_*` for users without ACP-aware CLIs
   - Mark `Deprecated:` and emit warning on use
3. **Permission UX in Stage 1: auto-approve, prompt-via-telegram, or interactive TTY?**
   - Recommend auto-approve in dev mode, telegram-gateway-prompt in prod
   - Reuse Telegram gateway from Phase 2 (commit `917f950`)
4. **Do we need Stage 2 at all?**
   - Answer revealed by Stage 3 metrics. If 0 external MCP calls in 1 month → no demand,
     skip Stage 2 and invest elsewhere.

---

## Non-goals

- Implementing ACP HTTP+SSE transport. Stdio only for v1.
- Authoring our own ACP-compatible IDE. Spore is the agent side, not the editor side.
- Wrapping every ACP feature. Subset that matches what claude-code/codex actually
  emit and what spore actually consumes.
- Backwards compat with pre-Phase-1 adapter behavior. Stream events ARE the contract now.

---

## Appendix A: ACP message flow (informative)

```
client                                      agent
  │ ── initialize (capabilities) ──────────► │
  │ ◄── InitializeResponse (capabilities) ── │
  │                                          │
  │ ── session/new(cwd, mcp_servers) ──────► │
  │ ◄── SessionId ─────────────────────────  │
  │                                          │
  │ ── session/prompt(text) ───────────────► │
  │                                          │  agent loop:
  │ ◄── session/update agent_message_chunk   │   - generate
  │ ◄── session/update tool_call (started)   │   - call tool
  │ ── fs/read_text_file ─────────────────── │   - need file
  │ ◄── ReadTextFileResponse (content)       │
  │ ── (response) ──────────────────────────►│
  │ ◄── session/update tool_call (complete)  │
  │ ◄── session/update agent_message_chunk   │   - generate more
  │ ◄── PromptResponse (stop_reason: end_turn)
  │                                          │
  │ ── session/cancel (notification, opt) ──►│
```

## Appendix B: Phase 1+2 → Stage 1 mapping

| StreamEvent (today)     | ACP equivalent                          |
|-------------------------|-----------------------------------------|
| `EventInit`             | `initialize` response + `session/new`   |
| `EventThinking`         | `session/update agent_message_chunk`    |
| `EventToolCall`         | `session/update tool_call (pending)`    |
| `EventToolResult`       | `session/update tool_call_update (complete)` |
| `EventComplete`         | `PromptResponse stop_reason=end_turn`   |
| `EventError (fatal)`    | `PromptResponse stop_reason=cancelled` + error details |
| `EventError (transient)`| `session/update agent_message_chunk` (logged) |

So Phase 1+2 was **not wasted work**: those events are the spore-internal IR and
Stage 1 just translates ACP into them. Nothing is rewritten.

---

## Implementation Status

**All three stages landed in a single push, 2026-06.** Total: 5 commits, ~5k lines of
production + test + demo code. Full test suite green with `-race -count=20`.

### Stage 1 — spore as ACP client ✅

**Commits:**
- `b8ac87e` — `feat(runtime): ACP client adapter — RFC-001 Stage 1` (+1524)
- `16d7d6d` — `feat(runtime): wire ACPRuntime into AutoDiscover; ACP claims 'claude-code'` (+326 -40)
- `fc96367` — `refactor(runtime): delete legacy claude_code stream-json adapter` (+81 -768)

**Deliverables:**
- `internal/runtime/acp.go` (833 lines) — ACP JSON-RPC 2.0 client. Hand-rolled because
  `joshgarnett/acp-go` v0.2 has a wire-format bug. Implements `initialize`,
  `session/new`, `session/prompt`, plus `session/update` notification handler for
  streaming `agent_message_chunk` / `tool_call` / `tool_call_update` → StreamEvent IR.
- `internal/runtime/acp_test.go` — 19 unit tests, all `-race -count=20` clean.
- `internal/runtime/registry.go` — three-tier `AutoDiscover`: ACP first → native
  adapters → abox fallback. New `aliasRuntime` maps abox `claude` → `claude-code`
  when ACP is absent (preserves backward-compat for existing manifests).
- `cmd/acp-runtime-demo/` + `cmd/acp-registry-demo/` — end-to-end demos.
- **Deleted:** `internal/runtime/claude_code.go` (-405), its test (-269),
  `cmd/runtime-stream-demo/` (-95). Made good on RFC promise to remove 4-of-6 hand-rolled
  stream parsers.

**Race bugs fixed during implementation** (both pre-existing latent issues exposed
by the larger test surface):
1. `call()`'s two-select randomization — first select no longer observes `c.closed`;
   second select peeks `respCh` when `c.closed` fires.
2. Write deadlock — moved the actual `c.out.Write` into a goroutine, with `ctx done`
   closing the writer to unblock.

**Verification:** `acp-runtime-demo "What is 2+2?"` → `claude-agent-acp v0.48` → `"4"`.
`spore runtimes` now lists `claude-code (source=acp, healthy=true)`.

### Stage 2 — spore as ACP server ✅

**Commit:** `b8384ce` — `feat(runtime): ACP server — RFC-001 Stage 2` (+1166)

**Deliverables:**
- `internal/runtime/acp_server.go` (~370 lines) — ACP server side. Reuses the existing
  `acpClient` as a JSON-RPC peer (ACP is symmetric peer-to-peer), so server-side just
  registers method handlers — no new transport package needed.
- `internal/runtime/acp_server_test.go` — 8 tests, `-race -count=20` clean.
- `cmd/spore-acp-server/main.go` — stdio entry point with `--inner` flag (default:
  `claude-code`).
- `cmd/acp-server-demo/main.go` — three-hop end-to-end chain demo.

**Agent identity:** `spore/0.1.0`. Implements required `initialize`, `session/new`,
`session/prompt`; pushes `session/update` notifications for streaming.

**Verification (three-hop chain):**
```
acp-server-demo (ACPRuntime as client)
  ↓ stdio
spore-acp-server (ACP server, inner=claude-code)
  ↓ spawns ACPRuntime
claude-agent-acp v0.48
```
All three hops carry streaming chunks and final response correctly.

**Streaming dedup bug fixed:** server was double-echoing the final answer for
streaming inner runtimes (`"4242"` instead of `"42"`). Streaming runtimes already
emit the answer via `agent_message_chunk` events; server now only appends a final
echo for non-streaming inner runtimes.

### Stage 3 — MCP server bridge ✅

**Commit:** `efbcd7d` — `feat(mcpserver): expose spore primitives as MCP tools — RFC-001 Stage 3` (+954)

**Deliverables:**
- `internal/mcpserver/server.go` — MCP server using `mark3labs/mcp-go`. Wraps a
  `SwarmAccessor` interface (satisfied by `*swarm.Manager`) and exposes 8 tools.
- `internal/mcpserver/server_test.go` — 11 test groups; tests use the library's
  `NewInProcessClient` so no subprocess is needed for unit tests.
- `cmd/spore-mcp-server/main.go` — stdio entry point. Loads agent config from a
  spore data dir, brings up the full swarm (LLM-equipped agents really run, so
  `spore_send_task` works), then runs `mcpserver.ServeStdio`.
- `cmd/mcp-server-demo/main.go` — end-to-end demo via real subprocess + mcp-go
  stdio client.

**The 8 tools exposed:**

| Tool                       | Backed by                              | Purpose                       |
|----------------------------|----------------------------------------|-------------------------------|
| `spore_list_agents`        | `Swarm.List()`                         | All agent `Info` snapshots    |
| `spore_get_agent`          | `Swarm.GetAgent(name).Info()`          | Single agent full state       |
| `spore_send_task`          | `Swarm.SendTask(agent, desc)`          | Delegate a task               |
| `spore_swarm_stats`        | `Swarm.Stats()`                        | Counts + transport mode       |
| `spore_recent_tasks`       | `Swarm.TaskLog(limit)`                 | Recent task events (1-200)    |
| `spore_agent_skills`       | `Agent.Skills().ActiveSkills()`        | Active skill list             |
| `spore_agent_experience`   | `Agent.Evolution().BuildDigest()`      | Evolution digest              |
| `spore_peer_fitness`       | `Agent.PeerEvo().Rankings()`           | Peer fitness rankings         |

**Verification:**
```
$ spore-mcp-server --dir examples/consciousness-demo/scout &
$ mcp-server-demo
[demo] initialized — server=spore/0.1.0
[demo] 8 tools advertised
[demo] spore_list_agents → scout (30 skills, full Info)
[demo] spore_swarm_stats → 1 agent, transport=local
```

### Architectural notes

- **Symmetric peer reuse**: ACP is JSON-RPC peer-to-peer at the wire level; spore's
  `acpClient` works as both client (Stage 1) and server (Stage 2) via different
  handler-registration paths. No second transport implementation.
- **Three-tier runtime discovery** preserves backward-compat: existing manifests that
  reference `claude-code` keep working whether ACP is installed, abox is installed,
  or both. ACP wins when present.
- **Separation of concerns**: `internal/mcp/` remains MCP *client* (consuming external
  MCP servers as tools); `internal/mcpserver/` is MCP *server* (exposing spore as
  tools to others). Different packages, no collision.

### What this unlocks

Per the RFC's original framing:
- Stage 1 alone lifts spore's "real integration" score from ~30% → ~60%, and removes
  maintenance debt every time Anthropic/OpenAI rev their event schemas.
- Stage 2 makes every Zed/JetBrains/Neovim user a possible spore network entry-point.
- Stage 3 is what makes the value prop reachable: any MCP-capable agent (Claude Code,
  Codex, Cursor, Goose, Zed, etc.) can now read spore's swarm state and delegate
  into the swarm — the "collective skill memory accessible to every agent runtime"
  promise from the TL;DR.

Spore is now a true bidirectional ACP+MCP node: consumes ACP agents, serves itself
as an ACP agent, and exposes its swarm primitives as MCP tools to the entire agent
ecosystem.
