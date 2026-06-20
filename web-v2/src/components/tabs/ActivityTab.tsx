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
            return (
              <Card key={(t.id as string) ?? i} className="flex items-center justify-between gap-4 !py-3">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px]">{(t.description as string) ?? '—'}</div>
                  <div className="mt-0.5 text-[11px] text-[var(--color-muted)]">
                    {(t.agent as string) ?? 'unknown'} · {formatRelative(t.created_at as number | undefined)}
                    {t.duration_seconds ? ` · ${formatDuration(Number(t.duration_seconds))}` : null}
                  </div>
                </div>
                <Badge tone={tone}>{status}</Badge>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
