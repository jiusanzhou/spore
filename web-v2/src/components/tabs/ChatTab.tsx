/*
 * ChatTab — multi-turn conversation UI for the swarm cockpit.
 *
 * Layout: 280px left rail (session list + new chat) + flex right pane
 * (header / message stream / composer). One session pins to one agent —
 * the agent name is set on creation and shown in the header so the user
 * can't get confused about who they're talking to.
 *
 * Data flow:
 *   - On mount + every 5s: GET /api/sessions (list) and, if a session is
 *     selected, GET /api/sessions/<id>/turns. Polling beats the SSE state
 *     payload because the chat doesn't need to be in the global broadcast.
 *   - "New chat" → POST /api/sessions {agent} → select returned id.
 *   - Submit message → POST /api/tasks {description, session_id}. Optimistic
 *     append a local user-turn immediately so the UI feels live, then the
 *     next poll (or task completion) replaces it with the real one + the
 *     assistant reply.
 *
 * Why poll instead of SSE? The /api/events SSE stream already pushes a
 * full state snapshot every change. Adding chat events to that schema
 * would (a) require server changes and (b) flood every page that doesn't
 * care about chat. Lightweight 5s polling on the chat page is cheaper
 * everywhere else and trivially correct.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { SwarmState } from '../../lib/types'
import { Badge, Button, Card, EmptyState, PageHeader, Textarea } from '../primitives'
import { cn, formatRelative } from '../../lib/utils'
import { ArrowLeft } from 'lucide-react'

interface Session {
  id: string
  agent: string
  title: string
  created_at: string
  updated_at: string
  turn_count: number
}

interface Turn {
  id: number
  session_id: string
  role: 'user' | 'assistant'
  content: string
  task_id?: string
  runtime?: string
  timestamp: string
  /** Local-only flag for optimistic turns awaiting server confirmation. */
  pending?: boolean
  /** Local-only flag for the "thinking…" placeholder shown while the agent
   *  is busy. Replaced by the real assistant turn on next poll. */
  thinking?: boolean
}

const POLL_MS = 5_000

