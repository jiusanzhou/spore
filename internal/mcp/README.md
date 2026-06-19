# MCP — Model Context Protocol Integration

> Spore agents can consume any [MCP](https://modelcontextprotocol.io) server
> as a set of tools, no Go wrapper required.

## What this gives you

The Model Context Protocol is an open spec for "external tool servers" —
processes that expose tools (and resources/prompts) over JSON-RPC. There are
already hundreds of servers in the wild: filesystem, GitHub, Postgres,
Playwright, Slack, Notion, Brave Search, fetch, time, memory, ... Anthropic's
catalog lives at <https://github.com/modelcontextprotocol/servers>.

Spore's built-in tool palette is small (`shell`, `search`, `fetch`,
`memory`, `delegate`, plus skill-evolved tools). MCP integration lets any
agent in your swarm **call any of those public servers** without hand-writing
a wrapper for each one.

## Configuration

Add an `[mcp]` section to your agent's `config.toml` (typically at
`~/.spore/<agent>/config.toml`):

```toml
[mcp]
enabled = true
tool_prefix = "mcp"          # optional, default "mcp"
init_timeout_seconds = 15    # optional, default 15
call_timeout_seconds = 60    # optional, default 60

# Filesystem server, scoped to one directory
[mcp.servers.fs]
transport = "stdio"
command   = "npx"
args      = ["-y", "@modelcontextprotocol/server-filesystem", "/Users/me/work"]

# GitHub server (Docker, env var passes the token)
[mcp.servers.github]
transport = "stdio"
command   = "docker"
args      = ["run", "-i", "--rm", "-e", "GITHUB_TOKEN", "ghcr.io/github/github-mcp-server"]
env       = { GITHUB_TOKEN = "ghp_..." }

# Limit which tools this server exposes (optional allow-list)
[mcp.servers.github]
allowed_tools = ["search_repositories", "get_issue", "list_pull_requests"]

# A remote HTTP server
[mcp.servers.notion]
transport = "http"
url       = "https://example.com/mcp"
headers   = { Authorization = "Bearer ..." }
```

Each server is started once at agent boot; the connection is reused for the
agent's lifetime.

## How it shows up to the agent

Each tool the server reports is registered with the engine as
`<prefix>:<server>:<tool>` — e.g. `mcp:fs:read_file`. The tool's description
is the server's description plus the input schema, so the LLM knows what
arguments to send.

The engine accepts free-form strings as tool input. The MCP wrapper handles
three forms automatically:

1. **JSON object** — used as-is: `{"path":"/etc/hosts","limit":10}`
2. **Bare string + single required string field** — coerced to
   `{<field>: input}`. Lets the LLM call `mcp:fs:read_file /etc/hosts`
   without writing JSON.
3. **Bare string with no obvious field** — wrapped as `{"input": <string>}`
   so loose servers still get something.

## Lifecycle

- **Load** happens in `agent.New()` after all other subsystems initialize.
  Any per-server failure (binary missing, bad URL, handshake timeout) is
  logged and skipped — surviving servers still work.
- **Reuse**: the underlying `*client.Client` lives for the agent's lifetime.
  Per-task engines (rebuilt in `runtime.Builtin.Execute`) get the same tool
  wrappers via `Builtin.MCPTools`, so we don't pay a stdio handshake per task.
- **Close**: `agent.Close()` shuts down all MCP clients. Stdio subprocesses
  exit when their pipe closes; HTTP/SSE connections are torn down cleanly.

## Architecture

```
┌──────────────────────────────────────────────┐
│ engine.Engine                                │
│   tools = map[string]engine.Tool             │
│           ├─ shell, search, fetch, memory    │
│           └─ mcp:fs:read_file, mcp:gh:...    │ ← one per remote tool
└──────────────────────────────────────────────┘
          │
          ▼ Execute(ctx, jsonOrBareString)
┌──────────────────────────────────────────────┐
│ mcp.tool                                     │
│   parses string → map[string]any             │
│   calls client.CallTool                      │
│   flattens CallToolResult.Content → string   │
└──────────────────────────────────────────────┘
          │
          ▼ JSON-RPC over stdio / HTTP
┌──────────────────────────────────────────────┐
│ mcp.Manager                                  │
│   one *client.Client per configured server   │
│   loaded once at agent startup               │
└──────────────────────────────────────────────┘
```

## Testing

Unit tests cover config validation, name building, argument parsing, and
result rendering — they need no external dependencies:

```bash
go test ./internal/mcp/...
```

An integration test under `//go:build mcp_integration` spawns the
[`@modelcontextprotocol/server-everything`](https://github.com/modelcontextprotocol/servers/tree/main/src/everything)
reference server via `npx`. It is skipped automatically when `npx` is
unavailable:

```bash
go test -tags mcp_integration ./internal/mcp/...
```

## Security

MCP servers run with the agent's own privileges. A few rules of thumb:

- **Filesystem servers**: always pass the explicit allowed root as the last
  arg (`@modelcontextprotocol/server-filesystem /scoped/path`).
- **Shell-execute servers**: avoid these in untrusted swarms; they break the
  ethics layer's command audit trail.
- **`allowed_tools`**: use it to reduce the LLM's option surface for
  servers that ship with sensitive tools you don't want exposed.
- **Secrets in config**: prefer env-var indirection (`env = { GITHUB_TOKEN
  = "..." }`) so token rotation does not require reissuing the config file.

## Differences from the runtime registry

Spore has two layers of pluggability:

| Layer            | What it pluggable swaps                              | Examples                              |
| ---------------- | ---------------------------------------------------- | ------------------------------------- |
| `runtime.Runtime` | The whole agent loop (Observe → Think → Act)         | claude-code, codex, openclaw, builtin |
| `engine.Tool`    | A single capability the LLM invokes inside the loop  | shell, search, **mcp:* * (this pkg)** |

MCP fits in the Tool layer: the agent's brain is still spore's engine (or
whatever runtime the user picked), but each turn can call out to dozens of
external tool servers that someone else wrote and ships standalone.
