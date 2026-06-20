# Quickstart: spore + MCP

**MCP (Model Context Protocol)** is the JSON-RPC protocol Anthropic
introduced for tool/resource sharing. Spore speaks **both directions**:

- **MCP client** (`internal/mcp`) — pulls tools from external MCP
  servers (filesystem, git, custom) into every spore agent.
- **MCP server** (`internal/mcpserver`, this guide) — exposes
  spore's swarm as MCP tools to anything that can speak MCP (Claude
  Code, Codex, Cursor, Goose, Zed, your own client).

This is what makes spore usable from inside any agent runtime. You
don't have to be a spore CLI user to query the swarm — you just point
your existing agent's MCP config at `spore-mcp-server` and call tools
like `spore_send_task` or `spore_propose_skill`.

---

## Prerequisites

```bash
cd spore
go build -o ./bin/spore-mcp-server ./cmd/spore-mcp-server

# Use an existing spore data dir (or any of the demo agents shipped
# in this repo)
ls examples/consciousness-demo
# boss/  forge/  scout/
```

---

## 30-second demo

A self-contained verification — boots `spore-mcp-server` over stdio,
opens an in-process MCP client, calls every tool, prints results:

```bash
./bin/spore-mcp-server --dir examples/consciousness-demo/scout &
SERVER_PID=$!
./bin/mcp-server-demo
kill $SERVER_PID
```

Expected output:

```
[demo] initialized — server=spore/0.1.0
[demo] 9 tools advertised
[demo] spore_list_agents       → scout (30 skills, full Info)
[demo] spore_swarm_stats       → 1 agent, transport=local
[demo] spore_agent_skills      → []
[demo] spore_agent_experience  → digest{...}
...
```

---

## Wire it into Claude Code

Add a server entry to `~/.config/claude-code/mcp.json`
(or `~/Library/Application Support/Claude/mcp.json` on macOS):

```json
{
  "servers": {
    "spore": {
      "command": "/absolute/path/to/spore-mcp-server",
      "args": ["--dir", "/absolute/path/to/your/spore/agent"]
    }
  }
}
```

Restart Claude Code. In the chat:

> Use the `spore` MCP server to list every agent in my swarm and
> tell me which one has the most skills.

Claude Code will call `spore_list_agents`, get the full `Info` array,
and answer. From there you can ask it to delegate a real task:

> Send a task to `scout` asking it to research the latest GoReleaser
> v2 release notes.

Claude Code will call `spore_send_task` and you can poll progress
with `spore_recent_tasks`.

---

## Wire it into Codex / Cursor / Goose / Zed

Every MCP-capable agent uses roughly the same JSON shape — `command`
and `args` pointing at the `spore-mcp-server` binary. Consult your
agent's docs for the exact config-file path:

| Agent       | Config file (rough)                          |
|-------------|----------------------------------------------|
| Claude Code | `~/.config/claude-code/mcp.json`             |
| Codex       | `~/.config/codex/mcp.json` (path varies)     |
| Cursor      | Settings → MCP servers (UI)                  |
| Goose       | `~/.config/goose/config.yaml` (`extensions`) |
| Zed         | Settings → context-server settings           |

---

## The 9 tools spore exposes

| Tool                       | Direction      | What it does |
|----------------------------|----------------|--------------|
| `spore_list_agents`        | read           | Every agent in the swarm with full `Info` |
| `spore_get_agent`          | read           | Detailed `Info` for one agent |
| `spore_send_task`          | write (low)    | Submit a task to a named agent |
| `spore_swarm_stats`        | read           | Aggregated swarm counters |
| `spore_recent_tasks`       | read           | Recent task lifecycle events |
| `spore_agent_skills`       | read           | One agent's active skills |
| `spore_agent_experience`   | read           | Evolution digest (drives, fitness, learnings) |
| `spore_peer_fitness`       | read           | Peer-evolution rankings |
| `spore_propose_skill`      | write (gated)  | Contribute a new skill — ethics-screened |

The write tools are deliberately conservative — `spore_send_task` runs
in an isolated agent runtime, and `spore_propose_skill` is gated by
the agent's ethics engine (destructive shell patterns and data-exfil
attempts are L0-rejected and never touch disk).

---

## Showcase: contributing a skill from Claude Code

The most differentiated tool is `spore_propose_skill`. It turns any
MCP-capable agent into a *contributor* to spore's collective memory
rather than just a reader. Try this in Claude Code:

> I just figured out that when the corporate Go proxy is unreachable,
> setting `GOPROXY=https://goproxy.cn,direct` works as a fallback.
> Use the `spore` MCP server's `spore_propose_skill` tool to teach
> this to my agent named `scout`.
>
> The skill should be:
>
> - name: `goproxy-cn-fallback`
> - description: "Use Aliyun GOPROXY mirror when corporate proxy is unreachable"
> - body: numbered steps for prepending the env var to a failing command
> - triggers: ["go module fetch hangs", "GOPROXY behind VPN"]
> - tags: ["go", "proxy", "network"]
> - proposer: "claude-code"

Claude Code calls `spore_propose_skill`. The tool:

1. Resolves agent `scout`.
2. Pushes the description and body through `scout`'s ethics engine
   (no destructive patterns, no exfil).
3. Persists the skill under `<scout>/skills/goproxy-cn-fallback/`
   with frontmatter `origin: proposed` and
   `source_task: mcp-propose: claude-code` for audit trail.

The tool returns:

```json
{
  "status": "accepted",
  "skill_name": "goproxy-cn-fallback",
  "agent": "scout",
  "origin": "proposed",
  "proposer": "claude-code",
  "reason": "passed ethics screening; persisted to SkillFS"
}
```

Now ask scout to do something Go-related and watch it pick the new
skill up automatically.

---

## What gets blocked

The ethics engine (`internal/ethics/engine.go`) blocks any proposal
where description **or** body matches an L0 pattern: destructive
file-system commands (`rm -rf /`, formatting commands, fork bombs),
device redirects to disk, or known data-exfil shapes. Rejections
return `status: "rejected"` with a structured `reason`, never write
to disk, and stay out of the audit log on the proposing client side.

If you need an `audit log` of *attempted* proposals (accepted *and*
rejected), they all go through `ethics.Engine.logAudit` on the
spore side — `spore` ships a `audit` subcommand for inspection.

---

## Troubleshooting

| Symptom | Likely cause / fix |
|---------|-------------------|
| `9 tools advertised` becomes `0` | spore-mcp-server crashed before initialize — check stderr; usually `--dir` points at an empty/invalid agent dir. |
| `spore_send_task` returns immediately, no progress | The target agent's runtime isn't configured. Check `agent.yaml → runtime.type` and `spore runtimes` output. |
| `spore_propose_skill → status: rejected` for harmless content | False-positive in regex L0 patterns. Open an issue with the offending body — we tune patterns conservatively. |
| Server hangs on first call | MCP handshake never completed. Verify your client sent `initialize` first; mcp-go is strict about lifecycle ordering. |

---

## What's next

- [QUICKSTART-ACP](./QUICKSTART-ACP.md) — the *other* protocol spore speaks bidirectionally.
- [RFC-001](./RFC-001-acp-integration.md) — full design + all three rollout stages.
- [JOINING.md](./JOINING.md) — turn your spore into a public swarm node.
