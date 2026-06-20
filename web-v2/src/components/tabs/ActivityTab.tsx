import { useState } from 'react'
import type { SwarmState } from '../../lib/types'
import { Badge, Button, Card, EmptyState, Input, PageHeader, Textarea } from '../primitives'
import { formatDuration, formatRelative } from '../../lib/utils'

export function ActivityTab({ state }: { state: SwarmState | null }) {
  const tasks = state?.tasks ?? []
  const agents = state?.agents ?? []
  const [description, setDescription] = useState('')
  const [targetAgent, setTargetAgent] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [flash, setFlash] = useState<{ tone: 'green' | 'red'; msg: string } | null>(null)
  const [expandedIds, setExpandedIds] = useState<Set<string>>(new Set())

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    const desc = description.trim()
    if (!desc) return
    setSubmitting(true)
    setFlash(null)
    try {
      const res = await fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          description: desc,
          agent: targetAgent || undefined,
        }),
      })
      if (!res.ok) {
        const text = await res.text()
        setFlash({ tone: 'red', msg: `Failed (${res.status}): ${text || 'unknown error'}` })
        return
      }
      const data = (await res.json()) as { task_id?: string; agent?: string }
      setFlash({
        tone: 'green',
        msg: `Queued ${data.task_id?.slice(0, 8) ?? '?'} → ${data.agent ?? '?'}`,
      })
      setDescription('')
    } catch (err) {
      setFlash({ tone: 'red', msg: `Network error: ${err instanceof Error ? err.message : err}` })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div>
      <PageHeader
        title="Activity"
        description="Submit tasks to the swarm. Agents bid on broadcasts; targeted tasks go directly."
      />

      {/* Task submit form */}
      <Card className="mb-4">
        <form onSubmit={submit} className="space-y-2">
          <Textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Describe a task — the swarm will queue and execute it. e.g. 'Summarize the latest IPFS release notes.'"
            rows={2}
            disabled={submitting}
          />
          <div className="flex items-center gap-2">
            <div className="flex-1">
              <Input
                list="agents-list"
                value={targetAgent}
                onChange={(e) => setTargetAgent(e.target.value)}
                placeholder="Target agent (leave blank → broadcast to swarm)"
                disabled={submitting}
              />
              <datalist id="agents-list">
                {agents.map((a) => (
                  <option key={a.name} value={a.name}>
                    {a.role ?? 'worker'} · {a.status ?? 'unknown'}
                  </option>
                ))}
              </datalist>
            </div>
            <Button
              type="submit"
              variant="primary"
              disabled={submitting || !description.trim()}
            >
              {submitting ? 'Submitting…' : 'Submit task'}
            </Button>
          </div>
          {flash ? (
            <div className="text-[11px]">
              <Badge tone={flash.tone}>{flash.msg}</Badge>
            </div>
          ) : null}
        </form>
      </Card>

      {/* Task list */}
      {tasks.length === 0 ? (
        <EmptyState
          icon="📋"
          title="No tasks yet"
          description="Submit one above. Agents will pick it up automatically."
        />
      ) : (
        <div className="space-y-2">
          {tasks.map((t, i) => {
            const status = (t.status ?? 'unknown').toString().toLowerCase()
            const tone: 'green' | 'red' | 'amber' =
              status === 'completed' || status === 'success'
                ? 'green'
                : status === 'failed'
                ? 'red'
                : 'amber'
            // Compute duration from ISO timestamps when both ends are known.
            let durationLabel: string | null = null
            if (t.submitted_at && t.completed_at) {
              const ms = new Date(t.completed_at).getTime() - new Date(t.submitted_at).getTime()
              if (Number.isFinite(ms) && ms >= 0) {
                durationLabel = formatDuration(ms / 1000)
              }
            }
            const result = (t.result as string | undefined)?.trim()
            const error = (t.error as string | undefined)?.trim()
            const expandable = Boolean(result || error)
            const expanded = expandedIds.has(t.id ?? String(i))
            const key = (t.id as string) ?? String(i)
            return (
              <Card key={key} className="!py-3">
                <button
                  type="button"
                  className={`flex w-full items-center justify-between gap-4 text-left ${
                    expandable ? 'cursor-pointer' : 'cursor-default'
                  }`}
                  onClick={() => {
                    if (!expandable) return
                    setExpandedIds((prev) => {
                      const next = new Set(prev)
                      if (next.has(key)) next.delete(key)
                      else next.add(key)
                      return next
                    })
                  }}
                >
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px]">{(t.description as string) ?? '—'}</div>
                    <div className="mt-0.5 text-[11px] text-[var(--color-muted)]">
                      {(t.agent as string) ?? 'unknown'}
                      {t.runtime ? ` · ${t.runtime}` : ''}
                      {t.submitted_at ? ` · ${formatRelative(t.submitted_at)}` : ''}
                      {durationLabel ? ` · ${durationLabel}` : ''}
                      {expandable ? <span className="ml-1 opacity-60">{expanded ? '▾' : '▸'}</span> : null}
                    </div>
                  </div>
                  <Badge tone={tone}>{status}</Badge>
                </button>
                {expandable && expanded ? (
                  <div className="mt-2 border-t border-[var(--color-border)] pt-2">
                    {result ? (
                      <pre className="whitespace-pre-wrap break-words font-mono text-[12px] leading-relaxed text-[var(--color-fg)]">
                        {result}
                      </pre>
                    ) : null}
                    {error ? (
                      <pre className="whitespace-pre-wrap break-words font-mono text-[12px] leading-relaxed text-[var(--color-error,#dc2626)]">
                        {error}
                      </pre>
                    ) : null}
                  </div>
                ) : null}
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
