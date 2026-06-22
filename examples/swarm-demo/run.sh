#!/usr/bin/env bash
# spore swarm demo — 2 agents working in parallel via the HTTP API
#
# Showcases:
#   1. Spinning up a 2-agent swarm with the claude-code ACP runtime
#   2. Listing agents and their status via the API
#   3. Submitting 2 independent tasks in parallel, one per agent
#   4. Watching both agents work concurrently
#   5. Hopping over to the live dashboard
#
# Run:
#   ./examples/swarm-demo/run.sh
#
# Stop:
#   Ctrl-C in this terminal (cleanup is automatic)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SPORE_BIN="${SPORE_BIN:-$REPO_ROOT/bin/spore}"
API_PORT="${API_PORT:-8765}"
DATA_DIR="${DATA_DIR:-/tmp/spore-swarm-demo}"
LOG="$DATA_DIR/swarm.log"

# Fresh data dir each run — otherwise `spore swarm` picks up the stored
# configs and ignores `-n 2`. The CLI help is explicit about this:
# "number of agents (ignored when config dir has .toml files)".
rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"
cd "$DATA_DIR"

if [[ ! -x "$SPORE_BIN" ]]; then
  echo "❌ spore binary not found at $SPORE_BIN — run 'make build' first" >&2
  exit 1
fi

if pgrep -f "spore swarm.*api-port $API_PORT" > /dev/null; then
  echo "⚠️  swarm already running on :$API_PORT (use 'pkill -f \"spore swarm\"' to stop)" >&2
  exit 1
fi

echo "🦠 Starting 2-agent swarm with claude-code ACP runtime on :$API_PORT ..."

# Hold stdin open with a fifo so the REPL doesn't EOF-shutdown immediately.
FIFO="$DATA_DIR/repl.fifo"
mkfifo "$FIFO"

# Keep the write end open in a background subshell so the REPL never sees EOF.
( exec 3>"$FIFO"; sleep 99999 ) &
FIFO_KEEPER=$!

"$SPORE_BIN" swarm -n 2 -r claude-code --api-port "$API_PORT" -d "$DATA_DIR" \
  > "$LOG" 2>&1 < "$FIFO" &
SWARM_PID=$!

cleanup() {
  echo
  echo "🛑 Shutting down swarm (pid $SWARM_PID)..."
  kill "$SWARM_PID" 2>/dev/null || true
  kill "$FIFO_KEEPER" 2>/dev/null || true
  rm -f "$FIFO"
}
trap cleanup EXIT INT TERM

# Wait for the API to come up (≤30s).
for i in $(seq 1 30); do
  if curl -sf --max-time 2 "http://localhost:$API_PORT/api/agents" > /dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -sf --max-time 2 "http://localhost:$API_PORT/api/agents" > /dev/null 2>&1; then
  echo "❌ Swarm didn't come up in 30s. Tail of log:"
  tail -30 "$LOG"
  exit 1
fi

# Pick out the two agent names dynamically — swarm names them
# "coordinator" + "worker-1" by default but that's not contractual.
# (Portable across bash 3.2 / 4 / 5 — no `mapfile`.)
AGENTS_JSON=$(curl -s "http://localhost:$API_PORT/api/agents")
A0=$(echo "$AGENTS_JSON" | jq -r '.agents[0].name // empty')
A1=$(echo "$AGENTS_JSON" | jq -r '.agents[1].name // empty')
if [[ -z "$A0" || -z "$A1" ]]; then
  echo "❌ Expected 2 agents, got: $(echo "$AGENTS_JSON" | jq -c '[.agents[].name]')"
  tail -30 "$LOG"
  exit 1
fi

echo "✅ Swarm up. Agents:"
curl -s "http://localhost:$API_PORT/api/agents" \
  | jq -r '.agents[] | "  • \(.name) (\(.role), runtime=\(.runtime), model=\(.model)) — \(.status)"'

echo
echo "📋 Submitting 2 independent tasks in parallel:"
echo "    $A0 → 'Count to 5 and tell a one-liner Go joke'"
echo "    $A1 → 'Compute fibonacci(10) step by step'"

T0=$(curl -sf -X POST "http://localhost:$API_PORT/api/tasks" \
  -H "Content-Type: application/json" \
  --data "{\"agent\":\"$A0\",\"description\":\"Count to 5 and tell me a one-liner joke about Go programming.\"}")
T1=$(curl -sf -X POST "http://localhost:$API_PORT/api/tasks" \
  -H "Content-Type: application/json" \
  --data "{\"agent\":\"$A1\",\"description\":\"Compute fibonacci(10) step by step and explain how you arrived at the answer.\"}")

ID0=$(echo "$T0" | jq -r '.task_id')
ID1=$(echo "$T1" | jq -r '.task_id')
echo "    $A0 task_id: $ID0"
echo "    $A1 task_id: $ID1"

echo
echo "👀 Watching agents work in parallel (poll every 3s, max ~90s):"
for i in $(seq 1 30); do
  sleep 3
  LINE=$(curl -s "http://localhost:$API_PORT/api/agents" \
    | jq -r '[.agents[] | "\(.name)=\(.status)(tc=\(.task_count))"] | join(" / ")')
  printf "  [t+%2ds] %s\n" "$((i*3))" "$LINE"
  ACTIVE=$(curl -s "http://localhost:$API_PORT/api/agents" \
    | jq -r '[.agents[] | select(.status != "idle")] | length')
  if [[ "$ACTIVE" == "0" && "$i" -gt 1 ]]; then
    echo "  🎉 both agents back to idle — tasks complete"
    break
  fi
done

echo
echo "📜 Final agent state:"
curl -s "http://localhost:$API_PORT/api/agents" \
  | jq '.agents[] | {name, status, task_count, balance: .economy.balance, completed: .economy.stats.tasks_completed}'

echo
echo "✅ Demo complete. Swarm is still running — open the dashboard at:"
echo "      http://localhost:$API_PORT/"
echo
echo "Press Ctrl-C here to shut everything down."

# Keep alive so the user can poke at the dashboard.
wait $SWARM_PID
