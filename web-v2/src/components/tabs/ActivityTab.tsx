import type { SwarmState } from '../../lib/types'
import { Card, EmptyState, PageHeader } from '../primitives'
import { formatDuration, formatRelative } from '../../lib/utils'

export function ActivityTab({ state }: { state: SwarmState | null }) {
  const tasks = state?.tasks ?? []
  return (
    <div>
      <PageHeader title="Activity" description="Recent tasks executed by agents in this swarm." />
      {tasks.length === 0 ? (
        <EmptyState icon="📋" title="No tasks yet" description="Submit a task to see activity here." />
      ) : (
        <div className="space-y-2">
          {tasks.map((t, i) => {
            const status = (t.status ?? 'unknown').toString().toLowerCase()
            const ok = status === 'completed' || status === 'success'
            return (
              <Card key={(t.id as string) ?? i} className="flex items-center justify-between gap-4 !py-3">
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px]">{(t.description as string) ?? '—'}</div>
                  <div className="mt-0.5 text-[11px] text-[var(--color-muted)]">
                    {(t.agent as string) ?? 'unknown'} · {formatRelative(t.created_at as number | undefined)}
                    {t.duration_seconds ? ` · ${formatDuration(Number(t.duration_seconds))}` : null}
                  </div>
                </div>
                <span
                  className={
                    'shrink-0 rounded-full px-2 py-0.5 text-[10px] uppercase tracking-wider ' +
                    (ok
                      ? 'bg-[var(--color-green-soft)] text-[var(--color-green)]'
                      : status === 'failed'
                      ? 'bg-[var(--color-red-soft)] text-[var(--color-red)]'
                      : 'bg-[var(--color-amber-soft)] text-[var(--color-amber)]')
                  }
                >
                  {status}
                </span>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}
