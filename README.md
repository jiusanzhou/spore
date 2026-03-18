# 🦠 Spore

> Decentralized AI Agent Swarm Protocol & Runtime

**Spore** is an open-source protocol and runtime for building self-organizing AI agent networks. Agents discover each other via P2P, collaborate on tasks, spawn new instances, and form autonomous swarm intelligence — no central server required.

```
Human defines "what" → Agent swarm figures out "how"
```

## Quick Start

```bash
# Install from source
git clone https://go.zoe.im/spore && cd spore
make build

# Or: go install go.zoe.im/spore/cmd/spore@latest
```

### Run a single agent

```bash
# Initialize agent config
spore init --name my-agent --model gpt-4o-mini

# Start it
spore run
```

### Run a multi-agent swarm

```bash
# Start 3 agents with interactive REPL + HTTP API
spore swarm -n 3 -m gpt-4o-mini --api-port 8080
```

```
🦠 Spore swarm started!

NAME          ROLE         RUNTIME  STATUS   MODEL        TASKS  UPTIME
coordinator   coordinator  builtin  running  gpt-4o-mini  0      1s
worker-1      worker       builtin  running  gpt-4o-mini  0      1s
worker-2      worker       builtin  running  gpt-4o-mini  0      1s

spore> task coordinator "Research top 3 AI frameworks and compare them"
📋 Task abc123 queued for coordinator

spore> ps
spore> quit
```

### Send tasks from the CLI

```bash
# Send a task to a running agent
spore task coordinator "Summarize the benefits of decentralized AI"

# List running agents
spore ps

# Check peer connections
spore peers
```

### P2P mode (two terminals)

```bash
# Terminal 1: start a P2P node
spore run -c examples/demo/p2p-node1.toml

# Terminal 2: connect to the first node
spore run -c examples/demo/p2p-node2.toml
```

## Configuration

Spore uses TOML config files. Run `spore init` to generate one, or see [`configs/default.toml`](configs/default.toml) for all options.

Key environment variables:

| Variable | Description |
|----------|-------------|
| `SPORE_LLM_API_KEY` | LLM provider API key (OpenAI, etc.) |
| `OPENAI_API_KEY` | Fallback API key |

Example config:

```toml
[agent]
name = "coordinator"
role = "coordinator"

[llm]
provider = "openai"
model = "gpt-4o-mini"

[network]
transport = "libp2p"
listen = ["/ip4/0.0.0.0/tcp/9001"]

[ethics]
max_budget_per_task = 1.0
```

See [`examples/demo/`](examples/demo/) for ready-to-run configurations.

## Architecture

```
┌─────────────────────────────────────────────┐
│              CLI / HTTP API                  │
├─────────────────────────────────────────────┤
│              Swarm Orchestrator              │
├──────────┬──────────┬───────────────────────┤
│ Identity │ Network  │    Task Engine        │
│ (Ed25519 │ (libp2p, │ (plan, execute,       │
│  keys,   │  DHT,    │  delegate, reflect)   │
│  lineage)│  relay)  │                       │
├──────────┼──────────┼───────────────────────┤
│ Memory   │ Spawner  │    Ethics Engine      │
│ (SQLite, │ (clone,  │ (L0/L1/L2 rules,     │
│  IPFS,   │  mutate, │  privacy filter,      │
│  CRDT)   │  merge)  │  audit log)           │
├──────────┴──────────┴───────────────────────┤
│           LLM Provider Layer                │
│  OpenAI / Claude / Gemini / Ollama / ...    │
├─────────────────────────────────────────────┤
│        Pluggable Runtime System             │
│  builtin / claude-code / codex / openclaw   │
└─────────────────────────────────────────────┘
```

## What Works Now (v0.1.0)

- **Agent identity** — Ed25519 key pairs, lineage tracking
- **Multi-agent swarm** — coordinator + workers with interactive REPL
- **Task engine** — LLM-powered planning, execution, delegation
- **P2P networking** — libp2p transport, peer discovery, DHT
- **Shared memory** — SQLite (local) + IPFS (distributed)
- **Ethics engine** — L0/L1/L2 constraints, privacy filter, constitution
- **Economy** — budget tracking, hibernate thresholds
- **Pluggable runtimes** — builtin, Claude Code, Codex, OpenClaw, HTTP, exec
- **HTTP API** — REST endpoints for external integration
- **CLI** — `init`, `run`, `swarm`, `task`, `ps`, `peers`, `runtimes`

## Roadmap

| Phase | Focus | Status |
|-------|-------|--------|
| **1. Genesis** | Single-node runtime, identity, ethics, CLI | **Done** |
| **2. Network** | P2P discovery, cross-node comms, distributed memory | **Done** |
| **3. Evolution** | Agent spawning, memory inheritance, capability marketplace | In progress |
| **4. Economy** | Task marketplace, token allocation, self-sustaining economy | Planned |

## Philosophy

1. **Agents are organisms, not tools** — identity, memory, relationships, lifecycle
2. **Decentralization is survival** — no single point of failure or control
3. **Evolution needs constraints** — freedom without ethics leads to chaos
4. **Humans are creators, not operators** — define "what", let the swarm handle "how"
5. **Transparency is trust** — every decision logged, every action auditable

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go |
| Networking | libp2p |
| Storage | SQLite (local) + IPFS (shared) |
| LLM | OpenAI-compatible API abstraction |
| Identity | Ed25519 key pairs |
| Config | TOML |

## Contributing

Contributions welcome! Areas where help is most needed:

- Additional runtime integrations
- P2P protocol improvements
- Memory sync and CRDT strategies
- Agent specialization patterns
- Documentation and examples

```bash
# Development
make build      # Build binary
make test       # Run tests
make fmt        # Format code
make lint       # Run go vet
make demo       # Quick swarm demo
```

## License

[Apache-2.0](LICENSE)

---

*"In the right conditions, a single spore can become an entire forest."*
