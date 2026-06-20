import type { SwarmState } from '../../lib/types'
import { Card, EmptyState, PageHeader } from '../primitives'
import { formatRelative } from '../../lib/utils'

export function ChangelogTab({ state }: { state: SwarmState | null }) {
  const entries = state?.changelog ?? []
  return (
    <div>
      <PageHeader
        title="Swarm Changelog"
        description="Notable events: skill creations, evolutions, peer joins, network changes."
      />
      {entries.length === 0 ? (
        <EmptyState icon="📜" title="No changelog entries" />
      ) : (
        <div className="space-y-2">
          {entries.map((e, i) => (
            <Card key={(e.id as string) ?? i} className="!py-3">
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="text-[12px] text-[var(--color-muted)]">
                    {(e.type as string) ?? 'event'} · {(e.agent as string) ?? 'system'}
                  </div>
                  <div className="mt-0.5 truncate text-[13px]">{(e.summary as string) ?? '—'}</div>
                </div>
                <span className="shrink-0 text-[10px] text-[var(--color-muted)]">
                  {formatRelative(e.ts as number | undefined)}
                </span>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