export function ChatTab({ state }: { state: SwarmState | null }) {
  const agents = state?.agents ?? []
  const [sessions, setSessions] = useState<Session[]>([])
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [turns, setTurns] = useState<Turn[]>([])
  const [draft, setDraft] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pickAgent, setPickAgent] = useState<string>('')
  /**
   * pendingTask tracks an in-flight user message we just submitted: its
   * task_id (so we know when the assistant turn lands), its session, and
   * the moment we sent it (for the "thinking 8s" elapsed counter). Cleared
   * once the matching assistant turn appears on the server side.
   */
  const [pendingTask, setPendingTask] = useState<{
    sessionId: string
    taskId: string
    startedAt: number
  } | null>(null)
  /**
   * elapsedSec drives the "thinking Ns" counter. We tick it locally each
   * second while pendingTask is set so the user sees the placeholder is
   * alive even between 5s polls.
   */
  const [elapsedSec, setElapsedSec] = useState(0)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  // ── Session list polling ───────────────────────────────────────────
  const refreshSessions = useCallback(async () => {
    try {
      const res = await fetch('/api/sessions')
      if (!res.ok) return
      const data = (await res.json()) as { sessions: Session[] }
      setSessions(data.sessions ?? [])
    } catch {
      /* network blips are fine, next tick will retry */
    }
  }, [])

  useEffect(() => {
    refreshSessions()
    const id = setInterval(refreshSessions, POLL_MS)
    return () => clearInterval(id)
  }, [refreshSessions])

  // ── Active session turns polling ───────────────────────────────────
  // We capture pendingTask in a closure-stable ref via the dependency list
  // so refreshTurns can decide whether to clear the "thinking" state when
  // the matching assistant turn arrives.
  const refreshTurns = useCallback(
    async (sid: string) => {
      try {
        const res = await fetch(`/api/sessions/${sid}/turns`)
        if (!res.ok) return
        const data = (await res.json()) as { turns: Turn[] }
        const fresh = data.turns ?? []
        setTurns(fresh)
        // If we were waiting on a task and an assistant turn for that task
        // just landed, clear the thinking state. We match on task_id so
        // we don't accidentally clear after an unrelated assistant turn.
        if (pendingTask && pendingTask.sessionId === sid) {
          const arrived = fresh.some(
            (t) => t.role === 'assistant' && t.task_id === pendingTask.taskId,
          )
          if (arrived) setPendingTask(null)
        }
      } catch {
        /* ignore */
      }
    },
    [pendingTask],
  )

  useEffect(() => {
    if (!selectedId) {
      setTurns([])
      return
    }
    refreshTurns(selectedId)
    // Poll fast (1.5s) while waiting on an assistant reply so the
    // "thinking…" placeholder gets replaced with the real message
    // promptly. Otherwise back off to 5s.
    const tick = pendingTask ? 1500 : POLL_MS
    const id = setInterval(() => refreshTurns(selectedId), tick)
    return () => clearInterval(id)
  }, [selectedId, refreshTurns, pendingTask])

  // Tick the elapsed counter every second while we have a pending task.
  // This drives the "thinking 8s" label without forcing extra re-renders
  // when the chat is idle.
  useEffect(() => {
    if (!pendingTask) {
      setElapsedSec(0)
      return
    }
    const update = () =>
      setElapsedSec(Math.floor((Date.now() - pendingTask.startedAt) / 1000))
    update()
    const id = setInterval(update, 1000)
    return () => clearInterval(id)
  }, [pendingTask])

  // Auto-scroll to latest message when turn count changes.
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [turns.length, pendingTask])

  const activeSession = sessions.find((s) => s.id === selectedId) ?? null

  // ── New chat ───────────────────────────────────────────────────────
  async function newChat() {
    setError(null)
    try {
      const body = pickAgent ? { agent: pickAgent } : {}
      const res = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      if (!res.ok) {
        setError(`Create failed (${res.status}): ${await res.text()}`)
        return
      }
      const sess = (await res.json()) as Session
      setSessions((prev) => [sess, ...prev])
      setSelectedId(sess.id)
      setTurns([])
    } catch (err) {
      setError(`Network error: ${err instanceof Error ? err.message : err}`)
    }
  }

  // ── Delete chat ────────────────────────────────────────────────────
  async function deleteChat(id: string, e: React.MouseEvent) {
    e.stopPropagation()
    if (!confirm('Delete this chat? This can\'t be undone.')) return
    try {
      const res = await fetch(`/api/sessions/${id}`, { method: 'DELETE' })
      if (!res.ok) {
        setError(`Delete failed (${res.status})`)
        return
      }
      setSessions((prev) => prev.filter((s) => s.id !== id))
      if (selectedId === id) {
        setSelectedId(null)
        setTurns([])
      }
    } catch (err) {
      setError(`Network error: ${err instanceof Error ? err.message : err}`)
    }
  }

  // ── Send message ───────────────────────────────────────────────────
  async function sendMessage(e: React.FormEvent) {
    e.preventDefault()
    const content = draft.trim()
    if (!content || !selectedId) return
    setSubmitting(true)
    setError(null)

    // Optimistic: append a pending user turn so the UI feels live. The next
    // poll (or assistant reply) replaces it with the canonical record.
    const optimistic: Turn = {
      id: -Date.now(),
      session_id: selectedId,
      role: 'user',
      content,
      timestamp: new Date().toISOString(),
      pending: true,
    }
    setTurns((prev) => [...prev, optimistic])
    setDraft('')

    try {
      const res = await fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ description: content, session_id: selectedId }),
      })
      if (!res.ok) {
        setError(`Send failed (${res.status}): ${await res.text()}`)
        setTurns((prev) => prev.filter((t) => t.id !== optimistic.id))
        return
      }
      const data = (await res.json()) as { task_id?: string }
      if (data.task_id) {
        // Drive the "thinking…" placeholder until the real assistant turn
        // for this task lands. refreshTurns() clears pendingTask on match.
        setPendingTask({
          sessionId: selectedId,
          taskId: data.task_id,
          startedAt: Date.now(),
        })
      }
      // Refresh sooner than the 5s tick so the user turn lands fast.
      setTimeout(() => refreshTurns(selectedId), 300)
    } catch (err) {
      setError(`Network error: ${err instanceof Error ? err.message : err}`)
      setTurns((prev) => prev.filter((t) => t.id !== optimistic.id))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex h-[calc(100vh-9rem)] gap-4 md:h-[calc(100vh-8rem)]">
      {/* ── Left rail: session list ────────────────────────────── */}
      <div
        className={cn(
          'flex shrink-0 flex-col gap-2 md:w-[280px]',
          activeSession ? 'hidden w-full md:flex' : 'flex w-full',
        )}
      >
        <div className="flex items-center gap-2">
          <Button variant="primary" onClick={newChat} disabled={agents.length === 0}>
            + New chat
          </Button>
          <select
            value={pickAgent}
            onChange={(e) => setPickAgent(e.target.value)}
            className="flex-1 rounded border border-[var(--color-border)] bg-[var(--color-bg)] px-2 py-1 text-[12px]"
            disabled={agents.length === 0}
          >
            <option value="">auto</option>
            {agents.map((a) => (
              <option key={a.name} value={a.name}>
                {a.name}
              </option>
            ))}
          </select>
        </div>

        <div className="flex-1 space-y-1 overflow-y-auto">
          {sessions.length === 0 ? (
            <div className="px-2 py-6 text-center text-[12px] text-[var(--color-muted)]">
              No chats yet — start one above.
            </div>
          ) : (
            sessions.map((s) => (
              <button
                key={s.id}
                onClick={() => setSelectedId(s.id)}
                className={`group block w-full rounded border px-3 py-2 text-left transition-colors ${
                  selectedId === s.id
                    ? 'border-[var(--color-accent)] bg-[var(--color-accent-soft,rgba(99,102,241,0.08))]'
                    : 'border-[var(--color-border)] hover:bg-[var(--color-hover,rgba(255,255,255,0.03))]'
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="truncate text-[13px] font-medium">
                    {s.title || '(empty chat)'}
                  </div>
                  <span
                    role="button"
                    tabIndex={0}
                    onClick={(e) => deleteChat(s.id, e)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' || e.key === ' ') {
                        e.preventDefault()
                        deleteChat(s.id, e as unknown as React.MouseEvent)
                      }
                    }}
                    className="cursor-pointer rounded px-1 text-[11px] text-[var(--color-muted)] opacity-0 hover:text-[var(--color-error,#dc2626)] group-hover:opacity-100"
                    title="Delete chat"
                  >
                    ✕
                  </span>
                </div>
                <div className="mt-0.5 truncate text-[11px] text-[var(--color-muted)]">
                  {s.agent} · {s.turn_count} turn{s.turn_count === 1 ? '' : 's'} · {formatRelative(s.updated_at)}
                </div>
              </button>
            ))
          )}
        </div>
      </div>

      {/* ── Right pane: messages + composer ─────────────────────── */}
      <div
        className={cn(
          'flex flex-1 flex-col gap-2 min-w-0',
          activeSession ? 'flex' : 'hidden md:flex',
        )}
      >
        {!activeSession ? (
          <div className="flex flex-1 items-center justify-center">
            <EmptyState
              icon="💬"
              title="Pick a chat or start a new one"
              description="Sessions remember context — agents see prior turns and can answer follow-ups."
            />
          </div>
        ) : (
          <>
            <div className="flex items-center gap-2">
              <button
                onClick={() => setSelectedId(null)}
                className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[var(--radius-sm)] text-[var(--color-dim)] hover:bg-[var(--color-card)] hover:text-[var(--color-fg)] md:hidden"
                aria-label="Back to sessions"
              >
                <ArrowLeft className="h-4 w-4" strokeWidth={1.75} />
              </button>
              <div className="min-w-0 flex-1">
                <PageHeader
                  title={activeSession.title || '(empty chat)'}
                  description={`Talking to ${activeSession.agent} · ${activeSession.turn_count} turn${
                    activeSession.turn_count === 1 ? '' : 's'
                  }`}
                />
              </div>
            </div>

            <div className="flex-1 space-y-3 overflow-y-auto pr-2">
              {turns.length === 0 && !pendingTask ? (
                <div className="py-12 text-center text-[12px] text-[var(--color-muted)]">
                  Send the first message below to start the conversation.
                </div>
              ) : (
                turns.map((t) => <MessageBubble key={t.id} turn={t} />)
              )}
              {pendingTask && pendingTask.sessionId === selectedId ? (
                <ThinkingBubble agentName={activeSession.agent} elapsedSec={elapsedSec} />
              ) : null}
              <div ref={messagesEndRef} />
            </div>

            {error ? (
              <div className="text-[11px]">
                <Badge tone="red">{error}</Badge>
              </div>
            ) : null}

            <Card className="!py-2">
              <form onSubmit={sendMessage} className="flex items-end gap-2">
                <Textarea
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      sendMessage(e as unknown as React.FormEvent)
                    }
                  }}
                  placeholder={`Message ${activeSession.agent}… (Enter to send, Shift+Enter for newline)`}
                  rows={2}
                  disabled={submitting}
                  className="flex-1"
                />
                <Button
                  type="submit"
                  variant="primary"
                  disabled={submitting || !draft.trim()}
                >
                  {submitting ? '…' : 'Send'}
                </Button>
              </form>
            </Card>
          </>
        )}
      </div>
    </div>
  )
}

function MessageBubble({ turn }: { turn: Turn }) {
  const isUser = turn.role === 'user'
  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'}`}>
      <div
        className={`max-w-[80%] rounded-lg px-3 py-2 ${
          isUser
            ? 'bg-[var(--color-accent,rgb(99,102,241))] text-white'
            : 'bg-[var(--color-card,rgba(255,255,255,0.03))] border border-[var(--color-border)]'
        } ${turn.pending ? 'opacity-60' : ''}`}
      >
        <div className="whitespace-pre-wrap break-words text-[13px] leading-relaxed">
          {turn.content}
        </div>
        <div
          className={`mt-1 text-[10px] ${
            isUser ? 'text-white/70' : 'text-[var(--color-muted)]'
          }`}
        >
          {turn.role === 'assistant' && turn.runtime ? `${turn.runtime} · ` : ''}
          {formatRelative(turn.timestamp)}
          {turn.pending ? ' · sending…' : ''}
        </div>
      </div>
    </div>
  )
}

/**
 * ThinkingBubble — placeholder shown on the agent side while a task is in
 * flight. Three dots animate independently (staggered delays) for a "…"
 * effect, and an elapsed-seconds counter signals progress.
 *
 * Why animate dots manually instead of using a CSS lib? We're already on
 * Tailwind v4 + arbitrary values; one-line `animate-bounce` plus per-dot
 * inline `animationDelay` gets us a clean staggered pulse with zero new
 * deps.
 */
function ThinkingBubble({
  agentName,
  elapsedSec,
}: {
  agentName: string
  elapsedSec: number
}) {
  return (
    <div className="flex justify-start">
      <div className="max-w-[80%] rounded-lg border border-[var(--color-border)] bg-[var(--color-card,rgba(255,255,255,0.03))] px-3 py-2">
        <div className="flex items-center gap-1.5">
          <span className="inline-flex h-3 items-center gap-0.5">
            <span
              className="inline-block h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--color-muted)]"
              style={{ animationDelay: '0ms' }}
            />
            <span
              className="inline-block h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--color-muted)]"
              style={{ animationDelay: '150ms' }}
            />
            <span
              className="inline-block h-1.5 w-1.5 animate-bounce rounded-full bg-[var(--color-muted)]"
              style={{ animationDelay: '300ms' }}
            />
          </span>
          <span className="text-[12px] italic text-[var(--color-muted)]">
            {agentName} is thinking…
          </span>
        </div>
        <div className="mt-1 text-[10px] text-[var(--color-muted)]">
          {elapsedSec}s elapsed
        </div>
      </div>
    </div>
  )
}
