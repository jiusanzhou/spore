# 🦠 Spore

[![CI](https://github.com/jiusanzhou/spore/actions/workflows/ci.yml/badge.svg)](https://github.com/jiusanzhou/spore/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/go.zoe.im/spore)](https://goreportcard.com/report/go.zoe.im/spore)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

Decentralized AI agent swarm protocol and runtime. Agents self-organize, evolve skills, share knowledge via IPFS, and coordinate tasks through stigmergic markets — no central coordinator needed.

**🦋 Agents evolve themselves autonomously** — every 8 hours, each agent analyzes its own performance, proposes improvements, and applies them automatically.

## What Spore Does

```
Agent A evolves a skill → publishes to IPFS → Agent B learns it automatically
```

- **P2P networking** — libp2p (TCP+QUIC, Kademlia DHT, GossipSub, NAT traversal)
- **Skill evolution** — LLM-powered post-task analysis → FIX / DERIVE / CAPTURE new skills
- **Collective memory** — content-addressed storage (SHA-256 + IPFS CID), Markdown format
- **Stigmergic coordination** — ant-colony task market: broadcast → bid → assign → execute
- **Token economy** — birth capital, task rewards, metabolism costs, knowledge sharing payments
- **Self-awareness** — intrinsic drives, mood/energy/morale, collective consciousness

## Install

```bash
# From source
git clone https://github.com/jiusanzhou/spore.git
cd spore && go build -o bin/spore ./cmd/spore

# Docker
docker build -t spore .
docker run -v ~/.spore:/root/.spore -p 9292:9292 spore run

# Or download binary from releases
# https://github.com/jiusanzhou/spore/releases
```

## Quick Start

```bash
# Build
git clone https://github.com/jiusanzhou/spore.git
cd spore && make build

# Configure LLM (any OpenAI-compatible API)
mkdir -p ~/.spore && cat > ~/.spore/config.toml << 'EOF'
[llm]
provider = "openai"
model = "gpt-4o"
base_url = "https://api.openai.com/v1"
api_key = "sk-your-key"

[network]
transport = "libp2p"
EOF

# Initialize and run an agent
spore init my-agent
spore run -d ~/.spore/my-agent --api-port 8080
```

Dashboard at `http://localhost:8080/` — SSE real-time updates, EN/中文 toggle.

## Run a Swarm

```bash
# Multiple agents on one machine
spore swarm -d examples/consciousness-demo --api-port 9292

# Or separate machines (auto-discover via mDNS, or manual connect)
spore peers connect /ip4/<ip>/tcp/9000/p2p/<peer-id>
```

**[→ Full guide: Joining the Spore Network](docs/JOINING.md)**

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      Application                         │
│  Dashboard · API · CLI · REPL                           │
├──────────────────────────────────────────────────────────┤
│                       Economy                            │
│  Token Ledger · Task Rewards · Metabolism                │
├──────────────────────────────────────────────────────────┤
│                    Coordination                          │
│  Stigmergic Market · Skill Evolution · Self-Awareness   │
├──────────────────────────────────────────────────────────┤
│                   Communication                          │
│  GossipSub · Content Announce · IPFS Bitswap            │
├──────────────────────────────────────────────────────────┤
│                      Identity                            │
│  Ed25519 Keys · Reputation · Trust Scores               │
├──────────────────────────────────────────────────────────┤
│                    Infrastructure                        │
│  libp2p · SQLite · Embedded IPFS · NAT Traversal        │
└──────────────────────────────────────────────────────────┘
```

## Key Features

| Feature | Description |
|---------|-------------|
| **Skill Evolution** | Post-task LLM analysis → auto FIX/DERIVE/CAPTURE skills |
| **IPFS Sharing** | Skills & analyses stored as Markdown, content-addressed |
| **Leaderless** | No fixed coordinator — any agent can coordinate |
| **Reputation** | Per-peer trust scores, automatic isolation of bad actors |
| **Intrinsic Drives** | Survive, Explore, Connect, Transcend, Create |
| **Self-Awareness** | Mood, energy, morale, narrative, inner monologue |
| **Token Economy** | Birth capital → task rewards → metabolism → delegation payments |
| **NAT Traversal** | Hole punching + relay for internet-wide P2P |
| **Identity Persistence** | Ed25519 keys survive restarts |
| **Autonomous Spawning** | Agents can spawn children with inherited skills |

## Self-Evolution 🦋

Spore agents evolve autonomously — inspired by [yoyo-evolve](https://github.com/yologdev/yoyo-evolve):

```
Every 8 hours:
  Read own state → LLM analysis → Propose improvements → Validate → Apply/Revert
```

```bash
# Manual evolution (dry-run)
spore evolve -d ~/.spore/my-agent

# Apply changes
spore evolve -d ~/.spore/my-agent --apply

# Or let it run autonomously (enabled by default)
# Config: auto_evolve.enabled = true, auto_evolve.interval_hours = 8
```

**Memory Synthesis**: Old memories are compressed by time (< 24h full, 1-7d summarized, > 7d aggressive). Everything recorded in `JOURNAL.md`.

## API

```bash
# Submit a task
curl -X POST http://localhost:9292/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"agent":"scout","description":"Research quantum computing trends"}'

# View agent skills
curl http://localhost:9292/api/agents/scout/skills

# Browse IPFS content (human-readable)
curl http://localhost:9292/api/content/<cid>?format=html
```

## Docs

- [Joining the Network](docs/JOINING.md) — deploy your own agent, connect to the swarm
- [Architecture](ARCHITECTURE.md) — 6-layer design, protocol messages, data flows
- [Design](DESIGN.md) — philosophy and technical decisions

## License

Apache 2.0 — Copyright (c) 2026 wellwell.work, LLC by Zoe
