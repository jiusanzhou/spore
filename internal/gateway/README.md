# Gateway — Chat-channel adapters

> Talk to a Spore agent from a real messaging platform.

A gateway is a long-lived process that maps inbound chat messages to
`agent.SubmitTask` calls and routes the agent's task results back to the
originating chat. It is **per-agent**, not per-swarm: one gateway speaks for
exactly one agent, in keeping with Spore's decentralised philosophy.

The first supported channel is **Telegram**. The package is structured so
adding Discord / Slack / Signal is just a new `*.go` file implementing the
`Gateway` interface.

## Telegram

### Setup

1. Create a bot with [@BotFather](https://t.me/BotFather), copy the token.
2. Send the bot a message, then visit
   `https://api.telegram.org/bot<TOKEN>/getUpdates` to find your `chat.id`.
   (Or message `/id` to the bot once the gateway is running — see "Slash
   commands" below.)
3. Add this to the agent's `config.toml`:

   ```toml
   [gateway.telegram]
   enabled  = true
   token    = "123456789:AAAA-bot-token"
   chat_ids = [12345678]              # REQUIRED allow-list
   # api_base = "https://api.telegram.org"   # override for proxies
   ```

4. Start the gateway:

   ```bash
   spore gateway -d ~/.spore/my-agent
   ```

### Behaviour

- Plain text → `agent.SubmitTask(text)` → bot replies "🌀 task `<id>` queued".
- When the task finishes, the gateway sends the result (or error) back to
  the same chat.
- Messages from chats **not** in the allow-list are rejected with a polite
  notice; they are also logged to stderr so a misuse is visible.
- Slash commands handled inline (no task submitted):
  - `/help`, `/start` — usage text
  - `/id` — your chat / user IDs (handy for the allow-list)

### Why an allow-list is mandatory

Without one, anyone who finds your bot can make your agent burn LLM credits.
The constructor refuses to start without `chat_ids`. There is no
"public mode".

### Result formatting

Long task outputs are truncated at ~3500 chars (well under Telegram's 4096
byte cap). Web-page previews are disabled to keep replies compact.

## Architecture

```
┌────────┐  text msg     ┌─────────┐  SubmitTask    ┌───────┐
│ Telegram │ ───────────▶│ Gateway │ ─────────────▶│ Agent │
│   user   │             │         │               │       │
│          │ ◀── result ─│         │ ◀─ callback ─│       │
└────────┘                └─────────┘               └───────┘
                          │
                          │ taskID → chatID  (in-memory map)
                          ▼
                   per-task routing
```

State is intentionally minimal: just a `taskID → chatID` map cleared on
delivery. The gateway has no business logic; reasoning, memory, and tools
all live inside the agent.

## Adding a new channel

1. Add `<NameConfig>` to `gateway.go` (or its own file).
2. Implement `Gateway` (`Name()`, `Start(ctx)`).
3. Register it under `[gateway.<name>]` in `internal/agent/config.go`.
4. Wire it up in `cmd/gateway.go: buildGateways`.

The interface is small enough that channels stay independent — adding
Discord must not require touching Telegram.

## Testing

Unit tests use an in-process `httptest.Server` to fake Telegram's API,
covering: validation, allow-list enforcement, slash commands, end-to-end
task→reply flow, and routing-table cleanup.

```bash
go test ./internal/gateway/...
```

No external dependencies — the gateway uses standard `net/http`. (We
intentionally did **not** pull in `go-telegram-bot-api` or similar; the
two endpoints we need — `getUpdates` + `sendMessage` — are simple enough
that a 200-line hand-roll is the lower-risk choice.)

## Future work

Two patterns the current per-agent design leaves room for:

- **One bot per node, multi-agent routing** — let the gateway dispatch
  to whichever agent on this node best matches the task description, using
  the existing stigmergic task market. This collapses N tokens into 1
  while staying decentralised at the network layer.
- **Cross-agent reply** — when the assigned agent broadcasts a task and a
  peer agent answers, the gateway should still attribute the reply to the
  original chat, not lose context to the protocol layer.

Both are deliberately deferred from the MVP: the simplest thing that
provides UX value is "chat with my one agent from Telegram", and that's
what the current design ships.
