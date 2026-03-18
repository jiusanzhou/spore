# Spore Demo Guide

This directory contains example configurations for running Spore in different modes.

## Prerequisites

```bash
# Build Spore
make build

# Set your LLM API key (required for task execution)
export SPORE_LLM_API_KEY="sk-..."
# Or: export OPENAI_API_KEY="sk-..."
```

## 1. Single Agent Mode

Run a single agent with a TOML config:

```bash
spore run -c examples/demo/agent-0.toml
```

In another terminal, send it a task:

```bash
spore task coordinator "Summarize the benefits of decentralized AI"
```

## 2. Multi-Agent Swarm Mode

Start a swarm of 3 agents with an interactive REPL:

```bash
spore swarm -n 3 -m gpt-4o-mini --api-port 8080
```

Inside the REPL:

```
spore> ps
spore> task coordinator "Research the top 3 AI frameworks and compare them"
spore> broadcast "What is the current state of open-source LLMs?"
spore> help
spore> quit
```

## 3. P2P Cross-Node Demo

Run two nodes in separate terminals that discover each other via libp2p.

**Terminal 1** — Start node 1:

```bash
spore run -c examples/demo/p2p-node1.toml
```

**Terminal 2** — Start node 2 (bootstraps to node 1):

```bash
spore run -c examples/demo/p2p-node2.toml
```

Check peer connections:

```bash
spore peers
```

## 4. API Server Demo

Start a swarm with the HTTP API enabled:

```bash
spore swarm -n 3 -m gpt-4o-mini --api-port 8080
```

Query the API:

```bash
# List agents
curl http://localhost:8080/api/agents

# Send a task
curl -X POST http://localhost:8080/api/task \
  -H "Content-Type: application/json" \
  -d '{"agent": "coordinator", "description": "Hello from the API"}'

# Check status
curl http://localhost:8080/api/status
```

## Configuration Reference

See `configs/default.toml` for all available options. Key environment variables:

| Variable | Description |
|----------|-------------|
| `SPORE_LLM_API_KEY` | LLM provider API key |
| `OPENAI_API_KEY` | Fallback API key |
