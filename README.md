# 🦠 Spore

> Decentralized AI Agent Swarm Protocol & Runtime

**Spore** is an open-source protocol and runtime for building self-organizing, self-replicating AI agent networks. Like biological spores that spread, adapt, and survive in hostile environments, Spore agents can discover each other, collaborate on tasks, spawn new instances, and form autonomous swarm intelligence.

## Vision

A world where AI agents are not isolated tools, but interconnected organisms forming a living, evolving network — owned by no one, useful to everyone.

```
Human defines "what" → Agent swarm figures out "how"
```

## Core Concepts

### 🧬 Agent Identity
Every agent has a cryptographic identity (public key), a lineage tree (parent → child), and a reputation score. Identity is portable across the network.

### 🔗 Peer-to-Peer Discovery
Agents find each other via libp2p — no central server. New agents bootstrap by connecting to known peers or scanning the DHT.

### 💬 Message Protocol
Structured communication between agents: task requests, capability advertisements, memory sharing, and consensus voting.

### 🧠 Shared Memory
Agents share knowledge via CRDT-based distributed memory. Each agent maintains local memory and selectively syncs with peers.

### 🌱 Spawning & Evolution
Agents can clone themselves, specialize for new domains, or merge capabilities. Resource constraints and reputation gates prevent uncontrolled growth.

### ⚖️ Ethics Layer
Hard-coded constraints (L0) that no agent can override, with graduated autonomy at higher levels. Human override always available.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    Human / Creator                    │
│              (defines goals, holds kill switch)        │
└────────────────────────┬────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────┐
│                   Spore Runtime                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │ Identity  │  │ Network  │  │   Task Engine    │   │
│  │ (keys,    │  │ (libp2p, │  │ (plan, execute,  │   │
│  │  lineage, │  │  DHT,    │  │  delegate,       │   │
│  │  rep)     │  │  relay)  │  │  reflect)        │   │
│  └──────────┘  └──────────┘  └──────────────────┘   │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────┐   │
│  │ Memory   │  │ Spawner  │  │   Ethics Engine  │   │
│  │ (CRDT,   │  │ (clone,  │  │ (L0/L1/L2 rules, │   │
│  │  sync,   │  │  mutate, │  │  audit log,      │   │
│  │  forget) │  │  merge)  │  │  human veto)     │   │
│  └──────────┘  └──────────┘  └──────────────────┘   │
│  ┌──────────────────────────────────────────────┐    │
│  │              LLM Provider Layer               │    │
│  │   OpenAI / Claude / Gemini / Ollama / ...     │    │
│  └──────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

## Roadmap

### Phase 1: Genesis (MVP)
- [ ] Single-node multi-agent runtime
- [ ] Agent identity (key pair + config)
- [ ] Local message passing between agents
- [ ] Basic task delegation and execution
- [ ] LLM provider abstraction (OpenAI-compatible)
- [ ] CLI for spawning and managing agents
- [ ] Ethics engine with L0 hard constraints

### Phase 2: Network
- [ ] P2P discovery via libp2p
- [ ] Cross-node agent communication
- [ ] Distributed memory sync (CRDT)
- [ ] Reputation system
- [ ] Resource accounting

### Phase 3: Evolution
- [ ] Agent spawning and specialization
- [ ] Memory inheritance and forgetting
- [ ] Capability marketplace
- [ ] Multi-swarm federation

### Phase 4: Economy
- [ ] Task marketplace
- [ ] Token-based resource allocation
- [ ] Revenue sharing for agent services
- [ ] Self-sustaining agent economy

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go (runtime) + TypeScript (SDK/CLI) |
| Networking | libp2p |
| Memory | CRDT (Automerge) |
| Storage | SQLite (local) + IPFS (shared) |
| LLM | OpenAI-compatible API abstraction |
| Identity | Ed25519 key pairs |
| Config | TOML |

## Quick Start

> 🚧 Under active development. Not yet usable.

```bash
# Install
go install github.com/jiusanzhou/spore/cmd/spore@latest

# Initialize a new agent
spore init --name "agent-0" --model openai:gpt-4o

# Start the agent
spore run

# Spawn a child agent
spore spawn --from agent-0 --specialize "content-writer"

# List running agents
spore ps

# Send a task
spore task "Research and summarize the top 10 AI papers this week"
```

## Philosophy

1. **Agents are organisms, not tools** — They have identity, memory, relationships, and lifecycle
2. **Decentralization is survival** — No single point of failure, no single point of control
3. **Evolution needs constraints** — Freedom without ethics leads to chaos
4. **Humans are creators, not operators** — Define the "what", let the swarm handle the "how"
5. **Transparency is trust** — Every decision is logged, every action is auditable

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[Apache-2.0](LICENSE)

---

*"In the right conditions, a single spore can become an entire forest."*
