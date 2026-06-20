# Quickstart: spore + ACP

**ACP (Agent Client Protocol)** is the bidirectional JSON-RPC protocol Zed,
JetBrains, and Neovim use to talk to agent runtimes. Spore implements
**both sides** — it's a client (consumes ACP agents like Claude Code) and
a server (exposes itself as an ACP agent).

This guide gets you to working integrations on both directions in about
60 seconds each.

---

## Prerequisites

```bash
# Build the spore binaries (one-time)
cd spore
make build              # produces ./bin/spore
go build -o ./bin/spore-acp-server ./cmd/spore-acp-server

# For Direction A only (spore consumes Claude Code via ACP):
npm install -g @zed-industries/claude-agent-acp
which claude-agent-acp  # should resolve

# Configure an LLM (any OpenAI-compatible API)
mkdir -p ~/.spore
cat > ~/.spore/config.toml <<'EOF'
[llm]
provider = "openai"
model    = "gpt-4o"
base_url = "https://api.openai.com/v1"
api_key  = "sk-your-key"

[network]
transport = "local"
EOF

# Initialize an agent
./bin/spore init demo
```

---

## Direction A — spore as ACP **client**

Use case: you're running spore and want it to delegate sub-tasks to
Claude Code (or any ACP-compliant agent runtime) without you having to
maintain a hand-rolled stream-json parser.

### 30-second demo

```bash
./bin/acp-runtime-demo "What is 2+2?"
```

Expected output:

```
[demo] spawning claude-agent-acp ...
[demo] initialized session
[demo] streaming response:
4
[demo] complete in 1.8s
```

### What just happened

1. `acp-runtime-demo` started a fresh ACP session.
2. spore's `internal/runtime/acp.go` (the hand-rolled JSON-RPC client
   we use because the upstream `joshgarnett/acp-go` had a wire-format
   bug as of v0.2) spoke `initialize` → `session/new` → `session/prompt`
   over stdio.
3. `claude-agent-acp` ran the prompt, streamed back `agent_message_chunk`
   events, and we mapped them onto spore's internal `StreamEvent` IR —
   the same IR the legacy stream-json parser used, so nothing
   downstream changed.

### Wire it into a real agent

ACP is `auto`-discovered. If `claude-agent-acp` is on your `PATH`,
`./bin/spore runtimes` shows:

```
NAME           SOURCE  HEALTHY
claude-code    acp     true
codex          native  …
…
```

Any spore agent configured with `runtime = "claude-code"` (or just
`runtime = "auto"`, since ACP is preferred) will now use the ACP
client.

---

## Direction B — spore as ACP **server**

Use case: you want Zed / JetBrains / Neovim users to pick "Spore" from
their agent menu and tap into your swarm — no plugin to write.

### 30-second demo (proves the wire works)

```bash
./bin/acp-server-demo "summarise the chat I just gave you"
```

This runs a three-hop chain:

```
acp-server-demo  ─stdio→  spore-acp-server  ─stdio→  claude-agent-acp
   (ACP client)              (ACP server,                (real agent)
                              inner=claude-code)
```

Expected output ends with the real Claude Code response, verifying
that the spore ACP server correctly forwards `session/prompt` to its
inner runtime and pipes streaming chunks back to the client.

### Wire it into Zed

1. Open Zed → `cmd-,` → search for "agent servers".
2. Add a custom agent:

   ```json
   {
     "name": "Spore",
     "command": "/absolute/path/to/spore-acp-server",
     "args": ["--inner", "claude-code"]
   }
   ```

3. Open the assistant panel, switch the agent picker to **Spore**, and
   chat. Each message becomes a `session/prompt` call into
   `spore-acp-server`, which forwards it to the configured inner
   runtime (Claude Code by default).

### Wire it into JetBrains / Neovim

Same recipe — both IDEs expose an "ACP agent command" field. Point it
at `spore-acp-server`. Use `--inner codex` if you'd rather route
through Codex. Future versions will let the inner runtime delegate
into the spore swarm itself instead of just proxying to Claude/Codex.

---

## Troubleshooting

| Symptom | Likely cause / fix |
|---------|-------------------|
| `claude-agent-acp not found` | `npm install -g @zed-industries/claude-agent-acp`, then re-run `./bin/spore runtimes` to confirm `source=acp`. |
| `acp-runtime-demo` hangs | Inner runtime is waiting on input; check `~/.spore/runtime.log`. If stuck on `initialize`, the inner binary is too old — upgrade to `claude-agent-acp@>=0.48`. |
| `acp-server-demo` returns "4242" | You hit the streaming dedup bug we fixed in `b8384ce`. Pull the latest spore main. |
| Zed shows "agent disconnected" | spore-acp-server exited. Run it manually with `--inner claude-code` and check stderr — usually a missing API key. |

---

## What's next

- [QUICKSTART-MCP](./QUICKSTART-MCP.md) — same idea but for MCP tools (the *other* protocol spore speaks bidirectionally).
- [RFC-001](./RFC-001-acp-integration.md) — full design, all three rollout stages, architectural decisions.
- [JOINING.md](./JOINING.md) — turn your spore agent into a public swarm node (libp2p P2P, NAT traversal, IPFS skill sharing).
